# ADR-0010: GitHub-Only Identity and Installation-Scoped Repository Access

## Status
Accepted (2026-11-10)

## Context
PR Follower is a public, team-shared web app: anyone, anywhere, should be able to sign in. The owner does not want to manage passwords or any other credential of our own. Until now `POST /api/v1/auth/login` fabricated a user from a typed handle, the API trusted an `X-User-ID` header (defaulting to user 1), and no GitHub token ever reached the GitHub gateway.

`manifest.md` already chose a **GitHub App** (fine-grained permissions, native webhooks). A GitHub App user access token can only reach repositories that **both** the user and the app can access (`GET /user/installations`), so repository access is governed by where the app is installed. Vercel's GitHub integration is the reference UX for that model: sign in with GitHub, then import repositories from the accounts where the app is installed, with a link to install or adjust the app when a repository is missing.

## Decision
1. **GitHub is the only identity provider.** No passwords, no local credentials, no typed handles. Sign-in uses the GitHub App **web application flow**:
   - `GET /api/v1/auth/github/login` generates a random `state` and a PKCE `code_verifier`, stores both in a short-lived HttpOnly cookie, and redirects to `https://github.com/login/oauth/authorize` with `client_id`, `redirect_uri`, `state`, `code_challenge` and `code_challenge_method=S256`.
   - `GET /api/v1/auth/github/callback` rejects a missing or mismatched `state`, exchanges `code` + `code_verifier` for a user access token (8 h) and refresh token (6 months), reads the profile from `GET /user`, and upserts the user by `github_id`.
   - Sign-in is open to every GitHub account. What a user sees is limited by GitHub, not by an allowlist.
2. **Server-side sessions.** The callback creates a session with a 256-bit random token; only its SHA-256 hash is stored. The browser holds the raw token in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` when `PUBLIC_URL` is https). Every `/api/v1` route except the auth flow, `/healthz` and the GitHub webhook requires a valid, unexpired session, and the user comes from the session only. `POST /api/v1/auth/logout` deletes the session and clears the cookie.
3. **GitHub tokens never reach the browser** and are stored encrypted at rest (AES-256-GCM, key from `TOKEN_ENCRYPTION_KEY`). Access tokens are refreshed with the refresh token shortly before they expire.
4. **Acting as the user.** Approvals, merges and comments use the user's access token, so GitHub attributes them to the person. The app's installation token is for background work only (webhook processing, tarball and lockfile reads).
5. **Installation-scoped repositories (Vercel model).** Followable repositories are the ones listed by `GET /user/installations` and `GET /user/installations/{id}/repositories` for that user. When a repository is missing, the UI links to the app's installation page; the app's Setup URL returns the user to the import screen. `installation` and `installation_repositories` webhooks keep access in sync.

## Consequences
- **Positive:** No password storage, reset, or breach surface; identity, teams and repository permissions are always GitHub's.
- **Positive:** Actions on GitHub are attributed to real users, and the app never sees repositories it was not installed on.
- **Negative:** Following an organization's repository requires the app to be installed there (an org owner installs it, or approves a member's request). The onboarding has to make that step obvious.
- **Negative:** Local development needs a GitHub App of its own (callback `http://localhost:4200/api/v1/auth/github/callback`) and the `GITHUB_*`, `PUBLIC_URL` and `TOKEN_ENCRYPTION_KEY` settings.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` fails if any non-test file under `backend/internal/` or `frontend/src/app/` references the `X-User-ID` header (identity must come from the session), or if any migration in `backend/internal/infrastructure/database/migrations/` defines a `password` column.
