import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  AUTH_GATEWAY,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
  TRANSLATION_GATEWAY,
} from '../../../domain/gateways/workspace.gateways';
import { createDefaultAuthenticatedUser } from '../../../domain/models/workspace.models';
import { AuthStore } from '../../../application/stores/auth.store';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { LoginViewComponent } from './login-view.component';

describe('LoginViewComponent', () => {
  it('renders GitHub handle/team login form, supports quick preset sign-in, and submits credentials', async () => {
    TestBed.configureTestingModule({
      imports: [LoginViewComponent],
      providers: [
        WorkspaceStore,
        AuthStore,
        {
          provide: AUTH_GATEWAY,
          useValue: {
            login: vi.fn(async (creds) => ({
              ...createDefaultAuthenticatedUser(),
              login: creds.login,
              primaryTeam: creds.team,
            })),
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

    const authStore = TestBed.inject(AuthStore);
    const loginSpy = vi.spyOn(authStore, 'login');

    const fixture = TestBed.createComponent(LoginViewComponent);
    fixture.detectChanges();

    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.pf-login-card')).toBeTruthy();

    // Select second quick preset and submit
    const presetButtons = el.querySelectorAll('.pf-login-preset');
    expect(presetButtons.length).toBeGreaterThanOrEqual(2);
    (presetButtons[1] as HTMLButtonElement).click();
    fixture.detectChanges();

    const form = el.querySelector('form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit'));
    expect(loginSpy).toHaveBeenCalledWith({
      login: 'bianca-dev',
      team: '@arvore/core-architects',
    });
  });
});
