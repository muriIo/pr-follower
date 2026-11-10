# PR Follower — Roadmap

What is finished and what is still open, by module. This is the single status list: tick an item (`[x]`) or add one in the same PR that changes it. Design lives in [`implementation-plan.md`](implementation-plan.md), rules in [`adr/`](adr/README.md).

Last reviewed: 2026-11-10 (after PR #14).

**Priorities** for open items:
- **P0**: wrong or unsafe today; fix before anything else.
- **P1**: core product promise not delivered yet.
- **P2**: improvement, hardening or scale.

## Next up

In order:
1. **P0** Timeline leaks other users' activity on workspace load ([Live updates](#7-live-updates--timeline)).
2. **P1** Load a repository's open PRs at import ([PR state](#3-pr-state-from-github)).
3. **P1** Compute turn state so "Minha Vez" fills with real PRs ([CODEOWNERS inbox](#4-codeowners-inbox--minha-vez)).
4. **P1** Act on GitHub as the user ([Actions](#6-actions-on-github)).
5. **P1** Run the Dependabot analysis ([Dependabot](#5-dependabot-radar--analysis)).

## 1. Identity & sessions

- [x] GitHub-only sign-in: web flow with state + PKCE, server-side sessions in an HttpOnly cookie, tokens encrypted at rest and refreshed (ADR-0010, #4).
- [x] Login screen with honest error messages and translated header (#4, #6).
- [x] Identity only from the session; no `X-User-ID`, no password columns (gated by ADR-0010).
- [ ] **P1** Expired session in the SPA: a 401 from any API call should return to the sign-in screen (today only `/auth/me` is checked at start-up).
- [ ] **P2** Purge expired sessions (rows are only deleted when an expired token is used again).

## 2. Repositories & GitHub App installation

- [x] Vercel-style import: accounts and repositories read with the user's token, import verified against GitHub, unfollow, "install / adjust permissions" links, return from the Setup URL (#7).
- [x] Installation webhooks keep access in sync: removed / suspended / restored, flagged in Seats with "Corrigir acesso" (#8).
- [x] CODEOWNERS read at import: present / absent / unknown, never guessed (#12).
- [ ] **P1** CODEOWNERS freshness: refresh on `push` to the default branch when a CODEOWNERS path changes (today only at import).

## 3. PR state from GitHub

- [x] `pull_request` webhooks ingested idempotently: redelivery changes nothing, CI kept on the same commit (ADR-0011, #8).
- [x] CI from `check_run` webhooks: running / passing / failing, `NONE` when nothing was reported (#12).
- [x] GitHub's `mergeable_state` stored and used as the only source of merge blockers (#12, #13).
- [ ] **P1** Backfill on import: list the repository's open PRs (and their check runs) when it is imported. Today a PR appears only after its next webhook.
- [ ] **P1** Review state: ingest `pull_request_review` (approved / changes requested / dismissed) so the drawer can say who reviewed.
- [ ] **P2** `mergeable_state` is often `unknown` in the opening webhook and GitHub sends no event when it is computed: fetch the PR when it stays unknown.
- [ ] **P2** Legacy commit statuses (`status` event) are not ingested; CI that only reports statuses shows as "Sem CI".
- [ ] **P2** Out-of-order webhooks: ignore a delivery older than the stored state (ADR-0011 consequence).

## 4. CODEOWNERS inbox ("Minha Vez")

- [x] Domain rules: CODEOWNERS matcher and turn-state machine (`domain/service`).
- [x] Inbox, dimmed cards, snooze and claim persisted per user.
- [x] Honest CODEOWNERS signal: you own files / approved / other owners / no CODEOWNERS / not checked (#13).
- [ ] **P1** Turn engine is not wired: nothing evaluates a PR's changed files against CODEOWNERS when it arrives, so real PRs never reach "Minha Vez". Evaluate on `pull_request` / `check_run` / review webhooks (changed files via the user or installation token) and store each follower's turn state.

## 5. Dependabot radar & analysis

- [x] npm lockfile analyzer (dependency path, scope) and in-memory tarball blast-radius scanner (ADR-0005).
- [x] Radar by risk tier, impact drawer, safe batch merge limited to analysed low-risk bumps with green CI.
- [x] Unanalysed bumps show only what their title says, sit in "Sem análise" and are never batch-merged (#13).
- [ ] **P1** Run the analysis: `AnalyzeUseCase` is wired but never called. Trigger it on Dependabot `pull_request` webhooks (and on demand, cancellable) and store the result.
- [ ] **P1** Installation token (GitHub App ID + private key) for background reads: tarballs and lockfiles cannot be fetched without it.
- [ ] **P2** yarn / pnpm lockfiles: accepted by `CanAnalyze`, but path reconstruction only covers npm v2/v3.

## 6. Actions on GitHub

- [x] Approve, merge (squash), batch merge, `@dependabot` commands, snooze, claim: UI and API (ADR-0007 cancellable end to end).
- [x] Non-idempotent calls never repeated blindly (`RetryNonIdempotent`, ADR-0011).
- [ ] **P1** Act as the user: approve / merge / comment use the user's token (today the gateway has none, so nothing reaches GitHub). Check the current state first (skip approving an approved PR, merging a merged one) and surface ambiguous failures as "could not confirm, check GitHub".
- [ ] **P2** Remove the artificial delays in `runSync` (2.4 s) and `runBatchApprove` (0.7 s per PR) once real calls provide the latency.

## 7. Live updates & timeline

- [x] SSE stream scoped to followed repositories, one `activity` event name, heartbeat, reconnect with jittered backoff, reload after reconnect (#9).
- [x] Every live change refreshes the workspace (debounced) and the open PR's files (#10, #14).
- [ ] **P0** Timeline leak: `GET /api/v1/workspace` returns the hub's recent events for **all** users; filter them by the user's follows like the stream.
- [ ] **P2** Activity is kept in memory only: it is lost on restart and limits the API to one replica (also the operation-cancellation registry). Persist activity, and fan out across replicas.

## 8. Inspection drawer

- [x] Signals and blockers only from GitHub: CI, CODEOWNERS state, merge state (#12, #13).
- [x] Changed-files preview: every file from GitHub as a collapsible card, owned files first with "Seu", loading and error states (#14).
- [ ] **P1** Review state in the drawer (depends on review ingestion, module 3).

## 9. Frontend platform

- [x] Angular 22 standalone, Signals, modular `common` / `features` / `layout` (ADR-0009).
- [x] pt-BR / en-US i18n, with a gate on keys used in templates (ADR-0008).
- [x] No demo data in production; sample data only in test fixtures (#10).
- [ ] **P2** Browser end-to-end tests of the main flows (sign-in, import, drawer, merge).

## 10. Backend platform & operations

- [x] Go 1.27 + chi, goose forward-only migrations + sqlc, full-jitter backoff (ADR-0003, 0004, 0006).
- [x] Docker images, dev compose, production runbook (#1, #5).
- [ ] **P2** Bound concurrent GitHub requests and honour `X-RateLimit-Reset` (only `Retry-After` today).
- [ ] **P2** Continuous integration: no GitHub Actions workflow runs `make check` on PRs yet.
- [ ] **P2** Gates from the plan not in the Makefile: `golangci-lint run`, frontend coverage.

## 11. Engineering guidelines

- [x] ADR-0001 to ADR-0011, each with an executable gate (`make adr-check`).
- [x] `AGENTS.md` / `CLAUDE.md` for agents; implementation plan; production runbook; this roadmap.
