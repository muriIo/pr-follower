import { DestroyRef, Injectable, computed, inject, signal } from '@angular/core';
import {
  ActionRequestOptions,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
} from '../../domain/gateways/workspace.gateways';
import {
  BlastFileItem,
  CiState,
  DiffHunkLine,
  PerspectiveView,
  PullRequestItem,
  RiskVerdictTier,
  SeatFilterType,
  SseConnectionState,
  TimelineEventItem,
  computeSemverBumpLabel,
  createInitialPrDataset,
  createInitialRepos,
  createInitialTimelineEvents,
  isActionableTurn,
  isSafeBatchCandidate,
} from '../../domain/models/workspace.models';
import { ActionCancellationManager, ActionViewModel } from '../use-cases/action-cancellation.manager';

export interface SyntaxToken {
  readonly s: string;
  readonly cls: string;
}

export interface RenderedDiffLine {
  readonly cls: string;
  readonly n: string;
  readonly tokens: readonly SyntaxToken[];
}

export interface SeatStreamRowViewModel {
  readonly id: number;
  readonly title: string;
  readonly sel: string;
  readonly dim: string;
  readonly showTurn: boolean;
  readonly hasOwn: boolean;
  readonly ownLabel: string;
  readonly showRisk: boolean;
  readonly vCls: string;
  readonly riskTip: string;
  readonly riskLabel: string;
  readonly isApproved: boolean;
  readonly approvedTip: string;
  readonly approvedLabel: string;
  readonly hasQuick: boolean;
  readonly act: ActionViewModel;
  readonly quickTip: string;
  readonly quickLabel: string;
  readonly open: () => void;
  readonly quick: () => void;
}

export interface SeatCardViewModel {
  readonly name: string;
  readonly branch: string;
  readonly cls: string;
  readonly ciDot: string;
  readonly ciCls: string;
  readonly ciLabel: string;
  readonly ciTip: string;
  readonly turn: number;
  readonly turnCls: string;
  readonly deps: number;
  readonly depCls: string;
  readonly openLabel: string;
  readonly isEmpty: boolean;
  /** i18n key explaining lost GitHub App access, or null when the repository is reachable. */
  readonly accessKey: string | null;
  readonly stream: readonly SeatStreamRowViewModel[];
}

export interface InboxCardViewModel {
  readonly id: number;
  readonly repo: string;
  readonly title: string;
  readonly sel: string;
  readonly authorLabel: string;
  readonly ciLabel: string;
  readonly ciTextCls: string;
  readonly hasPill: boolean;
  readonly ownPill?: string;
  readonly hasClaim: boolean;
  readonly claim?: string;
  readonly badge: string;
  readonly badgeTip: string;
}

export interface RadarCardViewModel {
  readonly id: number;
  readonly repo: string;
  readonly radarTitle: string;
  readonly radarMeta: string;
  readonly metaTip: string;
  readonly ciShort: string;
  readonly ciTextCls: string;
  readonly approvedMine: boolean;
  readonly sel: string;
}

export interface DrawerBlastFileViewModel {
  readonly index: number;
  readonly file: string;
  readonly cls: string;
  readonly isOpen: boolean;
  readonly expanded: string;
  readonly chevCls: string;
  readonly hasUses: boolean;
  readonly usesLabel: string;
  readonly breaks: boolean;
  readonly lines: readonly RenderedDiffLine[];
}

export interface DrawerViewModel {
  readonly id: number;
  readonly repo: string;
  readonly title: string;
  readonly ghUrl: string;
  readonly focused: boolean;
  readonly banner: string;
  readonly general: boolean;
  readonly vCls: string;
  readonly vLabel: string;
  readonly why: string;
  readonly signals: readonly { k: string; v: string; dot: string }[];
  readonly isDep: boolean;
  readonly notDep: boolean;
  readonly verdict: RiskVerdictTier;
  readonly rvHead: string;
  readonly rvDetail: string;
  readonly tree: readonly {
    index: number;
    label: string;
    hasLink: boolean;
    pressed: string;
    cls: string;
  }[];
  readonly treeInfo: string;
  readonly blastTitle: string;
  readonly blastSummary: string;
  readonly hasSummary: boolean;
  readonly hasBlast: boolean;
  readonly noBlast: boolean;
  readonly blasts: readonly DrawerBlastFileViewModel[];
  readonly hasBlastFoot: boolean;
  readonly hasMore: boolean;
  readonly moreLabel: string;
  readonly allLabel: string;
  readonly note: string;
  readonly noteCls: string;
  readonly showDiff: boolean;
  readonly diffFile: string;
  readonly hasDelta: boolean;
  readonly deltaAdd?: string;
  readonly deltaDel?: string;
  readonly lines: readonly RenderedDiffLine[];
  readonly showIdle: boolean;
  readonly showMerged: boolean;
  readonly showSecondary: boolean;
  readonly blocked: boolean;
  readonly blockedCls: string;
  readonly blockedText: string;
  readonly showApprove: boolean;
  readonly approvedMine: boolean;
  readonly mergeCls: string;
  readonly mergeDis: string;
  readonly mergeTip: string;
  readonly busyDis: string;
  readonly act: ActionViewModel;
}

@Injectable({ providedIn: 'root' })
export class WorkspaceStore {
  private readonly destroyRef = inject(DestroyRef);
  private readonly prGateway = inject(PULL_REQUEST_GATEWAY, { optional: true });
  private readonly sseGateway = inject(LIVE_EVENT_GATEWAY, { optional: true });
  private readonly cancellation = inject(ActionCancellationManager);

  readonly repos = signal(createInitialRepos());
  readonly prs = signal(createInitialPrDataset());
  readonly view = signal<PerspectiveView>('seats');
  readonly selectedPrId = signal<number | null>(null);
  readonly closingDrawer = signal(false);
  readonly drawerWidth = signal(this.loadInitialDrawerWidth());
  readonly isResizingDrawer = signal(false);
  readonly seatFilter = signal<SeatFilterType>('todos');
  readonly showMuted = signal(true);
  readonly mergedMap = signal<Record<number, boolean>>({});
  readonly approvedMap = signal<Record<number, boolean>>({});
  readonly silencedMap = signal<Record<number, string>>({});
  readonly ciOverrides = signal<Record<number, { ci: CiState; ciText: string }>>({});
  readonly events = signal<readonly TimelineEventItem[]>(createInitialTimelineEvents());
  readonly unreadEvents = signal(0);
  readonly pausedTimeline = signal(false);
  readonly freshRepo = signal<string | null>(null);
  readonly connectionState = signal<SseConnectionState>('conectado');
  readonly selectedTreeNode = signal<number | null>(null);
  readonly blastOpenMap = signal<Record<number, boolean>>({ 0: true });
  readonly blastShowAll = signal(false);
  private lastBatchTotal = 0;
  private disconnectLive: (() => void) | null = null;

  constructor() {
    this.cancellation.registerAbortCallback((opId) => {
      void this.prGateway?.cancelServerOperation(opId);
    });
    this.destroyRef.onDestroy(() => this.stopLiveSession());
  }

  /**
   * Loads the workspace and opens the live event stream for the signed-in user.
   * Nothing is fetched before sign-in, so signed-out visitors never hit the API (ADR-0006, ADR-0010).
   */
  startLiveSession(): void {
    if (this.disconnectLive) {
      return;
    }
    void this.loadWorkspace();
    this.disconnectLive =
      this.sseGateway?.connect(
        (evt) => {
          if (!this.pausedTimeline()) {
            this.appendTimelineEvent(evt.kind, evt.text, evt.repo, evt.pr);
          }
          if (evt.refreshWorkspace) {
            void this.loadWorkspace();
          }
        },
        (state) => this.onConnectionState(state),
      ) ?? (() => undefined);
  }

  private streamOpenedBefore = false;

  /** After a reconnect, reload: events sent while the stream was down were missed (ADR-0011). */
  private onConnectionState(state: SseConnectionState): void {
    const previous = this.connectionState();
    this.connectionState.set(state);
    if (state !== 'conectado') {
      return;
    }
    if (this.streamOpenedBefore && previous !== 'conectado') {
      void this.loadWorkspace();
    }
    this.streamOpenedBefore = true;
  }

  stopLiveSession(): void {
    const disconnect = this.disconnectLive;
    this.disconnectLive = null;
    this.streamOpenedBefore = false;
    disconnect?.();
  }

  async loadWorkspace(options?: ActionRequestOptions): Promise<void> {
    const dataset = await this.prGateway?.fetchWorkspace?.(options);
    if (!dataset) {
      return;
    }
    this.repos.set(dataset.repos);
    this.prs.set(dataset.prs);
    if (dataset.events.length > 0) {
      this.events.set(dataset.events);
    }
    if (dataset.approvedMap) {
      this.approvedMap.set(dataset.approvedMap);
    }
    if (dataset.silencedMap) {
      this.silencedMap.set(dataset.silencedMap);
    }
  }

  readonly openPrs = computed(() => {
    const merged = this.mergedMap();
    return this.prs().filter((p) => !merged[p.id]);
  });

  readonly openDependabotPrs = computed(() => {
    const silenced = this.silencedMap();
    return this.openPrs().filter((p) => p.dep && !silenced[p.id]);
  });

  readonly actionableTurnPrs = computed(() => {
    const silenced = this.silencedMap();
    const approved = this.approvedMap();
    return this.openPrs().filter((p) => isActionableTurn(p, silenced, approved));
  });

  readonly navCounts = computed(() => ({
    inbox: this.actionableTurnPrs().length,
    radar: this.openDependabotPrs().length,
    seats: this.repos().length,
    timeline: this.unreadEvents(),
  }));

  readonly syncAction = computed(() => this.cancellation.getView(this.cancellation.actionsState(), 'sync'));
  readonly batchAction = computed(() => this.cancellation.getView(this.cancellation.actionsState(), 'batch'));

  readonly allSeats = computed<readonly SeatCardViewModel[]>(() => {
    const open = this.openPrs();
    const silenced = this.silencedMap();
    const approved = this.approvedMap();
    const selected = this.selectedPrId();
    const fresh = this.freshRepo();
    const actionsMap = this.cancellation.actionsState();

    return this.repos().map((r) => {
      const repoPrs = open.filter((p) => p.repo === r.name);
      const deps = repoPrs.filter((p) => p.dep && !silenced[p.id]);
      const turnCount = repoPrs.filter((p) => isActionableTurn(p, silenced, approved)).length;
      const fails = repoPrs.filter((p) => this.effectiveCi(p).ci === 'fail').length;
      const runs = repoPrs.filter((p) => this.effectiveCi(p).ci === 'run').length;
      const risky = deps.some((p) => p.verdict !== 'baixo');
      const ciState: CiState = fails > 0 ? 'fail' : runs > 0 ? 'run' : 'ok';

      const stream: SeatStreamRowViewModel[] = repoPrs
        .slice()
        .sort((a, b) => a.rank - b.rank)
        .slice(0, 3)
        .map((p) => {
          const isTurn = isActionableTurn(p, silenced, approved);
          const showTurn = isTurn && !p.dep;
          const mine = Boolean(approved[p.id]);
          const isApproved = Boolean(p.approvedBy || (mine && !p.dep));
          const tier = { baixo: '🟢 Baixo Risco', medio: '🟡 Médio Risco', alto: '🔴 Alto Risco' }[p.verdict];
          const dev = p.depType === 'devDependencies';
          const vLabel = { baixo: 'Baixo Risco', medio: 'Médio Risco', alto: 'Alto Risco' }[p.verdict];
          const safe = isSafeBatchCandidate(p, this.effectiveCi(p).ci);

          return {
            id: p.id,
            title: p.seatTitle ?? p.title,
            sel: selected === p.id ? 'is-sel' : '',
            dim: p.approvedBy && !mine ? 'is-dim' : '',
            showTurn,
            hasOwn: Boolean(p.ownFiles),
            ownLabel: this.plural(p.ownFiles ?? 0, 'arquivo seu', 'arquivos seus'),
            showRisk: !showTurn && !isApproved && (!mine || Boolean(p.dep)),
            vCls: `pf-badge-${p.verdict}`,
            riskTip: p.dep
              ? dev
                ? 'Só afeta desenvolvimento e build. Não chega à produção.'
                : 'Código que roda em produção. Veja o raio de impacto no drawer.'
              : 'Veja o veredito completo no drawer.',
            riskLabel: p.dep ? `${tier}${dev ? ' (Dev)' : ' (Prod)'}` : vLabel,
            isApproved: isApproved || mine,
            approvedTip: mine
              ? 'Sua aprovação foi registrada. O PR ainda não foi mesclado.'
              : 'Já aprovado por alguém do time. Sua revisão não é necessária.',
            approvedLabel: mine ? 'Aprovado por você' : `Aprovado pelo Time (${p.approvedBy})`,
            hasQuick: safe,
            act: this.cancellation.getView(actionsMap, `merge:${p.id}`),
            quickTip: mine ? 'Mescla o PR com squash.' : 'Registra sua aprovação sem mesclar. Depois, use Squash.',
            quickLabel: mine ? 'Squash' : 'Aprovar',
            open: () => this.openPr(p.id),
            quick: () => {
              if (mine) {
                void this.mergePr(p.id);
              } else {
                void this.approvePr(p.id);
              }
            },
          };
        });

      const accessKey =
        r.access === 'removed' ? 'seats.access.removed' : r.access === 'suspended' ? 'seats.access.suspended' : null;

      return {
        name: r.name,
        branch: r.branch,
        cls: [fresh === r.name ? 'is-fresh' : '', accessKey ? 'pf-seat-no-access' : ''].filter(Boolean).join(' '),
        ciDot: {
          fail: 'pf-dot pf-dot-fail pf-dot-pulse',
          run: 'pf-dot pf-dot-warn pf-dot-pulse',
          ok: 'pf-dot pf-dot-ok',
        }[ciState],
        ciCls: { fail: 'pf-ci-fail', run: 'pf-ci-warn', ok: 'pf-ci-stable' }[ciState],
        ciLabel: { fail: `${fails} CI Falhando`, run: `${runs} CI Rodando`, ok: 'CI Estável' }[ciState],
        ciTip: {
          fail: 'PRs abertos deste repositório com CI falhando.',
          run: 'PRs abertos deste repositório com CI ainda rodando.',
          ok: 'Nenhum PR aberto com CI falhando ou rodando.',
        }[ciState],
        turn: turnCount,
        turnCls: turnCount > 0 ? 'pf-turn' : 'pf-badge-zero',
        deps: deps.length,
        depCls: deps.length === 0 ? 'pf-badge-zero' : risky ? 'pf-badge-medio' : 'pf-badge-baixo',
        openLabel: this.plural(repoPrs.length, 'PR Aberto', 'PRs Abertos'),
        isEmpty: repoPrs.length === 0,
        accessKey,
        stream,
      };
    });
  });

  readonly seatFilterOptions = computed(() => {
    const all = this.allSeats();
    const turnSeats = all.filter((s) => s.turn > 0);
    const depSeats = all.filter((s) => s.deps > 0);
    const current = this.seatFilter();
    return [
      { id: 'todos' as const, label: `Todos (${all.length})`, active: current === 'todos' },
      { id: 'turn' as const, label: `Com PRs na Minha Vez (${turnSeats.length})`, active: current === 'turn' },
      { id: 'deps' as const, label: `Dependabot Pendente (${depSeats.length})`, active: current === 'deps' },
    ];
  });

  readonly seats = computed(() => {
    const filter = this.seatFilter();
    const all = this.allSeats();
    if (filter === 'turn') {
      return all.filter((s) => s.turn > 0);
    }
    if (filter === 'deps') {
      return all.filter((s) => s.deps > 0);
    }
    return all;
  });

  readonly activeInboxCards = computed<readonly InboxCardViewModel[]>(() =>
    this.actionableTurnPrs()
      .filter((p) => !p.dep)
      .map((p) => this.buildInboxCard(p)),
  );

  readonly mutedInboxCards = computed<readonly InboxCardViewModel[]>(() => {
    const silenced = this.silencedMap();
    const approved = this.approvedMap();
    return this.openPrs()
      .filter((p) => Boolean(p.muteBadge || silenced[p.id] || (approved[p.id] && !p.dep)))
      .map((p) => this.buildInboxCard(p));
  });

  readonly radarGroups = computed(() => {
    const deps = this.openDependabotPrs();
    const approved = this.approvedMap();
    const selected = this.selectedPrId();
    const tiers: readonly [RiskVerdictTier, string][] = [
      ['alto', '🔴 Alto Risco — Requer Análise de Impacto'],
      ['baixo', '🟢 Baixo Risco — Seguros para Merge Rápido'],
      ['medio', '🟡 Médio Risco — Revisar Antes de Mesclar'],
    ];

    return tiers
      .map(([verdict, label]) => {
        const items: RadarCardViewModel[] = deps
          .filter((p) => p.verdict === verdict)
          .map((p) => {
            const ci = this.effectiveCi(p).ci;
            return {
              id: p.id,
              repo: p.repo,
              radarTitle: p.radarTitle ?? p.title,
              radarMeta: p.radarMeta ?? '',
              metaTip:
                p.via && p.via.length > 0
                  ? 'Transitiva: chega ao projeto por meio de outro pacote. Veja o caminho na árvore do drawer.'
                  : p.depType === 'devDependencies'
                    ? 'Usada só em desenvolvimento. Não entra no build de produção.'
                    : 'Declarada no package.json e carregada em produção.',
              ciShort: { ok: 'CI verde', run: 'CI rodando', fail: 'CI falhando' }[ci],
              ciTextCls: { ok: 'pf-ci-ok', run: 'pf-ci-warn', fail: 'pf-ci-fail' }[ci],
              approvedMine: Boolean(approved[p.id]),
              sel: selected === p.id ? 'is-sel' : '',
            };
          });
        return { label: `${label} (${items.length})`, items };
      })
      .filter((g) => g.items.length > 0);
  });

  readonly safeBatchSummary = computed(() => {
    const deps = this.openDependabotPrs();
    const approved = this.approvedMap();
    const safe = deps.filter((p) => isSafeBatchCandidate(p, this.effectiveCi(p).ci));
    const batchAct = this.batchAction();
    const batchN = batchAct.running && this.lastBatchTotal ? this.lastBatchTotal : safe.length;
    const safeApproved = safe.filter((p) => approved[p.id]);
    const safePending = safe.filter((p) => !approved[p.id]);

    return {
      has: safe.length > 0 || batchAct.running,
      title: `🟢 ${batchN} PRs de Baixo Risco prontos para mesclagem segura (devDependencies ou transitivos sem import direto e CI verde)`,
      canApprove: safePending.length > 0,
      approveLabel: safeApproved.length > 0 ? `Aprovar os ${safePending.length} Restantes` : `Aprovar Todos os ${batchN}`,
      mergeDis: safeApproved.length === 0 ? 'true' : 'false',
      mergeCls: safePending.length === 0 ? 'pf-btn-primary' : '',
      mergeLabel:
        safeApproved.length > 0 && safePending.length > 0
          ? `Mesclar ${safeApproved.length} Aprovado(s)`
          : `Mesclar Todos os ${batchN}`,
      safeApproved,
      safePending,
    };
  });

  readonly timelineGroups = computed(() => {
    const merged = this.mergedMap();
    const groups: {
      label: string;
      items: (TimelineEventItem & { time: string; dot: string; cls: string; noPr: boolean })[];
    }[] = [];
    const dots = { ok: 'pf-dot pf-dot-ok', fail: 'pf-dot pf-dot-fail', turn: 'pf-dot pf-dot-turn', info: 'pf-dot' };

    for (const ev of this.events()) {
      const mins = (Date.now() - ev.t) / 60_000;
      const label = mins <= 5 ? 'Agora' : mins <= 60 ? 'Última hora' : 'Hoje cedo';
      let group = groups.find((g) => g.label === label);
      if (!group) {
        group = { label, items: [] };
        groups.push(group);
      }
      const d = new Date(ev.t);
      const time = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
      const noPr = Boolean(!ev.pr || merged[ev.pr]);
      group.items.push({
        ...ev,
        time,
        dot: dots[ev.kind],
        cls: ev.fresh ? 'is-new' : '',
        noPr,
      });
    }
    return groups;
  });

  readonly drawer = computed<DrawerViewModel | null>(() => {
    const selId = this.selectedPrId();
    if (!selId) {
      return null;
    }
    const pr = this.prs().find((x) => x.id === selId);
    if (!pr) {
      return null;
    }
    const ci = this.effectiveCi(pr);
    const merged = Boolean(this.mergedMap()[pr.id]);
    const mine = Boolean(this.approvedMap()[pr.id]);
    const act = this.cancellation.getView(this.cancellation.actionsState(), `merge:${pr.id}`);
    const dotCi = { ok: 'pf-dot pf-dot-ok', run: 'pf-dot pf-dot-warn pf-dot-pulse', fail: 'pf-dot pf-dot-fail' }[
      ci.ci
    ];
    const dotOwners = { ok: 'pf-dot pf-dot-ok', voce: 'pf-dot pf-dot-turn', pend: 'pf-dot pf-dot-warn' }[pr.owners];

    let blockedText = '';
    if (ci.ci === 'fail') {
      blockedText = 'Mesclagem bloqueada: o CI está falhando.';
    } else if (ci.ci === 'run') {
      blockedText = 'Mesclagem bloqueada: o CI ainda está rodando.';
    } else if (pr.owners === 'pend') {
      blockedText = 'Mesclagem bloqueada: falta aprovação de CODEOWNERS.';
    }
    const needsApproval = !blockedText && !mine && !pr.approvedBy;
    if (needsApproval) {
      blockedText = 'Aprove antes de mesclar.';
    }

    const vLabel = { baixo: 'Baixo Risco', medio: 'Médio Risco', alto: 'Alto Risco' }[pr.verdict];
    const depVm = pr.dep ? this.buildDependabotDetail(pr) : null;

    return {
      id: pr.id,
      repo: pr.repo,
      title: pr.title,
      ghUrl: `https://github.com/${pr.repo}/pull/${pr.id}`,
      focused: Boolean(pr.focusRule),
      banner: pr.focusRule
        ? `Você foi acionado via regra ${pr.focusRule}. Exibindo apenas o diff dos arquivos sob sua responsabilidade.`
        : '',
      general: Boolean(!pr.focusRule && !pr.dep),
      vCls: `pf-badge-${pr.verdict}`,
      vLabel,
      why: pr.why,
      signals: [
        { k: 'CI', v: ci.ciText, dot: dotCi },
        { k: 'CODEOWNERS', v: pr.ownersText, dot: dotOwners },
        { k: 'Tamanho', v: pr.size, dot: 'pf-dot' },
      ],
      isDep: Boolean(pr.dep),
      notDep: !pr.dep,
      verdict: pr.verdict,
      rvHead: depVm?.rvHead ?? '',
      rvDetail: depVm?.rvDetail ?? '',
      tree: depVm?.tree ?? [],
      treeInfo: depVm?.treeInfo ?? '',
      blastTitle: depVm?.blastTitle ?? '',
      blastSummary: depVm?.blastSummary ?? '',
      hasSummary: depVm?.hasSummary ?? false,
      hasBlast: depVm?.hasBlast ?? false,
      noBlast: depVm?.noBlast ?? true,
      blasts: depVm?.blasts ?? [],
      hasBlastFoot: depVm?.hasBlastFoot ?? false,
      hasMore: depVm?.hasMore ?? false,
      moreLabel: depVm?.moreLabel ?? '',
      allLabel: depVm?.allLabel ?? '',
      note: depVm?.note ?? '',
      noteCls: depVm?.noteCls ?? '',
      showDiff: !pr.dep,
      diffFile: pr.file,
      hasDelta: Boolean(pr.deltaAdd),
      deltaAdd: pr.deltaAdd,
      deltaDel: pr.deltaDel,
      lines: this.renderDiffLines(pr.hunk),
      showIdle: act.idle && !merged,
      showMerged: act.idle && merged,
      showSecondary: !merged && !act.hasNote,
      blocked: Boolean(blockedText),
      blockedCls: needsApproval ? 'pf-note' : 'pf-blocked',
      blockedText,
      showApprove: !mine,
      approvedMine: mine,
      mergeCls: mine && !blockedText ? 'pf-btn-primary' : '',
      mergeDis: blockedText ? 'true' : 'false',
      mergeTip: blockedText || 'Mescla o PR com squash: todos os commits viram um só na branch padrão.',
      busyDis: act.running ? 'true' : 'false',
      act,
    };
  });

  setPerspective(nextView: PerspectiveView): void {
    if (nextView !== this.view() && this.selectedPrId()) {
      this.closeDrawer();
    }
    this.view.set(nextView);
    if (nextView === 'timeline') {
      this.unreadEvents.set(0);
    }
  }

  setSeatFilter(filter: SeatFilterType): void {
    this.seatFilter.set(filter);
  }

  toggleShowMuted(): void {
    this.showMuted.update((v) => !v);
  }

  togglePauseTimeline(): void {
    this.pausedTimeline.update((v) => !v);
  }

  openPr(prId: number): void {
    this.closingDrawer.set(false);
    this.selectedPrId.set(prId);
    this.selectedTreeNode.set(null);
    this.blastOpenMap.set({ 0: true });
    this.blastShowAll.set(false);
  }

  closeDrawer(): void {
    this.selectedPrId.set(null);
    this.closingDrawer.set(false);
    this.isResizingDrawer.set(false);
  }

  startDrawerResize(): void {
    this.isResizingDrawer.set(true);
  }

  setDrawerWidth(nextPx: number, maxPx: number = 960): void {
    const minPx = 420;
    const effectiveMax = Math.max(minPx, maxPx);
    const clamped = Math.round(Math.min(effectiveMax, Math.max(minPx, nextPx)));
    this.drawerWidth.set(clamped);
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem('pf-drawer-w', String(clamped));
      }
    } catch {
      // Ignore storage access errors
    }
  }

  stepDrawerWidth(deltaPx: number, maxPx: number = 960): void {
    this.setDrawerWidth(this.drawerWidth() + deltaPx, maxPx);
  }

  resetDrawerWidth(): void {
    this.setDrawerWidth(560, 960);
  }

  endDrawerResize(): void {
    this.isResizingDrawer.set(false);
  }

  private loadInitialDrawerWidth(): number {
    try {
      if (typeof localStorage !== 'undefined') {
        const saved = Number(localStorage.getItem('pf-drawer-w'));
        if (Number.isFinite(saved) && saved >= 420 && saved <= 1200) {
          return Math.round(saved);
        }
      }
    } catch {
      // Ignore storage access errors
    }
    return 560;
  }

  selectTreeNode(idx: number): void {
    this.selectedTreeNode.set(idx);
  }

  toggleBlastFile(idx: number): void {
    this.blastOpenMap.update((curr) => ({ ...curr, [idx]: !curr[idx] }));
  }

  showAllBlastFiles(): void {
    this.blastShowAll.set(true);
  }

  toggleAllBlastFiles(): void {
    const sel = this.selectedPrId();
    const pr = this.prs().find((x) => x.id === sel);
    if (!pr) return;
    const all = this.extractBlasts(pr);
    const openMap = this.blastOpenMap();
    const allOpen = this.blastShowAll() && all.every((_, i) => Boolean(openMap[i]));
    if (allOpen) {
      this.blastOpenMap.set({ 0: true });
      this.blastShowAll.set(false);
      return;
    }
    const next: Record<number, boolean> = {};
    all.forEach((_, i) => {
      next[i] = true;
    });
    this.blastOpenMap.set(next);
    this.blastShowAll.set(true);
  }

  handleEscapeKey(): void {
    const sel = this.selectedPrId();
    if (!sel) return;
    const key = `merge:${sel}`;
    const view = this.cancellation.getView(this.cancellation.actionsState(), key);
    if (view.running) {
      this.cancellation.cancel(key);
    } else {
      this.closeDrawer();
    }
  }

  cancelAction(key: string): void {
    this.cancellation.cancel(key);
  }

  async approvePr(prId: number, delayMs: number = 1200): Promise<void> {
    const pr = this.prs().find((x) => x.id === prId);
    if (!pr) return;
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: 'Aprovando PR...',
          execute: async (opts) => {
            await this.waitWithSignal(delayMs, opts.signal);
            await this.prGateway?.approvePullRequest(prId, opts);
          },
        },
      ],
      () => {
        this.markApproved(pr);
        return null;
      },
      () => 'Cancelado. Nada foi enviado ao GitHub.',
    );
  }

  async mergePr(prId: number, delayMs: number = 1600): Promise<void> {
    const pr = this.prs().find((x) => x.id === prId);
    if (!pr) return;
    let mergedSha = 'a1b2c3d';
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: 'Mesclando via squash...',
          execute: async (opts) => {
            await this.waitWithSignal(delayMs, opts.signal);
            const res = await this.prGateway?.mergePullRequest(prId, opts);
            if (res?.sha) {
              mergedSha = res.sha;
            }
          },
        },
      ],
      () => {
        this.markMerged(pr);
        return `Mesclado via squash em ${mergedSha}.`;
      },
      () => 'Cancelado. O PR continua aberto e aprovado.',
    );
  }

  async silencePr(prId: number, badge: string, delayMs: number = 1000): Promise<void> {
    const condition = badge.toLowerCase().includes('ci') ? 'UNTIL_CI_GREEN' : 'UNTIL_NEXT_COMMIT';
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: 'Silenciando...',
          execute: async (opts) => {
            await this.waitWithSignal(delayMs, opts.signal);
            await this.prGateway?.snoozePullRequest(prId, condition, opts);
          },
        },
      ],
      () => {
        this.silencedMap.update((curr) => ({ ...curr, [prId]: badge }));
        return 'PR silenciado.';
      },
      () => 'Cancelado. O PR continua na sua fila.',
    );
  }

  async commentOnPr(prId: number, command: string, delayMs: number = 1000): Promise<void> {
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: `Enviando ${command}...`,
          execute: async (opts) => {
            await this.waitWithSignal(delayMs, opts.signal);
            await this.prGateway?.commentOnPullRequest(prId, command, opts);
          },
        },
      ],
      () => `Comentário enviado: ${command}.`,
      () => 'Cancelado. Nada foi enviado ao GitHub.',
    );
  }

  async runSync(delayMs: number = 2400): Promise<void> {
    const count = this.repos().length;
    await this.cancellation.run(
      'sync',
      [
        {
          label: `Sincronizando ${count} repositórios...`,
          execute: async (opts) => {
            await this.waitWithSignal(delayMs, opts.signal);
            await this.loadWorkspace(opts);
          },
        },
      ],
      () => 'Sincronizado agora.',
      () => 'Sincronização cancelada.',
    );
  }

  async runBatchApprove(stepDelayMs: number = 700): Promise<void> {
    const list = this.safeBatchSummary().safePending;
    if (list.length === 0) return;
    const total = list.length;
    this.lastBatchTotal = total;

    await this.cancellation.run(
      'batch',
      list.map((pr, idx) => ({
        label: `Aprovando ${idx + 1} de ${total}...`,
        pct: ((idx + 1) / total) * 100,
        execute: async (opts: Required<ActionRequestOptions>) => {
          await this.waitWithSignal(stepDelayMs, opts.signal);
          await this.prGateway?.approvePullRequest(pr.id, opts);
          this.markApproved(pr);
        },
      })),
      () => null,
      (idx) =>
        idx === 0
          ? 'Lote cancelado. Nenhum PR foi aprovado.'
          : `Lote cancelado. ${this.plural(idx, 'PR aprovado', 'PRs aprovados')}, ${total - idx} sem aprovação.`,
    );
  }

  async runBatchMerge(stepDelayMs: number = 1100): Promise<void> {
    const list = this.safeBatchSummary().safeApproved;
    if (list.length === 0) return;
    const total = list.length;
    this.lastBatchTotal = total;

    await this.cancellation.run(
      'batch',
      list.map((pr, idx) => ({
        label: `Mesclando ${idx + 1} de ${total}...`,
        pct: ((idx + 1) / total) * 100,
        execute: async (opts: Required<ActionRequestOptions>) => {
          await this.waitWithSignal(stepDelayMs, opts.signal);
          await this.prGateway?.mergePullRequest(pr.id, opts);
          this.markMerged(pr);
        },
      })),
      () => `${total} PRs mesclados via squash.`,
      (idx) =>
        idx === 0
          ? 'Lote cancelado. Nenhum PR foi mesclado.'
          : `Lote cancelado. ${this.plural(idx, 'PR mesclado', 'PRs mesclados')}, ${total - idx} continuam abertos.`,
    );
  }

  private markApproved(pr: PullRequestItem): void {
    this.approvedMap.update((curr) => ({ ...curr, [pr.id]: true }));
    this.appendTimelineEvent('ok', `Você aprovou #${pr.id}`, pr.repo, pr.id);
  }

  private markMerged(pr: PullRequestItem): void {
    this.mergedMap.update((curr) => ({ ...curr, [pr.id]: true }));
    this.appendTimelineEvent('ok', `Você mesclou #${pr.id} via squash`, pr.repo, pr.id);
  }

  private appendTimelineEvent(
    kind: TimelineEventItem['kind'],
    text: string,
    repo: string,
    pr?: number,
  ): void {
    this.events.update((curr) => [{ key: Date.now() + Math.random(), t: Date.now(), kind, text, repo, pr, fresh: true }, ...curr]);
    if (this.view() !== 'timeline') {
      this.unreadEvents.update((n) => n + 1);
    }
  }

  private effectiveCi(pr: PullRequestItem): { ci: CiState; ciText: string } {
    return this.ciOverrides()[pr.id] ?? { ci: pr.ci, ciText: pr.ciText };
  }

  private buildInboxCard(pr: PullRequestItem): InboxCardViewModel {
    const silenced = this.silencedMap()[pr.id];
    const ci = this.effectiveCi(pr).ci;
    const selected = this.selectedPrId();
    return {
      id: pr.id,
      repo: pr.repo,
      title: pr.title,
      sel: selected === pr.id ? 'is-sel' : '',
      authorLabel: pr.author.startsWith('@') ? pr.author : `@${pr.author}`,
      ciLabel: { ok: 'CI Verde', run: 'CI Pendente', fail: 'CI Falhando' }[ci],
      ciTextCls: { ok: 'pf-ci-stable', run: 'pf-ci-warn', fail: 'pf-ci-fail' }[ci],
      hasPill: Boolean(pr.ownPill),
      ownPill: pr.ownPill,
      hasClaim: Boolean(pr.claim),
      claim: pr.claim,
      badge: silenced ?? pr.muteBadge ?? '✅ Você aprovou',
      badgeTip: silenced
        ? 'Silenciado por você. Volta à fila quando a condição for atendida.'
        : pr.muteTip ?? 'Sua aprovação foi registrada. O PR ainda não foi mesclado.',
    };
  }

  private extractBlasts(pr: PullRequestItem): readonly BlastFileItem[] {
    if (pr.blasts) {
      return pr.blasts;
    }
    if (pr.blast) {
      return [{ file: pr.blast.file, kind: 'prod', uses: 1, snippets: [pr.blast] }];
    }
    return [];
  }

  private buildDependabotDetail(pr: PullRequestItem) {
    const tier = { baixo: '🟢 Baixo Risco', medio: '🟡 Médio Risco', alto: '🔴 Alto Risco' }[pr.verdict];
    const bump = computeSemverBumpLabel(pr.from ?? '0.0.0', pr.to ?? '0.0.0');
    const repoShort = pr.repo.split('/')[1] ?? pr.repo;
    const nodes: { label: string; info: string; target?: boolean }[] = [
      { label: `📦 ${repoShort} (root)`, info: 'Raiz do projeto, definida no package.json.' },
      ...(pr.via ?? []),
      {
        label: `🎯 ${pr.pkg} ${pr.from} → ${pr.to} (${bump} Bump)`,
        info: 'Pacote que esta atualização altera.',
        target: true,
      },
    ];
    const selectedNode = this.selectedTreeNode() ?? nodes.length - 1;
    const allBlasts = this.extractBlasts(pr);
    const showAll = this.blastShowAll();
    const visible = showAll ? allBlasts : allBlasts.slice(0, 5);
    const hidden = showAll ? [] : allBlasts.slice(5);
    const prodCount = allBlasts.filter((f) => f.kind === 'prod').length;
    const testCount = allBlasts.length - prodCount;
    const breakCount = allBlasts.filter((f) => f.breaks).length;
    const openMap = this.blastOpenMap();
    const allOpen = hidden.length === 0 && allBlasts.length > 0 && allBlasts.every((_, i) => Boolean(openMap[i]));

    const blasts: DrawerBlastFileViewModel[] = visible.map((f, idx) => {
      const isOpen = Boolean(openMap[idx]);
      const lines: RenderedDiffLine[] = [];
      f.snippets.forEach((sn, k) => {
        if (k > 0) {
          lines.push({ cls: 'pf-line-hdr', n: '', tokens: [{ s: '⋯', cls: 'pf-tk-hdr' }] });
        }
        sn.lines.forEach((code, c) => {
          lines.push({
            n: String(sn.start + c),
            cls: sn.start + c === sn.hit ? 'pf-line-hit' : '',
            tokens: this.tokenizeLine('  ' + code, 'ctx'),
          });
        });
      });

      return {
        index: idx,
        file: f.file,
        cls: `${f.kind === 'test' ? 'pf-blast-test ' : ''}${isOpen ? 'pf-blast-open' : ''}`.trim(),
        isOpen,
        expanded: isOpen ? 'true' : 'false',
        chevCls: isOpen ? 'is-open' : '',
        hasUses: f.uses > 1,
        usesLabel: `${f.uses} usos`,
        breaks: Boolean(f.breaks),
        lines,
      };
    });

    return {
      rvHead: `${tier} (Score ${pr.score ?? 0}/100)`,
      rvDetail: pr.rvDetail ?? '',
      tree: nodes.map((n, idx) => ({
        index: idx,
        label: n.label,
        hasLink: idx > 0,
        pressed: idx === selectedNode ? 'true' : 'false',
        cls: `${idx === selectedNode ? 'is-on ' : ''}${n.target ? `pf-node-${pr.verdict}` : ''}`.trim(),
      })),
      treeInfo: nodes[selectedNode]?.info ?? '',
      blastTitle: `Raio de Impacto no Código — ${this.plural(allBlasts.length, 'arquivo afetado', 'arquivos afetados')}`,
      blastSummary: `${this.plural(prodCount, 'em produção', 'em produção')} · ${this.plural(testCount, 'em testes', 'em testes')} · ${this.plural(breakCount, 'usa API que quebra', 'usam API que quebra')}`,
      hasSummary: allBlasts.length > 1,
      hasBlast: allBlasts.length > 0,
      noBlast: allBlasts.length === 0,
      blasts,
      hasBlastFoot: allBlasts.length > 1,
      hasMore: hidden.length > 0,
      moreLabel:
        `Ver mais ${this.plural(hidden.length, 'arquivo', 'arquivos')}` +
        (hidden.every((f) => f.kind === 'test') ? ' (testes)' : ''),
      allLabel: allOpen ? 'Recolher todos' : 'Expandir todos',
      note: pr.note ?? '',
      noteCls: pr.noteWarn ? 'pf-alert-warn' : '',
    };
  }

  private renderDiffLines(hunk: readonly DiffHunkLine[]): readonly RenderedDiffLine[] {
    let lineNum = 1;
    return hunk.map((l) => {
      if (l.t === 'hdr') {
        const m = /\+(\d+)/.exec(l.s);
        lineNum = m ? Number(m[1]) : 1;
        return { cls: 'pf-line-hdr', n: '', tokens: [{ s: l.s, cls: 'pf-tk-hdr' }] };
      }
      const n = l.t === 'del' ? '' : String(lineNum++);
      return { cls: `pf-line-${l.t}`, n, tokens: this.tokenizeLine(l.s, l.t) };
    });
  }

  private tokenizeLine(s: string, t: 'ctx' | 'add' | 'del'): readonly SyntaxToken[] {
    const out: SyntaxToken[] = [
      { s: s.slice(0, 2), cls: t === 'add' ? 'pf-tk-add' : t === 'del' ? 'pf-tk-del' : '' },
    ];
    const code = s.slice(2);
    const re =
      /(\/\/.*$)|('[^']*'|"[^"]*"|`[^`]*`)|(@[A-Za-z]+)|\b(import|export|class|const|let|return|if|else|from|readonly|private|public|protected|async|await|function|new|this|true|false|null|void|constructor)\b|\b([A-Z][A-Za-z0-9_]*)\b|\b(\d+)\b/g;
    let last = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(code)) !== null) {
      if (m.index > last) {
        out.push({ s: code.slice(last, m.index), cls: '' });
      }
      const cls = m[1]
        ? 'pf-tk-com'
        : m[2]
          ? 'pf-tk-str'
          : m[3] || m[4]
            ? 'pf-tk-kw'
            : m[5]
              ? 'pf-tk-type'
              : 'pf-tk-num';
      out.push({ s: m[0], cls });
      last = m.index + m[0].length;
    }
    if (last < code.length) {
      out.push({ s: code.slice(last), cls: '' });
    }
    return out;
  }

  private plural(n: number, singular: string, pluralForm: string): string {
    return `${n} ${n === 1 ? singular : pluralForm}`;
  }

  private waitWithSignal(ms: number, signal: AbortSignal): Promise<void> {
    return new Promise((resolve, reject) => {
      if (signal.aborted) {
        reject(new DOMException('Cancelado', 'AbortError'));
        return;
      }
      const t = setTimeout(resolve, ms);
      signal.addEventListener(
        'abort',
        () => {
          clearTimeout(t);
          reject(new DOMException('Cancelado', 'AbortError'));
        },
        { once: true },
      );
    });
  }
}
