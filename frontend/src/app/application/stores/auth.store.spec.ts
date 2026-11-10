import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { AUTH_GATEWAY, AuthGateway } from '../../domain/gateways/workspace.gateways';
import { AuthErrorCode, AuthenticatedUser, buildAuthenticatedUser } from '../../domain/models/workspace.models';
import { AuthStore } from './auth.store';
import { WorkspaceStore } from './workspace.store';

const octocat = buildAuthenticatedUser({ id: 7, login: 'octocat', teams: ['@acme/web'] });

function setup(current: AuthenticatedUser | null, authError: AuthErrorCode | null = null) {
  const gateway = {
    fetchCurrentUser: vi.fn(async () => current),
    startSignIn: vi.fn(),
    consumeAuthError: vi.fn(() => authError),
    logout: vi.fn(async () => undefined),
  } satisfies AuthGateway;
  const workspace = {
    startLiveSession: vi.fn(),
    stopLiveSession: vi.fn(),
    closeDrawer: vi.fn(),
  };
  TestBed.configureTestingModule({
    providers: [
      AuthStore,
      { provide: AUTH_GATEWAY, useValue: gateway },
      { provide: WorkspaceStore, useValue: workspace },
    ],
  });
  return { store: TestBed.inject(AuthStore), gateway, workspace };
}

describe('AuthStore (GitHub-only sign-in, ADR-0010)', () => {
  it('starts in checking state and restores an existing session', async () => {
    const { store, workspace } = setup(octocat);
    expect(store.status()).toBe('checking');
    expect(store.isAuthenticated()).toBe(false);

    await store.restore();

    expect(store.status()).toBe('signed-in');
    expect(store.user()?.login).toBe('octocat');
    expect(store.isAuthenticated()).toBe(true);
    expect(workspace.startLiveSession).toHaveBeenCalledTimes(1);
  });

  it('shows the sign-in screen when there is no session, without starting the workspace', async () => {
    const { store, workspace } = setup(null, 'access_denied');
    await store.restore();

    expect(store.status()).toBe('signed-out');
    expect(store.user()).toBeNull();
    expect(store.authError()).toBe('access_denied');
    expect(workspace.startLiveSession).not.toHaveBeenCalled();
  });

  it('signs in by handing the browser to GitHub and clears any previous error', async () => {
    const { store, gateway } = setup(null, 'login_failed');
    await store.restore();

    store.signIn();

    expect(store.authError()).toBeNull();
    expect(store.status()).toBe('redirecting');
    expect(gateway.startSignIn).toHaveBeenCalledTimes(1);
  });

  it('logs out: ends the server session and stops the live workspace', async () => {
    const { store, gateway, workspace } = setup(octocat);
    await store.restore();

    await store.logout();

    expect(gateway.logout).toHaveBeenCalledTimes(1);
    expect(workspace.stopLiveSession).toHaveBeenCalledTimes(1);
    expect(workspace.closeDrawer).toHaveBeenCalledTimes(1);
    expect(store.user()).toBeNull();
    expect(store.status()).toBe('signed-out');
  });
});
