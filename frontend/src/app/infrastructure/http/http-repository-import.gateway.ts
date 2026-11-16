import { Injectable, inject } from '@angular/core';
import { SessionExpiryMonitor } from '../../application/use-cases/session-expiry.monitor';
import { ActionRequestOptions, RepositoryImportGateway } from '../../domain/gateways/workspace.gateways';
import {
  GitHubAccount,
  GitHubAccountsResult,
  ImportableRepository,
  RepositoryImportError,
  SERVER_IMPORT_ERROR_CODES,
} from '../../domain/models/repository-import.models';
import { BROWSER_WINDOW } from '../browser/browser-window';

interface InstallationDto {
  readonly id: number;
  readonly account_login: string;
  readonly account_type: string;
  readonly account_avatar_url?: string;
  readonly repository_selection: string;
  readonly settings_url: string;
}

interface ImportableRepositoryDto {
  readonly github_repo_id: number;
  readonly installation_id: number;
  readonly owner: string;
  readonly name: string;
  readonly full_name: string;
  readonly default_branch: string;
  readonly private: boolean;
  readonly followed: boolean;
  readonly repository_id?: number;
}

/** Talks to the backend import endpoints, which check everything against GitHub (ADR-0010). */
@Injectable({ providedIn: 'root' })
export class HttpRepositoryImportGateway implements RepositoryImportGateway {
  private readonly win = inject(BROWSER_WINDOW);
  private readonly session = inject(SessionExpiryMonitor);

  async listAccounts(options?: ActionRequestOptions): Promise<GitHubAccountsResult> {
    const body = await this.request<{ accounts?: InstallationDto[]; install_url?: string }>(
      '/api/v1/github/installations',
      'GET',
      undefined,
      options,
    );
    return {
      accounts: (body.accounts ?? []).map(toAccount),
      installUrl: body.install_url ?? '',
    };
  }

  async listRepositories(installationId: number, options?: ActionRequestOptions): Promise<readonly ImportableRepository[]> {
    const body = await this.request<{ repositories?: ImportableRepositoryDto[] }>(
      `/api/v1/github/installations/${installationId}/repositories`,
      'GET',
      undefined,
      options,
    );
    return (body.repositories ?? []).map(toImportableRepository);
  }

  async importRepository(installationId: number, githubRepoId: number, options?: ActionRequestOptions): Promise<number> {
    const body = await this.request<{ repository?: { id: number } }>(
      '/api/v1/repositories/import',
      'POST',
      { installation_id: installationId, github_repo_id: githubRepoId },
      options,
    );
    return body.repository?.id ?? 0;
  }

  async unfollowRepository(repositoryId: number, options?: ActionRequestOptions): Promise<void> {
    await this.request(`/api/v1/repositories/${repositoryId}/follow`, 'DELETE', undefined, options);
  }

  consumeSetupRedirect(): number | null {
    const params = new URLSearchParams(this.win.location.search);
    if (!params.has('installation_id') && !params.has('setup_action')) {
      return null;
    }
    const installationId = Number(params.get('installation_id'));
    params.delete('installation_id');
    params.delete('setup_action');
    const query = params.toString();
    this.win.history.replaceState(
      null,
      '',
      `${this.win.location.pathname}${query ? `?${query}` : ''}${this.win.location.hash}`,
    );
    return Number.isInteger(installationId) && installationId > 0 ? installationId : null;
  }

  private async request<T>(url: string, method: string, body: unknown, options?: ActionRequestOptions): Promise<T> {
    const headers: Record<string, string> = {};
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    if (options?.operationId) {
      headers['X-Operation-ID'] = options.operationId;
    }
    let response: Response;
    try {
      response = await fetch(url, {
        method,
        headers,
        credentials: 'same-origin',
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: options?.signal,
      });
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        throw err;
      }
      throw new RepositoryImportError('failed');
    }
    if (response.ok) {
      return (await response.json()) as T;
    }
    const payload = (await response.json().catch(() => null)) as { error?: string; code?: string; retry_at?: string } | null;
    if (response.status === 401) {
      const expired = payload?.code === 'github_token_expired';
      this.session.report(expired ? 'github_token_expired' : 'unauthorized');
      throw new RepositoryImportError(expired ? 'github_token_expired' : 'unauthorized', payload?.error ?? '');
    }
    if (response.status === 503 && (payload?.code === undefined || payload.code === 'unavailable')) {
      throw new RepositoryImportError('not_configured');
    }
    const known = SERVER_IMPORT_ERROR_CODES.find((code) => code === payload?.code);
    if (known) {
      throw new RepositoryImportError(known, payload?.error ?? '', payload?.retry_at);
    }
    throw new RepositoryImportError(response.status === 404 ? 'not_found' : 'failed', payload?.error ?? '');
  }
}

function toAccount(dto: InstallationDto): GitHubAccount {
  return {
    installationId: dto.id,
    login: dto.account_login,
    type: dto.account_type === 'Organization' ? 'Organization' : 'User',
    avatarUrl: dto.account_avatar_url ?? '',
    repositorySelection: dto.repository_selection === 'all' ? 'all' : 'selected',
    settingsUrl: dto.settings_url,
  };
}

function toImportableRepository(dto: ImportableRepositoryDto): ImportableRepository {
  return {
    githubRepoId: dto.github_repo_id,
    installationId: dto.installation_id,
    owner: dto.owner,
    name: dto.name,
    fullName: dto.full_name,
    defaultBranch: dto.default_branch,
    private: dto.private,
    followed: dto.followed,
    repositoryId: dto.repository_id ?? null,
  };
}
