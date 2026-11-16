# ADR-0012: Shared State Across API Replicas Lives in Postgres

## Status
Accepted (2026-11-16)

## Context
The API is meant to run as several replicas behind one load balancer (rolling deploys, more than one task). Any request may land on any replica, and none of them is special. Two pieces of state used to live in one replica's memory:
- **Sync jobs** (`POST/GET/DELETE /api/v1/sync`): a sync started on replica A was invisible to a progress poll or a cancel that reached replica B, and a deploy silently lost it.
- **Operation cancels** (`X-Operation-ID`, ADR-0007): `DELETE /api/v1/operations/{id}` only worked on the replica serving the operation.

Activity already solved this (`activity_events` + `LISTEN/NOTIFY`, #30). Without a rule, every new feature that keeps "a little state" in a map reintroduces the same bug, and it only shows up with two replicas.

## Decision
1. **State that a later request may need lives in Postgres.** Jobs, progress, results and flags that another request (possibly on another replica) reads or changes are rows, not maps. Example: `sync_jobs`, one row per user.
2. **Work that runs on one replica proves it is alive.** A long job records a heartbeat; a job whose heartbeat is stale is reported as interrupted (`sync_interrupted`) and may be claimed again. Claims are atomic (`INSERT … ON CONFLICT … DO UPDATE … WHERE` stale or finished), and each run carries its own id, so a run that lost its claim cannot overwrite the newer one.
3. **Signals between replicas go through Postgres `NOTIFY`.** A request that must reach whichever replica holds something process-bound (a request's cancel function, live streams) is broadcast (`port.ReplicaBroadcast`, `port.ActivitySignal`); a request that can wait for a heartbeat may set a flag in the row instead (a sync's `cancel_requested`).
4. **Memory is for what may differ per replica.** Caches, in-flight deduplication, connected streams and the handles of work running here are fine in memory, and say so: every struct holding a `sync.Mutex`/`sync.RWMutex` has a `replica-local:` note explaining why losing or not seeing that state on another replica is harmless.
5. **A user only reaches their own state.** Keys include the user id from the session (ADR-0010): a cancel names the operation of the signed-in user, never someone else's.

## Consequences
- **Positive:** a progress poll, a cancel or a second "Sincronizar" behaves the same on every replica; a deploy reports an interrupted sync instead of losing it.
- **Positive:** new in-memory state cannot appear unnoticed: the gate asks for a decision.
- **Negative:** a running sync writes a heartbeat every second, and a cancel asked on another replica takes up to one heartbeat to land.
- **Negative:** `NOTIFY` is at most once: a replica that is reconnecting misses a cancel broadcast. The operation then runs to its end, as it would have without a cancel.
- **Negative:** memory deduplication is per replica: two replicas may analyse the same Dependabot commit at once. The result converges (stored per commit, ADR-0011), but two `dependabot.analyzed` events can be published.

## Compliance & Automated Gate
`adrcheck.ValidateRepository` fails when a struct type in `backend/internal/` (outside tests and generated code) has a `sync.Mutex` or `sync.RWMutex` field and its doc comment has no `replica-local:` note. The two-replica behaviour is covered by `syncjob` tests with two managers on one store, `operation` tests with two registries on one broadcast, and the `sync_jobs` store contract, which runs on SQLite and, with `TEST_DATABASE_URL`, on Postgres.
