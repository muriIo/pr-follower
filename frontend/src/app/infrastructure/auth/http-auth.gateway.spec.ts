import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { HttpAuthGateway } from './http-auth.gateway';

describe('HttpAuthGateway', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it('calls POST /api/v1/auth/login and POST /api/v1/auth/logout and manages stored session', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/api/v1/auth/login')) {
        return new Response(
          JSON.stringify({
            token: 'pf_sess_1_999',
            user: {
              id: 1,
              login: 'murilo',
              name: 'Murilo',
              teams: ['@arvore/frontend', '@arvore/core-architects'],
            },
          }),
          { status: 200 },
        );
      }
      return new Response(JSON.stringify({ ok: true, logged_out: true }), { status: 200 });
    });

    TestBed.configureTestingModule({ providers: [HttpAuthGateway] });
    const gateway = TestBed.inject(HttpAuthGateway);

    const ctrl = new AbortController();
    const user = await gateway.login(
      { login: '@murilo', team: '@arvore/frontend' },
      { signal: ctrl.signal, operationId: 'op-login-1' },
    );
    expect(user.login).toBe('murilo');
    expect(user.primaryTeam).toBe('@arvore/frontend');
    expect(user.token).toBe('pf_sess_1_999');
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({ 'X-Operation-ID': 'op-login-1' }),
      }),
    );

    gateway.saveStoredSession(user);
    expect(gateway.loadStoredSession()?.login).toBe('murilo');

    await gateway.logout(user.token);
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/auth/logout',
      expect.objectContaining({ method: 'POST' }),
    );

    gateway.clearStoredSession();
    expect(gateway.loadStoredSession()).toBeNull();
  });
});
