import { DestroyRef, Injectable, computed, inject, signal } from '@angular/core';
import {
  ActionRequestOptions,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
} from '../../domain/gateways/workspace.gateways';
import {
  BlastFileItem,
  ChangedFileItem,
  CiState,
  DiffHunkLine,
  I18nText,
  MergeState,
  PerspectiveView,
  PullRequestItem,
  RepoInfo,
  RiskVerdictTier,
  SeatFilterType,
  SseConnectionState,
  TimelineEventItem,
  computeSemverBumpLabel,
  isActionableTurn,
  isSafeBatchCandidate,
} from '../../domain/models/workspace.models';
import { ActionCancellationManager, ActionViewModel } from '../use-cases/action-cancellation.manager';
import { ActivityIconName, ActivityTone, activityIconFor } from '../../domain/models/activity.models';
import { ActionError } from '../../domain/models/action-error.models';
import { LocaleStore } from './locale.store';

/** i18n keys for the GitHub mergeable states that stop a merge; the others do not block. */
const MERGE_BLOCKER_KEYS: Readonly<Partial<Record<MergeState, string>>> = {
  conflict: 'drawer.blocked.conflict',
  behind: 'drawer.blocked.behind',
  blocked: 'drawer.blocked.protection',
  draft: 'drawer.blocked.draft',
};

export interface TimelineItemViewModel extends Omit<TimelineEventItem, 'text'> {
  readonly text: string;
  readonly time: string;
  readonly icon: ActivityIconName;
  readonly tone: ActivityTone;
  readonly cls: string;
  readonly noPr: boolean;
}

export interface TimelineGroupViewModel {
  readonly label: string;
  readonly items: TimelineItemViewModel[];
}

export type ChangedFilesState = 'loading' | 'ready' | 'error';

/** One changed file in the drawer preview, collapsible like the Dependabot affected-code cards. */
export interface ChangedFileCardViewModel {
  readonly index: number;
  readonly path: string;
  readonly deltaAdd: string;
  readonly deltaDel: string;
  readonly expanded: boolean;
  readonly owned: boolean;
  readonly hasDiff: boolean;
  readonly lines: readonly RenderedDiffLine[];
}

const RISK_LABEL_KEYS: Readonly<Record<RiskVerdictTier, string>> = { baixo: 'risk.baixo', medio: 'risk.medio', alto: 'risk.alto' };
const RISK_TIER_KEYS: Readonly<Record<RiskVerdictTier, string>> = { baixo: 'risk.tier.baixo', medio: 'risk.tier.medio', alto: 'risk.tier.alto' };

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
  /** GitHub's PR number, shown as #N. */
  readonly number: number;
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
  /** The repository was checked and has no CODEOWNERS file. */
  readonly noCodeowners: boolean;
  readonly stream: readonly SeatStreamRowViewModel[];
}

export interface InboxCardViewModel {
  readonly id: number;
  readonly number: number;
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
  readonly number: number;
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
  readonly number: number;
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
  readonly verdict: RiskVerdictTier | null;
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
  readonly hasFiles: boolean;
  readonly files: readonly ChangedFileCardViewModel[];
  readonly filesState: ChangedFilesState;
  readonly hasVerdict: boolean;
  readonly showIdle: boolean;
  readonly showMerged: boolean;
  readonly showSecondary: boolean;
  readonly blocked: boolean;
  readonly blockedCls: string;
  readonly blockedText: string;
  readonly showApprove: boolean;
  /** A Dependabot PR with no analysis yet: the user can ask for it. */
  readonly showAnalyze: boolean;
  /** Why the user cannot approve this PR (they wrote it), or ''. */
  readonly ownNote: string;
  readonly approvedMine: boolean;
  readonly mergeCls: string;
  readonly mergeDis: string;
  readonly mergeTip: string;
  readonly busyDis: string;
  readonly act: ActionViewModel;
}

const WORKSPACE_REFRESH_DEBOUNCE_MS = 250;

@Injectable({ providedIn: 'root' })
export class WorkspaceStore {
  private readonly destroyRef = inject(DestroyRef);
  private readonly prGateway = inject(PULL_REQUEST_GATEWAY, { optional: true });
  private readonly sseGateway = inject(LIVE_EVENT_GATEWAY, { optional: true });
  private readonly cancellation = inject(ActionCancellationManager);
  private readonly locale = inject(LocaleStore);

  readonly repos = signal<readonly RepoInfo[]>([]);
  readonly prs = signal<readonly PullRequestItem[]>([]);
  readonly view = signal<PerspectiveView>('seats');
  readonly selectedPrId = signal<number | null>(null);
  readonly closingDrawer = signal(false);
  readonly drawerWidth = signal(this.loadInitialDrawerWidth());
  readonly isResizingDrawer = signal(false);
  readonly seatFilter = signal<SeatFilterType>('all');
  readonly showMuted = signal(true);
  readonly mergedMap = signal<Record<number, boolean>>({});
  readonly approvedMap = signal<Record<number, boolean>>({});
  readonly silencedMap = signal<Record<number, I18nText>>({});
  readonly ciOverrides = signal<Record<number, { ci: CiState; ciText: I18nText }>>({});
  readonly events = signal<readonly TimelineEventItem[]>([]);
  readonly unreadEvents = signal(0);
  readonly pausedTimeline = signal(false);
  readonly freshRepo = signal<string | null>(null);
  readonly connectionState = signal<SseConnectionState>('conectado');
  readonly selectedTreeNode = signal<number | null>(null);
  readonly blastOpenMap = signal<Record<number, boolean>>({ 0: true });
  readonly blastShowAll = signal(false);
  readonly changedFileOpenMap = signal<Record<number, boolean>>({ 0: true });
  /** Files loaded from GitHub for the open PR (see loadChangedFiles). */
  readonly changedFiles = signal<{
    readonly prId: number;
    readonly state: ChangedFilesState;
    readonly files: readonly ChangedFileItem[];
  } | null>(null);
  private filesLoad: AbortController | null = null;
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
  startLiveSession(viewerLogin?: string): void {
    if (viewerLogin !== undefined) {
      this.viewerLogin.set(viewerLogin);
    }
    if (this.disconnectLive) {
      return;
    }
    void this.loadWorkspace();
    this.disconnectLive =
      this.sseGateway?.connect(
        (evt) => {
          if (evt.id) {
            const id = evt.id;
            this.liveEventIds.update((ids) => new Set(ids).add(id));
          }
          if (!this.pausedTimeline()) {
            this.appendTimelineEvent(evt);
          }
          if (evt.refreshWorkspace) {
            this.scheduleWorkspaceRefresh();
          }
        },
        (state) => this.onConnectionState(state),
      ) ?? (() => undefined);
  }

  private streamOpenedBefore = false;
  private refreshTimer: ReturnType<typeof setTimeout> | null = null;

  /**
   * Reload the workspace shortly after live changes, coalescing a burst (a batch merge, many
   * webhooks) into one request instead of one per event (ADR-0006).
   */
  private scheduleWorkspaceRefresh(): void {
    if (this.refreshTimer !== null || !this.disconnectLive) {
      return;
    }
    this.refreshTimer = setTimeout(() => {
      this.refreshTimer = null;
      void this.loadWorkspace();
      // A live change may be a new commit on the open PR: reload its files too (cached by head).
      const open = this.selectedPrId();
      if (open !== null && this.changedFiles()?.prId === open) {
        void this.loadChangedFiles(open);
      }
    }, WORKSPACE_REFRESH_DEBOUNCE_MS);
  }

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
    if (this.refreshTimer !== null) {
      clearTimeout(this.refreshTimer);
      this.refreshTimer = null;
    }
    disconnect?.();
  }

  async loadWorkspace(options?: ActionRequestOptions): Promise<void> {
    const dataset = await this.prGateway?.fetchWorkspace?.(options);
    if (!dataset) {
      return;
    }
    this.repos.set(dataset.repos);
    this.prs.set(dataset.prs);
    this.events.set(dataset.events);
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
      // "Stable" needs evidence: when no open PR ever reported a check, the repository has no CI to show.
      const noCi = repoPrs.length > 0 && repoPrs.every((p) => this.effectiveCi(p).ci === 'none');
      const ciState: CiState = fails > 0 ? 'fail' : runs > 0 ? 'run' : noCi ? 'none' : 'ok';

      const stream: SeatStreamRowViewModel[] = repoPrs
        .slice()
        .sort((a, b) => a.rank - b.rank)
        .slice(0, 3)
        .map((p) => {
          const isTurn = isActionableTurn(p, silenced, approved);
          const showTurn = isTurn && !p.dep;
          const mine = Boolean(approved[p.id]);
          const isApproved = Boolean(p.approvedBy || (mine && !p.dep));
          const tier = this.t(p.verdict ? RISK_TIER_KEYS[p.verdict] : 'risk.tier.none');
          const dev = p.depType === 'devDependencies';
          const vLabel = p.verdict ? this.t(RISK_LABEL_KEYS[p.verdict]) : '';
          const safe = isSafeBatchCandidate(p, this.effectiveCi(p).ci);

          return {
            id: p.id,
            number: p.number,
            title: p.seatTitle ?? p.title,
            sel: selected === p.id ? 'is-sel' : '',
            dim: p.approvedBy && !mine ? 'is-dim' : '',
            showTurn,
            hasOwn: Boolean(p.ownFiles),
            ownLabel: this.n('seats.ownFiles', p.ownFiles ?? 0),
            showRisk: p.verdict !== null && !showTurn && !isApproved && (!mine || Boolean(p.dep)),
            vCls: `pf-badge-${p.verdict}`,
            riskTip: p.dep
              ? dev
                ? this.t('seats.riskTip.dev')
                : this.t('seats.riskTip.prod')
              : this.t('seats.riskTip.human'),
            riskLabel: p.dep ? `${tier}${dev ? ' (Dev)' : ' (Prod)'}` : vLabel,
            isApproved: isApproved || mine,
            approvedTip: mine
              ? this.t('seats.approvedTip.mine')
              : this.t('seats.approvedTip.team'),
            approvedLabel: mine ? this.t('seats.approvedByYou') : this.t('seats.approvedByTeam', { login: p.approvedBy ?? '' }),
            hasQuick: safe && (mine || !this.isOwnPr(p)),
            act: this.cancellation.getView(actionsMap, `merge:${p.id}`),
            quickTip: mine ? this.t('seats.quickTip.merge') : this.t('seats.quickTip.approve'),
            quickLabel: mine ? 'Squash' : this.t('seats.quick.approve'),
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
          none: 'pf-dot',
        }[ciState],
        ciCls: { fail: 'pf-ci-fail', run: 'pf-ci-warn', ok: 'pf-ci-stable', none: 'pf-ci-none' }[ciState],
        ciLabel: {
          fail: this.n('seats.ci.failing', fails),
          run: this.n('seats.ci.running', runs),
          ok: this.t('seats.ci.stable'),
          none: this.locale.translate('ci.none'),
        }[ciState],
        ciTip: {
          fail: this.t('seats.ci.failTip'),
          run: this.t('seats.ci.runTip'),
          ok: this.t('seats.ci.okTip'),
          none: this.locale.translate('seats.ci.noneTip'),
        }[ciState],
        turn: turnCount,
        turnCls: turnCount > 0 ? 'pf-turn' : 'pf-badge-zero',
        deps: deps.length,
        depCls: deps.length === 0 ? 'pf-badge-zero' : risky ? 'pf-badge-medio' : 'pf-badge-baixo',
        openLabel: this.n('seats.openPrs', repoPrs.length),
        isEmpty: repoPrs.length === 0,
        accessKey,
        noCodeowners: r.codeowners === 'absent',
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
      { id: 'all' as const, label: this.t('seats.filter.all', { count: all.length }), active: current === 'all' },
      { id: 'turn' as const, label: this.t('seats.filter.turn', { count: turnSeats.length }), active: current === 'turn' },
      { id: 'deps' as const, label: this.t('seats.filter.deps', { count: depSeats.length }), active: current === 'deps' },
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
    const tiers: readonly [RiskVerdictTier | null, string][] = [
      ['alto', this.t('radar.group.alto')],
      ['baixo', this.t('radar.group.baixo')],
      ['medio', this.t('radar.group.medio')],
      [null, this.t('radar.group.none')],
    ];

    return tiers
      .map(([verdict, label]) => {
        const items: RadarCardViewModel[] = deps
          .filter((p) => p.verdict === verdict)
          .map((p) => {
            const ci = this.effectiveCi(p).ci;
            return {
              id: p.id,
              number: p.number,
              repo: p.repo,
              radarTitle: p.radarTitle ?? p.title,
              radarMeta: this.locale.text(p.radarMeta),
              metaTip:
                p.via && p.via.length > 0
                  ? this.t('radar.metaTip.transitive')
                  : p.depType === 'devDependencies'
                    ? this.t('radar.metaTip.dev')
                    : this.t('radar.metaTip.prod'),
              ciShort: { ok: this.t('radar.ci.ok'), run: this.t('radar.ci.run'), fail: this.t('radar.ci.fail'), none: this.t('ci.none') }[ci],
              ciTextCls: { ok: 'pf-ci-ok', run: 'pf-ci-warn', fail: 'pf-ci-fail', none: 'pf-ci-none' }[ci],
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
      title: this.t('radar.batch.title', { count: batchN }),
      canApprove: safePending.length > 0,
      approveLabel: safeApproved.length > 0 ? this.t('radar.batch.approveRest', { count: safePending.length }) : this.t('radar.batch.approveAll', { count: batchN }),
      mergeDis: safeApproved.length === 0 ? 'true' : 'false',
      mergeCls: safePending.length === 0 ? 'pf-btn-primary' : '',
      mergeLabel:
        safeApproved.length > 0 && safePending.length > 0
          ? this.t('radar.batch.mergeApproved', { count: safeApproved.length })
          : this.t('radar.batch.mergeAll', { count: batchN }),
      safeApproved,
      safePending,
    };
  });

  /** "all": every followed repository's activity; "mine": only what the signed-in user did. */
  readonly timelineScope = signal<'all' | 'mine'>('all');
  /** Limit the timeline to one repository of the Seats, or null for all. */
  readonly timelineRepo = signal<string | null>(null);
  readonly timelineRepoOptions = computed(() => this.repos().map((r) => r.name).sort());
  private readonly viewerLogin = signal('');
  /** Ids of activity received live, still highlighted after the workspace reloads. */
  private readonly liveEventIds = signal<ReadonlySet<string>>(new Set());

  setTimelineScope(scope: 'all' | 'mine'): void {
    this.timelineScope.set(scope);
  }

  setTimelineRepo(repo: string | null): void {
    this.timelineRepo.set(repo || null);
  }

  readonly timelineGroups = computed(() => {
    const merged = this.mergedMap();
    const scope = this.timelineScope();
    const repo = this.timelineRepo();
    const viewer = this.viewerLogin().toLowerCase();
    const liveIds = this.liveEventIds();
    const now = new Date();
    const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
    const startOfYesterday = startOfToday - 24 * 60 * 60_000;

    const visible = this.events()
      .filter((ev) => !repo || ev.repo === repo)
      .filter((ev) => scope === 'all' || (viewer !== '' && (ev.actor ?? '').toLowerCase() === viewer))
      .slice()
      .sort((a, b) => b.t - a.t);

    const groups: TimelineGroupViewModel[] = [];
    for (const ev of visible) {
      const mins = (now.getTime() - ev.t) / 60_000;
      const label = this.t(
        mins <= 5
          ? 'timeline.group.now'
          : mins <= 60
            ? 'timeline.group.lastHour'
            : ev.t >= startOfToday
              ? 'timeline.group.today'
              : ev.t >= startOfYesterday
                ? 'timeline.group.yesterday'
                : 'timeline.group.older',
      );
      let group = groups.find((g) => g.label === label);
      if (!group) {
        group = { label, items: [] };
        groups.push(group);
      }
      const d = new Date(ev.t);
      const hhmm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
      const time = ev.t >= startOfYesterday ? hhmm : `${String(d.getDate()).padStart(2, '0')}/${String(d.getMonth() + 1).padStart(2, '0')} ${hhmm}`;
      const { icon, tone } = activityIconFor(ev.type, ev.kind);
      group.items.push({
        ...ev,
        text: this.locale.text(ev.text),
        time,
        icon,
        tone,
        cls: ev.fresh || (ev.id !== undefined && liveIds.has(ev.id)) ? 'is-new' : '',
        noPr: Boolean(!ev.pr || merged[ev.pr]),
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
    const own = this.isOwnPr(pr);
    const act = this.cancellation.getView(this.cancellation.actionsState(), `merge:${pr.id}`);
    const dotCi = {
      ok: 'pf-dot pf-dot-ok',
      run: 'pf-dot pf-dot-warn pf-dot-pulse',
      fail: 'pf-dot pf-dot-fail',
      none: 'pf-dot',
    }[ci.ci];
    const dotOwners = {
      ok: 'pf-dot pf-dot-ok',
      voce: 'pf-dot pf-dot-turn',
      others: 'pf-dot',
      none: 'pf-dot',
      unknown: 'pf-dot',
    }[pr.owners];
    const ciText = ci.ci === 'none' ? this.locale.translate('drawer.ci.none') : this.locale.text(ci.ciText);
    const ownersText =
      pr.owners === 'none'
        ? this.locale.translate('drawer.codeowners.none')
        : pr.owners === 'unknown'
          ? this.locale.translate('drawer.codeowners.unknown')
          : this.locale.text(pr.ownersText);

    // Only GitHub decides what blocks a merge (mergeable_state: conflicts, out of date, branch
    // protection with its required reviews and checks, draft). CI and CODEOWNERS are signals.
    const blockedText = pr.merge && MERGE_BLOCKER_KEYS[pr.merge] ? this.locale.translate(MERGE_BLOCKER_KEYS[pr.merge]!) : '';

    const vLabel = pr.verdict ? this.t(RISK_LABEL_KEYS[pr.verdict]) : '';
    const openFiles = this.changedFileOpenMap();
    const loaded = this.changedFiles();
    const filesState: ChangedFilesState = loaded?.prId === pr.id ? loaded.state : 'ready';
    const sourceFiles = loaded?.prId === pr.id ? loaded.files : pr.files;
    const files: ChangedFileCardViewModel[] = sourceFiles.map((f, index) => ({
      index,
      path: f.path,
      deltaAdd: `+${f.additions}`,
      deltaDel: `-${f.deletions}`,
      expanded: Boolean(openFiles[index]),
      owned: Boolean(f.owned),
      hasDiff: f.hunk.length > 0,
      lines: this.renderDiffLines(f.hunk),
    }));
    const signals = [
      { k: 'CI', v: ciText, dot: dotCi },
      { k: 'CODEOWNERS', v: ownersText, dot: dotOwners },
      ...(pr.size ? [{ k: 'Tamanho', v: pr.size, dot: 'pf-dot' }] : []),
    ];
    const depVm = pr.dep ? this.buildDependabotDetail(pr) : null;

    return {
      id: pr.id,
      number: pr.number,
      repo: pr.repo,
      title: pr.title,
      ghUrl: `https://github.com/${pr.repo}/pull/${pr.id}`,
      focused: Boolean(pr.focusRule),
      banner: pr.focusRule
        ? this.t('drawer.banner.focus', { rule: pr.focusRule })
        : '',
      general: Boolean(!pr.focusRule && !pr.dep),
      vCls: pr.verdict ? `pf-badge-${pr.verdict}` : '',
      hasVerdict: pr.verdict !== null,
      vLabel,
      why: this.locale.text(pr.why),
      signals,
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
      hasFiles: files.length > 0,
      files,
      filesState,
      showIdle: act.idle && !merged,
      showMerged: act.idle && merged,
      showSecondary: !merged && !act.hasNote,
      blocked: Boolean(blockedText),
      blockedCls: 'pf-blocked',
      blockedText,
      showAnalyze: pr.dep === true && pr.verdict === null && Boolean(pr.pkg),
      showApprove: !mine && !own,
      ownNote: own ? this.t('drawer.ownPr') : '',
      approvedMine: mine,
      mergeCls: (mine || pr.approvedBy) && !blockedText ? 'pf-btn-primary' : '',
      mergeDis: blockedText ? 'true' : 'false',
      mergeTip: blockedText || this.t('drawer.mergeTip'),
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
    this.changedFileOpenMap.set({ 0: true });
    void this.loadChangedFiles(prId);
  }

  /**
   * Loads the PR's changed files from GitHub, cancelling any load still in flight (ADR-0007).
   * Dependabot PRs show their impact analysis instead, so nothing is loaded for them.
   */
  async loadChangedFiles(prId: number): Promise<void> {
    this.filesLoad?.abort();
    this.filesLoad = null;
    const pr = this.prs().find((p) => p.id === prId);
    if (!pr || pr.dep || !this.prGateway?.fetchChangedFiles) {
      this.changedFiles.set(null);
      return;
    }
    const controller = new AbortController();
    this.filesLoad = controller;
    this.changedFiles.set({ prId, state: 'loading', files: [] });
    try {
      const files = await this.prGateway.fetchChangedFiles(pr.repo, pr.number, { signal: controller.signal });
      if (controller.signal.aborted) {
        return;
      }
      const firstOwned = files.findIndex((f) => f.owned);
      this.changedFileOpenMap.set({ [firstOwned >= 0 ? firstOwned : 0]: true });
      this.changedFiles.set({ prId, state: 'ready', files });
    } catch {
      if (!controller.signal.aborted) {
        this.changedFiles.set({ prId, state: 'error', files: [] });
      }
    }
  }

  closeDrawer(): void {
    this.filesLoad?.abort();
    this.filesLoad = null;
    this.changedFiles.set(null);
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

  toggleChangedFile(idx: number): void {
    this.changedFileOpenMap.update((curr) => ({ ...curr, [idx]: !curr[idx] }));
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

  /** Nobody approves a PR they wrote: GitHub refuses it, so it is not even offered. */
  private isOwnPr(pr: PullRequestItem): boolean {
    const viewer = this.viewerLogin().toLowerCase();
    return viewer !== '' && pr.author.replace(/^@/, '').toLowerCase() === viewer;
  }

  /** The note shown when an action fails: what happened, in the user's language. */
  private failureNote(err: unknown): string {
    if (err instanceof ActionError) {
      if (err.code === 'outcome_unknown' || err.code === 'conflict') {
        this.scheduleWorkspaceRefresh(); // GitHub's state may differ from ours: look again
      }
      return this.t(`actions.error.${err.code}`, { detail: err.detail });
    }
    return this.t('actions.error.unknown', { detail: '' });
  }

  async approvePr(prId: number): Promise<void> {
    const pr = this.prs().find((x) => x.id === prId);
    if (!pr) return;
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: this.t('actions.approving'),
          execute: async (opts) => {
            if (this.isOwnPr(pr)) {
              throw new ActionError('own_pull_request');
            }
            await this.prGateway?.approvePullRequest(prId, opts);
          },
        },
      ],
      () => {
        this.markApproved(pr);
        return null;
      },
      () => this.t('actions.canceled.nothingSent'),
      (err) => this.failureNote(err),
    );
  }

  async mergePr(prId: number): Promise<void> {
    const pr = this.prs().find((x) => x.id === prId);
    if (!pr) return;
    let mergedSha = '';
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: this.t('actions.merging'),
          execute: async (opts) => {
            const res = await this.prGateway?.mergePullRequest(prId, opts);
            if (res?.sha) {
              mergedSha = res.sha;
            }
          },
        },
      ],
      () => {
        this.markMerged(pr);
        return mergedSha ? this.t('actions.mergedSha', { sha: mergedSha }) : this.t('actions.merged');
      },
      () => this.t('actions.canceled.mergeKeepsOpen'),
      (err) => this.failureNote(err),
    );
  }

  /** Runs the Dependabot analysis now and reloads the workspace to show it. */
  async analyzePr(prId: number): Promise<void> {
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: this.t('actions.analyzing'),
          execute: async (opts) => {
            await this.prGateway?.analyzePullRequest?.(prId, opts);
            await this.loadWorkspace(opts);
          },
        },
      ],
      () => this.t('actions.analyzed'),
      () => this.t('actions.canceled.analyze'),
      (err) => this.failureNote(err),
    );
  }

  /** Snooze a PR until its next commit, until CI passes, or (Dependabot) out of the Radar. */
  async silencePr(prId: number, until: 'commit' | 'ci' | 'radar'): Promise<void> {
    const condition = until === 'ci' ? 'UNTIL_CI_GREEN' : 'UNTIL_NEXT_COMMIT';
    const badge: I18nText = { key: `silenced.${until}` };
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: this.t('actions.silencing'),
          execute: async (opts) => {
            await this.prGateway?.snoozePullRequest(prId, condition, opts);
          },
        },
      ],
      () => {
        this.silencedMap.update((curr) => ({ ...curr, [prId]: badge }));
        return this.t('actions.silenced');
      },
      () => this.t('actions.canceled.silence'),
      (err) => this.failureNote(err),
    );
  }

  async commentOnPr(prId: number, command: string): Promise<void> {
    await this.cancellation.run(
      `merge:${prId}`,
      [
        {
          label: this.t('actions.commenting', { command }),
          execute: async (opts) => {
            await this.prGateway?.commentOnPullRequest(prId, command, opts);
          },
        },
      ],
      () => this.t('actions.commented', { command }),
      () => this.t('actions.canceled.nothingSent'),
      (err) => this.failureNote(err),
    );
  }

  async runSync(): Promise<void> {
    const count = this.repos().length;
    await this.cancellation.run(
      'sync',
      [
        {
          label: this.n('actions.syncing', count),
          execute: async (opts) => {
            await this.loadWorkspace(opts);
          },
        },
      ],
      () => this.t('actions.synced'),
      () => this.t('actions.syncCanceled'),
    );
  }

  async runBatchApprove(): Promise<void> {
    const list = this.safeBatchSummary().safePending;
    if (list.length === 0) return;
    const total = list.length;
    this.lastBatchTotal = total;

    await this.cancellation.run(
      'batch',
      list.map((pr, idx) => ({
        label: this.t('actions.batch.approving', { current: idx + 1, total }),
        pct: ((idx + 1) / total) * 100,
        execute: async (opts: Required<ActionRequestOptions>) => {
          await this.prGateway?.approvePullRequest(pr.id, opts);
          this.markApproved(pr);
        },
      })),
      () => null,
      (idx) =>
        idx === 0
          ? this.t('actions.batch.approveCanceledNone')
          : this.n('actions.batch.approveCanceled', idx, { rest: total - idx }),
      (err) => this.failureNote(err),
    );
  }

  async runBatchMerge(): Promise<void> {
    const list = this.safeBatchSummary().safeApproved;
    if (list.length === 0) return;
    const total = list.length;
    this.lastBatchTotal = total;

    await this.cancellation.run(
      'batch',
      list.map((pr, idx) => ({
        label: this.t('actions.batch.merging', { current: idx + 1, total }),
        pct: ((idx + 1) / total) * 100,
        execute: async (opts: Required<ActionRequestOptions>) => {
          await this.prGateway?.mergePullRequest(pr.id, opts);
          this.markMerged(pr);
        },
      })),
      () => this.n('actions.batch.merged', total),
      (idx) =>
        idx === 0
          ? this.t('actions.batch.mergeCanceledNone')
          : this.n('actions.batch.mergeCanceled', idx, { rest: total - idx }),
      (err) => this.failureNote(err),
    );
  }

  private markApproved(pr: PullRequestItem): void {
    this.approvedMap.update((curr) => ({ ...curr, [pr.id]: true }));
    this.appendTimelineEvent({ kind: 'ok', type: 'pr.approved', actor: this.viewerLogin(), text: { key: 'timeline.local.approved', params: { pr: pr.number } }, repo: pr.repo, pr: pr.id, fresh: true });
  }

  private markMerged(pr: PullRequestItem): void {
    this.mergedMap.update((curr) => ({ ...curr, [pr.id]: true }));
    this.appendTimelineEvent({ kind: 'ok', type: 'pr.merged', actor: this.viewerLogin(), text: { key: 'timeline.local.merged', params: { pr: pr.number } }, repo: pr.repo, pr: pr.id, fresh: true });
  }

  private appendTimelineEvent(item: Omit<TimelineEventItem, 'key' | 't'>): void {
    this.events.update((curr) => [{ ...item, key: Date.now() + Math.random(), t: Date.now(), fresh: true }, ...curr]);
    if (this.view() !== 'timeline') {
      this.unreadEvents.update((n) => n + 1);
    }
  }

  private effectiveCi(pr: PullRequestItem): { ci: CiState; ciText: I18nText } {
    return this.ciOverrides()[pr.id] ?? { ci: pr.ci, ciText: pr.ciText };
  }

  private buildInboxCard(pr: PullRequestItem): InboxCardViewModel {
    const silenced = this.silencedMap()[pr.id];
    const ci = this.effectiveCi(pr).ci;
    const selected = this.selectedPrId();
    return {
      id: pr.id,
      number: pr.number,
      repo: pr.repo,
      title: pr.title,
      sel: selected === pr.id ? 'is-sel' : '',
      authorLabel: pr.author.startsWith('@') ? pr.author : `@${pr.author}`,
      ciLabel: { ok: this.t('inbox.ci.ok'), run: this.t('inbox.ci.run'), fail: this.t('inbox.ci.fail'), none: this.t('ci.none') }[ci],
      ciTextCls: { ok: 'pf-ci-stable', run: 'pf-ci-warn', fail: 'pf-ci-fail', none: 'pf-ci-none' }[ci],
      hasPill: Boolean(pr.ownPill),
      ownPill: this.locale.text(pr.ownPill),
      hasClaim: Boolean(pr.claim),
      claim: this.locale.text(pr.claim),
      badge: this.locale.text(silenced ?? pr.muteBadge ?? { key: 'inbox.badge.approvedByYou' }),
      badgeTip: silenced
        ? this.t('inbox.badgeTip.silenced')
        : this.locale.text(pr.muteTip ?? { key: 'seats.approvedTip.mine' }),
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
    const tier = this.t(pr.verdict ? RISK_TIER_KEYS[pr.verdict] : 'risk.tier.none');
    const bump = pr.from && pr.to ? computeSemverBumpLabel(pr.from, pr.to) : null;
    const repoShort = pr.repo.split('/')[1] ?? pr.repo;
    const nodes: { label: string; info: string; target?: boolean }[] = [
      { label: `📦 ${repoShort} (root)`, info: this.t('impact.root.info') },
      ...(pr.via ?? []).map((v) => ({ label: this.locale.text(v.label), info: this.locale.text(v.info) })),
      {
        label: pr.pkg ? `🎯 ${pr.pkg} ${pr.from} → ${pr.to}${bump ? ` (${bump} Bump)` : ''}` : `🎯 ${pr.title}`,
        info: this.t('impact.target.info'),
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
        usesLabel: this.n('impact.uses', f.uses),
        breaks: Boolean(f.breaks),
        lines,
      };
    });

    return {
      rvHead: pr.score !== undefined ? this.t('impact.rvHead', { tier, score: pr.score }) : tier,
      rvDetail: this.locale.text(pr.rvDetail),
      tree: nodes.map((n, idx) => ({
        index: idx,
        label: n.label,
        hasLink: idx > 0,
        pressed: idx === selectedNode ? 'true' : 'false',
        cls: `${idx === selectedNode ? 'is-on ' : ''}${n.target ? `pf-node-${pr.verdict}` : ''}`.trim(),
      })),
      treeInfo: nodes[selectedNode]?.info ?? '',
      blastTitle: this.n('impact.blast.title', allBlasts.length),
      blastSummary: [
        this.n('impact.blast.prod', prodCount),
        this.n('impact.blast.test', testCount),
        this.n('impact.blast.breaking', breakCount),
      ].join(' · '),
      hasSummary: allBlasts.length > 1,
      hasBlast: allBlasts.length > 0,
      noBlast: allBlasts.length === 0,
      blasts,
      hasBlastFoot: allBlasts.length > 1,
      hasMore: hidden.length > 0,
      moreLabel:
        this.n('impact.blast.more', hidden.length) + (hidden.every((f) => f.kind === 'test') ? this.t('impact.blast.moreTests') : ''),
      allLabel: allOpen ? this.t('impact.blast.collapseAll') : this.t('impact.blast.expandAll'),
      note: this.locale.text(pr.note),
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

  private t(key: string, params?: Readonly<Record<string, string | number>>): string {
    return this.locale.translate(key, params);
  }

  private n(base: string, count: number, params?: Readonly<Record<string, string | number>>): string {
    return this.locale.plural(base, count, params);
  }
}
