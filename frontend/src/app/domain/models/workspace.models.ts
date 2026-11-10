export type PerspectiveView = 'seats' | 'inbox' | 'radar' | 'timeline' | 'import';
export type SeatFilterType = 'todos' | 'turn' | 'deps';
export type RiskVerdictTier = 'baixo' | 'medio' | 'alto';
/** CI of the PR head as GitHub reported it; `none` means no check was ever reported (e.g. no CI). */
export type CiState = 'ok' | 'run' | 'fail' | 'none';
/** CODEOWNERS review state; `none` means the repository has no CODEOWNERS file. */
export type OwnersState = 'ok' | 'voce' | 'pend' | 'none';
/** GitHub's mergeable_state; `unknown` until GitHub computes it. */
export type MergeState = 'clean' | 'conflict' | 'behind' | 'blocked' | 'unstable' | 'draft' | 'unknown';
/** Whether a repository has a CODEOWNERS file, once checked. */
export type CodeownersState = 'present' | 'absent';
export type SseConnectionState = 'conectado' | 'reconectando' | 'desconectado';

export interface DiffHunkLine {
  readonly t: 'hdr' | 'ctx' | 'add' | 'del';
  readonly s: string;
}

export interface DependencyViaNode {
  readonly label: string;
  readonly info: string;
}

export interface BlastSnippet {
  readonly start: number;
  readonly hit: number;
  readonly lines: readonly string[];
}

export interface BlastFileItem {
  readonly file: string;
  readonly kind: 'prod' | 'test';
  readonly uses: number;
  readonly breaks?: boolean;
  readonly snippets: readonly BlastSnippet[];
}

export interface PullRequestItem {
  readonly id: number;
  readonly rank: number;
  readonly repo: string;
  readonly title: string;
  readonly seatTitle?: string;
  readonly radarTitle?: string;
  readonly radarMeta?: string;
  readonly author: string;
  readonly branch: string;
  readonly verdict: RiskVerdictTier;
  readonly ci: CiState;
  readonly ciText: string;
  readonly owners: OwnersState;
  readonly ownersText: string;
  readonly merge?: MergeState;
  readonly size: string;
  readonly turn: boolean;
  readonly ownFiles?: number;
  readonly ownPill?: string;
  readonly claim?: string;
  readonly approvedBy?: string;
  readonly muteBadge?: string;
  readonly muteTip?: string;
  readonly why: string;
  readonly file: string;
  readonly deltaAdd?: string;
  readonly deltaDel?: string;
  readonly focusRule?: string;
  readonly hunk: readonly DiffHunkLine[];
  readonly dep?: boolean;
  readonly pkg?: string;
  readonly from?: string;
  readonly to?: string;
  readonly depType?: 'dependencies' | 'devDependencies';
  readonly score?: number;
  readonly rvDetail?: string;
  readonly via?: readonly DependencyViaNode[];
  readonly blast?: {
    readonly file: string;
    readonly start: number;
    readonly hit: number;
    readonly lines: readonly string[];
  };
  readonly blasts?: readonly BlastFileItem[];
  readonly note?: string;
  readonly noteWarn?: boolean;
}

/** Whether the GitHub App still reaches a followed repository (ADR-0010). */
export type RepositoryAccessState = 'active' | 'removed' | 'suspended';

export interface RepoInfo {
  readonly name: string;
  readonly branch: string;
  readonly access?: RepositoryAccessState;
  /** Unset until the backend has checked the repository for a CODEOWNERS file. */
  readonly codeowners?: CodeownersState;
}

export interface TimelineEventItem {
  readonly key: number;
  readonly t: number;
  readonly kind: 'ok' | 'fail' | 'turn' | 'info';
  readonly text: string;
  readonly repo: string;
  readonly pr?: number;
  readonly fresh?: boolean;
  /** The event changed what the workspace shows (e.g. repository access), so reload it. */
  readonly refreshWorkspace?: boolean;
}

export interface WorkspaceDataset {
  readonly repos: readonly RepoInfo[];
  readonly prs: readonly PullRequestItem[];
  readonly events: readonly TimelineEventItem[];
  readonly approvedMap?: Record<number, boolean>;
  readonly silencedMap?: Record<number, string>;
}

/** The signed-in GitHub user (ADR-0010). Tokens never reach the browser. */
export interface AuthenticatedUser {
  readonly id: number;
  readonly login: string;
  readonly name: string;
  readonly avatarUrl: string;
  readonly avatarInitial: string;
  readonly primaryTeam: string;
  readonly teams: readonly string[];
}

export interface GitHubProfileInput {
  readonly id: number;
  readonly login: string;
  readonly name?: string;
  readonly avatarUrl?: string;
  readonly teams?: readonly string[];
}

/** Why the last GitHub sign-in attempt failed (`?auth_error=` from the backend callback). */
export type AuthErrorCode = 'not_configured' | 'state_mismatch' | 'access_denied' | 'login_failed' | 'unknown';

export type AuthStatus = 'checking' | 'signed-out' | 'redirecting' | 'signed-in';

export function buildAuthenticatedUser(profile: GitHubProfileInput): AuthenticatedUser {
  const teams = profile.teams ?? [];
  return {
    id: profile.id,
    login: profile.login,
    name: profile.name || profile.login,
    avatarUrl: profile.avatarUrl ?? '',
    avatarInitial: profile.login.charAt(0).toUpperCase(),
    primaryTeam: teams[0] ?? '',
    teams,
  };
}

export function computeSemverBumpLabel(fromVersion: string, toVersion: string): 'Major' | 'Minor' | 'Patch' {
  const fromParts = fromVersion.split('.');
  const toParts = toVersion.split('.');
  if (fromParts[0] !== toParts[0]) {
    return 'Major';
  }
  if (fromParts[1] !== toParts[1]) {
    return 'Minor';
  }
  return 'Patch';
}

export function isActionableTurn(
  pr: PullRequestItem,
  silenced: Record<number, string>,
  approved: Record<number, boolean>,
): boolean {
  return Boolean(pr.turn && !silenced[pr.id] && !approved[pr.id]);
}

export function isSafeBatchCandidate(pr: PullRequestItem, ciOverride?: CiState): boolean {
  const effectiveCi = ciOverride ?? pr.ci;
  return Boolean(pr.dep && pr.verdict === 'baixo' && effectiveCi === 'ok');
}
