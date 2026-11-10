import {
  BlastFileItem,
  ChangedFileItem,
  CiState,
  CodeownersState,
  DependencyViaNode,
  DiffHunkLine,
  MergeState,
  OwnersState,
  PullRequestItem,
  RepoInfo,
  RepositoryAccessState,
  RiskVerdictTier,
  TimelineEventItem,
  WorkspaceDataset,
} from '../../domain/models/workspace.models';

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
  readonly summary: string;
  readonly occurred_at: string;
}

export interface ApiWorkspaceResponseDto {
  readonly snapshot?: {
    readonly inbox_waiting_on_me?: readonly { pull_request: ApiPullRequestDto; user_state: ApiUserPrStateDto }[];
    readonly inbox_dimmed?: readonly { pull_request: ApiPullRequestDto; user_state: ApiUserPrStateDto }[];
    readonly dependabot_prs?: readonly { pull_request: ApiPullRequestDto; analysis?: ApiDependabotAnalysisDto }[];
    readonly seats?: readonly {
      repository: ApiRepositoryDto;
      open_pr_count: number;
      waiting_on_me_count: number;
      dependabot_pr_count: number;
      failing_ci_count: number;
      pull_requests: readonly ApiPullRequestDto[];
    }[];
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
  const silencedMap: Record<number, string> = {};

  for (const item of snapshot.inbox_waiting_on_me ?? []) {
    const mapped = mapInboxPrToModel(item.pull_request, item.user_state, true, repoRulesMap, codeownersOf(item.pull_request.repository_name));
    prs.push(mapped);
  }

  for (const item of snapshot.inbox_dimmed ?? []) {
    const mapped = mapInboxPrToModel(item.pull_request, item.user_state, false, repoRulesMap, codeownersOf(item.pull_request.repository_name));
    prs.push(mapped);
    if (item.user_state.turn_state === 'APPROVED_BY_ME') {
      approvedMap[mapped.id] = true;
    }
    if (item.user_state.turn_state === 'SNOOZED') {
      silencedMap[mapped.id] =
        item.user_state.snoozed_until_condition === 'UNTIL_CI_GREEN'
          ? '🔕 Silenciado até o CI passar'
          : '🔕 Silenciado até o próximo commit';
    }
  }

  for (const item of snapshot.dependabot_prs ?? []) {
    prs.push(mapDependabotPrToModel(item.pull_request, item.analysis, codeownersOf(item.pull_request.repository_name)));
  }

  const events: TimelineEventItem[] = (dto.timeline ?? []).map((ev, index) => ({
    ...mapActivityEventToTimelineItem(ev),
    fresh: false, // loaded history; only live activity is highlighted
    key: index + 1,
    t: Date.parse(ev.occurred_at) || Date.now(),
  }));

  return { repos, prs, events, approvedMap, silencedMap };
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
    text: ev.summary,
    repo: ev.repository_name,
    pr: ev.pr_number || ev.pull_request_id,
    fresh: true,
    // Every server activity changes what the workspace shows (a new PR, a commit, lost access).
    refreshWorkspace: true,
  };
}

function mapCiStatus(status: ApiPullRequestDto['ci_status']): { ci: CiState; ciText: string } {
  if (status === 'FAILURE') {
    return { ci: 'fail', ciText: 'Checks falhando no CI' };
  }
  if (status === 'PENDING') {
    return { ci: 'run', ciText: 'CI em execução' };
  }
  if (status === 'SUCCESS') {
    return { ci: 'ok', ciText: 'Todos os checks passaram' };
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

const OWNERS_REASON: Record<OwnersState, string> = {
  voce: 'Você é dono de arquivos alterados neste PR (CODEOWNERS).',
  ok: 'Já aprovado por alguém do time.',
  others: 'Os arquivos alterados pertencem a outros donos no CODEOWNERS.',
  none: 'Este repositório não tem CODEOWNERS: nenhuma revisão é atribuída automaticamente.',
  unknown: 'Ainda não verificamos o CODEOWNERS deste repositório.',
};

function mapInboxPrToModel(
  pr: ApiPullRequestDto,
  st: ApiUserPrStateDto,
  isActionable: boolean,
  repoRulesMap: Map<string, string>,
  codeowners: CodeownersState | undefined,
): PullRequestItem {
  const prId = pr.number || pr.id;
  const { ci, ciText } = mapCiStatus(pr.ci_status);
  const ownedFiles = st.owned_files_changed ?? [];
  const approvedBy = st.approved_by_login
    ? st.approved_by_login.startsWith('@')
      ? st.approved_by_login
      : `@${st.approved_by_login}`
    : undefined;

  const owners: OwnersState = isActionable ? 'voce' : ownersForNonOwner(codeowners, approvedBy);
  const ownersText = {
    voce: ownedFiles.length > 0 ? `Você é dono de ${ownedFiles.length} arquivo(s)` : 'Você é dono de arquivos alterados',
    ok: `Aprovado por ${approvedBy}`,
    others: 'Os arquivos alterados têm outros donos',
    none: '',
    unknown: '',
  }[owners];

  let muteBadge: string | undefined;
  let muteTip: string | undefined;
  if (!isActionable) {
    if (st.turn_state === 'TEAMMATE_APPROVED' && approvedBy) {
      muteBadge = `✅ Time já aprovou (${approvedBy})`;
      muteTip = 'A aprovação de um colega do seu time dispensa a sua revisão.';
    } else if (st.turn_state === 'WAITING_ON_AUTHOR') {
      muteBadge = '⏳ Aguardando Autor (você comentou no SHA atual)';
      muteTip = 'Volta para sua fila quando o autor enviar um novo commit.';
    } else if (pr.is_draft) {
      muteBadge = '🚧 Rascunho';
      muteTip = 'Entra na fila quando o autor marcar o PR como pronto para revisão.';
    } else if (st.turn_state === 'HIDDEN_GATE' && pr.ci_status === 'PENDING') {
      muteBadge = '🚧 CI em execução';
      muteTip = 'Entra na fila quando o CI terminar.';
    }
  }

  return {
    id: prId,
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
    ownPill: ownedFiles.length > 0 ? `🎯 Toca ${ownedFiles.length} arquivo(s) seu(s)` : undefined,
    claim: pr.claimed_by_login ? `Assumido por @${pr.claimed_by_login.replace(/^@/, '')}` : undefined,
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

/** Dependabot titles look like "chore(deps): bump axios from 1.7.7 to 2.0.0". */
const DEPENDABOT_TITLE = /\bbump (\S+) from (\S+) to (\S+)/i;

function mapDependabotPrToModel(
  pr: ApiPullRequestDto,
  analysis: ApiDependabotAnalysisDto | undefined,
  codeowners: CodeownersState | undefined,
): PullRequestItem {
  const prId = pr.number || pr.id;
  const { ci, ciText } = mapCiStatus(pr.ci_status);
  const owners = ownersForNonOwner(codeowners);
  const base = {
    id: prId,
    rank: 2,
    repo: pr.repository_name,
    title: pr.title,
    author: pr.author_login,
    branch: pr.base_ref || 'main',
    ci,
    ciText,
    owners,
    ownersText: owners === 'others' ? 'Os arquivos alterados têm outros donos' : '',
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
      radarMeta: 'Sem análise de impacto',
      pkg,
      from,
      to,
      rvDetail: 'Análise de impacto ainda não executada.',
      via: [],
      blasts: [],
      note: '',
      noteWarn: false,
      why: 'Análise de impacto ainda não executada para este PR.',
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
    info: `Dependência intermediária que traz ${pkg} para o projeto.`,
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
  const scopeLabel =
    analysis.dep_scope === 'DEV' ? 'Dev Direta' : analysis.is_direct_dep ? 'Prod Direta' : 'Prod Transitiva';

  return {
    ...base,
    radarTitle: `${pkg} (${from} -> ${to})`,
    radarMeta: `${scopeLabel} • ${usages.length} import(s) no código`,
    verdict,
    pkg,
    from,
    to,
    depType,
    score: analysis.risk_score,
    rvDetail: `${scopeLabel} (${pkg} ${from} → ${to}) com ${usages.length} import(s) detectado(s).`,
    via,
    blasts,
    note: breakingNote ? `⚠️ ${breakingNote.summary}` : 'Nenhuma breaking change ou CVE registrada nesta versão.',
    noteWarn: Boolean(breakingNote?.is_breaking_note),
    why: 'Análise zero-clone concluída a partir do lockfile e AST do repositório.',
  };
}
