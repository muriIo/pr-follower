import { Injectable, computed, inject, signal } from '@angular/core';
import { AUTH_GATEWAY } from '../../domain/gateways/workspace.gateways';
import { AuthErrorCode, AuthStatus, AuthenticatedUser } from '../../domain/models/workspace.models';
import { WorkspaceStore } from './workspace.store';

/** GitHub-only sign-in state (ADR-0010). The session lives in an HttpOnly cookie. */
@Injectable({ providedIn: 'root' })
export class AuthStore {
  private readonly authGateway = inject(AUTH_GATEWAY);
  private readonly workspaceStore = inject(WorkspaceStore);

  readonly status = signal<AuthStatus>('checking');
  readonly user = signal<AuthenticatedUser | null>(null);
  readonly authError = signal<AuthErrorCode | null>(null);
  readonly isAuthenticated = computed(() => this.user() !== null);

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
      this.workspaceStore.startLiveSession();
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
