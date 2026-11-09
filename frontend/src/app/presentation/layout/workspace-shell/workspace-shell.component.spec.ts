import { TestBed } from '@angular/core/testing';
import { describe, expect, it } from 'vitest';
import {
  AUTH_GATEWAY,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { createDefaultAuthenticatedUser } from '../../../domain/models/workspace.models';
import { AuthStore } from '../../../application/stores/auth.store';
import { WorkspaceShellComponent } from './workspace-shell.component';

describe('WorkspaceShellComponent', () => {
  it('renders login-view when unauthenticated, transitions to workspace after login, and returns to login-view after logout', async () => {
    TestBed.configureTestingModule({
      imports: [WorkspaceShellComponent],
      providers: [
        AuthStore,
        {
          provide: AUTH_GATEWAY,
          useValue: {
            login: async () => createDefaultAuthenticatedUser(),
            fetchCurrentUser: async () => null,
            logout: async () => undefined,
            loadStoredSession: () => null,
            saveStoredSession: () => undefined,
            clearStoredSession: () => undefined,
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
          provide: TRANSLATION_GATEWAY,
          useValue: { loadDictionary: async () => ({}) },
        },
      ],
    });

    const fixture = TestBed.createComponent(WorkspaceShellComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    // Starts unauthenticated -> shows login-view
    expect(el.querySelector('app-login-view')).toBeTruthy();
    expect(el.querySelector('app-sidebar-nav')).toBeNull();

    // Sign in -> transitions to workspace
    await fixture.componentInstance.authStore.login({ login: 'murilo', team: '@arvore/frontend' }, 5);
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

    // Logout -> returns to login-view
    await fixture.componentInstance.authStore.logout();
    fixture.detectChanges();
    expect(el.querySelector('app-login-view')).toBeTruthy();
    expect(el.querySelector('app-sidebar-nav')).toBeNull();
  });
});
