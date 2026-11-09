# ADR-0007: End-to-End Cooperative Action Cancellation (`AbortSignal` to `context.WithCancelCause`)

## Status
Accepted (2026-11-09)

## Context
Users executing time-consuming operations (approving/merging PRs, batch-merging Dependabot queues, or re-analyzing lockfiles and blast radius) must be able to cancel an action mid-flight without leaving orphan goroutines, wasted GitHub API calls, or unstopped batch loops running on the server.

## Decision
We implement **End-to-End Cooperative Cancellation** from the browser UI down to every Go adapter and use case:
1. **Frontend (`AbortController` & `X-Operation-ID`):**
   - Every time-consuming user action creates an `AbortController`, passes its `AbortSignal` to the HTTP gateway, and sends an `X-Operation-ID` header.
   - Clicking "Cancelar" calls `controller.abort()` and optionally invokes `DELETE /api/v1/operations/{id}`.
2. **Backend (`OperationRegistry` & `context.WithCancelCause`):**
   - HTTP handlers register `X-Operation-ID` in `operation.Registry`, creating a child context via `context.WithCancelCause(r.Context())`.
   - Every exported method on a use-case struct in `backend/internal/usecase/...` **must** accept `ctx context.Context` as its first parameter and check `ctx.Err()` before each batch item or expensive I/O step.
   - Canceled operations return `499 Client Closed Request` and emit an `operation.canceled` SSE event.

## Consequences
- **Positive:** Instant resource cleanup across HTTP, DB queries, backoff timers, and tarball streams when a user aborts an action.
- **Negative:** Every use-case and port method must thread `ctx context.Context` as its first argument.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` inspects the Go AST of every package in `backend/internal/usecase/...` and fails if any exported method on a use-case struct does not take `context.Context` as its first parameter.
