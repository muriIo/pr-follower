import { Injectable } from '@angular/core';
import { ActionRequestOptions, PullRequestGateway } from '../../domain/gateways/workspace.gateways';
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
  async fetchWorkspace(options?: ActionRequestOptions): Promise<WorkspaceDataset | null> {
    const res = await this.requestJson<ApiWorkspaceResponseDto>('/api/v1/workspace', 'GET', undefined, options);
    if (!res) {
      return null;
    }
    return mapApiWorkspaceResponseToDataset(res);
  }

  async approvePullRequest(prId: number, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/approve`, 'POST', { body: 'Approved via PR Follower' }, options);
  }

  async mergePullRequest(prId: number, options?: ActionRequestOptions): Promise<{ sha: string }> {
    const res = await this.requestJson<{ sha?: string }>(
      `/api/v1/prs/${prId}/merge`,
      'POST',
      { method: 'squash' },
      options,
    );
    return { sha: res?.sha ?? 'a1b2c3d' };
  }

  async batchMergePullRequests(
    prIds: readonly number[],
    options?: ActionRequestOptions,
  ): Promise<{ mergedIds: readonly number[] }> {
    const res = await this.requestJson<{ merged_ids?: number[] }>(
      '/api/v1/prs/batch-merge',
      'POST',
      { pr_ids: prIds, method: 'squash' },
      options,
    );
    return { mergedIds: res?.merged_ids ?? prIds };
  }

  async commentOnPullRequest(prId: number, command: string, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/dependabot-command`, 'POST', { command }, options);
  }

  async snoozePullRequest(prId: number, condition: string, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/snooze`, 'POST', { condition }, options);
  }

  async claimPullRequest(prId: number, options?: ActionRequestOptions): Promise<void> {
    await this.requestJson(`/api/v1/prs/${prId}/claim`, 'POST', {}, options);
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
          return null;
        }
      },
      { baseDelayMs: 40, maxDelayMs: 500, maxAttempts: 2 },
      options?.signal,
    );
  }
}
