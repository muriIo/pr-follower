# ADR-0004: Forward-Only `pressly/goose/v3` Migrations and `sqlc` Persistence

## Status
Accepted (2026-11-09)

## Context
Database schema management and persistence must be predictable, type-safe at compile time, and safe for continuous deployment. Runtime reflection ORMs (`gorm.io/gorm`) obscure SQL queries and auto-migrations can cause unexpected production lock contention. Furthermore, destructive rollback (`Down`) migrations in shared environments risk irreversible data loss or drift.

## Decision
1. **Schema Migrations (`pressly/goose/v3`):**
   - SQL migrations live in `backend/internal/infrastructure/database/migrations/` and are embedded via `//go:embed migrations/*.sql`.
   - **Strictly Forward-Only (`-- +goose Up` only):** We **NEVER** write or execute `-- +goose Down` sections in any migration file. If a schema change needs to be altered or reverted, a **new numbered forward migration** must be added.
2. **Type-Safe SQL Code Generation (`sqlc` + `pgx/v5/stdlib`):**
   - Queries are authored in `backend/internal/adapter/persistence/queries/queries.sql` and compiled via `backend/sqlc.yaml` into `backend/internal/adapter/persistence/sqlcgen/`.
   - Reflection-heavy ORMs (`gorm.io/gorm`) are forbidden.

## Consequences
- **Positive:** Compile-time verification of SQL queries and Go types with zero runtime reflection overhead.
- **Positive:** Forward-only migrations guarantee a linear, auditable schema history and eliminate destructive rollback accidents.
- **Negative:** Schema or query changes require updating `.sql` files and regenerating `sqlcgen`.

## Compliance & Automated Gate
- `database.ValidateForwardOnlyMigrations` checks all embedded `.sql` files at runtime and returns `ErrDownMigrationForbidden` if `+goose Down` appears anywhere.
- `adrcheck.ValidateRepository` and `make backend-migration-check` verify that `backend/sqlc.yaml` exists, `gorm.io/gorm` is absent from `backend/go.mod`, and every `.sql` migration file in `backend/internal/infrastructure/database/migrations/` contains `-- +goose Up` and never `+goose Down`.
