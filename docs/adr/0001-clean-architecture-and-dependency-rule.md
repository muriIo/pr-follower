# ADR-0001: Clean Architecture and Inward Dependency Rule

## Status
Accepted (2026-11-09)

## Context
PR Follower integrates multiple external mechanisms—GitHub App webhooks, REST/GraphQL endpoints, in-memory archive streaming, PostgreSQL persistence, Server-Sent Events (SSE), and an Angular v22 frontend. Without strict architectural boundaries, business rules (`CODEOWNERS` turn-based state transitions, Dependabot risk scoring, and blast-radius classification) quickly become coupled to HTTP routers, SQL drivers, or UI components, making fast deterministic unit testing impossible.

## Decision
Both the **Go v1.27 Backend** and the **Angular v22 Frontend** enforce strict **Clean Architecture** with the **Inward Dependency Rule**:

1. **Backend Layers (`backend/internal/`):**
   - `domain` (`entity`, `service`, `port`): Pure enterprise business rules and consumer-defined port interfaces. **Zero external/third-party imports** and zero imports from `usecase`, `adapter`, or `infrastructure`.
   - `usecase` (`triage`, `dependabot`, `webhook`, `action`): Application orchestration rules. May import `domain` and Go standard library packages only; **never** imports `adapter` or `infrastructure`.
   - `adapter` (`http`, `sse`, `persistence`, `github`, `analyzer`): Interface adapters converting between external formats (HTTP JSON, `sqlc` rows, GitHub payloads, lockfiles) and `domain` contracts.
   - `infrastructure` (`config`, `resilience`, `database`, `server`, `adrcheck`): Framework drivers, connection pools, migration runners, and HTTP server wiring.
2. **Frontend Layers (`frontend/src/app/`):**
   - `domain` (`models`, `gateways`): Pure TypeScript models and abstract gateway tokens. Zero Angular HTTP, Router, or UI component dependencies.
   - `application` (`stores`, `use-cases`): Signal-based state stores and use cases depending only on `domain`.
   - `infrastructure` (`http`, `sse`, `resilience`, `i18n`): Concrete gateway implementations.
   - `presentation` (`layout`, `features`, `shared`): Standalone Angular v22 UI components.

## Consequences
- **Positive:** Domain and use-case tests run in microseconds using pure in-memory fakes without database containers or network mocks.
- **Positive:** Framework or database changes (`sqlc`, `chi/v5`, GitHub API versions) are isolated to `adapter` and `infrastructure`.
- **Negative:** Requires explicit DTO-to-Entity mapping at adapter boundaries.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` parses Go AST imports across `backend/internal/domain/...` and `backend/internal/usecase/...` (and TypeScript imports in `frontend/src/app/domain/...` and `frontend/src/app/application/...`) and fails if any inward dependency boundary is violated.
- Enforced on every run of `make adr-check`, `make backend-arch-check`, and `make check`.
