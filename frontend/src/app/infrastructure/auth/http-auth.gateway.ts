import { Injectable, InjectionToken, inject } from '@angular/core';
import { ActionRequestOptions, AuthGateway } from '../../domain/gateways/workspace.gateways';
import {
  AuthErrorCode,
  AuthenticatedUser,
  buildAuthenticatedUser,
} from '../../domain/models/workspace.models';
import { PermanentError, retryWithJitter } from '../resilience/jitter-backoff';

/** The slice of `window` the gateway needs, injectable so tests never navigate. */
export interface BrowserWindow {
  readonly location: Pick<Location, 'assign' | 'search' | 'pathname' | 'hash'>;
  readonly history: Pick<History, 'replaceState'>;
}

export const BROWSER_WINDOW = new InjectionToken<BrowserWindow>('BROWSER_WINDOW', {
  providedIn: 'root',
  factory: () => window,
});

const SIGN_IN_PATH = '/api/v1/auth/github/login';
const KNOWN_AUTH_ERRORS: readonly AuthErrorCode[] = [
  'not_configured',
  'state_mismatch',
  'access_denied',
  'login_failed',
];

interface MeResponseDto {
  readonly user?: {
    readonly id: number;
    readonly login: string;
    readonly name?: string;
    readonly avatar_url?: string;
    readonly teams?: readonly string[] | null;
  };
}

@Injectable({ providedIn: 'root' })
export class HttpAuthGateway implements AuthGateway {
  private readonly win = inject(BROWSER_WINDOW);

  async fetchCurrentUser(options?: ActionRequestOptions): Promise<AuthenticatedUser | null> {
    try {
      const body = await retryWithJitter(
        async () => {
          const response = await fetch('/api/v1/auth/me', {
            method: 'GET',
            credentials: 'same-origin',
            signal: options?.signal,
          });
          if (response.status === 401) {
            return null;
          }
          if (response.status >= 400 && response.status < 500) {
            throw new PermanentError(`HTTP ${response.status}`);
          }
          if (!response.ok) {
            throw new Error(`Transient HTTP ${response.status}`);
          }
          return (await response.json()) as MeResponseDto;
        },
        { baseDelayMs: 100, maxDelayMs: 1000, maxAttempts: 3 },
        options?.signal,
      );
      if (!body?.user) {
        return null;
      }
      return buildAuthenticatedUser({
        id: body.user.id,
        login: body.user.login,
        name: body.user.name,
        avatarUrl: body.user.avatar_url,
        teams: body.user.teams ?? [],
      });
    } catch {
      // Unreachable backend or rejected session: the user sees the sign-in screen.
      return null;
    }
  }

  startSignIn(): void {
    this.win.location.assign(SIGN_IN_PATH);
  }

  consumeAuthError(): AuthErrorCode | null {
    const params = new URLSearchParams(this.win.location.search);
    const raw = params.get('auth_error');
    if (raw === null) {
      return null;
    }
    params.delete('auth_error');
    const query = params.toString();
    this.win.history.replaceState(
      null,
      '',
      `${this.win.location.pathname}${query ? `?${query}` : ''}${this.win.location.hash}`,
    );
    return (KNOWN_AUTH_ERRORS as readonly string[]).includes(raw) ? (raw as AuthErrorCode) : 'unknown';
  }

  async logout(): Promise<void> {
    try {
      await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'same-origin' });
    } catch {
      // The local sign-out still happens; the server session expires on its own.
    }
  }
}
