# Architecture Decision Records (ADRs)

This directory contains the **Architecture Decision Records (ADRs)** for **PR Follower**. Every significant architectural, tooling, resilience, or workflow decision is documented here and enforced automatically via executable compliance gates (`make adr-check` and `backend/internal/infrastructure/adrcheck`).

## ADR Index

| ID | Title | Status | Automated Gate |
| :--- | :--- | :--- | :--- |
| [ADR-0001](./0001-clean-architecture-and-dependency-rule.md) | Clean Architecture and Inward Dependency Rule | Accepted | `adrcheck` AST import check + `make backend-arch-check` |
| [ADR-0002](./0002-strict-tdd-and-test-first-gate.md) | Strict Red-Green-Refactor TDD and Test-First Gate | Accepted | `adrcheck` package test pairing + `make backend-test` |
| [ADR-0003](./0003-http-routing-with-chi-and-net-http.md) | HTTP Routing with `go-chi/chi/v5` and Standard `net/http` | Accepted | `adrcheck` `go.mod` framework verification |
| [ADR-0004](./0004-forward-only-goose-migrations-and-sqlc.md) | Forward-Only `pressly/goose/v3` Migrations and `sqlc` Persistence | Accepted | `adrcheck` migration & ORM scan + `make backend-migration-check` |
| [ADR-0005](./0005-zero-clone-in-memory-tarball-analysis.md) | Zero-Clone In-Memory Repository Tarball and Lockfile Analysis | Accepted | `adrcheck` AST scan forbidding `os/exec` git clones |
| [ADR-0006](./0006-resilience-exponential-backoff-and-anti-spam.md) | Resilience, Full-Jitter Exponential Backoff, and Anti-Spam | Accepted | Unit tests in `internal/infrastructure/resilience` |
| [ADR-0007](./0007-cooperative-end-to-end-action-cancellation.md) | End-to-End Cooperative Action Cancellation (`AbortSignal` to `context.WithCancelCause`) | Accepted | `adrcheck` use-case `context.Context` signature inspection |
| [ADR-0008](./0008-english-codebase-and-pt-br-i18n-architecture.md) | English-Only Code/Commits and Multi-Locale `pt-BR` Default i18n | Accepted | `adrcheck` i18n dictionary & template checks |
| [ADR-0009](./0009-angular-v22-signals-and-mcp-architecture.md) | Angular v22 Standalone, Signals, and Angular CLI MCP Architecture | Accepted | `adrcheck` frontend layer & MCP check |
| [ADR-0010](./0010-github-only-identity-and-installation-scoped-access.md) | GitHub-Only Identity and Installation-Scoped Repository Access | Accepted | `adrcheck` session-identity & no-password scan |
| [ADR-0011](./0011-idempotent-integrations.md) | Idempotent Integrations | Accepted | `adrcheck` upsert, re-runnable schema, retry-class & redelivery-test scan |
| [ADR-0012](./0012-shared-state-across-api-replicas.md) | Shared State Across API Replicas Lives in Postgres | Accepted | `adrcheck` scan for locked in-memory state without a `replica-local:` note |
| [ADR-0013](./0013-acknowledged-and-recoverable-github-failures.md) | Acknowledged and Recoverable GitHub Failures | Accepted | `adrcheck` scan for unclassified GitHub errors and API error codes the SPA cannot show |
| [ADR-0014](./0014-static-landing-page-on-github-pages.md) | Static Landing Page on GitHub Pages | Accepted | `adrcheck` scan of `landing/` for build files, scripts and third-party requests |

## Required ADR Sections

Every ADR file in `docs/adr/` (`NNNN-slug.md`) **must** be listed in the table above and contain the following markdown headings (enforced by `adrcheck.ValidateRepository`):

1. `# ADR-NNNN: <Title>`
2. `## Status`
3. `## Context`
4. `## Decision`
5. `## Consequences`
6. `## Compliance & Automated Gate`
