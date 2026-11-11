import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionExpiryMonitor } from '../../application/use-cases/session-expiry.monitor';
import { RepositoryImportError } from '../../domain/models/repository-import.models';
import { BROWSER_WINDOW, BrowserWindow } from '../browser/browser-window';
import { HttpRepositoryImportGateway } from './http-repository-import.gateway';

function fakeWindow(search = '') {
  const replaceState = vi.fn<(data: unknown, unused: string, url?: string | URL | null) => void>();
  const win: BrowserWindow = {
    location: { assign: vi.fn(), search, pathname: '/', hash: '' },
    history: { replaceState },
  };
  return { win, replaceState };
}

function setup(win: BrowserWindow = fakeWindow().win): HttpRepositoryImportGateway {
  TestBed.configureTestingModule({
    providers: [HttpRepositoryImportGateway, { provide: BROWSER_WINDOW, useValue: win }],
  });
  return TestBed.inject(HttpRepositoryImportGateway);
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

describe('HttpRepositoryImportGateway', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    TestBed.resetTestingModule();
  });

  it('lists the GitHub accounts where the app is installed', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      json({
        accounts: [
          {
            id: 20,
            account_login: 'acme',
            account_type: 'Organization',
            account_avatar_url: 'https://a/20',
            repository_selection: 'selected',
            settings_url: 'https://github.com/organizations/acme/settings/installations/20',
          },
        ],
        install_url: 'https://github.com/apps/pr-follower/installations/new',
      }),
    );
    const ctrl = new AbortController();

    const result = await setup().listAccounts({ signal: ctrl.signal });

    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/github/installations',
      expect.objectContaining({ method: 'GET', credentials: 'same-origin', signal: ctrl.signal }),
    );
    expect(result).toEqual({
      accounts: [
        {
          installationId: 20,
          login: 'acme',
          type: 'Organization',
          avatarUrl: 'https://a/20',
          repositorySelection: 'selected',
          settingsUrl: 'https://github.com/organizations/acme/settings/installations/20',
        },
      ],
      installUrl: 'https://github.com/apps/pr-follower/installations/new',
    });
  });

  it('lists repositories of an installation with their follow state', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      json({
        repositories: [
          { github_repo_id: 900, installation_id: 20, owner: 'acme', name: 'web', full_name: 'acme/web', default_branch: 'main', private: true, followed: true, repository_id: 3 },
          { github_repo_id: 901, installation_id: 20, owner: 'acme', name: 'api', full_name: 'acme/api', default_branch: 'trunk', private: false, followed: false },
        ],
      }),
    );

    const repos = await setup().listRepositories(20);

    expect(globalThis.fetch).toHaveBeenCalledWith('/api/v1/github/installations/20/repositories', expect.anything());
    expect(repos[0]).toEqual({
      githubRepoId: 900,
      installationId: 20,
      owner: 'acme',
      name: 'web',
      fullName: 'acme/web',
      defaultBranch: 'main',
      private: true,
      followed: true,
      repositoryId: 3,
    });
    expect(repos[1].repositoryId).toBeNull();
    expect(repos[1].followed).toBe(false);
  });

  it('imports with POST /api/v1/repositories/import and unfollows with DELETE', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(json({ repository: { id: 3, github_repo_id: 900 } }))
      .mockResolvedValueOnce(json({ ok: true }));
    const gateway = setup();

    const repositoryId = await gateway.importRepository(20, 900, { operationId: 'op-1' });
    expect(repositoryId).toBe(3);
    expect(fetchSpy).toHaveBeenNthCalledWith(
      1,
      '/api/v1/repositories/import',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ installation_id: 20, github_repo_id: 900 }),
        headers: expect.objectContaining({ 'Content-Type': 'application/json', 'X-Operation-ID': 'op-1' }),
      }),
    );

    await gateway.unfollowRepository(3);
    expect(fetchSpy).toHaveBeenNthCalledWith(2, '/api/v1/repositories/3/follow', expect.objectContaining({ method: 'DELETE' }));
  });

  it('turns HTTP failures into typed import errors', async () => {
    const gateway = setup();
    const cases: [Response, string][] = [
      [json({ error: 'unauthorized' }, 401), 'unauthorized'],
      [json({ error: 'resource not found' }, 404), 'not_found'],
      [json({ error: 'github_import_not_configured' }, 503), 'not_configured'],
      [json({ error: 'boom' }, 500), 'failed'],
    ];
    for (const [response, code] of cases) {
      vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(response);
      await expect(gateway.listAccounts()).rejects.toEqual(new RepositoryImportError(code as never));
    }
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new TypeError('offline'));
    await expect(gateway.listAccounts()).rejects.toEqual(new RepositoryImportError('failed'));
  });

  it('lets aborts through untouched', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new DOMException('Aborted', 'AbortError'));
    await expect(setup().listRepositories(20)).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('consumes the installation_id GitHub appends to the Setup URL', () => {
    const { win, replaceState } = fakeWindow('?installation_id=20&setup_action=install&tab=x');
    expect(setup(win).consumeSetupRedirect()).toBe(20);
    expect(replaceState).toHaveBeenCalledWith(null, '', '/?tab=x');

    TestBed.resetTestingModule();
    const none = fakeWindow('');
    expect(setup(none.win).consumeSetupRedirect()).toBeNull();
    expect(none.replaceState).not.toHaveBeenCalled();

    TestBed.resetTestingModule();
    expect(setup(fakeWindow('?installation_id=abc&setup_action=update').win).consumeSetupRedirect()).toBeNull();
  });

  it('reports an unauthenticated answer so the app can return to sign-in', async () => {
    const gateway = setup();
    const reported = vi.fn();
    TestBed.inject(SessionExpiryMonitor).listen(reported);

    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(json({ error: 'boom' }, 500));
    await expect(gateway.listAccounts()).rejects.toBeInstanceOf(RepositoryImportError);
    expect(reported).not.toHaveBeenCalled();

    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(json({ error: 'unauthorized' }, 401));
    await expect(gateway.listAccounts()).rejects.toEqual(new RepositoryImportError('unauthorized'));
    expect(reported).toHaveBeenCalledTimes(1);
  });
});
