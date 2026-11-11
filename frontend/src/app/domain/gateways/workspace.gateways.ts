import { InjectionToken } from '@angular/core';
import {
  AuthErrorCode,
  AuthenticatedUser,
  ChangedFileItem,
  SseConnectionState,
  TimelineEventItem,
  WorkspaceDataset,
} from '../models/workspace.models';
import { GitHubAccountsResult, ImportableRepository } from '../models/repository-import.models';

export interface ActionRequestOptions {
  readonly signal?: AbortSignal;
  readonly operationId?: string;
}

/** GitHub-only sign-in backed by a server-side session cookie (ADR-0010). */
export interface AuthGateway {
  /** The session user, or null when signed out. */
  fetchCurrentUser(options?: ActionRequestOptions): Promise<AuthenticatedUser | null>;
  /** Hands the browser to the backend, which redirects to GitHub. */
  startSignIn(): void;
  /** Reads (once) why the last sign-in failed, if it did. */
  consumeAuthError(): AuthErrorCode | null;
  logout(): Promise<void>;
}

export interface PullRequestGateway {
  fetchWorkspace?(options?: ActionRequestOptions): Promise<WorkspaceDataset | null>;
  approvePullRequest(prId: number, options?: ActionRequestOptions): Promise<void>;
  /** `sha` is set only when GitHub reported the merge commit. Writes reject with an ActionError. */
  mergePullRequest(prId: number, options?: ActionRequestOptions): Promise<{ sha?: string }>;
  batchMergePullRequests?(prIds: readonly number[], options?: ActionRequestOptions): Promise<{ mergedIds: readonly number[] }>;
  commentOnPullRequest(prId: number, command: string, options?: ActionRequestOptions): Promise<void>;
  /** Runs the Dependabot analysis for a PR; rejects with an ActionError (`unavailable` without the GitHub App credentials). */
  analyzePullRequest?(prId: number, options?: ActionRequestOptions): Promise<void>;
  snoozePullRequest(prId: number, condition: string, options?: ActionRequestOptions): Promise<void>;
  claimPullRequest?(prId: number, options?: ActionRequestOptions): Promise<void>;
  cancelServerOperation(operationId: string): Promise<void>;
  /** A PR's changed files from GitHub, owned files first; rejects when they cannot be read. */
  fetchChangedFiles?(repo: string, number: number, options?: ActionRequestOptions): Promise<readonly ChangedFileItem[]>;
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

/** Vercel-style repository import: what the user reaches through the GitHub App (ADR-0010). */
export interface RepositoryImportGateway {
  listAccounts(options?: ActionRequestOptions): Promise<GitHubAccountsResult>;
  listRepositories(installationId: number, options?: ActionRequestOptions): Promise<readonly ImportableRepository[]>;
  /** Follows the repository and returns its PR Follower repository id. */
  importRepository(installationId: number, githubRepoId: number, options?: ActionRequestOptions): Promise<number>;
  unfollowRepository(repositoryId: number, options?: ActionRequestOptions): Promise<void>;
  /** The installation GitHub sent the user back from (Setup URL), read once. */
  consumeSetupRedirect(): number | null;
}

export const PULL_REQUEST_GATEWAY = new InjectionToken<PullRequestGateway>('PULL_REQUEST_GATEWAY');
export const LIVE_EVENT_GATEWAY = new InjectionToken<LiveEventGateway>('LIVE_EVENT_GATEWAY');
export const TRANSLATION_GATEWAY = new InjectionToken<TranslationGateway>('TRANSLATION_GATEWAY');
export const AUTH_GATEWAY = new InjectionToken<AuthGateway>('AUTH_GATEWAY');
export const REPOSITORY_IMPORT_GATEWAY = new InjectionToken<RepositoryImportGateway>('REPOSITORY_IMPORT_GATEWAY');
