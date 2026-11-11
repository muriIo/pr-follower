# PR Follower

A team-shared dashboard that turns CODEOWNERS review requests and Dependabot PRs into one live triage workspace. Product spec: `manifest.md`. Design, data model, analysis and turn-state behaviour: `docs/implementation-plan.md`. What is done and open, by module, with priorities: `docs/roadmap.md` (read it before starting a feature; tick items off in the PR that finishes them). UI direction (Hive-inspired): `design/`.

```
backend/    Go 1.27 · chi/net/http · pgx + sqlc · goose (embedded) · SSE hub
frontend/   Angular 22 standalone · Signals · Vitest (ng test) · nginx in prod
docs/adr/   Architecture Decision Records, each enforced by an executable gate
```

The apps talk only over HTTP under `/api/v1`. The frontend calls relative `/api/...` paths: `proxy.conf.json` in dev, nginx (`frontend/nginx.conf.template`) in the image.

## ADRs are law

`docs/adr/` is the single source of truth for architecture. Read the relevant ADR before touching a layer, a migration, a use case, routing, i18n or a component; `make adr-check` (`backend/internal/infrastructure/adrcheck`) turns each one into a failing test. The rules most often hit:

- **Inward dependencies** (ADR-0001). Backend: `domain` ← `usecase` ← `adapter` / `infrastructure`; `domain` and `usecase` import only stdlib and inner layers. Frontend: `domain` ← `application` ← `infrastructure` / `presentation`; gateways are `InjectionToken`s in `domain/gateways`, bound in `app.config.ts`.
- **Test first** (ADR-0002). Write the failing test, watch it go red, then the minimal code. Every backend package under `internal/` needs a `*_test.go` (except `domain/port`, `sqlcgen`); every frontend store, gateway, use case and component gets a sibling `.spec.ts`.
- **Forward-only migrations** (ADR-0004). New numbered file in `backend/internal/infrastructure/database/migrations/` with only `-- +goose Up`. To change a shipped migration, add a new one. Queries go in `internal/adapter/persistence/queries/queries.sql`, then regenerate `sqlcgen` (never edit it by hand).
- **Zero clone** (ADR-0005). Repository contents come from the GitHub API and in-memory tarballs; `os/exec` is banned inside `backend/internal/`.
- **Retry with full-jitter backoff** (ADR-0006) via `internal/infrastructure/resilience`; wrap 400/401/404/422 in `resilience.Permanent`.
- **Cancellable end to end** (ADR-0007). Every exported use-case method takes `ctx context.Context` first and checks `ctx.Err()` before each batch item or expensive I/O. Long frontend actions carry an `AbortSignal` and an `X-Operation-ID` header.
- **English code, pt-BR UI** (ADR-0008). Identifiers, comments, tests, JSON fields and commits are English. Every visible string is a key added to **both** `frontend/public/i18n/pt-BR.json` and `en-US.json` (matching keys), rendered through `LocaleStore` / the translate pipe.
- **Idempotent integrations** (ADR-0011). Webhooks, retries and user actions are at-least-once: converge to a target state, publish events only when state changed, upsert by the provider's id, `IF NOT EXISTS` in schema, `resilience.RetryNonIdempotent` for `POST`/`PATCH`, and a `*Redeliver*` test for every webhook ingest method.
- **Angular shape** (ADR-0009). OnPush, `input()`/`output()`, `inject()`, `@if`/`@for`, Signals in `application/stores`. Components live in `presentation/common` (primitives), `features` (views) or `layout` (shell); keep each `.component.html` under 220 lines by extracting sub-components.

A new architectural decision is a new ADR (`NNNN-slug.md` with the six headings listed in `docs/adr/README.md`, added to its index table) plus its gate in `adrcheck`.

## Verify before declaring done

Run `make check` from the root (ADR gate, gofmt, vet, migration and architecture checks, race tests, frontend `tsc` and tests). It is done when it exits 0. For a narrower loop: `make backend-check` or `make frontend-check`; a single Go package: `cd backend && go test ./internal/<path>/...`.

## Running it

- Full dev stack: `make dev` (`docker-compose.local.yml`): panel :4200, API :8080, Postgres :5433. Go changes need `docker compose -f docker-compose.local.yml restart api`. `down -v` resets the database.
- Postgres only, for running the apps on the host: `make db-up`, then `cd backend && go run ./cmd/server` and `cd frontend && npm start`.
- Migrations run automatically when the server starts.
- Production images: `make docker-build`. Deploying, the production GitHub App, secrets and rotation: `docs/production-setup.md` (update it when env vars, routes or deploy shape change); the AWS mapping of it is `docs/aws-setup.md`.

## Gotchas

- `go.mod` pins Go 1.27.3; with `GOTOOLCHAIN=auto` the host Go downloads it on first use.
- `sqlc` is not installed on the host. Regenerate with `cd backend && docker run --rm -v "$PWD":/src -w /src sqlc/sqlc:1.28.0 generate`, matching the version in the `sqlcgen` headers.
- Sign-in is GitHub-only (ADR-0010). Locally it needs your own GitHub App: copy `.env.example` to `.env` and fill `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` (callback `http://localhost:4200/api/v1/auth/github/callback`). Backend tests sign in through the real callback against a fake `port.GitHubOAuth`; reuse `testServer.signIn` in `internal/adapter/http`.
- Identity comes only from the session (`currentUser(r)` behind `RequireSession`); never from a request header or body.
- Secrets go in a git-ignored `.env`; keep them out of commits, compose files and logs.
- The Angular CLI MCP server is configured in `frontend/.mcp.json`; use it for Angular API and best-practice lookups.

## Git

- Branch from `main` as `feat/<topic>`, `fix/<topic>`, `chore/<topic>`, `docs/<topic>`; changes land through a PR. Commit or push only when asked.
- Conventional Commits in English: `type(scope): description`, lowercase description, no trailing period, header up to 72 characters. Scope names the area touched: `backend`, `frontend`, `auth`, `docker`, `gate`, `adr`, `design`, … (see `git log`).
- Each commit leaves `make check` green.
