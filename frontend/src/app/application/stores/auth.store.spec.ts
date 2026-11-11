import { TestBed } from '@angular/core/testing';
import { describe, expect, it, vi } from 'vitest';
import { AUTH_GATEWAY, AuthGateway, SessionCheck } from '../../domain/gateways/workspace.gateways';
import { SessionExpiryMonitor } from '../use-cases/session-expiry.monitor';
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
    verifySession: vi.fn(async (): Promise<SessionCheck> => (current ? 'active' : 'expired')),
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
  return { store: TestBed.inject(AuthStore), gateway, workspace, monitor: TestBed.inject(SessionExpiryMonitor) };
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
    expect(workspace.startLiveSession).toHaveBeenCalledWith('octocat');
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

describe('AuthStore when the session expires while the app is open', () => {
  it('signs out at once, and clears the server session, when the user\'s GitHub access is gone', async () => {
    const { store, gateway, workspace, monitor } = setup(octocat);
    await store.restore();

    monitor.report('github_token_expired'); // the session is valid, but GitHub no longer accepts this user
    await vi.waitFor(() => expect(store.status()).toBe('signed-out'));

    expect(gateway.verifySession).not.toHaveBeenCalled(); // there is nothing to verify: the answer is known
    expect(gateway.logout).toHaveBeenCalled(); // the stale session must not survive the next page load
    expect(store.authError()).toBe('github_token_expired');
    expect(store.user()).toBeNull();
    expect(workspace.stopLiveSession).toHaveBeenCalled();
  });

  it('returns to the sign-in screen with an explanation when the server no longer knows the session', async () => {
    const { store, gateway, workspace, monitor } = setup(octocat);
    await store.restore();
    gateway.verifySession.mockResolvedValue('expired');

    monitor.report(); // an API call came back 401
    await vi.waitFor(() => expect(store.status()).toBe('signed-out'));

    expect(store.user()).toBeNull();
    expect(store.isAuthenticated()).toBe(false);
    expect(store.authError()).toBe('session_expired');
    expect(workspace.stopLiveSession).toHaveBeenCalled();
    expect(workspace.closeDrawer).toHaveBeenCalled();
  });

  it('stays signed in when the session is still valid: the 401 was something else (e.g. GitHub refusing the app)', async () => {
    const { store, gateway, workspace, monitor } = setup(octocat);
    await store.restore();

    monitor.report();
    await vi.waitFor(() => expect(gateway.verifySession).toHaveBeenCalled());

    expect(store.status()).toBe('signed-in');
    expect(store.authError()).toBeNull();
    expect(workspace.stopLiveSession).not.toHaveBeenCalled();
  });

  it('does not sign the user out because the server could not be reached', async () => {
    const { store, gateway, monitor } = setup(octocat);
    await store.restore();
    gateway.verifySession.mockResolvedValue('unreachable');

    monitor.report();
    await vi.waitFor(() => expect(gateway.verifySession).toHaveBeenCalled());
    await Promise.resolve();

    expect(store.status()).toBe('signed-in');
    expect(store.authError()).toBeNull();
  });

  it('checks once for a burst of refused calls, and ignores them when nobody is signed in', async () => {
    const { store, gateway, monitor } = setup(octocat);
    monitor.report(); // not signed in yet: nothing to expire
    expect(gateway.verifySession).not.toHaveBeenCalled();

    await store.restore();
    let release!: (v: SessionCheck) => void;
    gateway.verifySession.mockImplementation(() => new Promise<SessionCheck>((resolve) => (release = resolve)));
    monitor.report();
    monitor.report();
    monitor.report();
    expect(gateway.verifySession).toHaveBeenCalledTimes(1);

    release('expired');
    await vi.waitFor(() => expect(store.status()).toBe('signed-out'));
    monitor.report();
    expect(gateway.verifySession).toHaveBeenCalledTimes(1);
  });
});
