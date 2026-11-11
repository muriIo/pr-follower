import { Injectable } from '@angular/core';

/** Why an API call was refused as unauthenticated (HTTP 401). */
export type SessionIssue = 'unauthorized' | 'github_token_expired';

/**
 * Hears every API call the server refused as unauthenticated (HTTP 401) and tells the auth store,
 * which decides what to do. The gateways report; they never decide.
 */
@Injectable({ providedIn: 'root' })
export class SessionExpiryMonitor {
  private listener: ((issue: SessionIssue) => void) | null = null;

  listen(listener: (issue: SessionIssue) => void): void {
    this.listener = listener;
  }

  /** `github_token_expired`: the session is fine but the user's own GitHub access is gone. */
  report(issue: SessionIssue = 'unauthorized'): void {
    this.listener?.(issue);
  }
}
