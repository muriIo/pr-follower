import { Injectable, computed, inject, signal } from '@angular/core';
import { AUTH_GATEWAY } from '../../domain/gateways/workspace.gateways';
import { AuthErrorCode, AuthStatus, AuthenticatedUser } from '../../domain/models/workspace.models';
import { SessionExpiryMonitor } from '../use-cases/session-expiry.monitor';
import { WorkspaceStore } from './workspace.store';

/** GitHub-only sign-in state (ADR-0010). The session lives in an HttpOnly cookie. */
@Injectable({ providedIn: 'root' })
export class AuthStore {
  private readonly authGateway = inject(AUTH_GATEWAY);
  private readonly workspaceStore = inject(WorkspaceStore);
  private verifying = false;

  readonly status = signal<AuthStatus>('checking');
  readonly user = signal<AuthenticatedUser | null>(null);
  readonly authError = signal<AuthErrorCode | null>(null);
  readonly isAuthenticated = computed(() => this.user() !== null);

  constructor() {
    inject(SessionExpiryMonitor).listen((issue) => {
      if (issue === 'github_token_expired') {
        void this.endSession('github_token_expired');
      } else {
        void this.verifySession();
      }
    });
  }

  /**
   * An API call was refused as unauthenticated: ask the server whether the session is really gone
   * (a 401 can also be GitHub refusing the app) and, if so, return to the sign-in screen. One
   * check at a time covers a burst of refused calls; "could not ask" never signs anyone out.
   */
  private async verifySession(): Promise<void> {
    if (this.status() !== 'signed-in' || this.verifying) {
      return;
    }
    this.verifying = true;
    try {
      if ((await this.authGateway.verifySession()) === 'expired') {
        this.leave('session_expired');
      }
    } finally {
      this.verifying = false;
    }
  }

  /** The user's own GitHub access is gone: nothing works until they sign in again. */
  private async endSession(reason: AuthErrorCode): Promise<void> {
    if (this.status() !== 'signed-in') {
      return;
    }
    this.leave(reason);
    await this.authGateway.logout(); // clear the server session too, so a reload does not resume it
  }

  private leave(reason: AuthErrorCode): void {
    this.workspaceStore.stopLiveSession();
    this.workspaceStore.closeDrawer();
    this.user.set(null);
    this.authError.set(reason);
    this.status.set('signed-out');
  }

  /** Resolves the current session (e.g. on app start or back from GitHub). */
  async restore(): Promise<void> {
    const error = this.authGateway.consumeAuthError();
    if (error) {
      this.authError.set(error);
    }
    const user = await this.authGateway.fetchCurrentUser();
    this.user.set(user);
    if (user) {
      this.status.set('signed-in');
      this.workspaceStore.startLiveSession(user.login);
    } else {
      this.status.set('signed-out');
    }
  }

  signIn(): void {
    this.authError.set(null);
    this.status.set('redirecting');
    this.authGateway.startSignIn();
  }

  async logout(): Promise<void> {
    this.workspaceStore.stopLiveSession();
    this.workspaceStore.closeDrawer();
    this.user.set(null);
    this.status.set('signed-out');
    await this.authGateway.logout();
  }
}
