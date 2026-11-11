import {
  BlastFileItem,
  ChangedFileItem,
  CiState,
  CodeownersState,
  DependencyViaNode,
  DiffHunkLine,
  I18nText,
  MergeState,
  OwnersState,
  CheckItem,
  PullRequestItem,
  ReviewItem,
  RepoInfo,
  RepositoryAccessState,
  RiskVerdictTier,
  SyncStatus,
  TimelineEventItem,
  WorkspaceDataset,
} from '../../domain/models/workspace.models';
import { isKnownActivityType } from '../../domain/models/activity.models';

export interface ApiRepositoryDto {
  readonly id: number;
  readonly owner: string;
  readonly name: string;
  readonly full_name: string;
  readonly default_branch: string;
  readonly cached_codeowners_rules?: readonly { pattern: string; owners: readonly string[] }[];
  /** '' until checked, then whether the repository has a CODEOWNERS file. */
  readonly codeowners_status?: '' | 'present' | 'absent';
  readonly access?: RepositoryAccessState;
}

export interface ApiPullRequestDto {
  readonly id: number;
  readonly repository_id: number;
  readonly repository_name: string;
  readonly number: number;
  readonly title: string;
  readonly author_login: string;
  readonly html_url: string;
  readonly is_dependabot: boolean;
  readonly is_draft: boolean;
  readonly state: 'OPEN' | 'CLOSED' | 'MERGED';
  readonly mergeable_state: 'CLEAN' | 'CONFLICTING' | 'BLOCKED' | 'BEHIND' | 'UNSTABLE' | 'DRAFT' | 'UNKNOWN';
  /** NONE: GitHub reported no check run for the head commit (e.g. the repository has no CI). */
  readonly ci_status: 'NONE' | 'PENDING' | 'SUCCESS' | 'FAILURE';
  readonly head_sha: string;
  readonly base_ref: string;
  readonly claimed_by_login?: string;
}

export interface ApiChangedFileDto {
  readonly path: string;
  readonly additions: number;
  readonly deletions: number;
  readonly patch: string;
}

export interface ApiUserPrStateDto {
  readonly pull_request_id: number;
  readonly user_id: number;
  readonly turn_state:
    | 'HIDDEN_GATE'
    | 'WAITING_ON_ME'
    | 'WAITING_ON_AUTHOR'
    | 'TEAMMATE_APPROVED'
    | 'APPROVED_BY_ME'
    | 'SNOOZED'
    | 'MERGED_OR_CLOSED';
  readonly team_already_approved: boolean;
  readonly approved_by_login?: string;
  readonly snoozed_until_condition?: string;
  readonly owned_files_changed?: readonly ApiChangedFileDto[];
}

export interface ApiCheckRunDto {
  readonly id: number;
  readonly name: string;
  readonly status: string;
  readonly conclusion: string;
  readonly title: string;
  readonly details_url: string;
}

const CHECK_STATE_RANK: Readonly<Record<CheckItem['state'], number>> = { failure: 0, pending: 1, success: 2, skipped: 3 };

/** GitHub's grouping: anything not finished is pending; success passes; neutral and skipped are set apart. */
function checkState(c: ApiCheckRunDto): CheckItem['state'] {
  if (c.status !== 'completed') {
    return 'pending';
  }
  switch (c.conclusion) {
    case 'success':
      return 'success';
    case 'neutral':
    case 'skipped':
      return 'skipped';
    default: // failure, cancelled, timed_out, action_required, startup_failure
      return 'failure';
  }
}

/** Only an http(s) link is ever kept: a check's URL comes from outside and must not run script. */
function safeHttpUrl(raw: string): string | undefined {
  try {
    const url = new URL(raw);
    return url.protocol === 'https:' || url.protocol === 'http:' ? url.toString() : undefined;
  } catch {
    return undefined;
  }
}

function mapCheckRun(c: ApiCheckRunDto): CheckItem {
  const url = safeHttpUrl(c.details_url);
  return { name: c.name, state: checkState(c), detail: c.title?.trim() ?? '', ...(url ? { url } : {}) };
}

export interface ApiReviewDecisionDto {
  readonly reviewer: string;
  readonly state: string;
  readonly commit_sha: string;
  readonly submitted_at: string;
  readonly current: boolean;
}

const REVIEW_STATES: Readonly<Record<string, ReviewItem['state']>> = {
  APPROVED: 'approved',
  CHANGES_REQUESTED: 'changes_requested',
  COMMENTED: 'commented',
  DISMISSED: 'dismissed',
};

function mapReviewDecision(d: ApiReviewDecisionDto): ReviewItem {
  return { reviewer: d.reviewer, state: REVIEW_STATES[d.state] ?? 'commented', current: d.current };
}

export interface ApiDependabotAnalysisDto {
  readonly pull_request_id: number;
  readonly package_name: string;
  readonly from_version: string;
  readonly to_version: string;
  readonly semver_bump_type: 'PATCH' | 'MINOR' | 'MAJOR' | 'UNKNOWN';
  readonly is_direct_dep: boolean;
  readonly dep_scope: 'PROD' | 'DEV' | 'TRANSITIVE';
  readonly risk_verdict: 'LOW' | 'MEDIUM' | 'HIGH';
  readonly risk_score: number;
  readonly verdict_summary_key: string;
  readonly dependency_path_graph?: readonly { name: string; version: string; scope: string }[];
  readonly blast_radius_usages?: readonly {
    file_path: string;
    line_number: number;
    import_stmt: string;
    code_snippet: string;
  }[];
  readonly advisories_and_notes?: readonly {
    cve_id?: string;
    severity?: string;
    summary: string;
    is_breaking_note: boolean;
  }[];
}

export interface ApiActivityEventDto {
  readonly id: string;
  readonly type: string;
  readonly repository_name: string;
  readonly pull_request_id?: number;
  readonly pr_number?: number;
  readonly actor_login?: string;
  /** Facts the client phrases in the user's language (title, method, package...). */
  readonly params?: Readonly<Record<string, string>>;
  readonly occurred_at: string;
}

export interface ApiWorkspaceResponseDto {
  readonly snapshot?: {
    readonly inbox_waiting_on_me?: readonly { pull_request: ApiPullRequestDto; user_state: ApiUserPrStateDto }[];
    readonly inbox_dimmed?: readonly { pull_request: ApiPullRequestDto; user_state: ApiUserPrStateDto }[];
    readonly dependabot_prs?: readonly {
      pull_request: ApiPullRequestDto;
      analysis?: ApiDependabotAnalysisDto;
      /** I have a standing approval of the head commit (from GitHub's review). */
      approved_by_me?: boolean;
      /** A teammate approved the head commit. */
      approved_by_login?: string;
    }[];
    readonly seats?: readonly {
      repository: ApiRepositoryDto;
      open_pr_count: number;
      waiting_on_me_count: number;
      dependabot_pr_count: number;
      failing_ci_count: number;
      pull_requests: readonly ApiPullRequestDto[];
    }[];
    /** PRs (by id) whose changed files are not known yet: nobody can say whose turn they are. */
    readonly ownership_pending_pr_ids?: readonly number[];
    /** Where each reviewer stands, by PR id, for the PRs somebody reviewed. */
    readonly reviews?: Readonly<Record<string, readonly ApiReviewDecisionDto[]>>;
    /** Each open PR's current checks, by PR id, for the PRs whose commit reported any. */
    readonly checks?: Readonly<Record<string, readonly ApiCheckRunDto[]>>;
  };
  readonly timeline?: readonly ApiActivityEventDto[];
}

export function mapApiWorkspaceResponseToDataset(dto: ApiWorkspaceResponseDto): WorkspaceDataset | null {
  const snapshot = dto.snapshot;
  if (!snapshot) {
    return null;
  }

  const seats = snapshot.seats ?? [];
  const repos: RepoInfo[] = seats.map((s) => {
    const codeowners = s.repository.codeowners_status;
    return {
      name: s.repository.full_name,
      branch: s.repository.default_branch || 'main',
      access: s.repository.access ?? 'active',
      ...(codeowners === 'present' || codeowners === 'absent' ? { codeowners } : {}),
    };
  });
  const codeownersByRepo = new Map(repos.map((r) => [r.name, r.codeowners]));
  const codeownersOf = (repoName: string): CodeownersState | undefined => codeownersByRepo.get(repoName);

  const repoRulesMap = new Map<string, string>();
  for (const s of seats) {
    const rule = s.repository.cached_codeowners_rules?.[0];
    if (rule) {
      repoRulesMap.set(s.repository.full_name, `${rule.pattern} (${rule.owners.join(', ')})`);
    }
  }

  const prs: PullRequestItem[] = [];
  const approvedMap: Record<number, boolean> = {};
  const silencedMap: Record<number, I18nText> = {};

  // Each PR is listed once. The inbox holds only PRs touching the user's files, the radar holds
  // Dependabot's; every other open PR comes from its seat, outside the inbox.
  const listed = new Set<number>();
  for (const item of snapshot.inbox_waiting_on_me ?? []) {
    listed.add(item.pull_request.id);
    const mapped = mapInboxPrToModel(item.pull_request, item.user_state, true, repoRulesMap, codeownersOf(item.pull_request.repository_name));
    prs.push(mapped);
  }

  for (const item of snapshot.inbox_dimmed ?? []) {
    listed.add(item.pull_request.id);
    const mapped = mapInboxPrToModel(item.pull_request, item.user_state, false, repoRulesMap, codeownersOf(item.pull_request.repository_name));
    prs.push(mapped);
    if (item.user_state.turn_state === 'APPROVED_BY_ME') {
      approvedMap[mapped.id] = true;
    }
    if (item.user_state.turn_state === 'SNOOZED') {
      silencedMap[mapped.id] =
        item.user_state.snoozed_until_condition === 'UNTIL_CI_GREEN' ? { key: 'silenced.ci' } : { key: 'silenced.commit' };
    }
  }

  for (const item of snapshot.dependabot_prs ?? []) {
    listed.add(item.pull_request.id);
    const bump = mapDependabotPrToModel(item.pull_request, item.analysis, codeownersOf(item.pull_request.repository_name));
    // Approval comes from the review GitHub recorded: a reload must not forget it.
    if (item.approved_by_me) {
      approvedMap[bump.id] = true;
    }
    const teammate = item.approved_by_login ? `@${item.approved_by_login.replace(/^@/, '')}` : undefined;
    prs.push(teammate && !item.approved_by_me ? { ...bump, approvedBy: teammate } : bump);
  }

  const pending = new Set(snapshot.ownership_pending_pr_ids ?? []);
  for (const seat of seats) {
    for (const pr of seat.pull_requests ?? []) {
      if (!listed.has(pr.id)) {
        listed.add(pr.id);
        prs.push(mapSeatPrToModel(pr, codeownersOf(pr.repository_name), pending.has(pr.id)));
      }
    }
  }

  const events: TimelineEventItem[] = (dto.timeline ?? []).map((ev, index) => ({
    ...mapActivityEventToTimelineItem(ev),
    fresh: false, // loaded history; only live activity is highlighted
    key: index + 1,
    t: Date.parse(ev.occurred_at) || Date.now(),
  }));

  const reviews = snapshot.reviews ?? {};
  const checks = snapshot.checks ?? {};
  const withReviews = prs.map((pr) => {
    const decisions = reviews[String(pr.id)];
    const runs = checks[String(pr.id)];
    return {
      ...pr,
      ...(decisions?.length ? { reviews: decisions.map(mapReviewDecision) } : {}),
      // Failing first, then running, then the rest; each group keeps the server's order (by name).
      ...(runs?.length ? { checks: runs.map(mapCheckRun).sort((a, b) => CHECK_STATE_RANK[a.state] - CHECK_STATE_RANK[b.state]) } : {}),
    };
  });

  return { repos, prs: withReviews, events, approvedMap, silencedMap };
}

export function mapActivityEventToTimelineItem(
  ev: ApiActivityEventDto,
): Omit<TimelineEventItem, 'key' | 't'> {
  const kind: TimelineEventItem['kind'] =
    ev.type === 'pr.approved' || ev.type === 'pr.merged' || ev.type === 'ci.passed' || ev.type === 'repository.access_restored'
      ? 'ok'
      : ev.type === 'ci.failed' || ev.type === 'repository.access_lost' || ev.type === 'repository.access_suspended'
        ? 'fail'
        : ev.type === 'pr.claimed' || ev.type === 'codeowners.turn'
          ? 'turn'
          : 'info';

  return {
    kind,
    id: ev.id,
    type: ev.type,
    actor: ev.actor_login || undefined,
    text: activityText(ev),
    repo: ev.repository_name,
    // The PR the activity is about, by id (numbers repeat across repositories).
    pr: ev.pull_request_id || undefined,
    fresh: true,
    // Every server activity changes what the workspace shows (a new PR, a commit, lost access).
    refreshWorkspace: true,
  };
}

/** An I18nText for a count-dependent message: `<base>.one` or `<base>.other`, with `{count}`. */
function pluralText(base: string, count: number, params?: Readonly<Record<string, string | number>>): I18nText {
  return { key: `${base}.${count === 1 ? 'one' : 'other'}`, params: { count, ...params } };
}

/** The activity sentence, phrased by the client from the type and the facts the server sent. */
function activityText(ev: ApiActivityEventDto): I18nText {
  const base = { repo: ev.repository_name, pr: ev.pr_number || ev.pull_request_id || '', actor: ev.actor_login ? `@${ev.actor_login.replace(/^@/, '')}` : '' };
  if (!isKnownActivityType(ev.type)) {
    return { key: 'timeline.event.other', params: { ...base, type: ev.type } };
  }
  // The risk verdict is a word too: one sentence per verdict keeps it translated.
  const verdict = ev.type === 'dependabot.analyzed' ? ev.params?.['verdict']?.toLowerCase() : undefined;
  const verdictKey = verdict && ['low', 'medium', 'high'].includes(verdict) ? `timeline.event.${ev.type}.${verdict}` : null;
  // A removed CODEOWNERS reads differently from an updated one.
  const removed = ev.type === 'repository.codeowners_updated' && ev.params?.['status'] === 'absent';
  const key = verdictKey ?? (removed ? `timeline.event.${ev.type}.absent` : `timeline.event.${ev.type}`);
  return { key, params: { ...base, ...ev.params } };
}

function mapCiStatus(status: ApiPullRequestDto['ci_status']): { ci: CiState; ciText: I18nText } {
  if (status === 'FAILURE') {
    return { ci: 'fail', ciText: { key: 'ci.text.fail' } };
  }
  if (status === 'PENDING') {
    return { ci: 'run', ciText: { key: 'ci.text.run' } };
  }
  if (status === 'SUCCESS') {
    return { ci: 'ok', ciText: { key: 'ci.text.ok' } };
  }
  // NONE, or a value this client does not know yet: never claim a result GitHub did not report.
  return { ci: 'none', ciText: '' };
}

const MERGE_STATES: Readonly<Record<ApiPullRequestDto['mergeable_state'], MergeState>> = {
  CLEAN: 'clean',
  CONFLICTING: 'conflict',
  BLOCKED: 'blocked',
  BEHIND: 'behind',
  UNSTABLE: 'unstable',
  DRAFT: 'draft',
  UNKNOWN: 'unknown',
};

function mapMergeState(state: ApiPullRequestDto['mergeable_state']): MergeState {
  return MERGE_STATES[state] ?? 'unknown';
}

export interface ApiPullRequestFileDto {
  readonly path: string;
  readonly status?: string;
  readonly additions: number;
  readonly deletions: number;
  readonly patch?: string;
  readonly owned: boolean;
}

export function mapPullRequestFile(dto: ApiPullRequestFileDto): ChangedFileItem {
  return {
    path: dto.path,
    status: dto.status,
    additions: dto.additions,
    deletions: dto.deletions,
    owned: dto.owned,
    hunk: parsePatchToHunkLines(dto.patch),
  };
}

function parsePatchToHunkLines(patch?: string): readonly DiffHunkLine[] {
  if (!patch) {
    return [];
  }
  return patch.split('\n').map((rawLine): DiffHunkLine => {
    if (rawLine.startsWith('@@')) {
      return { t: 'hdr', s: rawLine };
    }
    if (rawLine.startsWith('+')) {
      return { t: 'add', s: '+ ' + rawLine.slice(1).trimStart() };
    }
    if (rawLine.startsWith('-')) {
      return { t: 'del', s: '- ' + rawLine.slice(1).trimStart() };
    }
    return { t: 'ctx', s: '  ' + rawLine.replace(/^\s/, '') };
  });
}

/** CODEOWNERS state for someone who does not own the changed files (no review tracking yet). */
function ownersForNonOwner(codeowners: CodeownersState | undefined, approvedBy?: string): OwnersState {
  if (codeowners === 'absent') return 'none';
  if (approvedBy) return 'ok';
  if (codeowners === undefined) return 'unknown';
  return 'others';
}

const OWNERS_REASON: Record<OwnersState, I18nText> = {
  voce: { key: 'drawer.why.voce' },
  ok: { key: 'drawer.why.ok' },
  others: { key: 'drawer.why.others' },
  none: { key: 'drawer.why.none' },
  unknown: { key: 'drawer.why.unknown' },
};

function mapInboxPrToModel(
  pr: ApiPullRequestDto,
  st: ApiUserPrStateDto,
  isActionable: boolean,
  repoRulesMap: Map<string, string>,
  codeowners: CodeownersState | undefined,
): PullRequestItem {
  const { ci, ciText } = mapCiStatus(pr.ci_status);
  const ownedFiles = st.owned_files_changed ?? [];
  const approvedBy = st.approved_by_login
    ? st.approved_by_login.startsWith('@')
      ? st.approved_by_login
      : `@${st.approved_by_login}`
    : undefined;

  // Mine when it is my turn, or when it touches my files but the gate is closed.
  const owners: OwnersState = isActionable || ownedFiles.length > 0 ? 'voce' : ownersForNonOwner(codeowners, approvedBy);
  const ownersText: I18nText = {
    voce: ownedFiles.length > 0 ? pluralText('owners.voce.count', ownedFiles.length) : { key: 'owners.voce' },
    ok: { key: 'owners.approvedBy', params: { login: approvedBy ?? '' } },
    others: { key: 'owners.others' },
    none: '',
    unknown: '',
  }[owners];

  let muteBadge: I18nText | undefined;
  let muteTip: I18nText | undefined;
  if (!isActionable) {
    if (st.turn_state === 'TEAMMATE_APPROVED' && approvedBy) {
      muteBadge = { key: 'inbox.mute.teammate', params: { login: approvedBy } };
      muteTip = { key: 'inbox.mute.teammateTip' };
    } else if (st.turn_state === 'WAITING_ON_AUTHOR') {
      muteBadge = { key: 'inbox.mute.author' };
      muteTip = { key: 'inbox.mute.authorTip' };
    } else if (pr.is_draft) {
      muteBadge = { key: 'inbox.mute.draft' };
      muteTip = { key: 'inbox.mute.draftTip' };
    } else if (st.turn_state === 'HIDDEN_GATE' && pr.ci_status === 'PENDING') {
      muteBadge = { key: 'inbox.mute.ci' };
      muteTip = { key: 'inbox.mute.ciTip' };
    } else if (st.turn_state === 'HIDDEN_GATE' && pr.ci_status === 'FAILURE') {
      muteBadge = { key: 'inbox.mute.ciFail' };
      muteTip = { key: 'inbox.mute.ciFailTip' };
    } else if (st.turn_state === 'HIDDEN_GATE' && pr.mergeable_state === 'CONFLICTING') {
      muteBadge = { key: 'inbox.mute.conflict' };
      muteTip = { key: 'inbox.mute.conflictTip' };
    }
  }

  return {
    id: pr.id,
    number: pr.number,
    rank: isActionable ? 1 : 3,
    repo: pr.repository_name,
    title: pr.title,
    author: pr.author_login.startsWith('@') ? pr.author_login : `@${pr.author_login}`,
    branch: pr.base_ref || 'main',
    verdict: null,
    ci,
    ciText,
    owners,
    ownersText,
    merge: mapMergeState(pr.mergeable_state),
    turn: isActionable,
    ownFiles: ownedFiles.length || undefined,
    ownPill: ownedFiles.length > 0 ? pluralText('inbox.ownPill', ownedFiles.length) : undefined,
    claim: pr.claimed_by_login ? { key: 'inbox.claim', params: { login: `@${pr.claimed_by_login.replace(/^@/, '')}` } } : undefined,
    approvedBy,
    muteBadge,
    muteTip,
    why: OWNERS_REASON[owners],
    files: ownedFiles.map((f) => ({
      path: f.path,
      additions: f.additions,
      deletions: f.deletions,
      hunk: parsePatchToHunkLines(f.patch),
    })),
    focusRule: isActionable ? repoRulesMap.get(pr.repository_name) : undefined,
  };
}

/**
 * An open PR that is not in the user's inbox: it touches none of their files, or (pending) its
 * changed files are not known yet. It shows in its seat only, with no inbox badge.
 */
function mapSeatPrToModel(pr: ApiPullRequestDto, codeowners: CodeownersState | undefined, pending: boolean): PullRequestItem {
  const state: ApiUserPrStateDto = { pull_request_id: pr.id, user_id: 0, turn_state: 'HIDDEN_GATE', team_already_approved: false };
  const item: PullRequestItem = { ...mapInboxPrToModel(pr, state, false, new Map(), codeowners), muteBadge: undefined, muteTip: undefined };
  // Not evaluated is not "other owners": say that the files were not checked.
  return pending ? { ...item, owners: 'unknown', ownersText: '', why: { key: 'drawer.why.pending' } } : item;
}

/** Dependabot titles look like "chore(deps): bump axios from 1.7.7 to 2.0.0". */
const DEPENDABOT_TITLE = /\bbump (\S+) from (\S+) to (\S+)/i;

function mapDependabotPrToModel(
  pr: ApiPullRequestDto,
  analysis: ApiDependabotAnalysisDto | undefined,
  codeowners: CodeownersState | undefined,
): PullRequestItem {
  const { ci, ciText } = mapCiStatus(pr.ci_status);
  const owners = ownersForNonOwner(codeowners);
  const base = {
    id: pr.id,
    number: pr.number,
    rank: 2,
    repo: pr.repository_name,
    title: pr.title,
    author: pr.author_login,
    branch: pr.base_ref || 'main',
    ci,
    ciText,
    owners,
    ownersText: owners === 'others' ? { key: 'owners.others' } : '',
    merge: mapMergeState(pr.mergeable_state),
    turn: false,
    dep: true,
    files: [],
  } satisfies Partial<PullRequestItem>;

  if (!analysis) {
    // Nothing analysed this bump yet: say only what the PR title tells us.
    const match = DEPENDABOT_TITLE.exec(pr.title);
    const [pkg, from, to] = match ? [match[1], match[2], match[3]] : [undefined, undefined, undefined];
    return {
      ...base,
      verdict: null,
      radarTitle: pkg ? `${pkg} (${from} -> ${to})` : pr.title,
      radarMeta: { key: 'radar.meta.none' },
      pkg,
      from,
      to,
      rvDetail: { key: pkg ? 'impact.rvDetail.none' : 'impact.rvDetail.grouped' },
      via: [],
      blasts: [],
      note: '',
      noteWarn: false,
      why: { key: 'drawer.why.depNone' },
    };
  }

  const verdictMap: Record<string, RiskVerdictTier> = { LOW: 'baixo', MEDIUM: 'medio', HIGH: 'alto' };
  const verdict = verdictMap[analysis.risk_verdict] ?? null;
  const pkg = analysis.package_name;
  const from = analysis.from_version;
  const to = analysis.to_version;
  const depType = analysis.dep_scope === 'DEV' ? 'devDependencies' : 'dependencies';

  const graph = analysis.dependency_path_graph ?? [];
  const intermediateNodes = graph.length > 2 ? graph.slice(1, -1) : [];
  const via: DependencyViaNode[] = intermediateNodes.map((n) => ({
    label: `⚙️ ${n.name} v${n.version} (${n.scope === 'DEV' ? 'Dev' : 'Prod'})`,
    info: { key: 'impact.via.info', params: { pkg } },
  }));

  const usages = analysis.blast_radius_usages ?? [];
  const blasts: BlastFileItem[] = usages.map((u) => ({
    file: `${u.file_path}:${u.line_number}`,
    kind: u.file_path.includes('test') || u.file_path.includes('spec') ? 'test' : 'prod',
    uses: 1,
    breaks: verdict === 'alto',
    snippets: [
      {
        start: Math.max(1, u.line_number - 1),
        hit: u.line_number,
        lines: u.code_snippet.split('\n'),
      },
    ],
  }));

  const breakingNote = analysis.advisories_and_notes?.[0];
  const scope = analysis.dep_scope === 'DEV' ? 'devDirect' : analysis.is_direct_dep ? 'prodDirect' : 'prodTransitive';

  return {
    ...base,
    radarTitle: `${pkg} (${from} -> ${to})`,
    radarMeta: { key: `radar.meta.${scope}`, params: { count: usages.length } },
    verdict,
    pkg,
    from,
    to,
    depType,
    score: analysis.risk_score,
    rvDetail: { key: `impact.rvDetail.${scope}`, params: { pkg, from, to, count: usages.length } },
    via,
    blasts,
    note: breakingNote ? `⚠️ ${breakingNote.summary}` : { key: 'impact.note.clean' },
    noteWarn: Boolean(breakingNote?.is_breaking_note),
    why: { key: 'drawer.why.depDone' },
  };
}

export interface ApiSyncStatusDto {
  state?: string;
  total?: number;
  done?: number;
  current?: string;
  result?: {
    repositories?: number;
    added?: number;
    updated?: number;
    closed?: number;
    merged?: number;
    skipped?: number;
    rate_limited_until?: string;
    failed?: { repository: string; code: string }[];
  };
  error?: { code?: string; retry_at?: string };
}

const SYNC_STATES: readonly SyncStatus['state'][] = ['idle', 'running', 'done', 'failed', 'canceled'];

export function mapSyncStatus(dto: ApiSyncStatusDto | undefined): SyncStatus {
  const state = SYNC_STATES.find((s) => s === dto?.state) ?? 'idle';
  const r = dto?.result;
  return {
    state,
    total: dto?.total ?? 0,
    done: dto?.done ?? 0,
    current: dto?.current ?? '',
    result: r && {
      repositories: r.repositories ?? 0,
      added: r.added ?? 0,
      updated: r.updated ?? 0,
      closed: r.closed ?? 0,
      merged: r.merged ?? 0,
      skipped: r.skipped ?? 0,
      ...(r.rate_limited_until ? { rateLimitedUntil: r.rate_limited_until } : {}),
      failed: r.failed ?? [],
    },
    error: dto?.error?.code ? { code: dto.error.code, ...(dto.error.retry_at ? { retryAt: dto.error.retry_at } : {}) } : undefined,
  };
}
