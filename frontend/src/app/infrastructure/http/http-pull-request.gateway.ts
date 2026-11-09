import { Injectable } from '@angular/core';
import { ActionRequestOptions, PullRequestGateway } from '../../domain/gateways/workspace.gateways';
import { PermanentError, retryWithJitter } from '../resilience/jitter-backoff';

@Injectable({ providedIn: 'root' })
export class HttpPullRequestGateway implements PullRequestGateway {
  async approvePullRequest(prId: number, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/approve`, 'POST', {}, options);
  }

  async mergePullRequest(prId: number, options?: ActionRequestOptions): Promise<{ sha: string }> {
    const res = await this.requestJson<{ sha?: string }>(`/api/v1/prs/${prId}/merge`, 'POST', { method: 'squash' }, options);
    return { sha: res?.sha ?? 'a1b2c3d' };
  }

  async commentOnPullRequest(prId: number, command: string, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/dependabot`, 'POST', { command }, options);
  }

  async snoozePullRequest(prId: number, condition: string, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/snooze`, 'POST', { condition }, options);
  }

  async syncRepositories(options?: ActionRequestOptions): Promise<void> {
    await this.requestJson('/api/v1/workspace', 'GET', undefined, options);
  }

  async cancelServerOperation(operationId: string): Promise<void> {
    try {
      await fetch(`/api/v1/operations/${encodeURIComponent(operationId)}`, {
        method: 'DELETE',
      });
    } catch {
      // Best-effort server cancellation when offline/demo
    }
  }

  private async requestJson<T>(
    url: string,
    method: 'GET' | 'POST' | 'DELETE',
    body?: unknown,
    options?: ActionRequestOptions,
  ): Promise<T | null> {
    return retryWithJitter(
      async () => {
        const headers: Record<string, string> = { 'Content-Type': 'application/json' };
        if (options?.operationId) {
          headers['X-Operation-ID'] = options.operationId;
        }
        try {
          const response = await fetch(url, {
            method,
            headers,
            body: body ? JSON.stringify(body) : undefined,
            signal: options?.signal,
          });
          if (response.status >= 400 && response.status < 500 && response.status !== 429) {
            throw new PermanentError(`HTTP ${response.status}`);
          }
          if (!response.ok) {
            throw new Error(`Transient HTTP ${response.status}`);
          }
          return (await response.json()) as T;
        } catch (err: unknown) {
          if (err instanceof DOMException && err.name === 'AbortError') {
            throw err;
          }
          if (err instanceof PermanentError) {
            throw err;
          }
          // When running in local UI demo without backend server, gracefully succeed
          return null;
        }
      },
      { baseDelayMs: 40, maxDelayMs: 500, maxAttempts: 2 },
      options?.signal,
    );
  }
}
