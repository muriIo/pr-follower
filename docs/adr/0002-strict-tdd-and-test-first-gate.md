# ADR-0002: Strict Red-Green-Refactor TDD and Test-First Gate

## Status
Accepted (2026-11-09)

## Context
Regressions in PR triage rules, `CODEOWNERS` glob matching, or Dependabot risk scoring erode developer trust—the exact problem PR Follower is designed to solve. Writing tests after implementation often leads to untestable interfaces and blind spots in edge cases.

## Decision
We enforce **Strict Test-Driven Development (TDD — "Always Test First")** across the entire monorepo:

1. **Red-Green-Refactor Cycle:**
   - **RED:** Before adding, modifying, or removing a single line of production code, write or update a failing automated test (`*_test.go` or `*.spec.ts`) and run the test suite to observe the failure.
   - **GREEN:** Write the minimal production code required to make the failing test pass.
   - **REFACTOR:** Clean up duplication and run the full quality gate (`make check`).
2. **Mandatory Package Test Pairing:**
   - Every non-generated production package in `backend/internal/...` (excluding pure interface definition packages like `domain/port` and generated `sqlcgen`) **must** contain automated unit tests (`*_test.go`).
   - Every non-trivial frontend service, store, gateway, and component in `frontend/src/app/...` **must** have a paired `.spec.ts` test file.

## Consequences
- **Positive:** Guarantees testable interfaces, high confidence when refactoring, and living documentation of every business rule.
- **Negative:** Requires discipline to write test harnesses before implementation code.

## Compliance & Automated Gate
- `adrcheck.ValidateRepository` scans all packages under `backend/internal/...` (and `frontend/src/app/...` when present) and fails the build if any production package lacks `*_test.go` / `*.spec.ts` test files.
- `make backend-test` runs `go test -v -race -coverprofile=coverage.out ./...`.
