import { Injectable, computed, inject, signal } from '@angular/core';
import { AUTH_GATEWAY } from '../../domain/gateways/workspace.gateways';
import {
  AuthenticatedUser,
  LoginCredentials,
  createDefaultAuthenticatedUser,
} from '../../domain/models/workspace.models';
import { ActionCancellationManager } from '../use-cases/action-cancellation.manager';
import { WorkspaceStore } from './workspace.store';

@Injectable({ providedIn: 'root' })
export class AuthStore {
  private readonly authGateway = inject(AUTH_GATEWAY, { optional: true });
  private readonly workspaceStore = inject(WorkspaceStore, { optional: true });
  private readonly cancellation = inject(ActionCancellationManager);

  readonly user = signal<AuthenticatedUser | null>(this.authGateway?.loadStoredSession() ?? null);
  readonly isAuthenticated = computed(() => this.user() !== null);
  readonly loginAction = computed(() =>
    this.cancellation.getView(this.cancellation.actionsState(), 'auth:login'),
  );

  async login(credentials: LoginCredentials, delayMs: number = 450): Promise<void> {
    const cleanLogin = credentials.login.trim().replace(/^@/, '') || 'murilo';
    const cleanTeam = credentials.team.trim() || '@arvore/frontend';
    const normalized: LoginCredentials = {
      ...credentials,
      login: cleanLogin,
      team: cleanTeam,
    };

    let resolvedUser: AuthenticatedUser | null = null;
    await this.cancellation.run(
      'auth:login',
      [
        {
          label: `Autenticando @${cleanLogin}...`,
          execute: async (opts) => {
            await this.waitWithSignal(delayMs, opts.signal);
            const fromApi = await this.authGateway?.login(normalized, opts);
            resolvedUser = fromApi ?? {
              ...createDefaultAuthenticatedUser(),
              login: cleanLogin,
              avatarInitial: cleanLogin[0]?.toUpperCase() ?? 'M',
              primaryTeam: cleanTeam,
              teams: [cleanTeam],
            };
          },
        },
      ],
      () => {
        if (resolvedUser) {
          this.user.set(resolvedUser);
          this.authGateway?.saveStoredSession(resolvedUser);
          void this.workspaceStore?.loadWorkspace();
        }
        return null;
      },
      () => 'Autenticação cancelada.',
    );
  }

  cancelLogin(): void {
    this.cancellation.cancel('auth:login');
  }

  async logout(): Promise<void> {
    const token = this.user()?.token;
    this.authGateway?.clearStoredSession();
    this.workspaceStore?.closeDrawer();
    this.user.set(null);
    await this.authGateway?.logout(token);
  }

  private waitWithSignal(ms: number, signal: AbortSignal): Promise<void> {
    if (signal.aborted) {
      return Promise.reject(new DOMException('Aborted', 'AbortError'));
    }
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        signal.removeEventListener('abort', onAbort);
        resolve();
      }, ms);
      const onAbort = () => {
        clearTimeout(timer);
        reject(new DOMException('Aborted', 'AbortError'));
      };
      signal.addEventListener('abort', onAbort, { once: true });
    });
  }
}
