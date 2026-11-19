# ADR-0015: Org-Allowlisted Sign-In and Membership Recheck

## Status
Accepted (2026-11-18). Amends ADR-0010 Decision 1 ("Sign-in is open to every GitHub account").

## Context
ADR-0010 opened sign-in to every GitHub account and let GitHub limit what each user sees: a stranger who signs in finds nothing to import, because imports are checked against their own GitHub access. Two gaps remain for a company that runs PR Follower for its own organization:

1. **Anyone can still sign in.** The owner wants only members of their GitHub organization to get a session at all, not a stranger with an empty panel.
2. **Nothing notices a leaver.** Removing someone from the organization takes away their GitHub access, but not their PR Follower session or follows. Until the session expires (30 days) they keep reading what is stored: PR titles, CI state, reviews and the Dependabot analysis with its blast-radius code snippets.

GitHub can answer "is this account a member of this organization?" in two ways: with the user's own token (`GET /user/memberships/orgs/{org}`), which depends on what the user authorized and needs them to be signed in, or as the app installed on the organization (`GET /orgs/{org}/members/{username}` with an installation token), which needs the organization **Members: read** permission (already requested for CODEOWNERS teams), sees private (concealed) members, and works for a background sweep with no user present. GitHub's documentation (REST "Check organization membership for a user"; "Permissions required for GitHub Apps", Members: read, user and installation tokens) gives the answers: `204` member, `404` not a member, `302` the requester cannot see the members.

## Decision
1. **An optional allowlist.** `ALLOWED_GITHUB_ORGS` (comma-separated organization logins, case-insensitive). Unset or empty keeps ADR-0010's open sign-in for every other self-hoster. Set without the GitHub App's credentials, the API refuses to start: membership could never be checked.
2. **Checked before anything is stored.** The GitHub callback reads the profile, then asks each allowed organization, as the app's installation there (`GET /orgs/{org}/installation` with the app JWT, then `GET /orgs/{org}/members/{login}` with the installation token, redirects not followed). Only then is the user upserted and a session created. A member of any listed organization is admitted.
3. **Fail closed, and say which way.** `not_org_member` only when every listed organization answered "not a member". When any answer is missing (GitHub unavailable, rate limited, the app not installed on the organization, the Members permission not granted, a `302`) and none confirmed membership, the answer is `org_membership_unverified`: no session, a message saying it could not be verified, the cause in the API log. Nobody is let in on a guess and nobody is told they are not a member on a guess. A `404` is trusted only after `GET /user/{id}` confirms the stored login still names the account; a renamed account is checked under its current login.
4. **Leavers lose access within 15 minutes.** Every replica runs a sweep at start and every 15 minutes: each user with a session or a follow is rechecked; a user GitHub confirms is in none of the organizations loses, in one transaction, their sessions, follows, per-user PR state (`pr_user_states`: snoozes and turn state derived from the organization's PRs, worthless to someone outside it) and sync job. The user row stays, so claims and the activity history keep a name. An unverified answer changes nothing for that user and is logged; the next sweep tries again. Deletes converge, so several replicas sweeping at once is safe (ADR-0011, ADR-0012), and the sweep stops between users when cancelled (ADR-0007).
5. **No check on token refresh or per request.** A leaver who only reads stored data may never trigger a token refresh (reads of the workspace do not call GitHub), so that hook would not close the gap; checking on every request would put a GitHub call on every page load. The sweep bounds the exposure instead.

## Consequences
- **Positive:** a company deployment is closed to strangers, and a leaver's access to stored data ends within 15 minutes without anyone deleting rows by hand.
- **Positive:** private members are admitted, and the check does not depend on what each user authorized.
- **Negative:** each sign-in costs two more GitHub calls per allowed organization, and each sweep one per user with access (plus the installation lookup), on the organization installation's rate limit.
- **Negative:** when GitHub cannot be asked, nobody new can sign in (fail closed) and leavers are not removed until it can.
- **Negative:** a leaver's follows and snoozes are gone; if they rejoin they import their repositories again. Their claims stay until someone releases them.

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails when:
- `CompleteLogin` in `backend/internal/usecase/auth/` never calls `requireAllowedOrgMember`, or calls `users.Upsert` or `sessions.Create` before it;
- an `auth_error` code the callback sends (`redirectWithAuthError(..., "<code>")` in `backend/internal/adapter/http/`) is missing from `KNOWN_AUTH_ERRORS` in `frontend/src/app/infrastructure/auth/http-auth.gateway.ts`, or has no `auth.error.<code>` key in `pt-BR.json` or `en-US.json`;
- the server reads `ALLOWED_GITHUB_ORGS` (`config.go`) but `docs/production-setup.md` or `.env.example` does not document it.

The behaviour is covered by `usecase/auth` tests (admitted, refused, unverified, sweep, cancellation, idempotence), `adapter/github` tests against a fake GitHub (204, 404, renamed account, 302, missing permission, app not installed, 5xx), the `MemberAccessStore` contract on SQLite, and the callback tests in `adapter/http`.
