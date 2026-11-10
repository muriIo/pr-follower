import {
  BlastFileItem,
  CiState,
  DependencyViaNode,
  DiffHunkLine,
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
  readonly mergeable_state: 'CLEAN' | 'CONFLICTING' | 'UNKNOWN';
  readonly ci_status: 'PENDING' | 'SUCCESS' | 'FAILURE';
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
  const repos: RepoInfo[] = seats.map((s) => ({
    name: s.repository.full_name,
    branch: s.repository.default_branch || 'main',
    access: s.repository.access ?? 'active',
  }));

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
    const mapped = mapInboxPrToModel(item.pull_request, item.user_state, true, repoRulesMap);
    prs.push(mapped);
  }

  for (const item of snapshot.inbox_dimmed ?? []) {
    const mapped = mapInboxPrToModel(item.pull_request, item.user_state, false, repoRulesMap);
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
    prs.push(mapDependabotPrToModel(item.pull_request, item.analysis));
  }

  const events: TimelineEventItem[] = (dto.timeline ?? []).map((ev, index) => ({
    ...mapActivityEventToTimelineItem(ev),
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
  return { ci: 'ok', ciText: 'Todos os checks passaram' };
}

function parsePatchToHunkLines(patch?: string): readonly DiffHunkLine[] {
  if (!patch) {
    return [{ t: 'hdr', s: '@@ -1,1 +1,1 @@' }];
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

function mapInboxPrToModel(
  pr: ApiPullRequestDto,
  st: ApiUserPrStateDto,
  isActionable: boolean,
  repoRulesMap: Map<string, string>,
): PullRequestItem {
  const prId = pr.number || pr.id;
  const { ci, ciText } = mapCiStatus(pr.ci_status);
  const ownedFiles = st.owned_files_changed ?? [];
  const firstFile = ownedFiles[0];
  const approvedBy = st.approved_by_login
    ? st.approved_by_login.startsWith('@')
      ? st.approved_by_login
      : `@${st.approved_by_login}`
    : undefined;

  const owners: OwnersState = isActionable ? 'voce' : approvedBy ? 'ok' : 'pend';
  const ownersText = isActionable
    ? `Você é dono de ${ownedFiles.length || 1} arquivo(s)`
    : approvedBy
      ? `Aprovado por ${approvedBy}`
      : 'Revisão pendente';

  let muteBadge: string | undefined;
  let muteTip: string | undefined;
  if (!isActionable) {
    if (st.turn_state === 'TEAMMATE_APPROVED' && approvedBy) {
      muteBadge = `✅ Time já aprovou (${approvedBy})`;
      muteTip = 'A aprovação de um colega do seu time dispensa a sua revisão.';
    } else if (st.turn_state === 'WAITING_ON_AUTHOR') {
      muteBadge = '⏳ Aguardando Autor (você comentou no SHA atual)';
      muteTip = 'Volta para sua fila quando o autor enviar um novo commit.';
    } else if (st.turn_state === 'HIDDEN_GATE' || pr.is_draft) {
      muteBadge = '🚧 Oculto pelo Gate (Rascunho / CI Pendente)';
      muteTip = 'Entra na sua fila quando sair do rascunho e o CI passar.';
    }
  }

  return {
    id: prId,
    rank: isActionable ? 1 : 3,
    repo: pr.repository_name,
    title: pr.title,
    author: pr.author_login.startsWith('@') ? pr.author_login : `@${pr.author_login}`,
    branch: pr.base_ref || 'main',
    verdict: 'baixo',
    ci,
    ciText,
    owners,
    ownersText,
    size: `${Math.max(ownedFiles.length, 1)} arquivo(s)`,
    turn: isActionable,
    ownFiles: ownedFiles.length || undefined,
    ownPill:
      ownedFiles.length > 0 ? `🎯 Toca ${ownedFiles.length} arquivo(s) seu(s)` : undefined,
    claim: pr.claimed_by_login ? `Assumido por @${pr.claimed_by_login.replace(/^@/, '')}` : undefined,
    approvedBy,
    muteBadge,
    muteTip,
    why: isActionable
      ? 'Acionado via regra CODEOWNERS para arquivos sob sua responsabilidade.'
      : 'Filtrado automaticamente pelo Gate de Turno.',
    file: firstFile?.path ?? 'src/index.ts',
    deltaAdd: firstFile ? `+${firstFile.additions}` : undefined,
    deltaDel: firstFile ? `-${firstFile.deletions}` : undefined,
    focusRule: isActionable ? repoRulesMap.get(pr.repository_name) : undefined,
    hunk: parsePatchToHunkLines(firstFile?.patch),
  };
}

function mapDependabotPrToModel(
  pr: ApiPullRequestDto,
  analysis?: ApiDependabotAnalysisDto,
): PullRequestItem {
  const prId = pr.number || pr.id;
  const { ci, ciText } = mapCiStatus(pr.ci_status);
  const verdictMap: Record<string, RiskVerdictTier> = {
    LOW: 'baixo',
    MEDIUM: 'medio',
    HIGH: 'alto',
  };
  const verdict: RiskVerdictTier = verdictMap[analysis?.risk_verdict ?? 'LOW'] ?? 'baixo';
  const pkg = analysis?.package_name ?? 'dependency';
  const from = analysis?.from_version ?? '0.0.0';
  const to = analysis?.to_version ?? '0.0.1';
  const depType = analysis?.dep_scope === 'DEV' ? 'devDependencies' : 'dependencies';

  const graph = analysis?.dependency_path_graph ?? [];
  const intermediateNodes = graph.length > 2 ? graph.slice(1, -1) : [];
  const via: DependencyViaNode[] = intermediateNodes.map((n) => ({
    label: `⚙️ ${n.name} v${n.version} (${n.scope === 'DEV' ? 'Dev' : 'Prod'})`,
    info: `Dependência intermediária que traz ${pkg} para o projeto.`,
  }));

  const usages = analysis?.blast_radius_usages ?? [];
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

  const breakingNote = analysis?.advisories_and_notes?.[0];
  const scopeLabel =
    analysis?.dep_scope === 'DEV'
      ? 'Dev Direta'
      : analysis?.is_direct_dep
        ? 'Prod Direta'
        : 'Prod Transitiva';

  return {
    id: prId,
    rank: 2,
    repo: pr.repository_name,
    title: pr.title,
    radarTitle: `${pkg} (${from} -> ${to})`,
    radarMeta: `${scopeLabel} • ${usages.length} import(s) no código`,
    author: pr.author_login,
    branch: pr.base_ref || 'main',
    verdict,
    ci,
    ciText,
    owners: 'ok',
    ownersText: 'Sem dono obrigatório',
    size: '2 arquivos, lockfile incluído',
    turn: false,
    dep: true,
    pkg,
    from,
    to,
    depType,
    score: analysis?.risk_score ?? 10,
    rvDetail: `${scopeLabel} (${pkg} ${from} → ${to}) com ${usages.length} import(s) detectado(s).`,
    via,
    blasts,
    note: breakingNote
      ? `⚠️ ${breakingNote.summary}`
      : 'Nenhuma breaking change ou CVE registrada nesta versão.',
    noteWarn: Boolean(breakingNote?.is_breaking_note),
    why: 'Análise zero-clone concluída a partir do lockfile e AST do repositório.',
    file: 'package.json',
    hunk: [
      { t: 'hdr', s: `@@ -18,3 +18,3 @@ "${depType}": {` },
      { t: 'del', s: `- "${pkg}": "^${from}",` },
      { t: 'add', s: `+ "${pkg}": "^${to}",` },
    ],
  };
}
