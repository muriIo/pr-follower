# ADR-0005: Zero-Clone In-Memory Repository Tarball and Lockfile Analysis

## Status
Accepted (2026-11-09)

## Context
Analyzing `CODEOWNERS` rules, Node.js dependency graphs (`package.json`, `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`), and source code import blast radius across many repositories could consume massive disk I/O and introduce security/cleanup risks if the backend shelled out to `git clone` on the local filesystem.

## Decision
We enforce a **Zero Local Git Clone** architecture:
- All repository inspection uses the GitHub API and in-memory archive streaming (`archive/tar` + `compress/gzip` in `backend/internal/adapter/analyzer/blastradius`).
- Results are deduplicated in flight via `SingleFlightGroup` and cached in PostgreSQL (`dependabot_analyses` and `repositories.cached_codeowners_rules`) keyed by `(repository_id, head_sha)`.
- Executing `os/exec` to invoke `git clone` or local shell commands inside `backend/internal/` is strictly forbidden.

## Consequences
- **Positive:** Stateless backend containers with zero disk cleanup overhead, fast streaming memory footprint, and immunity to shell injection.
- **Negative:** Requires streaming tarball parsing (`archive/tar`) with per-entry `ctx.Err()` cancellation checks.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` parses Go AST imports across all packages in `backend/internal/...` and fails if `os/exec` is imported.
