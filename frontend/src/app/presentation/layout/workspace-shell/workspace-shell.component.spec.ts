import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  AUTH_GATEWAY,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  REPOSITORY_IMPORT_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { AuthenticatedUser, buildAuthenticatedUser } from '../../../domain/models/workspace.models';
import { AuthStore } from '../../../application/stores/auth.store';
import { WorkspaceShellComponent } from './workspace-shell.component';

describe('WorkspaceShellComponent', () => {
  it('checks the session, renders login-view when signed out, the workspace when signed in, and login-view after logout', async () => {
    let sessionUser: AuthenticatedUser | null = null;
    TestBed.configureTestingModule({
      imports: [WorkspaceShellComponent],
      providers: [
        AuthStore,
        {
          provide: AUTH_GATEWAY,
          useValue: {
            fetchCurrentUser: async () => sessionUser,
            startSignIn: vi.fn(),
            consumeAuthError: () => null,
            logout: async () => {
              sessionUser = null;
            },
          },
        },
        {
          provide: PULL_REQUEST_GATEWAY,
          useValue: {
            fetchWorkspace: async () => null,
            approvePullRequest: async () => undefined,
            mergePullRequest: async () => ({ sha: 'a1b2c3d' }),
            commentOnPullRequest: async () => undefined,
            snoozePullRequest: async () => undefined,
            cancelServerOperation: async () => undefined,
          },
        },
        {
          provide: LIVE_EVENT_GATEWAY,
          useValue: {
            connect: (_onEvent: unknown, onState: (s: 'conectado') => void) => {
              onState('conectado');
              return () => undefined;
            },
          },
        },
        {
          provide: REPOSITORY_IMPORT_GATEWAY,
          useValue: {
            listAccounts: async () => ({ accounts: [], installUrl: '' }),
            listRepositories: async () => [],
            importRepository: async () => 1,
            unfollowRepository: async () => undefined,
            consumeSetupRedirect: () => null,
          },
        },
        {
          provide: TRANSLATION_GATEWAY,
          useValue: { loadDictionary: async () => ({}) },
        },
      ],
    });

    const fixture = TestBed.createComponent(WorkspaceShellComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    // While the session is being checked neither screen flashes
    expect(el.querySelector('.pf-auth-checking')).toBeTruthy();
    expect(el.querySelector('app-login-view')).toBeNull();

    // No session -> shows login-view
    await fixture.whenStable();
    fixture.detectChanges();
    expect(el.querySelector('app-login-view')).toBeTruthy();
    expect(el.querySelector('app-sidebar-nav')).toBeNull();

    // Back from GitHub with a session -> transitions to workspace
    sessionUser = buildAuthenticatedUser({ id: 7, login: 'octocat', teams: ['@acme/web'] });
    await fixture.componentInstance.authStore.restore();
    fixture.detectChanges();

    expect(el.querySelector('app-login-view')).toBeNull();
    expect(el.querySelector('app-sidebar-nav')).toBeTruthy();
    expect(el.querySelector('app-seats-view')).toBeTruthy();
    expect(el.querySelector('app-inspection-drawer')).toBeNull();

    // Switch to Minha Vez (inbox)
    fixture.componentInstance.store.setPerspective('inbox');
    fixture.detectChanges();
    expect(el.querySelector('app-codeowners-inbox-view')).toBeTruthy();

    // Open PR #142 in inspection drawer and verify drawer width CSS var on .pf-app
    fixture.componentInstance.store.openPr(142);
    fixture.componentInstance.store.setDrawerWidth(640, 900);
    fixture.detectChanges();
    expect(el.querySelector('app-inspection-drawer')).toBeTruthy();
    const appDiv = el.querySelector('.pf-app') as HTMLElement;
    expect(appDiv.style.getPropertyValue('--pf-dw')).toBe('640px');

    // Switch to Dependabot Radar
    fixture.componentInstance.store.setPerspective('radar');
    fixture.detectChanges();
    expect(el.querySelector('app-dependabot-radar-view')).toBeTruthy();

    // Switch to Timeline
    fixture.componentInstance.store.setPerspective('timeline');
    fixture.detectChanges();
    expect(el.querySelector('app-timeline-view')).toBeTruthy();

    // Open the repository import screen
    fixture.componentInstance.store.setPerspective('import');
    fixture.detectChanges();
    expect(el.querySelector('app-repository-import-view')).toBeTruthy();

    // Logout -> returns to login-view
    await fixture.componentInstance.authStore.logout();
    fixture.detectChanges();
    expect(el.querySelector('app-login-view')).toBeTruthy();
    expect(el.querySelector('app-sidebar-nav')).toBeNull();
  });
});
