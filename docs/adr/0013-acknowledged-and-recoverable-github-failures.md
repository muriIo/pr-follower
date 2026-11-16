# ADR-0013: Acknowledged and Recoverable GitHub Failures

## Status
Accepted (2026-11-16)

## Context
Every useful thing PR Follower does goes through GitHub: reading PRs, files, checks and reviews, importing a repository, approving, merging, commenting. GitHub says no in many ways: a branch rule blocks the merge (405), the PR changed under the user (409/422), the token lost a permission (403), a limit was reached (403/429 with headers), the service hiccups (5xx), or nothing answers at all.

ADR-0006 retries what is safe to retry and ADR-0011 never repeats a write blindly. Neither says what the user sees once the retries are over. The answer had drifted per path:
- some read failures left the GitHub adapter as bare errors ("github server error (502)", a transport error, "github rate limited (429)"), so the API answered `500 internal` and the panel said "Internal error";
- the import screen turned every failure into "Could not reach GitHub", even a rate limit with a known reset time or a permission GitHub refused;
- an action's error note disappeared after six seconds, so a user who looked away never knew the merge had failed;
- a batch that failed midway said why, but not which PR failed or how many had already gone through.

A failure nobody sees cannot be recovered from, and a failure the panel cannot name makes the user distrust everything else it shows.

## Decision
A GitHub interaction ends in one of two ways: it succeeded, or the user is told, in their language, what GitHub said and what they can do next. The app keeps working either way.

1. **The adapter names every failure.** `internal/adapter/github` turns each GitHub answer (or the lack of one) into a domain error kind (`entity.Err*`), keeping GitHub's own message as detail:
   | GitHub said | Kind | API code |
   | :--- | :--- | :--- |
   | 401 to the user's token | `ErrGitHubTokenExpired` | `github_token_expired` |
   | 401 to the app's credentials | `ErrUnauthorized` | `unauthorized` |
   | 403 (no permission) | `ErrForbidden` | `forbidden` |
   | 403/429 with a limit | `RateLimitedError` (reset time) | `rate_limited` |
   | 404 | `ErrNotFound` | `not_found` |
   | 405/409/422 or another refusal | `ErrConflict` (GitHub's message) | `conflict` |
   | 5xx or no answer to a **read** | `ErrGitHubUnavailable` | `github_unavailable` |
   | 5xx or no answer to a **write** | `ErrOutcomeUnknown` | `outcome_unknown` |
   No bare `errors.New` or `fmt.Errorf` without `%w` leaves the adapter.
2. **The API always answers with a code.** Every error response carries a stable `code`, the human `error` (GitHub's explanation when it refused) and, for limits, `retry_at`. Every code the API can send is one the SPA knows and has a translation for.
3. **The SPA acknowledges every failure.** A failed action leaves a note that says what happened, with GitHub's detail, and stays until the user dismisses it or starts another action (success and cancel notes still fade). No promise rejection escapes to the console, and no button stays disabled or spinning after a failure.
4. **Recovery is offered, not guessed.** When the failure means GitHub's state may differ from ours (`outcome_unknown`, `conflict`, `checks_running`, `not_found`), the workspace is re-read. A rate limit says when to try again. An expired GitHub access signs the user out (ADR-0010). A batch that stops reports how many PRs went through and which one failed, so the rest can be retried.
5. **Every screen that talks to GitHub uses the same codes.** The import screen shows the server's code and detail, like the drawer does, instead of its own catch-all.

## Consequences
- **Positive:** "Internal error" now means a bug in PR Follower, never GitHub being GitHub; support can read the code.
- **Positive:** A new GitHub call cannot quietly add an untranslated failure: the gate fails first.
- **Negative:** Every new error kind is three edits (domain, HTTP mapping, SPA code plus both locales).
- **Negative:** Error notes stay on screen until dismissed, which costs the user a click.

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails when:
- a non-test file in `backend/internal/adapter/github/` calls `errors.New`, or `fmt.Errorf` without `%w` (wrap an `entity.Err*` kind instead);
- an error `code` the API sends (`statusAndCode` and `"code"` literals in `backend/internal/adapter/http/`) is missing from `KNOWN_CODES` in `frontend/src/app/domain/models/action-error.models.ts`;
- a code in `KNOWN_CODES` has no `actions.error.<code>` key in `frontend/public/i18n/pt-BR.json` or `en-US.json`;
- a non-spec gateway in `frontend/src/app/infrastructure/http/` throws a plain `Error` (throw an `ActionError` with the server's code).
