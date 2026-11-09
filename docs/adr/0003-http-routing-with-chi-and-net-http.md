# ADR-0003: HTTP Routing with `go-chi/chi/v5` and Standard `net/http`

## Status
Accepted (2026-11-09)

## Context
We evaluated Go HTTP routing options (`go-chi/chi/v5`, `gin-gonic/gin`, `labstack/echo`, `gofiber/fiber`, and stdlib `net/http.ServeMux`) against three critical PR Follower requirements:
1. Clean Architecture isolation (no framework context leakage into adapters or use cases).
2. Native `r.Context()` cancellation propagation (`AbortSignal` $\rightarrow$ HTTP request cancellation $\rightarrow$ `context.WithCancelCause`).
3. Long-lived Server-Sent Events (`GET /api/v1/events`) with per-response write deadline control via `http.ResponseController`.

## Decision
We use **`go-chi/chi/v5`** on top of Go's standard `net/http` library:
- Handlers use standard `http.HandlerFunc(w http.ResponseWriter, r *http.Request)` signatures.
- Route groups and middleware (`chi.NewRouter()`, `r.Route(...)`) provide clean modular composition.
- Framework-specific context wrappers (`*gin.Context`, `echo.Context`, `*fiber.Ctx`) are strictly forbidden.

## Consequences
- **Positive:** 100% compatibility with standard `net/http`, `httptest`, `http.ResponseController`, and native `r.Context()` cancellation.
- **Positive:** Zero external transitive dependencies from the router (`chi/v5` has zero external dependencies).
- **Negative:** JSON decoding/encoding and error responses are written explicitly using lightweight helper functions rather than framework magic.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` inspects `backend/go.mod` to confirm `github.com/go-chi/chi/v5` is used and rejects forbidden web frameworks (`github.com/gin-gonic/gin`, `github.com/labstack/echo`, `github.com/gofiber/fiber`).
