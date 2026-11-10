import { Injectable, computed, inject, signal } from '@angular/core';
import { REPOSITORY_IMPORT_GATEWAY } from '../../domain/gateways/workspace.gateways';
import {
  GitHubAccount,
  ImportableRepository,
  RepositoryImportError,
  RepositoryImportErrorCode,
  filterImportableRepositories,
} from '../../domain/models/repository-import.models';
import { WorkspaceStore } from './workspace.store';

export type RepositoryImportStatus = 'idle' | 'loading-accounts' | 'loading-repositories' | 'ready' | 'error';

/** Vercel-style "Import repository" screen state (ADR-0010). */
@Injectable({ providedIn: 'root' })
export class RepositoryImportStore {
  private readonly gateway = inject(REPOSITORY_IMPORT_GATEWAY);
  private readonly workspace = inject(WorkspaceStore);

  readonly accounts = signal<readonly GitHubAccount[]>([]);
  readonly installUrl = signal('');
  readonly selectedInstallationId = signal<number | null>(null);
  readonly repositories = signal<readonly ImportableRepository[]>([]);
  readonly query = signal('');
  readonly status = signal<RepositoryImportStatus>('idle');
  readonly error = signal<RepositoryImportErrorCode | null>(null);
  private readonly pending = signal<ReadonlySet<number>>(new Set());

  readonly selectedAccount = computed(
    () => this.accounts().find((a) => a.installationId === this.selectedInstallationId()) ?? null,
  );
  readonly visibleRepositories = computed(() => filterImportableRepositories(this.repositories(), this.query()));

  private preferredInstallationId: number | null = null;
  private listing: AbortController | null = null;

  /**
   * After GitHub sends the user back from installing or adjusting the app (Setup URL),
   * open the import screen on that account. Returns whether that happened.
   */
  resumeAfterInstall(): boolean {
    const installationId = this.gateway.consumeSetupRedirect();
    if (installationId === null) {
      return false;
    }
    this.preferredInstallationId = installationId;
    this.workspace.setPerspective('import');
    return true;
  }

  /** Loads the accounts and the repositories of the preferred (or first) account. */
  async open(preferredInstallationId: number | null = null): Promise<void> {
    const preferred = preferredInstallationId ?? this.preferredInstallationId ?? this.selectedInstallationId();
    this.preferredInstallationId = null;
    this.error.set(null);
    this.status.set('loading-accounts');
    try {
      const result = await this.gateway.listAccounts();
      this.accounts.set(result.accounts);
      this.installUrl.set(result.installUrl);
    } catch (err) {
      this.fail(err);
      return;
    }
    const target =
      this.accounts().find((a) => a.installationId === preferred) ?? this.accounts()[0] ?? null;
    if (!target) {
      this.selectedInstallationId.set(null);
      this.repositories.set([]);
      this.status.set('ready');
      return;
    }
    await this.selectAccount(target.installationId);
  }

  /** Lists one account's repositories, cancelling any listing still in flight (ADR-0007). */
  async selectAccount(installationId: number): Promise<void> {
    this.listing?.abort();
    const controller = new AbortController();
    this.listing = controller;
    this.selectedInstallationId.set(installationId);
    this.repositories.set([]);
    this.error.set(null);
    this.status.set('loading-repositories');
    try {
      const repositories = await this.gateway.listRepositories(installationId, { signal: controller.signal });
      if (controller.signal.aborted) {
        return;
      }
      this.repositories.set(repositories);
      this.status.set('ready');
    } catch (err) {
      if (!controller.signal.aborted) {
        this.fail(err);
      }
    }
  }

  setQuery(query: string): void {
    this.query.set(query);
  }

  isPending(githubRepoId: number): boolean {
    return this.pending().has(githubRepoId);
  }

  async toggleFollow(repository: ImportableRepository): Promise<void> {
    if (this.isPending(repository.githubRepoId)) {
      return;
    }
    this.setPending(repository.githubRepoId, true);
    this.error.set(null);
    try {
      if (repository.followed && repository.repositoryId !== null) {
        await this.gateway.unfollowRepository(repository.repositoryId);
        this.patch(repository.githubRepoId, { followed: false, repositoryId: null });
      } else {
        const repositoryId = await this.gateway.importRepository(repository.installationId, repository.githubRepoId);
        this.patch(repository.githubRepoId, { followed: true, repositoryId });
      }
      void this.workspace.loadWorkspace();
    } catch (err) {
      this.error.set(err instanceof RepositoryImportError ? err.code : 'failed');
    } finally {
      this.setPending(repository.githubRepoId, false);
    }
  }

  private patch(githubRepoId: number, changes: Partial<ImportableRepository>): void {
    this.repositories.update((list) => list.map((r) => (r.githubRepoId === githubRepoId ? { ...r, ...changes } : r)));
  }

  private setPending(githubRepoId: number, on: boolean): void {
    this.pending.update((current) => {
      const next = new Set(current);
      if (on) {
        next.add(githubRepoId);
      } else {
        next.delete(githubRepoId);
      }
      return next;
    });
  }

  private fail(err: unknown): void {
    this.error.set(err instanceof RepositoryImportError ? err.code : 'failed');
    this.status.set('error');
  }
}
