import { Injectable } from '@angular/core';
import { ActionRequestOptions, AuthGateway } from '../../domain/gateways/workspace.gateways';
import {
  AuthenticatedUser,
  LoginCredentials,
  createDefaultAuthenticatedUser,
} from '../../domain/models/workspace.models';
import { PermanentError, retryWithJitter } from '../resilience/jitter-backoff';

const STORAGE_KEY = 'pf-auth-session';

@Injectable({ providedIn: 'root' })
export class HttpAuthGateway implements AuthGateway {
  async login(
    credentials: LoginCredentials,
    options?: ActionRequestOptions,
  ): Promise<AuthenticatedUser> {
    const cleanLogin = credentials.login.replace(/^@/, '').trim() || 'murilo';
    const cleanTeam = credentials.team.trim()
      ? credentials.team.trim().startsWith('@')
        ? credentials.team.trim()
        : `@${credentials.team.trim()}`
      : '@arvore/frontend';

    const res = await retryWithJitter(
      async () => {
        const headers: Record<string, string> = { 'Content-Type': 'application/json' };
        if (options?.operationId) {
          headers['X-Operation-ID'] = options.operationId;
        }
        try {
          const response = await fetch('/api/v1/auth/login', {
            method: 'POST',
            headers,
            body: JSON.stringify({
              login: cleanLogin,
              team: cleanTeam,
              github_token: credentials.githubToken ?? '',
            }),
            signal: options?.signal,
          });
          if (response.status >= 400 && response.status < 500 && response.status !== 429) {
            throw new PermanentError(`HTTP ${response.status}`);
          }
          if (!response.ok) {
            throw new Error(`Transient HTTP ${response.status}`);
          }
          return (await response.json()) as {
            token?: string;
            user?: { id?: number; login?: string; name?: string; teams?: string[] };
          };
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
      { baseDelayMs: 40, maxDelayMs: 400, maxAttempts: 2 },
      options?.signal,
    );

    const fallback = createDefaultAuthenticatedUser(
      res?.user?.login ?? cleanLogin,
      res?.user?.teams?.[0] ?? cleanTeam,
      res?.token ?? `pf_sess_${Date.now()}`,
    );

    return {
      ...fallback,
      id: res?.user?.id ?? fallback.id,
      name: res?.user?.name ?? fallback.name,
      teams: res?.user?.teams && res.user.teams.length > 0 ? res.user.teams : fallback.teams,
    };
  }

  async logout(token?: string): Promise<void> {
    try {
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      if (token) {
        headers['Authorization'] = `Bearer ${token}`;
      }
      await fetch('/api/v1/auth/logout', {
        method: 'POST',
        headers,
      });
    } catch {
      // Best-effort server logout when offline
    }
  }

  loadStoredSession(): AuthenticatedUser | null {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) {
        return null;
      }
      const parsed = JSON.parse(raw) as AuthenticatedUser;
      if (!parsed || !parsed.login) {
        return null;
      }
      return parsed;
    } catch {
      return null;
    }
  }

  saveStoredSession(user: AuthenticatedUser): void {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(user));
    } catch {
      // Ignore storage quota errors
    }
  }

  clearStoredSession(): void {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // Ignore storage errors
    }
  }
}
