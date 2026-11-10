# ADR-0011: Idempotent Integrations

## Status
Accepted (2026-11-10)

## Context
Every integration PR Follower has is delivered **at least once**, never exactly once:
- **GitHub webhooks** are redelivered on timeouts and from **Recent Deliveries**, and can arrive out of order.
- **Our own retries** (ADR-0006) repeat a request whenever the response is lost, even if the server already acted on it.
- **Users** double-click, retry after a network blip, or reopen a tab that replays a request.
- **Live events** (SSE) are replayed to reconnecting clients.

Without a shared rule each integration reinvents this, and the failure modes are quiet: a duplicated approval review on GitHub, a duplicated `@dependabot` comment, a timeline that repeats one event per redelivery. The installation webhooks (ADR-0010) set the pattern this ADR generalises.

## Decision
An operation is **idempotent** when repeating it leaves the same state and the same visible side effects as running it once. Every integration is designed so that repetition is harmless:

1. **Converge to state; never toggle or increment.** Handlers set the target state derived from the input ("this repository is `removed`", "this PR is `closed` at `head_sha` X"), never "flip" or "add one". Applying the same input twice is then a no-op by construction.
2. **Side effects only on a real change.** Events, notifications and outbound calls fire only when persisted state actually changed (`entity.Repository.ApplyAccess` returning `changed` is the reference shape). A redelivered webhook therefore changes nothing and publishes nothing.
3. **Natural keys and upserts.** Rows that mirror external objects are keyed by the provider's stable id (`github_id`, `github_repo_id`, `github_pr_id`, `(pull_request_id, head_sha)`), and writes are `INSERT … ON CONFLICT … DO UPDATE / DO NOTHING`. A plain `INSERT` is allowed only when its key cannot collide (e.g. a random 256-bit session token) and the query says so with an `-- idempotent:` note.
4. **Schema statements are re-runnable.** `CREATE TABLE` / `CREATE INDEX` always use `IF NOT EXISTS` (with ADR-0004's forward-only migrations).
5. **Retry only what is safe to repeat.** `GET`, `PUT`, `DELETE` and our own upserts may be retried with `resilience.Retry` (ADR-0006). A **non-idempotent call** (`POST`, `PATCH`: creating a review, posting a comment) uses `resilience.RetryNonIdempotent`, which retries only when the server explicitly said the request was not processed (`Retry-After`). Any other failure is an *ambiguous outcome* and is returned to the caller instead of being repeated blindly. Single-use exchanges (OAuth code → token) are never retried.
6. **Check before acting on the outside world.** When a user action maps to a non-idempotent provider call, first read the current state and skip the call if the target state already holds (do not approve twice, do not merge a merged PR).
7. **Consumers tolerate duplicates.** The SPA treats events as hints: reloading the workspace or re-rendering a timeline entry twice is harmless.
8. **Prove it with a redelivery test.** Every webhook ingest method has a test that delivers the same payload twice and asserts the state and the published events of a single delivery.

## Consequences
- **Positive:** GitHub redeliveries, lost responses and double clicks cannot duplicate reviews, comments, rows or timeline entries.
- **Positive:** New integrations have a checklist (state, change detection, natural key, retry class, redelivery test) instead of case-by-case reasoning.
- **Negative:** Handlers need a read before the write to detect change; non-idempotent calls surface ambiguous failures to the user ("could not confirm, check GitHub") instead of silently retrying.
- **Negative:** Ordering is not solved by idempotency alone. When a payload carries a version (`updated_at`, `head_sha`), a handler must not let an older delivery overwrite newer state.

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails when:
- an `INSERT` in `backend/internal/adapter/persistence/queries/queries.sql` has neither `ON CONFLICT` nor an `-- idempotent:` note in its query block;
- a migration has a `CREATE TABLE` or `CREATE INDEX` without `IF NOT EXISTS`;
- a function literal passed to `resilience.Retry` in `backend/internal/adapter/` builds an `http.MethodPost` or `http.MethodPatch` request (use `resilience.RetryNonIdempotent`);
- an exported `Ingest*` method in `backend/internal/usecase/webhook/` is not called by a test whose name contains `Redeliver`.
