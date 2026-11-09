import { InjectionToken } from '@angular/core';
import {
  AuthenticatedUser,
  LoginCredentials,
  RepoInfo,
  SseConnectionState,
  TimelineEventItem,
  WorkspaceDataset,
} from '../models/workspace.models';

export interface ActionRequestOptions {
  readonly signal?: AbortSignal;
  readonly operationId?: string;
}

export interface AuthGateway {
  login(credentials: LoginCredentials, options?: ActionRequestOptions): Promise<AuthenticatedUser>;
  logout(token?: string): Promise<void>;
  loadStoredSession(): AuthenticatedUser | null;
  saveStoredSession(user: AuthenticatedUser): void;
  clearStoredSession(): void;
}

export interface PullRequestGateway {
  fetchWorkspace?(options?: ActionRequestOptions): Promise<WorkspaceDataset | null>;
  followRepository?(owner: string, name: string, defaultBranch?: string, options?: ActionRequestOptions): Promise<RepoInfo | null>;
  approvePullRequest(prId: number, options?: ActionRequestOptions): Promise<void>;
  mergePullRequest(prId: number, options?: ActionRequestOptions): Promise<{ sha: string }>;
  batchMergePullRequests?(prIds: readonly number[], options?: ActionRequestOptions): Promise<{ mergedIds: readonly number[] }>;
  commentOnPullRequest(prId: number, command: string, options?: ActionRequestOptions): Promise<void>;
  snoozePullRequest(prId: number, condition: string, options?: ActionRequestOptions): Promise<void>;
  claimPullRequest?(prId: number, options?: ActionRequestOptions): Promise<void>;
  seedDemoWorkspace?(options?: ActionRequestOptions): Promise<void>;
  cancelServerOperation(operationId: string): Promise<void>;
}

export interface LiveEventGateway {
  connect(
    onEvent: (event: Omit<TimelineEventItem, 'key' | 't'>) => void,
    onStateChange: (state: SseConnectionState) => void,
  ): () => void;
}

export interface TranslationGateway {
  loadDictionary(locale: string): Promise<Record<string, string>>;
}

export const PULL_REQUEST_GATEWAY = new InjectionToken<PullRequestGateway>('PULL_REQUEST_GATEWAY');
export const LIVE_EVENT_GATEWAY = new InjectionToken<LiveEventGateway>('LIVE_EVENT_GATEWAY');
export const TRANSLATION_GATEWAY = new InjectionToken<TranslationGateway>('TRANSLATION_GATEWAY');
export const AUTH_GATEWAY = new InjectionToken<AuthGateway>('AUTH_GATEWAY');
