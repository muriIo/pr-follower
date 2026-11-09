import { TestBed } from '@angular/core/testing';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { HttpPullRequestGateway } from './http-pull-request.gateway';

describe('HttpPullRequestGateway', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('sends X-Operation-ID header and AbortSignal on approve, merge, and cancelServerOperation', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input);
      if (url.endsWith('/merge')) {
        return new Response(JSON.stringify({ sha: 'f00ba77' }), { status: 200 });
      }
      return new Response('{}', { status: 200 });
    });

    TestBed.configureTestingModule({ providers: [HttpPullRequestGateway] });
    const gateway = TestBed.inject(HttpPullRequestGateway);

    const ctrl = new AbortController();
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

    await gateway.cancelServerOperation('op-merge-142');
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/operations/op-merge-142',
      expect.objectContaining({ method: 'DELETE' }),
    );
  });
});
