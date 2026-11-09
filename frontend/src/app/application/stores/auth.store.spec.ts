import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import {
  AUTH_GATEWAY,
  AuthGateway,
  LIVE_EVENT_GATEWAY,
  PULL_REQUEST_GATEWAY,
} from '../../domain/gateways/workspace.gateways';
import { AuthenticatedUser, createDefaultAuthenticatedUser } from '../../domain/models/workspace.models';
import { ActionCancellationManager } from '../use-cases/action-cancellation.manager';
import { AuthStore } from './auth.store';
import { WorkspaceStore } from './workspace.store';

describe('AuthStore', () => {
  it('starts unauthenticated when no stored session exists, logs in via AuthGateway, and logs out cleanly', async () => {
    let savedUser: AuthenticatedUser | null = null;
    const authGateway: AuthGateway = {
      login: vi.fn(async (creds) => ({
        ...createDefaultAuthenticatedUser(),
        login: creds.login,
        primaryTeam: creds.team,
        teams: [creds.team],
      })),
      logout: vi.fn(async () => undefined),
      loadStoredSession: vi.fn(() => savedUser),
      saveStoredSession: vi.fn((u) => {
        savedUser = u;
      }),
      clearStoredSession: vi.fn(() => {
        savedUser = null;
      }),
    };

    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        AuthStore,
        { provide: AUTH_GATEWAY, useValue: authGateway },
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
      ],
    });

    const authStore = TestBed.inject(AuthStore);
    const workspaceStore = TestBed.inject(WorkspaceStore);
    expect(authStore.isAuthenticated()).toBe(false);
    expect(authStore.user()).toBeNull();

    // Sign in
    await authStore.login({ login: 'murilo', team: '@arvore/frontend' }, 5);
    expect(authStore.isAuthenticated()).toBe(true);
    expect(authStore.user()?.login).toBe('murilo');
    expect(authStore.user()?.primaryTeam).toBe('@arvore/frontend');
    expect(authGateway.saveStoredSession).toHaveBeenCalled();

    // Open a drawer then logout -> closes drawer and clears session
    workspaceStore.openPr(142);
    expect(workspaceStore.selectedPrId()).toBe(142);

    await authStore.logout();
    expect(authStore.isAuthenticated()).toBe(false);
    expect(authStore.user()).toBeNull();
    expect(workspaceStore.selectedPrId()).toBeNull();
    expect(authGateway.clearStoredSession).toHaveBeenCalled();
    expect(authGateway.logout).toHaveBeenCalled();
  });

  it('supports cooperative cancellation while login is in flight', async () => {
    const authGateway: AuthGateway = {
      login: vi.fn(async () => createDefaultAuthenticatedUser()),
      logout: vi.fn(async () => undefined),
      loadStoredSession: vi.fn(() => null),
      saveStoredSession: vi.fn(),
      clearStoredSession: vi.fn(),
    };

    TestBed.configureTestingModule({
      providers: [
        ActionCancellationManager,
        WorkspaceStore,
        AuthStore,
        { provide: AUTH_GATEWAY, useValue: authGateway },
      ],
    });

    const authStore = TestBed.inject(AuthStore);
    const loginPromise = authStore.login({ login: 'murilo', team: '@arvore/frontend' }, 200);
    expect(authStore.loginAction().running).toBe(true);

    authStore.cancelLogin();
    await loginPromise;

    expect(authStore.loginAction().running).toBe(false);
    expect(authStore.isAuthenticated()).toBe(false);
    expect(authStore.loginAction().note).toContain('cancelada');
  });
});
