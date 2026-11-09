# ADR-0006: Resilience, Full-Jitter Exponential Backoff, and Anti-Spam

## Status
Accepted (2026-11-09)

## Context
Webhook bursts (e.g., 30 simultaneous Dependabot PRs opened across followed repositories) or transient network/database hiccups can trigger thundering-herd retry storms against GitHub's API or PostgreSQL.

## Decision
All external and internal service integrations must use **Full-Jitter Exponential Backoff** and **In-Flight Request Deduplication**:
1. **Full-Jitter Formula (`backend/internal/infrastructure/resilience`):**
   $$\text{sleep} = \text{random}\left(0, \min\left(\text{maxDelay}, \text{baseDelay} \times 2^{\text{attempt}}\right)\right)$$
2. **Error Classification:**
   - Permanent errors (`400`, `401`, `404`, `422`) wrapped with `resilience.Permanent(err)` abort immediately without retrying.
   - Rate-limit responses with `Retry-After` headers use `resilience.WithRetryAfter(err, delay)` to honor server-mandated wait times.
3. **SingleFlight Deduplication:**
   - Concurrent identical requests (e.g., fetching the tarball for `(repo_id, head_sha)`) are coalesced via `resilience.SingleFlightGroup[T]`.
4. **Database Pool Bounds:**
   - PostgreSQL connections enforce explicit `MaxOpenConns`, `MaxIdleConns`, and `ConnMaxLifetime` bounds alongside jittered readiness/migration retries.

## Consequences
- **Positive:** Eliminates retry storms, honors GitHub secondary rate limits, and protects PostgreSQL connection pools.
- **Negative:** Requires propagating `context.Context` and injectable clock/random sources for fast unit tests.

## Compliance & Automated Gate
- Verified by unit tests in `backend/internal/infrastructure/resilience/backoff_test.go` and `adrcheck.ValidateRepository` checking the presence of the resilience package and tests.
