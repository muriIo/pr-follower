# ADR-0016: Migrations Take a Postgres Advisory Lock

## Status
Accepted (2026-11-19). Builds on ADR-0004 (forward-only goose migrations) and ADR-0012 (any replica serves any request).

## Context
Every API process applies the pending migrations when it starts (ADR-0004). The migrator called goose's package-level `goose.UpContext`, which takes no lock across processes; a `sync.Mutex` only serialised goroutines inside one process. Two replicas starting together against a database with pending migrations both read `goose_db_version`, both saw the same versions as pending, and both ran them:
- `IF NOT EXISTS` (ADR-0011) does not make concurrent DDL safe: two transactions creating the same table can still fail on `pg_type_typname_nsp_index`, so one replica crash-loops on its first start;
- a data migration (`UPDATE`/`INSERT`) would run twice;
- `goose_db_version` has no unique constraint on `version_id`, so a version can be recorded twice.

The runbook told operators to "let one replica start first", while `deploy/kubernetes/api.yaml` shipped `replicas: 2`. The first company deployment found the contradiction and ran a single replica with `strategy: Recreate`, which costs downtime on every deploy and does nothing for Docker Compose `--scale`, ECS with several tasks, or an autoscaler.

## Decision
1. **The migrator holds a Postgres session-level advisory lock** (`pg_try_advisory_lock`, id `database.MigrationLockID`) while it migrates, through goose's provider API: `goose.NewProvider(…, goose.WithSessionLocker(lock.NewPostgresSessionLocker(…)))`. The first process takes the lock and migrates; the others wait, then find nothing pending.
2. **The lock belongs to a connection**, so a crashed replica cannot leave it held: Postgres releases it when the session ends.
3. **Waiting is bounded and cancellable** (ADR-0007): the locker retries every second for up to 5 minutes, and gives up as soon as the start-up context ends. Probes must leave a waiting replica alone for that long (`startupProbe` in `deploy/kubernetes/api.yaml`).
4. **SQLite (tests only) uses the same provider without a locker**: SQLite has no advisory locks and only one process uses an in-memory database.
5. **goose's package-level migration API is not used** in `backend/internal/`: it shares global state and takes no lock.

## Consequences
- **Positive:** any number of replicas can start together on any platform; the runbook's "start one replica first" step is gone, and `api.yaml` keeps `replicas: 2` with a rolling update.
- **Positive:** the migrator no longer mutates goose globals (`SetBaseFS`, `SetDialect`), so the process-local mutex is gone too.
- **Negative:** a replica waits up to 5 minutes for another's migration; a migration that runs longer makes the waiting replica fail its start and be restarted by the platform, which then finds nothing pending.
- **Negative:** the lock holds one extra connection while it migrates; with `DB_MAX_OPEN_CONNS` of 2 or more this fits in the pool.

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails when a Go file in `backend/internal/` (outside tests) calls goose's package-level `Up*`, `Down*`, `Run*`, `Reset*` or `Redo*` functions, or creates a provider with `goose.NewProvider` without `goose.WithSessionLocker` in the same file. With `TEST_DATABASE_URL`, `internal/infrastructure/database` starts four migrators at once against a fresh database (each version applied once, all start) and checks that a migrator waits while another session holds the lock and gives up when its context ends.
