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
  CheckItem,
  PullRequestItem,
  RepoInfo,
  ReviewItem,
  SyncResult,
  SyncStatus,
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
import { ActionError, toActionErrorCode } from '../../domain/models/action-error.models';
import { NotificationItem } from '../../domain/models/notification.models';
import { LocaleStore } from './locale.store';
import { NotificationStore } from './notification.store';

/** How often the progress of a background sync is read. */
const SYNC_POLL_MS = 1000;

/** Failures after which GitHub's state may differ from ours: the workspace is read again (ADR-0013). */
const REREAD_AFTER: ReadonlySet<string> = new Set(['outcome_unknown', 'conflict', 'checks_running', 'not_found']);

/** Waits, or rejects with an AbortError the moment the signal aborts. */
function sleepUnlessAborted(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException('Aborted', 'AbortError'));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(new DOMException('Aborted', 'AbortError'));
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

/** A moment as the clock time the user reads it. */
/** A time of day in the user's locale (hh:mm), e.g. when a rate limit lifts. */
export function formatClock(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

/** i18n keys for the GitHub mergeable states that stop a merge whatever reviews or checks do next. */
const MERGE_BLOCKER_KEYS: Readonly<Partial<Record<MergeState, string>>> = {
  conflict: 'drawer.blocked.conflict',
  behind: 'drawer.blocked.behind',
  draft: 'drawer.blocked.draft',
};

/**
 * i18n keys for the mergeable states that only warn. GitHub's "blocked" can lag a review or a check
 * it has not recounted yet, and "unknown" is still being computed: the merge stays possible, GitHub
 * decides when it is sent and says which rule refused it.
 */
const MERGE_HINT_KEYS: Readonly<Partial<Record<MergeState, string>>> = {
  blocked: 'drawer.mergeBlockedHint',
  unknown: 'drawer.mergeUnknown',
};

/** How long the merge button waits after an approval made here, for GitHub to count it. */
const MERGE_COOLDOWN_MS = 10_000;

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
  /** 'true' while the merge waits for GitHub to count the approval just made. */
  readonly quickDis: string;
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

/** One check in the drawer's checks panel. */
export interface DrawerCheckViewModel {
  readonly name: string;
  readonly detail: string;
  readonly url: string | null;
  readonly state: CheckItem['state'];
}

export interface DrawerCheckGroupViewModel {
  readonly key: CheckItem['state'];
  readonly label: string;
  /** Whether the group starts expanded (failing and running ones do, like on GitHub). */
  readonly open: boolean;
  readonly items: readonly DrawerCheckViewModel[];
}

/** GitHub's checks panel for the open PR: a verdict, the counts, and the checks grouped by outcome. */
export interface DrawerChecksViewModel {
  readonly has: boolean;
  readonly tone: 'ok' | 'fail' | 'run';
  readonly title: string;
  readonly subtitle: string;
  /** A conic-gradient ring sized by outcome. */
  readonly ring: string;
  readonly groups: readonly DrawerCheckGroupViewModel[];
}

/** One reviewer in the drawer. */
export interface DrawerReviewViewModel {
  readonly reviewer: string;
  /** What they decided, in the user's language. */
  readonly label: string;
  readonly badgeCls: string;
  /** Set when the review is of an older commit, else ''. */
  readonly stale: string;
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
  /** Why the files could not be loaded (empty unless filesState is 'error'). */
  readonly filesError: string;
  readonly hasVerdict: boolean;
  readonly showIdle: boolean;
  readonly showMerged: boolean;
  readonly showSecondary: boolean;
  readonly blocked: boolean;
  readonly blockedCls: string;
  readonly blockedText: string;
  /**
   * About the merge, without blocking it beyond the cooldown: the wait after an approval made here,
   * GitHub's branch-protection warning, or a state GitHub has not computed yet; or ''.
   */
  readonly mergeNote: string;
  readonly showApprove: boolean;
  /** 'true' while checks are still running: approving waits for them. */
  readonly approveDis: string;
  /** Why approving is unavailable, or ''. */
  readonly approveTip: string;
  /** A Dependabot PR with no analysis yet, or one that failed and may work again: the user can ask for it. */
  readonly showAnalyze: boolean;
  /** "Analisar agora", or "Tentar de novo" after a failure. */
  readonly analyzeLabel: string;
  /** 'true' once CI has passed (or never reports): waiting for it is pointless. */
  readonly silenceCiDis: string;
  /** Why "silence until CI passes" is unavailable, or ''. */
  readonly silenceCiTip: string;
  readonly hasReviews: boolean;
  readonly reviews: readonly DrawerReviewViewModel[];
  readonly checks: DrawerChecksViewModel;
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
  private readonly notifications = inject(NotificationStore);

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
  /** PR id -> when (epoch ms) its merge button may be used again after an approval made here. */
  private readonly mergeCooldownUntil = signal<Readonly<Record<number, number>>>({});
  /** Ticks every second while a cooldown runs, so the countdown re-renders. */
  private readonly clock = signal(Date.now());
  private cooldownTimer: ReturnType<typeof setInterval> | null = null;
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
    readonly error?: string;
  } | null>(null);
  private filesLoad: AbortController | null = null;
  private lastBatchTotal = 0;
  private disconnectLive: (() => void) | null = null;

  constructor() {
    this.cancellation.registerAbortCallback((opId) => {
      void this.prGateway?.cancelServerOperation(opId);
    });
    this.destroyRef.onDestroy(() => {
      this.stopLiveSession();
      this.stopMergeCooldownTimer();
    });
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
    this.notifications.setOpener((items) => void this.openNotified(items));
    void this.notifications.start();
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
          if (evt.pr !== undefined && evt.pr === this.selectedPrId() && (evt.type === 'pr.merged' || evt.type === 'pr.closed')) {
            this.endDrawer(evt.pr, evt.type === 'pr.merged' ? 'notice.pr.merged' : 'notice.pr.closed');
          }
          if (evt.refreshWorkspace) {
            this.scheduleWorkspaceRefresh();
          }
        },
        (state) => this.onConnectionState(state),
        (notification) => this.notifications.receive(notification),
      ) ?? (() => undefined);
  }

  /**
   * Opens what notifications are about: one PR's drawer, or the perspective of a burst. A PR the
   * workspace does not list yet (the notification outran the reload) is looked for once more; a PR
   * no longer open, or a repository no longer followed, is said so rather than opened.
   */
  private async openNotified(items: readonly NotificationItem[]): Promise<void> {
    const first = items[0];
    if (!this.repos().some((r) => r.name === first.repositoryName)) {
      this.noticeState.set({ key: 'notifications.unfollowed', params: { repo: first.repositoryName } });
      return;
    }
    if (items.length > 1) {
      this.setPerspective(first.kind === 'risk' ? 'radar' : 'inbox');
      return;
    }
    if (!this.prs().some((p) => p.id === first.pullRequestId)) {
      await this.loadWorkspace();
    }
    if (!this.prs().some((p) => p.id === first.pullRequestId)) {
      this.noticeState.set({ key: 'notifications.prGone', params: { pr: first.prNumber } });
      return;
    }
    this.setPerspective(first.kind === 'risk' ? 'radar' : 'inbox');
    this.openPr(first.pullRequestId);
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
      void this.notifications.refresh(); // notifications sent while the stream was down
    }
    this.streamOpenedBefore = true;
  }

  stopLiveSession(): void {
    const disconnect = this.disconnectLive;
    this.disconnectLive = null;
    this.streamOpenedBefore = false;
    this.notifications.stop();
    if (this.refreshTimer !== null) {
      clearTimeout(this.refreshTimer);
      this.refreshTimer = null;
    }
    disconnect?.();
  }

  /** Numbers each workspace request, so an older answer never replaces a newer one. */
  private workspaceRequestSeq = 0;

  async loadWorkspace(options?: ActionRequestOptions): Promise<void> {
    const request = ++this.workspaceRequestSeq;
    const dataset = await this.prGateway?.fetchWorkspace?.(options);
    // Reloads overlap (a sync's own reload and a live event's): the newest request wins.
    if (!dataset || request !== this.workspaceRequestSeq) {
      return;
    }
    const selected = this.selectedPrId();
    const shown = selected === null ? undefined : this.prs().find((p) => p.id === selected);
    this.repos.set(dataset.repos);
    this.prs.set(dataset.prs);
    // The PR in the drawer is no longer open (merged or closed meanwhile): never leave a drawer, or
    // room reserved for one, behind a PR that nobody can approve or merge any more.
    if (selected !== null && !dataset.prs.some((p) => p.id === selected)) {
      this.endDrawer(selected, 'notice.pr.gone', shown?.number);
    }
    this.events.set(dataset.events);
    if (dataset.approvedMap) {
      this.approvedMap.set(dataset.approvedMap);
    }
    if (dataset.silencedMap) {
      this.silencedMap.set(dataset.silencedMap);
    }
  }

  private readonly noticeState = signal<{ readonly key: string; readonly params: Readonly<Record<string, string | number>> } | null>(null);
  /** A one-line explanation of something that changed under the user (e.g. the PR in the drawer was merged). */
  readonly notice = computed(() => {
    const n = this.noticeState();
    return n ? this.t(n.key, n.params) : null;
  });

  dismissNotice(): void {
    this.noticeState.set(null);
  }

  /** Closes the drawer of a PR that can no longer be acted on, and tells the user why. */
  private endDrawer(prId: number, key: string, number?: number): void {
    if (this.selectedPrId() !== prId) {
      return;
    }
    const pr = this.prs().find((p) => p.id === prId);
    this.closeDrawer();
    this.noticeState.set({ key, params: { pr: number ?? pr?.number ?? prId } });
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
        .map((p) => {
          const isTurn = isActionableTurn(p, silenced, approved);
          const showTurn = isTurn && !p.dep;
          const mine = Boolean(approved[p.id]);
          const isApproved = Boolean(p.approvedBy || (mine && !p.dep));
          const tier = this.t(p.verdict ? RISK_TIER_KEYS[p.verdict] : p.analysisFailed ? 'risk.tier.failed' : 'risk.tier.none');
          const dev = p.depType === 'devDependencies';
          const vLabel = p.verdict ? this.t(RISK_LABEL_KEYS[p.verdict]) : '';
          const safe = isSafeBatchCandidate(p, this.effectiveCi(p).ci);
          const cooldown = mine ? this.mergeCooldownSeconds(p.id) : 0;

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
            quickTip: mine && cooldown > 0 ? this.t('drawer.mergeCooldown', { seconds: cooldown }) : mine ? this.t('seats.quickTip.merge') : this.t('seats.quickTip.approve'),
            quickLabel: mine ? (cooldown > 0 ? `Squash (${cooldown}s)` : 'Squash') : this.t('seats.quick.approve'),
            quickDis: mine && cooldown > 0 ? 'true' : 'false',
            open: () => this.openPr(p.id),
            quick: () => {
              if (mine && this.mergeCooldownSeconds(p.id) > 0) {
                return;
              }
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
    // A failed analysis has no verdict either, but "not analysed" would hide that it was tried.
    const tiers: readonly [(p: PullRequestItem) => boolean, string][] = [
      [(p) => p.verdict === 'alto', this.t('radar.group.alto')],
      [(p) => p.verdict === 'baixo', this.t('radar.group.baixo')],
      [(p) => p.verdict === 'medio', this.t('radar.group.medio')],
      [(p) => p.verdict === null && p.analysisFailed === true, this.t('radar.group.failed')],
      [(p) => p.verdict === null && !p.analysisFailed, this.t('radar.group.none')],
    ];

    return tiers
      .map(([inGroup, label]) => {
        const items: RadarCardViewModel[] = deps
          .filter(inGroup)
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
    const cooldown = Math.max(0, ...safeApproved.map((p) => this.mergeCooldownSeconds(p.id)));

    return {
      has: safe.length > 0 || batchAct.running,
      title: this.t('radar.batch.title', { count: batchN }),
      canApprove: safePending.length > 0,
      approveLabel: safeApproved.length > 0 ? this.t('radar.batch.approveRest', { count: safePending.length }) : this.t('radar.batch.approveAll', { count: batchN }),
      mergeDis: safeApproved.length === 0 || cooldown > 0 ? 'true' : 'false',
      mergeCls: safePending.length === 0 && cooldown === 0 ? 'pf-btn-primary' : '',
      /** Why merging waits: GitHub is still counting an approval made here, or ''. */
      mergeTip: cooldown > 0 ? this.t('drawer.mergeCooldown', { seconds: cooldown }) : '',
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

    // Only GitHub decides what blocks a merge (mergeable_state: conflicts, out of date, draft). Branch
    // protection and a state not computed yet only warn (MERGE_HINT_KEYS); CI and CODEOWNERS are signals.
    const runningText = this.checksRunning(pr) ? this.t('drawer.checksRunning') : '';
    const blockedText = (pr.merge && MERGE_BLOCKER_KEYS[pr.merge] ? this.locale.translate(MERGE_BLOCKER_KEYS[pr.merge]!) : '') || runningText;
    const cooldown = this.mergeCooldownSeconds(pr.id);
    const cooldownText = cooldown > 0 ? this.t('drawer.mergeCooldown', { seconds: cooldown }) : '';
    const hintKey = pr.merge ? MERGE_HINT_KEYS[pr.merge] : undefined;

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
    const checks = this.checksPanel(pr.checks ?? []);
    // With the checks listed, the CI signal is their verdict: it can never say something else.
    const checksDot = { ok: 'pf-dot pf-dot-ok', run: 'pf-dot pf-dot-warn pf-dot-pulse', fail: 'pf-dot pf-dot-fail' }[checks.tone];
    const signals = [
      { k: 'CI', v: checks.has ? checks.title : ciText, dot: checks.has ? checksDot : dotCi },
      { k: 'CODEOWNERS', v: ownersText, dot: dotOwners },
      ...(pr.size ? [{ k: 'Tamanho', v: pr.size, dot: 'pf-dot' }] : []),
    ];
    const depVm = pr.dep ? this.buildDependabotDetail(pr) : null;

    return {
      id: pr.id,
      number: pr.number,
      repo: pr.repo,
      title: pr.title,
      ghUrl: `https://github.com/${pr.repo}/pull/${pr.number}`,
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
      filesError: loaded?.prId === pr.id ? (loaded.error ?? '') : '',
      showIdle: act.idle && !merged,
      showMerged: act.idle && merged,
      showSecondary: !merged,
      blocked: Boolean(blockedText),
      blockedCls: 'pf-blocked',
      blockedText,
      mergeNote: blockedText ? '' : cooldownText || (hintKey ? this.t(hintKey) : ''),
      showAnalyze: pr.dep === true && pr.verdict === null && Boolean(pr.pkg) && (!pr.analysisFailed || pr.analysisRetryable === true),
      analyzeLabel: this.t(pr.analysisFailed ? 'drawer.analyzeRetry' : 'drawer.analyze'),
      silenceCiDis: this.canSilenceUntilCi(pr) ? 'false' : 'true',
      silenceCiTip: this.silenceCiReason(pr),
      hasReviews: (pr.reviews?.length ?? 0) > 0,
      reviews: (pr.reviews ?? []).map((r) => this.reviewRow(r)),
      checks,
      showApprove: !mine && !own,
      approveDis: runningText ? 'true' : 'false',
      approveTip: runningText,
      ownNote: own ? this.t('drawer.ownPr') : '',
      approvedMine: mine,
      mergeCls: (mine || pr.approvedBy) && !blockedText && !cooldownText && pr.merge !== 'blocked' ? 'pf-btn-primary' : '',
      mergeDis: blockedText || cooldownText ? 'true' : 'false',
      mergeTip: blockedText || cooldownText || this.t('drawer.mergeTip'),
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
    } catch (err: unknown) {
      if (!controller.signal.aborted) {
        this.changedFiles.set({ prId, state: 'error', files: [], error: this.failureText(err) });
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

  private checksPanel(checks: readonly CheckItem[]): DrawerChecksViewModel {
    const of = (state: CheckItem['state']) => checks.filter((c) => c.state === state);
    const failing = of('failure');
    const pending = of('pending');
    const success = of('success');
    const skipped = of('skipped');

    const tone = failing.length > 0 ? 'fail' : pending.length > 0 ? 'run' : 'ok';
    const counts = [
      failing.length ? this.t('drawer.checks.count.failing', { count: failing.length }) : '',
      pending.length ? this.t('drawer.checks.count.running', { count: pending.length }) : '',
      success.length ? this.t('drawer.checks.count.successful', { count: success.length }) : '',
      skipped.length ? this.t('drawer.checks.count.skipped', { count: skipped.length }) : '',
    ].filter(Boolean);

    const group = (key: CheckItem['state'], list: readonly CheckItem[], open: boolean): DrawerCheckGroupViewModel[] =>
      list.length === 0
        ? []
        : [
            {
              key,
              label: this.n(`drawer.checks.group.${key}`, list.length),
              open,
              items: list.map((c) => ({ name: c.name, detail: c.detail, url: c.url ?? null, state: c.state })),
            },
          ];

    const total = checks.length || 1;
    const fail = (failing.length / total) * 100;
    const run = fail + (pending.length / total) * 100;
    return {
      has: checks.length > 0,
      tone,
      title: this.t(`drawer.checks.title.${tone}`),
      subtitle: counts.join(', '),
      ring: `conic-gradient(#F43F5E 0 ${fail}%, #F59E0B ${fail}% ${run}%, #10B981 ${run}% 100%)`,
      groups: [...group('failure', failing, true), ...group('pending', pending, true), ...group('success', success, false), ...group('skipped', skipped, false)],
    };
  }

  private reviewRow(r: ReviewItem): DrawerReviewViewModel {
    const badgeCls = { approved: 'pf-badge-baixo', changes_requested: 'pf-badge-alto', commented: 'pf-tag', dismissed: 'pf-tag' }[r.state];
    return {
      reviewer: r.reviewer.startsWith('@') ? r.reviewer : `@${r.reviewer}`,
      label: this.t(`drawer.review.${r.state}`),
      badgeCls,
      stale: r.current ? '' : this.t('drawer.review.stale'),
    };
  }

  /** Checks that have not finished are blockers: neither approving nor merging waits for nothing. */
  private checksRunning(pr: PullRequestItem): boolean {
    return this.effectiveCi(pr).ci === 'run';
  }

  /** Silencing until CI passes only makes sense while CI can still pass: it is running or failing. */
  private canSilenceUntilCi(pr: PullRequestItem): boolean {
    const ci = this.effectiveCi(pr).ci;
    return ci === 'run' || ci === 'fail';
  }

  private silenceCiReason(pr: PullRequestItem): string {
    switch (this.effectiveCi(pr).ci) {
      case 'ok':
        return this.t('drawer.silenceCi.passed');
      case 'none':
        return this.t('drawer.silenceCi.none');
      default:
        return '';
    }
  }

  /** Nobody approves a PR they wrote: GitHub refuses it, so it is not even offered. */
  private isOwnPr(pr: PullRequestItem): boolean {
    const viewer = this.viewerLogin().toLowerCase();
    return viewer !== '' && pr.author.replace(/^@/, '').toLowerCase() === viewer;
  }

  /** The note shown when an action fails: what happened, in the user's language. */
  private failureNote(err: unknown): string {
    if (err instanceof ActionError && REREAD_AFTER.has(err.code)) {
      this.scheduleWorkspaceRefresh(); // GitHub's state may differ from ours: look again
    }
    return this.failureText(err);
  }

  /** What a failure was, in the user's language, with GitHub's explanation when it gave one. */
  private failureText(err: unknown): string {
    if (err instanceof ActionError) {
      return this.t(`actions.error.${err.code}`, {
        detail: err.detail,
        time: err.retryAt ? formatClock(err.retryAt) : '',
      });
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
            if (this.checksRunning(pr)) {
              throw new ActionError('checks_running');
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
            if (this.checksRunning(pr)) {
              throw new ActionError('checks_running');
            }
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
    const target = this.prs().find((p) => p.id === prId);
    if (until === 'ci' && target && !this.canSilenceUntilCi(target)) {
      return; // the button is disabled, but a keyboard can still reach it
    }
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

  /**
   * Brings the workspace in line with GitHub: the server re-reads every followed repository in the
   * background (closing PRs that ended while no webhook arrived) and this follows its progress until
   * it is over, then reloads the workspace. Cancelling asks the server to stop. A sync already
   * running (another tab, a double click) is joined, not repeated.
   */
  async runSync(): Promise<void> {
    const count = this.repos().length;
    let found: SyncResult | null = null;
    await this.cancellation.run(
      'sync',
      [
        {
          label: this.n('actions.syncing', count),
          execute: async (opts) => {
            if (this.prGateway?.startSync && this.prGateway.syncStatus) {
              try {
                found = await this.followSync(opts);
              } catch (err: unknown) {
                if (err instanceof DOMException && err.name === 'AbortError') {
                  await this.prGateway.cancelSync?.();
                }
                throw err;
              }
            }
            await this.loadWorkspace(opts);
          },
        },
      ],
      () => this.syncNote(found),
      () => this.t('actions.syncCanceled'),
      (err) => this.failureNote(err),
    );
  }

  private async followSync(opts: Required<ActionRequestOptions>): Promise<SyncResult | null> {
    const gateway = this.prGateway;
    let status = (await gateway!.startSync!(opts)).status;
    while (status.state === 'running' || status.state === 'idle') {
      this.showSyncProgress(status);
      await sleepUnlessAborted(SYNC_POLL_MS, opts.signal);
      status = await gateway!.syncStatus!(opts);
    }
    if (status.state === 'failed') {
      throw new ActionError(toActionErrorCode(status.error?.code), '', status.error?.retryAt);
    }
    if (status.state === 'canceled') {
      throw new DOMException('Aborted', 'AbortError');
    }
    return status.result ?? null;
  }

  private showSyncProgress(status: SyncStatus): void {
    if (!status.current || status.total === 0) {
      return;
    }
    this.cancellation.setProgress(
      'sync',
      this.t('actions.syncing.progress', { done: status.done + 1, total: status.total, current: status.current }),
      (status.done / status.total) * 100,
    );
  }

  /** What a sync found, or that everything already agreed; what it could not sync, and why, is named. */
  private syncNote(found: SyncResult | null): string {
    if (!found) {
      return this.t('actions.synced');
    }
    const ended = found.closed + found.merged;
    const parts = [
      ended ? this.n('actions.synced.ended', ended) : '',
      found.added ? this.n('actions.synced.added', found.added) : '',
      found.updated ? this.n('actions.synced.updated', found.updated) : '',
    ].filter(Boolean);
    const limited = found.skipped > 0 && !!found.rateLimitedUntil;
    const base = parts.length
      ? this.t('actions.synced.summary', { parts: parts.join(', ') })
      : limited
        ? ''
        : this.t('actions.synced');
    // The limit explains the repositories it stopped at, so they are not named again as failures.
    const failed = found.failed.filter((f) => !(limited && f.code === 'rate_limited'));
    return [
      base,
      failed.length ? this.t('actions.synced.failed', { repos: failed.map((f) => f.repository).join(', ') }) : '',
      limited
        ? this.t('actions.synced.rateLimited', { time: formatClock(found.rateLimitedUntil!), count: found.skipped })
        : '',
    ]
      .filter(Boolean)
      .join(' ');
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
      (err, idx) => this.t('actions.batch.approveFailed', { done: idx, total, pr: list[idx]?.number ?? '', reason: this.failureNote(err) }),
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
      (err, idx) => this.t('actions.batch.mergeFailed', { done: idx, total, pr: list[idx]?.number ?? '', reason: this.failureNote(err) }),
    );
  }

  private markApproved(pr: PullRequestItem): void {
    this.approvedMap.update((curr) => ({ ...curr, [pr.id]: true }));
    this.startMergeCooldown(pr.id);
    this.appendTimelineEvent({ kind: 'ok', type: 'pr.approved', actor: this.viewerLogin(), text: { key: 'timeline.local.approved', params: { pr: pr.number } }, repo: pr.repo, pr: pr.id, fresh: true });
  }

  /**
   * GitHub counts an approval for branch protection a moment after accepting it, and a merge sent
   * before that is refused. The panel waits MERGE_COOLDOWN_MS before offering the merge; the wait is
   * GitHub's, which the countdown says.
   */
  private startMergeCooldown(prId: number): void {
    const now = Date.now();
    this.mergeCooldownUntil.update((curr) => ({ ...curr, [prId]: now + MERGE_COOLDOWN_MS }));
    this.clock.set(now);
    if (this.cooldownTimer !== null) {
      return;
    }
    this.cooldownTimer = setInterval(() => {
      const tick = Date.now();
      this.clock.set(tick);
      const running = Object.entries(this.mergeCooldownUntil()).filter(([, until]) => until > tick);
      this.mergeCooldownUntil.set(Object.fromEntries(running));
      if (running.length === 0) {
        this.stopMergeCooldownTimer();
      }
    }, 1000);
  }

  private stopMergeCooldownTimer(): void {
    if (this.cooldownTimer !== null) {
      clearInterval(this.cooldownTimer);
      this.cooldownTimer = null;
    }
  }

  /** Whole seconds left before the PR's merge button may be used, 0 when it may. */
  private mergeCooldownSeconds(prId: number): number {
    const until = this.mergeCooldownUntil()[prId];
    return until ? Math.max(0, Math.ceil((until - this.clock()) / 1000)) : 0;
  }

  private markMerged(pr: PullRequestItem): void {
    this.mergedMap.update((curr) => ({ ...curr, [pr.id]: true }));
    this.endDrawer(pr.id, 'notice.pr.mergedByYou', pr.number);
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
    const tier = this.t(pr.verdict ? RISK_TIER_KEYS[pr.verdict] : pr.analysisFailed ? 'risk.tier.failed' : 'risk.tier.none');
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
