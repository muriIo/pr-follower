# PR Follower — Roadmap

What is finished and what is still open, by module. This is the single status list: tick an item (`[x]`) or add one in the same PR that changes it. Design lives in [`implementation-plan.md`](implementation-plan.md), rules in [`adr/`](adr/README.md).

Last reviewed: 2026-11-11 (after CODEOWNERS freshness).

**Priorities** for open items:
- **P0**: wrong or unsafe today; fix before anything else.
- **P1**: core product promise not delivered yet.
- **P2**: improvement, hardening or scale.

## Next up

No P1 item is open. In order of value:
1. **P2** Verify the GitHub-facing paths against a real repository (installation token, analysis, acting as the user, push refresh): the open "not verified" items below.
2. **P2** Resync a followed repository on demand ([PR state](#3-pr-state-from-github)), which would also refill reviews of PRs stored before reviews existed.
3. **P2** Persist activity and fan out across replicas ([Live updates](#7-live-updates--timeline)).

## 1. Identity & sessions

- [x] GitHub-only sign-in: web flow with state + PKCE, server-side sessions in an HttpOnly cookie, tokens encrypted at rest and refreshed (ADR-0010, #4).
- [x] Login screen with honest error messages and translated header (#4, #6).
- [x] Identity only from the session; no `X-User-ID`, no password columns (gated by ADR-0010).
- [x] Expired session in the SPA: any API call refused as unauthenticated (401) makes the app ask `/auth/me`; if the server no longer knows the session it returns to the sign-in screen saying so, stopping the live stream and closing the drawer. A 401 that is not the session (GitHub refusing the app's own credentials) or a server that cannot be reached never signs anyone out.
- [x] When the signed-in user's own GitHub access is gone (token expired and not refreshable, or revoked) the server answers `github_token_expired` and the app signs out at once, clearing the server session, instead of leaving a valid session that can do nothing.
- [ ] **P2** Purge expired sessions (rows are only deleted when an expired token is used again).

## 2. Repositories & GitHub App installation

- [x] Vercel-style import: accounts and repositories read with the user's token, import verified against GitHub, unfollow, "install / adjust permissions" links, return from the Setup URL (#7).
- [x] Installation webhooks keep access in sync: removed / suspended / restored, flagged in Seats with "Corrigir acesso" (#8).
- [x] CODEOWNERS read at import: present / absent / unknown, never guessed (#12).
- [x] CODEOWNERS freshness: a `push` to the default branch that touches a CODEOWNERS path (or that GitHub truncated at 20 commits) re-reads the file as the GitHub App, with a follower's token as fallback; the cached rules and status are stored only when they changed and the change is announced, so every user's turn follows. A file that disappears is recorded as "no CODEOWNERS"; a read that cannot tell never is, and fails the delivery so it can be redelivered. Needs the app subscribed to Push events.
- [ ] **P2** Not verified against real GitHub yet: a real push that edits CODEOWNERS (the app must be subscribed to Push events), and that a very large push (more than 20 commits) refreshes.

## 3. PR state from GitHub

- [x] `pull_request` webhooks ingested idempotently: redelivery changes nothing, CI kept on the same commit (ADR-0011, #8).
- [x] CI from `check_run` webhooks: running / passing / failing, `NONE` when nothing was reported (#12).
- [x] GitHub's `mergeable_state` stored and used as the only source of merge blockers (#12, #13).
- [x] Backfill on import: the repository's open PRs (up to 1,000) and their check runs are read with the user's token before the follow is recorded, so existing PRs appear without waiting for a webhook. A failure leaves the repository unfollowed so the import can be retried; PRs already kept live by webhooks are never rewound; merge state stays `unknown` (the list does not carry it).
- [ ] **P2** Resync a followed repository on demand (re-read open PRs, closing those GitHub no longer lists as open), for PRs that changed while the app was not receiving webhooks.
- [x] Reviews ingested: `pull_request_review` webhooks (approved / changes requested / commented / dismissed) are stored by GitHub's review id, idempotently (a redelivery changes nothing, a dismissal is never revived), and the open human PRs' reviews are read at import for repositories with CODEOWNERS.
- [ ] **P2** Reviews of PRs stored before this existed (imported earlier) are missing until the repository is imported again or a new review arrives: refresh them (resync, or the installation token).
- [ ] **P2** `mergeable_state` is often `unknown` in the opening webhook and GitHub sends no event when it is computed: fetch the PR when it stays unknown.
- [ ] **P2** Legacy commit statuses (`status` event) are not ingested; CI that only reports statuses shows as "Sem CI".
- [ ] **P2** Out-of-order webhooks: ignore a delivery older than the stored state (ADR-0011 consequence).

## 4. CODEOWNERS inbox ("Minha Vez")

- [x] Domain rules: CODEOWNERS matcher and turn-state machine (`domain/service`).
- [x] Inbox, dimmed cards, snooze and claim persisted per user.
- [x] Honest CODEOWNERS signal: you own files / approved / other owners / no CODEOWNERS / not checked (#13).
- [x] Turn engine phase 1: "Minha Vez" holds only PRs that touch files the user owns. Each PR's changed files are stored per head commit (read at import, on `pull_request` webhooks with a follower's token, and as a capped fallback when a follower loads the workspace); ownership and the turn are derived on read, never stored. A PR whose files are unknown is "not evaluated", never a turn; PRs the user does not own show only in Seats. Before this, every open PR was shown as the user's turn.
- [x] CODEOWNERS matcher follows GitHub's pattern rules (root anchoring, `**`, `dir/*`, trailing slash), checked against GitHub's documented examples.
- [x] Turn engine phase 2: "approved by me", "teammate approved" and "waiting on author" are derived on read from the stored reviews, per head commit: each reviewer's latest decision stands, comments never undo one, the author's own approval counts for nobody, and a new commit makes every older review stale, so the PR returns to "Minha Vez".
- [x] Webhook-time file reads use the GitHub App's installation token first, so they no longer depend on a follower being signed in; a follower's token is only the fallback (app not configured, uninstalled or rejected).
- [ ] **P2** Team ownership uses the teams read at sign-in; refresh them periodically.

## 5. Dependabot radar & analysis

- [x] npm lockfile analyzer (dependency path, scope) and in-memory tarball blast-radius scanner (ADR-0005).
- [x] Radar by risk tier, impact drawer, safe batch merge limited to analysed low-risk bumps with green CI.
- [x] Unanalysed bumps show only what their title says, sit in "Sem análise" and are never batch-merged (#13).
- [x] The analysis runs: a Dependabot `pull_request` webhook that changes an open PR, an import that finds one, or a click on "Analisar agora" (cancellable) schedules `AnalyzePR` on a bounded background runner (2 workers, 256 queued, 2 minute timeout per PR, stops with the server). A PR scheduled twice waits once, and one that gets a new commit mid-analysis is analysed again afterwards. The result is stored per commit and converges (concurrent requests share one run and one event). It reads as the GitHub App, so it only exists with the app credentials, and only followers of the repository can ask for it.
- [x] The analysis refuses what it cannot read honestly: a grouped update ("bump the X group with 3 updates", "bump a and b") or a non-npm PR stays "Sem análise", with its own explanation (`not_analysable`) and no button, instead of being analysed as package "unknown" or answered "invalid request", and the stored verdict no longer bakes in the CI of the moment (CI gates the batch merge live).
- [ ] **P2** Failed analyses are only logged, and PRs stored before this shipped are analysed on their next webhook, a re-import or a click: resync them on start, and show "analysis failed" instead of silence.
- [ ] **P2** Grouped Dependabot updates (several packages in one PR) are not analysed.
- [x] Installation token: the server signs an app JWT (`GITHUB_APP_ID` + `GITHUB_APP_PRIVATE_KEY` or `_FILE`) and exchanges it for a per-installation token, cached and renewed five minutes before expiry, one exchange shared by concurrent callers. The gateway's reads (file contents, PR files, tarballs) use it; approve, merge and comment never do, so nothing acts as the app. Without credentials the server warns and falls back to followers' tokens; invalid credentials stop it at start.
- [ ] **P2** Not verified against real GitHub yet: exchange a token with the dev app's key and read a PR's files through it (see the production runbook).
- [ ] **P2** yarn / pnpm lockfiles: accepted by `CanAnalyze`, but path reconstruction only covers npm v2/v3.

## 6. Actions on GitHub

- [x] Approve, merge (squash), batch merge, `@dependabot` commands, snooze, claim: UI and API (ADR-0007 cancellable end to end).
- [x] Non-idempotent calls never repeated blindly (`RetryNonIdempotent`, ADR-0011).
- [x] Act as the user: approve, merge and `@dependabot` comments are written with the signed-in user's own token (never the app's), after checking what GitHub says now. Nobody approves their own PR (refused before anything is sent, also against GitHub's author; the UI does not offer it; merging your own PR is allowed); a closed PR is not approved or merged; an approval already on the head commit is not repeated and a merged PR converges to success; a batch containing the user's own PR is refused as a whole before any write.
- [x] Ambiguous outcomes are never guessed: a timeout, dropped connection or 5xx on approve or merge is checked against GitHub (the review or the merge is there, so it succeeded; it is not, so the user is told "could not confirm, check GitHub" and nothing is recorded or announced). GitHub's refusals reach the user with its own explanation (403, 404, 405/409/422).
- [x] "Approved by me" is the review GitHub recorded for the head commit (stored from the approve response and from webhooks), no longer a flag set in the app; a new commit makes it stale, and every Radar item reports it (`approved_by_me`, plus the teammate who approved) so a reload after an action keeps it.
- [x] The SPA shows action failures in the user's language instead of swallowing them (a 502 used to look like success), sends each write once, and no longer invents a merge commit.
- [x] Artificial delays removed from every action: they finish when the server answers.
- [ ] **P2** Not verified against real GitHub yet: the Dependabot analysis of a real PR (lockfile and tarball reads as the app), and approve, merge and comment with a real user token, and the GitHub app's permission to write pull requests for the user (see the production runbook).

## 7. Live updates & timeline

- [x] SSE stream scoped to followed repositories, one `activity` event name, heartbeat, reconnect with jittered backoff, reload after reconnect (#9).
- [x] Every live change refreshes the workspace (debounced) and the open PR's files (#10, #14).
- [x] Timeline scoped to followed repositories on workspace load too (the P0 leak), newest first.
- [x] Timeline views: all activity or only mine (who acted: the webhook sender, the user behind an app action), filtered by a repository of the Seats; one icon per activity type (merge, approval, open, close, commit, CI, claim, access...).
- [x] Merged PRs are reported as merged (`pr.merged`, state `MERGED`), not as closed.
- [ ] **P2** Activity is kept in memory only: it is lost on restart and limits the API to one replica (also the operation-cancellation registry). Persist activity, and fan out across replicas.

## 8. Inspection drawer

- [x] Signals and blockers only from GitHub: CI, CODEOWNERS state, merge state (#12, #13).
- [x] Changed-files preview: every file from GitHub as a collapsible card, owned files first with "Seu", loading and error states (#14).
- [x] Review state in the drawer: a "Revisões" section lists each reviewer's latest decision (approved, requested changes, commented, dismissed), current ones first and reviews of an older commit marked, from the stored reviews. The author's own comments are not reviews; it shows for every PR, whoever owns the files.

## 9. Frontend platform

- [x] Angular 22 standalone, Signals, modular `common` / `features` / `layout` (ADR-0009).
- [x] pt-BR / en-US i18n, with a gate on keys used in templates (ADR-0008).
- [x] No hardcoded display text: store, mapper and templates render keys (with params and plurals); the ADR-0008 gate fails on literal text.
- [x] Timeline follows the locale: activity carries its type and params (title, method, package...), never a sentence; the client phrases it.
- [x] No demo data in production; sample data only in test fixtures (#10).
- [x] A PR is identified in the SPA by its unique id; GitHub's number is only for display and GitHub URLs. The SPA used to key PRs by number and send it to routes that take the database id, so actions could reach another PR (and numbers repeat across repositories); the activity timeline now points at PRs by id too.
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
