import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  REPOSITORY_IMPORT_GATEWAY,
  RepositoryImportGateway,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { ImportableRepository } from '../../../domain/models/repository-import.models';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { RepositoryImportViewComponent } from './repository-import-view.component';

const repo = (githubRepoId: number, fullName: string, extra: Partial<ImportableRepository> = {}): ImportableRepository => ({
  githubRepoId,
  installationId: 20,
  owner: 'acme',
  name: fullName.split('/')[1],
  fullName,
  defaultBranch: 'main',
  private: false,
  followed: false,
  repositoryId: null,
  ...extra,
});

async function render(overrides: Partial<RepositoryImportGateway> = {}) {
  const gateway: RepositoryImportGateway = {
    listAccounts: vi.fn(async () => ({
      accounts: [
        { installationId: 10, login: 'octocat', type: 'User' as const, avatarUrl: '', repositorySelection: 'all' as const, settingsUrl: 'https://github.com/settings/installations/10' },
        { installationId: 20, login: 'acme', type: 'Organization' as const, avatarUrl: '', repositorySelection: 'selected' as const, settingsUrl: 'https://github.com/organizations/acme/settings/installations/20' },
      ],
      installUrl: 'https://github.com/apps/pr-follower/installations/new',
    })),
    listRepositories: vi.fn(async (id: number) =>
      id === 20 ? [repo(900, 'acme/web', { private: true }), repo(901, 'acme/api', { followed: true, repositoryId: 5 })] : [repo(100, 'octocat/dotfiles')],
    ),
    importRepository: vi.fn(async () => 7),
    unfollowRepository: vi.fn(async () => undefined),
    consumeSetupRedirect: () => null,
    ...overrides,
  };
  const workspace = { loadWorkspace: vi.fn(async () => undefined), setPerspective: vi.fn() };
  TestBed.configureTestingModule({
    imports: [RepositoryImportViewComponent],
    providers: [
      { provide: REPOSITORY_IMPORT_GATEWAY, useValue: gateway },
      { provide: WorkspaceStore, useValue: workspace },
      { provide: TRANSLATION_GATEWAY, useValue: { loadDictionary: async () => ({}) } },
    ],
  });
  const fixture = TestBed.createComponent(RepositoryImportViewComponent);
  fixture.detectChanges();
  await fixture.whenStable();
  await new Promise((r) => setTimeout(r, 0));
  fixture.detectChanges();
  return { fixture, el: fixture.nativeElement as HTMLElement, gateway, workspace };
}

describe('RepositoryImportViewComponent', () => {
  it('lists accounts and repositories, follows, and links to GitHub to adjust access', async () => {
    const { fixture, el, gateway, workspace } = await render();

    const select = el.querySelector('#pf-import-account') as HTMLSelectElement;
    expect(Array.from(select.options).map((o) => o.textContent?.trim())).toEqual(['octocat', 'acme']);
    expect(el.querySelectorAll('.pf-import-row').length).toBe(1);

    select.value = '20';
    select.dispatchEvent(new Event('change'));
    await new Promise((r) => setTimeout(r, 0));
    fixture.detectChanges();

    const rows = Array.from(el.querySelectorAll('.pf-import-row'));
    expect(rows.map((r) => r.querySelector('.pf-import-name')?.textContent?.trim())).toEqual(['acme/api', 'acme/web']);
    expect(rows[1].querySelector('.pf-import-private')).toBeTruthy();

    (rows[1].querySelector('button') as HTMLButtonElement).click();
    await new Promise((r) => setTimeout(r, 0));
    fixture.detectChanges();
    expect(gateway.importRepository).toHaveBeenCalledWith(20, 900);
    expect(workspace.loadWorkspace).toHaveBeenCalled();

    const adjust = el.querySelector('#pf-import-adjust') as HTMLAnchorElement;
    expect(adjust.href).toBe('https://github.com/organizations/acme/settings/installations/20');
    const add = el.querySelector('#pf-import-add-account') as HTMLAnchorElement;
    expect(add.href).toBe('https://github.com/apps/pr-follower/installations/new');

    const search = el.querySelector('#pf-import-search') as HTMLInputElement;
    search.value = 'api';
    search.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    expect(el.querySelectorAll('.pf-import-row').length).toBe(1);

    (el.querySelector('#pf-import-back') as HTMLButtonElement).click();
    expect(workspace.setPerspective).toHaveBeenCalledWith('seats');
  });

  it('shows that a follow is under way, and on which repository, until GitHub answers', async () => {
    let finish: (id: number) => void = () => undefined;
    const { fixture, el } = await render({ importRepository: vi.fn(() => new Promise<number>((resolve) => (finish = resolve))) });
    const select = el.querySelector('#pf-import-account') as HTMLSelectElement;
    select.value = '20';
    select.dispatchEvent(new Event('change'));
    await new Promise((r) => setTimeout(r, 0));
    fixture.detectChanges();

    const web = () => Array.from(el.querySelectorAll('.pf-import-row')).find((r) => r.textContent?.includes('acme/web'))!;
    (web().querySelector('button') as HTMLButtonElement).click();
    fixture.detectChanges();

    const busy = web().querySelector('button') as HTMLButtonElement;
    expect(busy.getAttribute('aria-busy')).toBe('true');
    expect(busy.disabled).toBe(true);
    expect(busy.querySelector('.pf-spin')).toBeTruthy();
    expect(busy.textContent).toContain('import.followPending');

    finish(7);
    await new Promise((r) => setTimeout(r, 0));
    fixture.detectChanges();
    const done = web().querySelector('button') as HTMLButtonElement;
    expect(done.getAttribute('aria-busy')).toBeNull();
    expect(done.querySelector('.pf-spin')).toBeNull();
  });

  it('shows that an unfollow is under way too', async () => {
    const { fixture, el } = await render({ unfollowRepository: vi.fn(() => new Promise<void>(() => undefined)) });
    const select = el.querySelector('#pf-import-account') as HTMLSelectElement;
    select.value = '20';
    select.dispatchEvent(new Event('change'));
    await new Promise((r) => setTimeout(r, 0));
    fixture.detectChanges();

    const api = Array.from(el.querySelectorAll('.pf-import-row')).find((r) => r.textContent?.includes('acme/api'))!;
    (api.querySelector('button') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(api.querySelector('button')!.textContent).toContain('import.unfollowPending');
  });

  it('asks the user to install the app when no account is reachable', async () => {
    const { el } = await render({ listAccounts: vi.fn(async () => ({ accounts: [], installUrl: 'https://github.com/apps/pr-follower/installations/new' })) });

    expect(el.querySelector('#pf-import-account')).toBeNull();
    const install = el.querySelector('#pf-import-install') as HTMLAnchorElement;
    expect(install.href).toBe('https://github.com/apps/pr-follower/installations/new');
  });
});
