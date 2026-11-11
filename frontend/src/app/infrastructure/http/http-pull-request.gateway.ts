import { Injectable, inject } from '@angular/core';
import { SessionExpiryMonitor } from '../../application/use-cases/session-expiry.monitor';
import { ActionRequestOptions, PullRequestGateway } from '../../domain/gateways/workspace.gateways';
import { ActionError, toActionErrorCode } from '../../domain/models/action-error.models';
import { ChangedFileItem, WorkspaceDataset } from '../../domain/models/workspace.models';
import { PermanentError, retryWithJitter } from '../resilience/jitter-backoff';
import {
  ApiPullRequestFileDto,
  ApiWorkspaceResponseDto,
  mapApiWorkspaceResponseToDataset,
  mapPullRequestFile,
} from './workspace-api.mapper';

@Injectable({ providedIn: 'root' })
export class HttpPullRequestGateway implements PullRequestGateway {
  private readonly session = inject(SessionExpiryMonitor);

  async fetchWorkspace(options?: ActionRequestOptions): Promise<WorkspaceDataset | null> {
    const res = await this.requestJson<ApiWorkspaceResponseDto>('/api/v1/workspace', 'GET', undefined, options);
    if (!res) {
      return null;
    }
    return mapApiWorkspaceResponseToDataset(res);
  }

  async approvePullRequest(prId: number, options?: ActionRequestOptions): Promise<void> {
    await this.postAction(`/api/v1/prs/${prId}/approve`, { body: 'Approved via PR Follower' }, options);
  }

  async mergePullRequest(prId: number, options?: ActionRequestOptions): Promise<{ sha?: string }> {
    const res = await this.postAction<{ sha?: string }>(`/api/v1/prs/${prId}/merge`, { method: 'squash' }, options);
    return { sha: res?.sha };
  }

  async batchMergePullRequests(
    prIds: readonly number[],
    options?: ActionRequestOptions,
  ): Promise<{ mergedIds: readonly number[] }> {
    const res = await this.postAction<{ merged_ids?: number[] }>(
      '/api/v1/prs/batch-merge',
      { pr_ids: prIds, method: 'squash' },
      options,
    );
    return { mergedIds: res?.merged_ids ?? [] };
  }

  async commentOnPullRequest(prId: number, command: string, options?: ActionRequestOptions): Promise<void> {
    await this.postAction(`/api/v1/prs/${prId}/dependabot-command`, { command }, options);
  }

  async analyzePullRequest(prId: number, options?: ActionRequestOptions): Promise<void> {
    await this.postAction(`/api/v1/prs/${prId}/analyze`, {}, options);
  }

  async snoozePullRequest(prId: number, condition: string, options?: ActionRequestOptions): Promise<void> {
    await this.postAction(`/api/v1/prs/${prId}/snooze`, { condition }, options);
  }

  async claimPullRequest(prId: number, options?: ActionRequestOptions): Promise<void> {
    await this.postAction(`/api/v1/prs/${prId}/claim`, {}, options);
  }

  async cancelServerOperation(operationId: string): Promise<void> {
    try {
      await fetch(`/api/v1/operations/${encodeURIComponent(operationId)}`, {
        method: 'DELETE',
      });
    } catch {
      // Best-effort server cancellation; the request may already be gone
    }
  }

  async fetchChangedFiles(
    repo: string,
    number: number,
    options?: ActionRequestOptions,
  ): Promise<readonly ChangedFileItem[]> {
    const res = await this.requestJson<{ files?: ApiPullRequestFileDto[] }>(
      `/api/v1/repositories/${repo}/pulls/${number}/files`,
      'GET',
      undefined,
      options,
    );
    if (!res) {
      throw new Error('changed files unavailable');
    }
    return (res.files ?? []).map(mapPullRequestFile);
  }

  /**
   * A change the user asked for: sent once (a retried POST could repeat it), never reported as
   * done when it was not. A refusal rejects with the server's code and explanation; no answer at
   * all is an unknown outcome, because the request may have been applied.
   */
  private async postAction<T>(url: string, body: unknown, options?: ActionRequestOptions): Promise<T | null> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (options?.operationId) {
      headers['X-Operation-ID'] = options.operationId;
    }
    let response: Response;
    try {
      response = await fetch(url, { method: 'POST', headers, body: JSON.stringify(body), signal: options?.signal });
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        throw err;
      }
      throw new ActionError('outcome_unknown');
    }
    const payload = (await response.json().catch(() => null)) as { error?: string; code?: string } | null;
    if (response.status === 401) {
      this.session.report(payload?.code === 'github_token_expired' ? 'github_token_expired' : 'unauthorized');
    }
    if (!response.ok) {
      throw new ActionError(toActionErrorCode(payload?.code), payload?.error ?? '');
    }
    return payload as T | null;
  }

  private async reportUnauthorized(response: Response): Promise<void> {
    let code: string | undefined;
    try {
      code = ((await response.clone().json()) as { code?: string } | null)?.code;
    } catch {
      // No readable body: it is still an unauthenticated answer.
    }
    this.session.report(code === 'github_token_expired' ? 'github_token_expired' : 'unauthorized');
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
          if (response.status === 401) {
            await this.reportUnauthorized(response);
          }
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
          return null;
        }
      },
      { baseDelayMs: 40, maxDelayMs: 500, maxAttempts: 2 },
      options?.signal,
    );
  }
}
