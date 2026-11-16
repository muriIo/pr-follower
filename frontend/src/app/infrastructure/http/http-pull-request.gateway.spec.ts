import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SessionExpiryMonitor } from '../../application/use-cases/session-expiry.monitor';
import { ActionError } from '../../domain/models/action-error.models';
import { HttpPullRequestGateway } from './http-pull-request.gateway';

describe('HttpPullRequestGateway', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('wires all backend API routes (/api/v1/*) with X-Operation-ID and AbortSignal', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/api/v1/workspace')) {
        return new Response(
          JSON.stringify({
            snapshot: {
              seats: [
                {
                  repository: {
                    id: 1,
                    owner: 'arvore',
                    name: 'hive-web',
                    full_name: 'arvore/hive-web',
                    default_branch: 'main',
                  },
                  open_pr_count: 0,
                  waiting_on_me_count: 0,
                  dependabot_pr_count: 0,
                  failing_ci_count: 0,
                  pull_requests: [],
                },
              ],
            },
            timeline: [],
          }),
          { status: 200 },
        );
      }
      if (url.endsWith('/merge')) {
        return new Response(JSON.stringify({ sha: 'f00ba77' }), { status: 200 });
      }
      if (url.endsWith('/batch-merge')) {
        return new Response(JSON.stringify({ ok: true, merged_ids: [145, 146] }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    });

    TestBed.configureTestingModule({ providers: [HttpPullRequestGateway] });
    const gateway = TestBed.inject(HttpPullRequestGateway);

    const ctrl = new AbortController();

    const ws = await gateway.fetchWorkspace({ signal: ctrl.signal, operationId: 'op-ws-1' });
    expect(ws?.repos).toEqual([{ name: 'arvore/hive-web', branch: 'main', access: 'active' }]);

    await gateway.approvePullRequest(142, { signal: ctrl.signal, operationId: 'op-approve-142' });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/prs/142/approve',
      expect.objectContaining({
        method: 'POST',
        signal: ctrl.signal,
        headers: expect.objectContaining({ 'X-Operation-ID': 'op-approve-142' }),
      }),
    );

    const mergeRes = await gateway.mergePullRequest(142, { signal: ctrl.signal, operationId: 'op-merge-142' });
    expect(mergeRes.sha).toBe('f00ba77');

    const batchRes = await gateway.batchMergePullRequests([145, 146], {
      signal: ctrl.signal,
      operationId: 'op-batch-1',
    });
    expect(batchRes.mergedIds).toEqual([145, 146]);
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/prs/batch-merge',
      expect.objectContaining({ method: 'POST' }),
    );

    await gateway.commentOnPullRequest(89, '@dependabot rebase', {
      signal: ctrl.signal,
      operationId: 'op-dep-89',
    });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/prs/89/dependabot-command',
      expect.objectContaining({ method: 'POST' }),
    );

    await gateway.snoozePullRequest(142, 'UNTIL_NEXT_COMMIT', {
      signal: ctrl.signal,
      operationId: 'op-snooze-142',
    });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/prs/142/snooze',
      expect.objectContaining({ method: 'POST' }),
    );

    await gateway.claimPullRequest(142, { signal: ctrl.signal, operationId: 'op-claim-142' });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/prs/142/claim',
      expect.objectContaining({ method: 'POST' }),
    );

    await gateway.cancelServerOperation('op-merge-142');
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/operations/op-merge-142',
      expect.objectContaining({ method: 'DELETE' }),
    );
  });
});

describe('HttpPullRequestGateway changed files', () => {
  it('reads a PR\'s changed files with ownership and parses each patch', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          files: [
            { path: 'src/app.ts', status: 'modified', additions: 3, deletions: 1, patch: '@@ -1 +1 @@\n-a\n+b', owned: true },
            { path: 'assets/logo.png', status: 'added', additions: 0, deletions: 0, owned: false },
          ],
        }),
        { status: 200 },
      ),
    );
    TestBed.configureTestingModule({ providers: [HttpPullRequestGateway] });
    const ctrl = new AbortController();

    const files = await TestBed.inject(HttpPullRequestGateway).fetchChangedFiles('acme/web', 7, { signal: ctrl.signal });

    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/repositories/acme/web/pulls/7/files',
      expect.objectContaining({ method: 'GET', signal: ctrl.signal }),
    );
    expect(files.map((f) => [f.path, f.status, f.additions, f.deletions, f.owned])).toEqual([
      ['src/app.ts', 'modified', 3, 1, true],
      ['assets/logo.png', 'added', 0, 0, false],
    ]);
    expect(files[0].hunk.length).toBe(3);
    expect(files[1].hunk).toEqual([]);
    vi.restoreAllMocks();
  });

  it('fails instead of pretending there are no files when GitHub cannot be reached', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 502 }));
    TestBed.configureTestingModule({ providers: [HttpPullRequestGateway] });
    await expect(TestBed.inject(HttpPullRequestGateway).fetchChangedFiles('acme/web', 7)).rejects.toThrow();
    vi.restoreAllMocks();
  });

  it('rejects with the server\'s code and GitHub\'s explanation, so the drawer can say why (ADR-0013)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ code: 'forbidden', error: 'Resource not accessible by integration' }), { status: 403 }),
    );
    TestBed.configureTestingModule({ providers: [HttpPullRequestGateway] });

    const failure = await TestBed.inject(HttpPullRequestGateway).fetchChangedFiles('acme/web', 7).catch((err: unknown) => err);

    expect(failure).toBeInstanceOf(ActionError);
    expect((failure as ActionError).code).toBe('forbidden');
    expect((failure as ActionError).detail).toBe('Resource not accessible by integration');
    expect(fetchSpy).toHaveBeenCalledTimes(1); // a refusal is not retried
    vi.restoreAllMocks();
  });

  it('retries a GitHub outage once, then reports it as one', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(
      async () => new Response(JSON.stringify({ code: 'github_unavailable', error: 'github server error (502)' }), { status: 502 }),
    );
    TestBed.configureTestingModule({ providers: [HttpPullRequestGateway] });

    const failure = await TestBed.inject(HttpPullRequestGateway).fetchChangedFiles('acme/web', 7).catch((err: unknown) => err);

    expect((failure as ActionError).code).toBe('github_unavailable');
    expect(fetchSpy).toHaveBeenCalledTimes(2);
    vi.restoreAllMocks();
  });
});

describe('HttpPullRequestGateway actions are never silent and never repeated', () => {
  const answer = (status: number, body: unknown) =>
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(body), { status }));

  afterEach(() => {
    vi.restoreAllMocks();
  });

  const gateway = (): HttpPullRequestGateway => TestBed.runInInjectionContext(() => new HttpPullRequestGateway());

  it('rejects with the server code and GitHub\'s explanation, after exactly one request', async () => {
    const cases: [number, string, string][] = [
      [422, 'own_pull_request', 'you cannot approve your own pull request'],
      [409, 'conflict', 'resource conflict: Pull Request is not mergeable'],
      [403, 'forbidden', 'forbidden by github: Resource not accessible'],
      [502, 'outcome_unknown', 'could not confirm the outcome on github'],
      [401, 'unauthorized', 'unauthorized'],
    ];
    for (const [status, code, error] of cases) {
      const spy = answer(status, { error, code });
      await expect(gateway().approvePullRequest(1)).rejects.toMatchObject({ name: 'ActionError', code, detail: error });
      expect(spy, `${status} must not be retried`).toHaveBeenCalledTimes(1);
      spy.mockRestore();
    }
  });

  it('treats a lost connection as an unknown outcome, because the request may have been applied', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network down'));
    const g = gateway();
    await expect(g.approvePullRequest(1)).rejects.toMatchObject({ code: 'outcome_unknown' });
    await expect(g.mergePullRequest(1)).rejects.toMatchObject({ code: 'outcome_unknown' });
    await expect(g.commentOnPullRequest(1, '@dependabot rebase')).rejects.toMatchObject({ code: 'outcome_unknown' });
    expect(spy).toHaveBeenCalledTimes(3);
  });

  it('keeps a failure without a body readable, and lets a cancellation through as is', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('<html>bad gateway</html>', { status: 502 }));
    await expect(gateway().mergePullRequest(1)).rejects.toMatchObject({ code: 'unknown' });

    vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new DOMException('Aborted', 'AbortError'));
    await expect(gateway().mergePullRequest(1)).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('does not invent a merge commit when the server reports none', async () => {
    answer(200, { ok: true, pr_id: 1 });
    expect((await gateway().mergePullRequest(1)).sha).toBeUndefined();
  });

  it('asks the server to analyse a Dependabot PR, and says when the analysis is not available', async () => {
    const spy = answer(200, { ok: true, pr_id: 233 });
    await gateway().analyzePullRequest(233, { operationId: 'op-analyze-233' });
    expect(spy).toHaveBeenCalledWith(
      '/api/v1/prs/233/analyze',
      expect.objectContaining({ method: 'POST', headers: expect.objectContaining({ 'X-Operation-ID': 'op-analyze-233' }) }),
    );
    spy.mockRestore();

    answer(503, { error: 'dependabot analysis needs the GitHub App credentials', code: 'unavailable' });
    await expect(gateway().analyzePullRequest(233)).rejects.toMatchObject({ code: 'unavailable' });
  });

  it('reports an unauthenticated answer, so the app can return to sign-in, and nothing else', async () => {
    const monitor = TestBed.inject(SessionExpiryMonitor);
    const reported = vi.fn();
    monitor.listen(reported);
    const g = gateway();

    answer(403, { error: 'forbidden', code: 'forbidden' });
    await expect(g.approvePullRequest(1)).rejects.toMatchObject({ code: 'forbidden' });
    answer(502, { error: 'x', code: 'outcome_unknown' });
    await expect(g.mergePullRequest(1)).rejects.toMatchObject({ code: 'outcome_unknown' });
    expect(reported).not.toHaveBeenCalled();

    answer(401, { error: 'unauthorized', code: 'unauthorized' });
    await expect(g.approvePullRequest(1)).rejects.toMatchObject({ code: 'unauthorized' });
    expect(reported).toHaveBeenCalledTimes(1);

    expect(reported).toHaveBeenLastCalledWith('unauthorized');

    answer(401, { error: 'unauthorized', code: 'unauthorized' });
    await g.fetchWorkspace().catch(() => undefined); // a read refused as 401 reports too
    expect(reported).toHaveBeenCalledTimes(2);

    // The server says it is the user's own GitHub access that is gone: that is a different remedy.
    answer(401, { error: 'github token expired', code: 'github_token_expired' });
    await expect(g.mergePullRequest(1)).rejects.toMatchObject({ code: 'github_token_expired' });
    expect(reported).toHaveBeenLastCalledWith('github_token_expired');
  });

  it('starts a sync, reads its progress and cancels it, in the client\'s own shape', async () => {
    const running = { state: 'running', total: 10, done: 3, current: 'acme/web' };
    const spy = answer(202, { started: true, status: running });
    const started = await gateway().startSync({ operationId: 'op-sync-1' });
    expect(spy).toHaveBeenCalledWith(
      '/api/v1/sync',
      expect.objectContaining({ method: 'POST', headers: expect.objectContaining({ 'X-Operation-ID': 'op-sync-1' }) }),
    );
    expect(started).toEqual({ started: true, status: { state: 'running', total: 10, done: 3, current: 'acme/web' } });
    spy.mockRestore();

    const done = answer(200, {
      state: 'done', total: 10, done: 10, current: '',
      result: { repositories: 10, added: 1, updated: 3, closed: 1, merged: 2, skipped: 4, rate_limited_until: '2026-11-11T12:30:00Z', failed: [{ repository: 'acme/api', code: 'not_found' }] },
    });
    const status = await gateway().syncStatus();
    expect(done).toHaveBeenCalledWith('/api/v1/sync', expect.objectContaining({ method: 'GET' }));
    expect(status.result).toEqual({
      repositories: 10, added: 1, updated: 3, closed: 1, merged: 2, skipped: 4,
      rateLimitedUntil: '2026-11-11T12:30:00Z', failed: [{ repository: 'acme/api', code: 'not_found' }],
    });
    done.mockRestore();

    const failed = answer(200, { state: 'failed', total: 0, done: 0, current: '', error: { code: 'rate_limited', retry_at: '2026-11-11T12:30:00Z' } });
    expect((await gateway().syncStatus()).error).toEqual({ code: 'rate_limited', retryAt: '2026-11-11T12:30:00Z' });
    failed.mockRestore();

    const cancel = answer(200, { canceled: true });
    await gateway().cancelSync();
    expect(cancel).toHaveBeenCalledWith('/api/v1/sync', expect.objectContaining({ method: 'DELETE' }));
  });

  it('keeps the reset time of a rate limit on the error it raises', async () => {
    answer(429, { error: 'limit', code: 'rate_limited', retry_at: '2026-11-11T12:30:00Z' });
    await expect(gateway().mergePullRequest(1)).rejects.toMatchObject({ code: 'rate_limited', retryAt: '2026-11-11T12:30:00Z' });
  });
});
