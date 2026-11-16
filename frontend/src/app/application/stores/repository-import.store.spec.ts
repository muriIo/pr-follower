import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { REPOSITORY_IMPORT_GATEWAY, RepositoryImportGateway } from '../../domain/gateways/workspace.gateways';
import {
  GitHubAccount,
  ImportableRepository,
  RepositoryImportError,
} from '../../domain/models/repository-import.models';
import { RepositoryImportStore } from './repository-import.store';
import { WorkspaceStore } from './workspace.store';

const account = (installationId: number, login: string): GitHubAccount => ({
  installationId,
  login,
  type: 'Organization',
  avatarUrl: '',
  repositorySelection: 'selected',
  settingsUrl: `https://github.com/organizations/${login}/settings/installations/${installationId}`,
});

const repo = (githubRepoId: number, fullName: string, followed = false): ImportableRepository => ({
  githubRepoId,
  installationId: 20,
  owner: fullName.split('/')[0],
  name: fullName.split('/')[1],
  fullName,
  defaultBranch: 'main',
  private: false,
  followed,
  repositoryId: followed ? 1 : null,
});

function setup(overrides: Partial<RepositoryImportGateway> = {}) {
  const gateway: RepositoryImportGateway = {
    listAccounts: vi.fn(async () => ({
      accounts: [account(10, 'octocat'), account(20, 'acme')],
      installUrl: 'https://github.com/apps/pr-follower/installations/new',
    })),
    listRepositories: vi.fn(async (id: number) =>
      id === 20 ? [repo(900, 'acme/web'), repo(901, 'acme/api', true)] : [repo(100, 'octocat/dotfiles')],
    ),
    importRepository: vi.fn(async () => 42),
    unfollowRepository: vi.fn(async () => undefined),
    consumeSetupRedirect: vi.fn(() => null),
    ...overrides,
  };
  const workspace = { loadWorkspace: vi.fn(async () => undefined), setPerspective: vi.fn() };
  TestBed.configureTestingModule({
    providers: [
      RepositoryImportStore,
      { provide: REPOSITORY_IMPORT_GATEWAY, useValue: gateway },
      { provide: WorkspaceStore, useValue: workspace },
    ],
  });
  return { store: TestBed.inject(RepositoryImportStore), gateway, workspace };
}

describe('RepositoryImportStore (Vercel-style import, ADR-0010)', () => {
  it('opens on the first account and lists its repositories', async () => {
    const { store, gateway } = setup();
    await store.open();

    expect(store.accounts().map((a) => a.login)).toEqual(['octocat', 'acme']);
    expect(store.installUrl()).toContain('/installations/new');
    expect(store.selectedAccount()?.login).toBe('octocat');
    expect(gateway.listRepositories).toHaveBeenCalledWith(10, expect.objectContaining({ signal: expect.any(AbortSignal) }));
    expect(store.visibleRepositories().map((r) => r.fullName)).toEqual(['octocat/dotfiles']);
    expect(store.status()).toBe('ready');
  });

  it('switches accounts, cancelling the previous listing, and filters by query', async () => {
    const signals: AbortSignal[] = [];
    const { store } = setup({
      listRepositories: vi.fn(async (id: number, options?: { signal?: AbortSignal }) => {
        if (options?.signal) signals.push(options.signal);
        return id === 20 ? [repo(900, 'acme/web'), repo(901, 'acme/api', true)] : [repo(100, 'octocat/dotfiles')];
      }),
    });
    await store.open();
    await store.selectAccount(20);

    expect(signals[0].aborted).toBe(true);
    expect(store.visibleRepositories().map((r) => r.fullName)).toEqual(['acme/api', 'acme/web']);

    store.setQuery('WEB');
    expect(store.visibleRepositories().map((r) => r.fullName)).toEqual(['acme/web']);
  });

  it('returns to the account the user just installed the app on', async () => {
    const { store, workspace } = setup({ consumeSetupRedirect: vi.fn(() => 20) });

    expect(store.resumeAfterInstall()).toBe(true);
    expect(workspace.setPerspective).toHaveBeenCalledWith('import');

    await store.open();
    expect(store.selectedAccount()?.login).toBe('acme');
  });

  it('does nothing after sign-in when GitHub did not send the user back from an install', () => {
    const { store, workspace } = setup();
    expect(store.resumeAfterInstall()).toBe(false);
    expect(workspace.setPerspective).not.toHaveBeenCalled();
  });

  it('follows and unfollows, refreshing the workspace each time', async () => {
    const { store, gateway, workspace } = setup();
    await store.open(20);
    const web = store.visibleRepositories().find((r) => r.fullName === 'acme/web')!;

    const pending = store.toggleFollow(web);
    expect(store.isPending(900)).toBe(true);
    await pending;

    expect(gateway.importRepository).toHaveBeenCalledWith(20, 900);
    expect(store.isPending(900)).toBe(false);
    const followed = store.visibleRepositories().find((r) => r.fullName === 'acme/web')!;
    expect(followed.followed).toBe(true);
    expect(followed.repositoryId).toBe(42);
    expect(workspace.loadWorkspace).toHaveBeenCalledTimes(1);

    await store.toggleFollow(followed);
    expect(gateway.unfollowRepository).toHaveBeenCalledWith(42);
    expect(store.visibleRepositories().find((r) => r.fullName === 'acme/web')!.followed).toBe(false);
    expect(workspace.loadWorkspace).toHaveBeenCalledTimes(2);
  });

  it('reports failures without losing the list', async () => {
    const { store } = setup({ importRepository: vi.fn(async () => Promise.reject(new RepositoryImportError('not_found'))) });
    await store.open(20);
    await store.toggleFollow(store.visibleRepositories().find((r) => r.fullName === 'acme/web')!);

    expect(store.error()).toBe('not_found');
    expect(store.visibleRepositories().length).toBe(2);
    expect(store.status()).toBe('ready');
  });

  it('says what GitHub said when a follow fails, e.g. when its limit lifts (ADR-0013)', async () => {
    const { store } = setup({
      importRepository: vi.fn(async () => Promise.reject(new RepositoryImportError('rate_limited', 'github rate limit reached', '2026-11-16T14:05:00Z'))),
    });
    await store.open(20);
    await store.toggleFollow(store.visibleRepositories().find((r) => r.fullName === 'acme/web')!);

    expect(store.error()).toBe('rate_limited');
    expect(store.errorParams().detail).toBe('github rate limit reached');
    expect(store.errorParams().time).not.toBe('');
    expect(store.isPending(900)).toBe(false);
  });

  it('reports an expired GitHub session when accounts cannot be listed', async () => {
    const { store } = setup({ listAccounts: vi.fn(async () => Promise.reject(new RepositoryImportError('unauthorized'))) });
    await store.open();
    expect(store.status()).toBe('error');
    expect(store.error()).toBe('unauthorized');
  });

  it('shows the empty state when the app is installed nowhere the user can reach', async () => {
    const { store, gateway } = setup({ listAccounts: vi.fn(async () => ({ accounts: [], installUrl: 'https://x/new' })) });
    await store.open();
    expect(store.accounts()).toEqual([]);
    expect(store.selectedAccount()).toBeNull();
    expect(gateway.listRepositories).not.toHaveBeenCalled();
    expect(store.status()).toBe('ready');
  });
});
