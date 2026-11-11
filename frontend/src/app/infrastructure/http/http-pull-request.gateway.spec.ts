import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
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
});

describe('HttpPullRequestGateway actions are never silent and never repeated', () => {
  const answer = (status: number, body: unknown) =>
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(body), { status }));

  afterEach(() => {
    vi.restoreAllMocks();
  });

  const gateway = (): HttpPullRequestGateway => new HttpPullRequestGateway();

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
});
