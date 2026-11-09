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
    expect(ws?.repos).toEqual([{ name: 'arvore/hive-web', branch: 'main' }]);

    const followed = await gateway.followRepository('arvore', 'design-system', 'main', {
      signal: ctrl.signal,
      operationId: 'op-follow-1',
    });
    expect(followed).toEqual({ name: 'arvore/design-system', branch: 'main' });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/repositories/follow',
      expect.objectContaining({ method: 'POST' }),
    );

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

    await gateway.seedDemoWorkspace({ signal: ctrl.signal, operationId: 'op-seed-1' });
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/demo/seed',
      expect.objectContaining({ method: 'POST' }),
    );

    await gateway.cancelServerOperation('op-merge-142');
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/operations/op-merge-142',
      expect.objectContaining({ method: 'DELETE' }),
    );
  });
});
