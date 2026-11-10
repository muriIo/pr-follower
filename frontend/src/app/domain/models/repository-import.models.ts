/** A GitHub user or organization account where the PR Follower app is installed (ADR-0010). */
export interface GitHubAccount {
  readonly installationId: number;
  readonly login: string;
  readonly type: 'User' | 'Organization';
  readonly avatarUrl: string;
  readonly repositorySelection: 'all' | 'selected';
  /** GitHub page where the user changes which repositories the app can reach. */
  readonly settingsUrl: string;
}

export interface GitHubAccountsResult {
  readonly accounts: readonly GitHubAccount[];
  /** GitHub page for installing the app on another account (empty when not configured). */
  readonly installUrl: string;
}

/** A repository the user can reach through an installation, with their follow state. */
export interface ImportableRepository {
  readonly githubRepoId: number;
  readonly installationId: number;
  readonly owner: string;
  readonly name: string;
  readonly fullName: string;
  readonly defaultBranch: string;
  readonly private: boolean;
  readonly followed: boolean;
  /** PR Follower repository id once followed. */
  readonly repositoryId: number | null;
}

export type RepositoryImportErrorCode = 'unauthorized' | 'not_found' | 'not_configured' | 'failed';

export class RepositoryImportError extends Error {
  constructor(readonly code: RepositoryImportErrorCode) {
    super(`repository import failed: ${code}`);
    this.name = 'RepositoryImportError';
  }
}

export function filterImportableRepositories(
  repositories: readonly ImportableRepository[],
  query: string,
): readonly ImportableRepository[] {
  const needle = query.trim().toLowerCase();
  return repositories
    .filter((r) => needle === '' || r.fullName.toLowerCase().includes(needle))
    .slice()
    .sort((a, b) => a.fullName.toLowerCase().localeCompare(b.fullName.toLowerCase()));
}
