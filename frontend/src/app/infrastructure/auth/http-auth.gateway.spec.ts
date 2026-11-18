import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BROWSER_WINDOW, BrowserWindow } from '../browser/browser-window';
import { HttpAuthGateway } from './http-auth.gateway';

function fakeWindow(search = '') {
  const assign = vi.fn<(url: string | URL) => void>();
  const replaceState = vi.fn<(data: unknown, unused: string, url?: string | URL | null) => void>();
  const win: BrowserWindow = {
    location: { assign, search, pathname: '/', hash: '' },
    history: { replaceState },
  };
  return { ...win, location: { ...win.location, assign }, history: { replaceState } };
}

function setup(win: BrowserWindow = fakeWindow()): HttpAuthGateway {
  TestBed.configureTestingModule({
    providers: [HttpAuthGateway, { provide: BROWSER_WINDOW, useValue: win }],
  });
  return TestBed.inject(HttpAuthGateway);
}

describe('HttpAuthGateway (GitHub-only sign-in, ADR-0010)', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    TestBed.resetTestingModule();
  });

  it('reads the session user from GET /api/v1/auth/me', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          user: { id: 7, login: 'octocat', name: 'The Octocat', avatar_url: 'https://a/u/7', teams: ['@acme/web'] },
        }),
        { status: 200 },
      ),
    );
    const gateway = setup();

    const user = await gateway.fetchCurrentUser();

    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/auth/me',
      expect.objectContaining({ method: 'GET', credentials: 'same-origin' }),
    );
    expect(user).toEqual(
      expect.objectContaining({ id: 7, login: 'octocat', avatarUrl: 'https://a/u/7', primaryTeam: '@acme/web' }),
    );
  });

  it('treats 401 and unreachable backends as signed out', async () => {
    const gateway = setup();
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('{"error":"unauthorized"}', { status: 401 }));
    expect(await gateway.fetchCurrentUser()).toBeNull();

    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'));
    expect(await gateway.fetchCurrentUser()).toBeNull();
  });

  it('starts sign-in by navigating the browser to the backend GitHub login route', () => {
    const win = fakeWindow();
    setup(win).startSignIn();
    expect(win.location.assign).toHaveBeenCalledWith('/api/v1/auth/github/login');
  });

  it('consumes ?auth_error= from the URL once and maps unknown codes', () => {
    const win = fakeWindow('?auth_error=access_denied&tab=1');
    const gateway = setup(win);

    expect(gateway.consumeAuthError()).toBe('access_denied');
    expect(win.history.replaceState).toHaveBeenCalledWith(null, '', '/?tab=1');

    TestBed.resetTestingModule();
    expect(setup(fakeWindow('?auth_error=weird')).consumeAuthError()).toBe('unknown');

    TestBed.resetTestingModule();
    const clean = fakeWindow('');
    expect(setup(clean).consumeAuthError()).toBeNull();
    expect(clean.history.replaceState).not.toHaveBeenCalled();
  });

  it('recognises the org allowlist refusals the callback sends (ADR-0015)', () => {
    expect(setup(fakeWindow('?auth_error=not_org_member')).consumeAuthError()).toBe('not_org_member');
    TestBed.resetTestingModule();
    expect(setup(fakeWindow('?auth_error=org_membership_unverified')).consumeAuthError()).toBe(
      'org_membership_unverified',
    );
  });

  it('logs out with POST /api/v1/auth/logout and tolerates network failures', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"ok":true}', { status: 200 }));
    const gateway = setup();
    await gateway.logout();
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/auth/logout',
      expect.objectContaining({ method: 'POST', credentials: 'same-origin' }),
    );

    fetchSpy.mockRejectedValue(new TypeError('offline'));
    await expect(gateway.logout()).resolves.toBeUndefined();
  });

  it('tells an expired session from a server that cannot be reached', async () => {
    const gateway = setup();
    const ok = new Response(JSON.stringify({ user: { id: 7, login: 'octocat' } }), { status: 200 });

    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(ok);
    expect(await gateway.verifySession()).toBe('active');

    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('{}', { status: 401 }));
    expect(await gateway.verifySession()).toBe('expired');

    // Not knowing is not the same as expired: a dropped connection or a 5xx must not sign anyone out.
    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new TypeError('offline'));
    expect(await gateway.verifySession()).toBe('unreachable');
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('{}', { status: 503 }));
    expect(await gateway.verifySession()).toBe('unreachable');
  });
});
