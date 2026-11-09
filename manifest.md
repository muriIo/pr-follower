# Problems

1º I use my main github account for work. And many repositories (where other developers are working) have the "CODEOWNERS" file. I'm receiving these notifications in my e-mail and that is hard to follow: status, comments, approvals, etc.

2º We have dependabot for most of our repositories meaning that we get many dependabot PR generated making is hard for our developers to do these dependencies management. Our team lack culture, expertise and time to handle these kinds of PR, so they normally are forgotten for weeks.

# Objective

Would be nice to have one place to remedy those problems. A clean, minimal and straightforward (ALWAYS avoiding mental overload) dashboard that can be divided by repo, by issues, by PR, by timeline, by activities, etc. Live updates are mandatory.

UX decisions that I'm enjoying these days are the ones applied to apps like [Hive](https://github.com/arvoreeducacao/hive). A sidebar for controlling view and agents division in the main area. Maybe we could adapt that for our usecase.

Users could login via github and select which repositories they want to follow.

---

# Validated Product & Architecture Specification

## 1. Deployment & Stack
- **Deployment Model:** Team-shared Web Application (enables shared triage state, PR claiming/hand-offs, and native GitHub Webhook ingestion).
- **Architectural Pattern:** **Clean Architecture** enforced across both Backend (`domain` $\leftarrow$ `usecase` $\leftarrow$ `adapter` $\leftarrow$ `infrastructure`) and Frontend (`domain` $\leftarrow$ `application` $\leftarrow$ `infrastructure` / `presentation`). Inner layers have zero dependencies on outer frameworks, ORMs, or transport protocols.
- **Frontend:** **Angular v22** (Standalone Components, Signals for reactive real-time state, minimal/low-cognitive-load UI inspired by Hive, preceded by a dedicated **Design Generation** step using Claude Design / UI generation prompts).
- **Backend:** **Go v1.27** + **Chi (`go-chi/chi/v5`)** (100% `net/http` standard router with native `r.Context()` cancellation, SSE streaming via `http.ResponseController`, high-concurrency webhook processor, and in-memory repository tarball & lockfile analyzer).
- **Database & Migrations:** **PostgreSQL** (via Docker Compose) + **`pressly/goose/v3`** (strictly forward-only SQL migrations — **NEVER** use `Down` migrations; always add a new forward migration) + **`sqlc`** (`https://sqlc.dev/`) for type-safe SQL code generation isolated in the persistence adapter layer.
- **GitHub Integration:** **GitHub App** (OAuth user login + Installation token for repository read/write actions + real-time GitHub Webhooks).
- **Tooling, AI & Docs Standard:** Always target the latest versions (**Go v1.27**, **Angular v22**), use the **Angular CLI MCP Server** (`ng mcp` — https://angular.dev/ai/mcp) for Angular workspace intelligence and best practices, and verify up-to-date library documentation via **Context7**.

## 2. Core Functional Pillars

### Pillar A: CODEOWNERS Noise Reduction & Turn-Based Triage
- **My Owned Files Only:** Parses each repository's `CODEOWNERS` file and matches against the PR's changed files. Displays *only* the files the current user (or their team) actually owns, with an inline diff preview for just those owned files.
- **Teammate Already Approved:** Automatically dims/mutes PRs where another member of the same `CODEOWNERS` group has already approved the current revision.
- **Ready-for-Review Gate:** Automatically hides or dims PRs that are Drafts, have failing CI checks, or have merge conflicts until they become actionable.
- **Turn-Based State Machine ("Waiting on Me" vs. "Waiting on Author"):** Once a user comments or requests changes, the PR moves out of their active queue until the author pushes new commits, replies, or re-requests review.
- **Mute / Snooze & Teammate Hand-off:** Users can snooze noisy PRs (until next commit or green CI) or claim/assign a PR to a teammate within the dashboard.

### Pillar B: Dependabot Intelligence & Friendly Dependency Analysis
Designed with a pluggable `DependencyAnalyzer` interface in Go (starting with **Node.js / TypeScript**: `package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, extensible to Go, Python, Ruby, etc.):
- **1-Glance Risk Verdict Card:** Traffic-light summary badge at the top of every Dependabot PR (e.g., `🟢 Low Risk: Transitive devDependency, 0 direct imports, CI Green` vs. `🔴 High Risk: Direct prod dependency, Major bump, imported in 4 files`).
- **Direct vs. Transitive Depth & Visual Graph:** Interactive flowchart showing `Repository -> Parent Package -> Bumped Package` rather than raw lockfile diffs.
- **Prod vs. Dev Scope:** Clearly distinguishes `dependencies` (runtime) vs. `devDependencies` (build/test-only).
- **Blast Radius (Actual Code Usage):** Scans the repository tree in-memory (cached by commit SHA) for `import` / `require` statements referencing the bumped package and displays collapsible inline code snippets of exact usages.
- **CVE & Breaking Change Digest:** Highlights GitHub Security Advisories (CVEs) fixed by the bump and extracts breaking-change notes from release notes/changelogs.

### Pillar C: Hive-Inspired Workspace UX & Direct Actions
- **Curated Sidebar Perspectives + Hive "Seats" Grid:**
  - **My Turn Inbox:** Focused CODEOWNERS & review queue filtered to PRs genuinely waiting on the user.
  - **Dependabot Radar:** Grouped by risk verdict or package across all followed repositories for rapid batch triage.
  - **Repo "Seats" Grid:** Hive-inspired tiled canvas where each followed repository gets its own live "Seat" card showing active PRs, CI pulse, and Dependabot health side-by-side.
  - **Live Activity Timeline:** Real-time stream of reviews, CI transitions, and merges across followed repos.
- **In-App Actions (Zero Tab-Switching):**
  - One-click **Approve**, **Merge / Squash**, or comment commands (`@dependabot rebase`, `@dependabot recreate`).
  - Inline diff preview of owned files + quick jump link to GitHub when full-repo review is needed.
- **Real-Time Live Updates:**
  - GitHub App Webhooks (`pull_request`, `pull_request_review`, `check_suite`, `check_run`, `issue_comment`) $\rightarrow$ Go Backend $\rightarrow$ Server-Sent Events (SSE) $\rightarrow$ Angular Signals store.
- **Resilience, Anti-Spam & Exponential Backoff with Jitter:**
  - **External Integrations (GitHub API & Tarball Fetching):** Full-jitter exponential backoff (`random(0, min(maxDelay, base * 2^attempt))`), honoring `Retry-After` / `X-RateLimit-Reset` headers, combined with in-flight request deduplication (`singleflight`) and bounded worker pools to prevent rate-limit exhaustion or API spamming.
  - **Internal Services (PostgreSQL / GORM & SSE):** Exponential backoff with jitter for DB startup/transient retries, strict SQL connection pool bounds (`MaxOpenConns`, `MaxIdleConns`), and jittered exponential reconnection on Frontend SSE streams and transient HTTP retries to prevent thundering-herd spikes.
- **End-to-End Action Cancellation (`AbortSignal` $\rightarrow$ Go `context.Context`):**
  - **Frontend (`AbortController` / `AbortSignal`):** Every time-consuming user action (Approve/Merge, Batch Dependabot Merge, Repo Sync, Deep Analysis) exposes a cancel button wired to an `AbortController` (`AbortSignal`) and sends an `X-Operation-ID` header.
  - **Backend (`r.Context()` + `OperationRegistry` with `context.WithCancelCause`):** Propagates `context.Context` across all Clean Architecture layers (HTTP $\rightarrow$ UseCase $\rightarrow$ GORM `db.WithContext(ctx)`, GitHub HTTP calls, tarball stream reader, and backoff sleeps) AND tracks active operations in an `OperationRegistry` so aborting an HTTP request or calling `DELETE /api/v1/operations/{id}` immediately halts in-flight work in Go.

## 3. Mandatory Engineering & Quality Gates (Strict TDD)
- **Strict Red-Green-Refactor TDD:**
  1. **RED First:** Always write or update the test *before* touching production code. Run the test suite to prove it fails for the expected reason.
  2. **GREEN Second:** Write the minimal production code required to make the test pass, then re-run the test suite to prove it passes.
  3. **REFACTOR Third:** Clean up duplication and improve structure while keeping all tests green.
- **Zero Untested Changes:** If even a single line of code is added, modified, or removed, a corresponding test must be written or updated first.
- **Automated Code Quality Gates & Best Practices:**
  - **Code & Commits Language:** All source code, identifiers, comments, tests, documentation, and Git commit messages MUST be written in **English**.
  - **UI Language & Internationalization (i18n):** The application UI defaults to **PT-BR (Brazilian Portuguese)** for now, built on a structured **i18n translation architecture** (`assets/i18n/pt-BR.json` + reactive i18n service/pipe) so additional locales (`en-US`, `es-ES`, etc.) can be added effortlessly without touching components.
  - **Backend (Go v1.27 Best Practices):** Idiomatic Go (Effective Go, explicit error wrapping, context propagation, small consumer-defined interfaces), verified via `gofmt -s`, `go vet ./...`, `golangci-lint run`, and `go test -race -cover ./...`.
  - **Frontend (Angular v22 & TypeScript Best Practices):** Strict TypeScript (`strict: true`, `strictTemplates: true`), Standalone Components, `OnPush` / Signals reactivity, `inject()`, ESLint (`ng lint`), and unit/component tests for every signal store, service, and component.
