# PR Follower — Roadmap

What is finished and what is still open, by module. This is the single status list: tick an item (`[x]`) or add one in the same PR that changes it. Design lives in [`implementation-plan.md`](implementation-plan.md), rules in [`adr/`](adr/README.md).

Last reviewed: 2026-11-16 (open items checked against the code after #47 and the v1 hardening; GitHub-facing paths verified against a real repository, see [`real-github-verification.md`](real-github-verification.md)).

**Priorities** for open items:
- **P0**: wrong or unsafe today; fix before anything else.
- **P1**: core product promise not delivered yet.
- **P2**: improvement, hardening or scale.

## Next up

No P1 item is open. In order of value:
1. **P2** A periodic background resync, so a repository nobody syncs after downtime does not stay stale ([PR state](#3-pr-state-from-github)).

## 1. Identity & sessions

- [x] GitHub-only sign-in: web flow with state + PKCE, server-side sessions in an HttpOnly cookie, tokens encrypted at rest and refreshed (ADR-0010, #4).
- [x] Login screen with honest error messages and translated header (#4, #6).
- [x] Identity only from the session; no `X-User-ID`, no password columns (gated by ADR-0010).
- [x] Expired session in the SPA: any API call refused as unauthenticated (401) makes the app ask `/auth/me`; if the server no longer knows the session it returns to the sign-in screen saying so, stopping the live stream and closing the drawer. A 401 that is not the session (GitHub refusing the app's own credentials) or a server that cannot be reached never signs anyone out.
- [x] When the signed-in user's own GitHub access is gone (token expired and not refreshable, or revoked) the server answers `github_token_expired` and the app signs out at once, clearing the server session, instead of leaving a valid session that can do nothing (#25).
- [x] Expired sessions are purged every hour (and at start) by every replica; they used to be deleted only when their cookie came back, which never happens once someone stops using the app.

## 2. Repositories & GitHub App installation

- [x] Vercel-style import: accounts and repositories read with the user's token, import verified against GitHub, unfollow, "install / adjust permissions" links, return from the Setup URL (#7).
- [x] Installation webhooks keep access in sync: removed / suspended / restored, flagged in Seats with "Corrigir acesso" (#8).
- [x] CODEOWNERS read at import: present / absent / unknown, never guessed (#12).
- [x] CODEOWNERS freshness: a `push` to the default branch that touches a CODEOWNERS path (or that GitHub truncated at 20 commits) re-reads the file as the GitHub App, with a follower's token as fallback; the cached rules and status are stored only when they changed and the change is announced, so every user's turn follows. A file that disappears is recorded as "no CODEOWNERS"; a read that cannot tell never is, and fails the delivery so it can be redelivered. Needs the app subscribed to Push events (#26).
- [x] Verified against real GitHub (2026-11-16): a push that edits CODEOWNERS refreshes the rules within seconds, deleting the file records "no CODEOWNERS" and restoring it brings the rules back, and a push of 26 commits with the CODEOWNERS edit first (GitHub truncates the list) still refreshes.
- [x] Following or unfollowing a repository shows that it is under way on its button ("Importando PRs..." with a spinner, the button disabled) until GitHub answers; it used to show nothing during the backfill.
- [x] The import screen shows the server's code and GitHub's explanation (rate limit with the time it lifts, permission refused, GitHub unavailable, access expired, refused) instead of "Could not reach GitHub" for everything (ADR-0013).
- [ ] **P2** Not verified against real GitHub yet: a `push` delivery that fails without the app credentials and a follower's token, then passes on **Redeliver** (covered by the `*Redeliver*` tests only).

## 3. PR state from GitHub

- [x] `pull_request` webhooks ingested idempotently: redelivery changes nothing, CI kept on the same commit (ADR-0011, #8).
- [x] CI from `check_run` webhooks: running / passing / failing, `NONE` when nothing was reported (#12). Only the latest run of each check name counts, as on GitHub: a re-run that passes clears the earlier failure, and one that is still going is pending.
- [x] GitHub's `mergeable_state` stored and used as the only source of merge blockers (#12, #13).
- [x] Backfill on import: the repository's open PRs (up to 1,000) and their check runs are read with the user's token before the follow is recorded, so existing PRs appear without waiting for a webhook. A failure leaves the repository unfollowed so the import can be retried; PRs already kept live by webhooks are never rewound; merge state stays `unknown` (the list does not carry it) (#18).
- [x] "Sincronizar" resyncs with GitHub as a background job (`POST /sync` starts or joins it, `GET /sync` reports progress, `DELETE /sync` cancels; one per user, in memory). For every repository the user follows, one after the other, it refreshes the open PRs (title, head commit, draft, checks and statuses, reviews, files), adds ones it missed, reopens ones reopened, and closes the ones GitHub no longer has open after asking GitHub what became of each (merged or closed; a PR missing only from the capped listing stays open), announcing each close so open drawers follow. The panel polls once a second ("Sincronizando 3 de 10: acme/web..."), cancelling stops the server too, and the workspace reloads when it is done. One repository failing is reported by name and does not stop the others; the user's own GitHub access failing ends it. It used to only reload what the server already had (#29).
- [x] GitHub's rate limit is honoured, not guessed at: `Retry-After` and `X-RateLimit-Remaining: 0` + `X-RateLimit-Reset` (and the secondary-limit message). A wait up to 30s is waited out in the request; a longer one fails at once as `rate_limited` (HTTP 429, `retry_at`), and a sync stops there, names how many repositories it left for later and when to try again. nginx's `/api/` read timeout is 300s (was the 60s default) (#29).
- [ ] **P2** A sync runs only when the user asks: a repository nobody syncs after downtime stays stale until someone does (or a webhook arrives). A periodic background resync would close that.
- [x] Verified against real GitHub (2026-11-16): after the API was down, "Sincronizar" added a PR opened meanwhile, closed one merged meanwhile (announcing `pr.merged`), took a recreated Dependabot PR's new head and re-analysed it, and read a commit status that no webhook ever delivered. GitHub sends the `X-RateLimit-Remaining` / `X-RateLimit-Reset` headers the client reads.
- [ ] **P2** Not verified against real GitHub yet: cancelling a sync midway (with two repositories it ends in about two seconds) and a real `429 rate_limited`.
- [ ] **P2** A PR opened and merged (or closed) while the API was down leaves no trace: the sync lists only open PRs, so it never appears in Seats or the timeline.
- [ ] **P2** A sync reads repositories one by one and every open PR in full: conditional requests (ETag), skipping PRs whose head did not change, and a little parallelism across repositories would cut its time and its share of the 5,000 requests/hour per user.
- [x] Reviews ingested: `pull_request_review` webhooks (approved / changes requested / commented / dismissed) are stored by GitHub's review id, idempotently (a redelivery changes nothing, a dismissal is never revived), and the open human PRs' reviews are read at import for repositories with CODEOWNERS.
- [x] Reviews of PRs stored before reviews were ingested are filled in by "Sincronizar" (or a re-import), which re-reads the reviews of every open human PR of a repository with CODEOWNERS (#29).
- [x] `mergeable_state` is often `unknown` in the opening webhook (and after an import or a new commit) and GitHub sends no event when it is computed: a workspace load asks GitHub, with the reader's token, for up to 10 open PRs still unknown and stores an answer for the same head commit. GitHub still computing, failing, or the reader having no token leaves it unknown and never fails the load. Seen on 2026-11-16: a PR stayed `UNKNOWN` in the panel while GitHub said `unstable`.
- [ ] **P2** Not verified against real GitHub yet: an `UNKNOWN` merge state turning into GitHub's value on the next workspace load.
- [x] Legacy commit statuses (Jenkins, Vercel) count like checks: the `status` webhook and a commit's combined status at import are stored (own table, own id space), listed beside check runs, the latest status of each context decides, and a pending one keeps a PR from looking green. Needs the app subscribed to Status events (#28).
- [x] Statuses of PRs stored before statuses were ingested are filled in by "Sincronizar", which re-reads the check runs and statuses of every open PR's head commit (#29).
- [x] Verified against real GitHub (2026-11-16) with statuses created through the API: pending, success and failure arrive by the `status` webhook within seconds, a pending one disables approve and merge, and a status created while no webhook could arrive is read by the sync.
- [ ] **P2** Not verified against real GitHub yet: a real Vercel or Jenkins status.
- [x] Out-of-order `pull_request` webhooks: GitHub's `pull_request.updated_at` of the newest delivery applied is kept per PR (`pull_request_github_versions`, migration 00012, one conditional upsert so replicas cannot race), and an older delivery changes and publishes nothing: a late `synchronize` can no longer reopen or move a PR merged after it. Same-second deliveries both apply. Check runs, statuses and reviews were already ordered by GitHub's ids.

## 4. CODEOWNERS inbox ("Minha Vez")

- [x] Domain rules: CODEOWNERS matcher and turn-state machine (`domain/service`).
- [x] Inbox, dimmed cards, snooze and claim persisted per user.
- [x] Honest CODEOWNERS signal: you own files / approved / other owners / no CODEOWNERS / not checked (#13).
- [x] Turn engine phase 1: "Minha Vez" holds only PRs that touch files the user owns. Each PR's changed files are stored per head commit (read at import, on `pull_request` webhooks, and as a capped fallback when a follower loads the workspace); ownership and the turn are derived on read, never stored. A PR whose files are unknown is "not evaluated", never a turn; PRs the user does not own show only in Seats. Before this, every open PR was shown as the user's turn (#19).
- [x] CODEOWNERS matcher follows GitHub's pattern rules (root anchoring, `**`, `dir/*`, trailing slash), checked against GitHub's documented examples.
- [x] Turn engine phase 2: "approved by me", "teammate approved" and "waiting on author" are derived on read from the stored reviews, per head commit: each reviewer's latest decision stands, comments never undo one, the author's own approval counts for nobody, and a new commit makes every older review stale, so the PR returns to "Minha Vez" (#20).
- [x] Webhook-time file reads use the GitHub App's installation token first, so they no longer depend on a follower being signed in; a follower's token is only the fallback (app not configured, uninstalled or rejected).
- [ ] **P2** Team ownership uses the teams read at sign-in; refresh them periodically.

## 5. Dependabot radar & analysis

- [x] npm lockfile analyzer (dependency path, scope) and in-memory tarball blast-radius scanner (ADR-0005).
- [x] Radar by risk tier, impact drawer, safe batch merge limited to analysed low-risk bumps with green CI.
- [x] Unanalysed bumps show only what their title says, sit in "Sem análise" and are never batch-merged (#13).
- [x] The analysis runs: a Dependabot `pull_request` webhook that changes an open PR, an import that finds one, or a click on "Analisar agora" (cancellable) schedules `AnalyzePR` on a bounded background runner (2 workers, 256 queued, 2 minute timeout per PR, stops with the server). A PR scheduled twice waits once, and one that gets a new commit mid-analysis is analysed again afterwards. The result is stored per commit and converges (concurrent requests share one run and one event). It reads as the GitHub App, so it only exists with the app credentials, and only followers of the repository can ask for it (#23).
- [x] The analysis refuses what it cannot read honestly: a grouped update ("bump the X group with 3 updates", "bump a and b") or a non-npm PR stays "Sem análise", with its own explanation (`not_analysable`) and no button, instead of being analysed as package "unknown" or answered "invalid request", and the stored verdict no longer bakes in the CI of the moment (CI gates the batch merge live).
- [x] Dependabot PRs stored before the analysis existed are scheduled for analysis by "Sincronizar" (and a re-import), besides their next webhook or a click (#29).
- [x] Failed analyses are stored per PR commit (`dependabot_analysis_failures`, the error's kind and message) and shown: the Radar groups them as "Análise falhou", and the drawer says why (GitHub refused, not found, rate limit, timeout, unreadable lockfile, unexpected error) with "Tentar de novo", except when the reason is lasting (a lockfile no analyzer reads). A cancel (the user's, or the server stopping) is not a failure. An analysis of the commit supersedes its failure, and a new commit starts clean.
- [ ] **P2** Grouped Dependabot updates (several packages in one PR) are not analysed.
- [x] Installation token: the server signs an app JWT (`GITHUB_APP_ID` + `GITHUB_APP_PRIVATE_KEY` or `_FILE`) and exchanges it for a per-installation token, cached and renewed five minutes before expiry, one exchange shared by concurrent callers. The gateway's reads (file contents, PR files, tarballs) use it; approve, merge and comment never do, so nothing acts as the app. Without credentials the server warns and falls back to followers' tokens; invalid credentials stop it at start (#21).
- [x] Verified against real GitHub (2026-11-16): the dev app's key is exchanged for an installation token, a PR's changed files are read with it on the opening webhook, and a real Dependabot PR (lodash) is analysed at import, after an `@dependabot rebase` (webhook) and after a sync (`MEDIUM`, path `root → lodash`).
- [ ] **P2** Not verified against real GitHub yet: a grouped Dependabot update staying "Sem análise" with its own explanation.
- [ ] **P2** yarn / pnpm lockfiles: accepted by `CanAnalyze`, but path reconstruction only covers npm v2/v3.

## 6. Actions on GitHub

- [x] Approve, merge (squash), batch merge, `@dependabot` commands, snooze, claim: UI and API (ADR-0007 cancellable end to end).
- [x] Non-idempotent calls never repeated blindly (`RetryNonIdempotent`, ADR-0011).
- [x] Act as the user: approve, merge and `@dependabot` comments are written with the signed-in user's own token (never the app's), after checking what GitHub says now. Nobody approves their own PR (refused before anything is sent, also against GitHub's author; the UI does not offer it; merging your own PR is allowed); a closed PR is not approved or merged; an approval already on the head commit is not repeated and a merged PR converges to success; a batch containing the user's own PR is refused as a whole before any write (#22).
- [x] Ambiguous outcomes are never guessed: a timeout, dropped connection or 5xx on approve or merge is checked against GitHub (the review or the merge is there, so it succeeded; it is not, so the user is told "could not confirm, check GitHub" and nothing is recorded or announced). GitHub's refusals reach the user with its own explanation (403, 404, 405/409/422).
- [x] Checks still running are blockers: approving and merging (and so the batch) are refused while any check run or commit status of the head commit has not finished, in the drawer (disabled, with the reason) and on the server (409 `checks_running`), which asks GitHub right now and never lets an action through on checks it could not read. Failing checks are not blockers (GitHub allows them unless the branch requires them); an approval GitHub already has, and a PR already merged, converge without being blocked.
- [x] "Approved by me" is the review GitHub recorded for the head commit (stored from the approve response and from webhooks), no longer a flag set in the app; a new commit makes it stale, and every Radar item reports it (`approved_by_me`, plus the teammate who approved) so a reload after an action keeps it.
- [x] The SPA shows action failures in the user's language instead of swallowing them (a 502 used to look like success), sends each write once, and no longer invents a merge commit.
- [x] Artificial delays removed from every action: they finish when the server answers.
- [x] Verified against real GitHub (2026-11-16): approve, squash merge and `@dependabot rebase` are written under the user's name (the comment shows "via" the app, as GitHub marks every user-to-server token); a second approve of the same head writes nothing; a running check disables approve and merge in the drawer; the merge webhook that follows the panel's own merge publishes no second `pr.merged`; the user's own PR offers merge and not approve.
- [x] Every GitHub failure is acknowledged and recoverable (ADR-0013, gated): the adapter names each GitHub answer with a domain kind (a new `github_unavailable` for a read GitHub did not answer, never `500 internal`), every API error carries a code the SPA translates, an error note stays until dismissed with the buttons back for a retry, a stopped batch says how many PRs went through and which one failed, `not_found` re-reads the workspace like `conflict` does, the drawer says why the changed files could not be loaded, and a GitHub outage during a token refresh no longer signs the user out.
- [ ] **P2** Show why GitHub blocks a merge ("needs 2 approvals, has 1", "required check `ci` failing") instead of only "blocked by branch protection": needs reading the branch's rules (`GET /repos/{owner}/{repo}/rules/branches/{branch}`) or the protection with *Administration: read*.

## 7. Live updates & timeline

- [x] SSE stream scoped to followed repositories, one `activity` event name, heartbeat, reconnect with jittered backoff, reload after reconnect (#9).
- [x] Every live change refreshes the workspace (debounced) and the open PR's files (#10, #14).
- [x] Timeline scoped to followed repositories on workspace load too (the P0 leak), newest first (#16).
- [x] Timeline views: all activity or only mine (who acted: the webhook sender, the user behind an app action), filtered by a repository of the Seats; one icon per activity type (merge, approval, open, close, commit, CI, claim, access...) (#16).
- [x] Merged PRs are reported as merged (`pr.merged`, state `MERGED`), not as closed.
- [x] Activity is persisted and shared: every published event is appended to the `activity_events` table (migration 00009) and each API replica delivers what it reads back from that log, in order, to its own SSE subscribers; the workspace timeline reads the same table (newest 200 of the followed repositories). Restarts lose nothing, and replicas wake each other with Postgres `LISTEN/NOTIFY` (no payload; readers go to the log), with a 5s poll as the safety net, a catch-up read after every listener reconnect, and a re-read of the newest 64 positions so a row that commits late is not missed. Activity older than 7 days is pruned hourly. A publish fails if the event cannot be recorded; a failed wake-up only delays delivery (#30).
- [x] A dead stream is noticed: the heartbeat is a `heartbeat` event (it was an SSE comment, which `EventSource` never shows the page), and the SPA closes and reopens a stream that sent nothing for 60s, so the reload after reconnecting catches up. Found in the 2026-11-16 verification: stopping the API in dev left the browser's stream open through the Vite proxy, the panel kept saying "Ao vivo (SSE)" and missed every event after the restart (a Dependabot analysis stayed "Sem análise").
- [x] Overlapping workspace reloads (a sync's own and a live event's) keep the newest: an older answer that arrives last is dropped.
- [x] Verified with two running replicas (2026-11-16): two API processes on one Postgres behind an nginx that picks a replica per request. Webhooks accepted by either replica reached a live stream held by the other (8 real commit statuses, CI events in the drawer and timeline), and a sync started on one replica was followed to the end by progress polls answered by the other.
- [ ] **P2** Not verified with two running replicas yet: a sync or an operation cancelled on the replica that does not run it (covered by tests only; a two-repository sync ends in about two seconds, too fast to cancel by hand).
- [ ] **P2** A stream that reconnects does not replay what it missed (the SPA reloads the workspace instead), and the timeline shows at most 200 events.

## 8. Inspection drawer

- [x] Signals and blockers only from GitHub: CI, CODEOWNERS state, merge state (#12, #13).
- [x] Checks panel like GitHub's: the drawer lists the head commit's checks (the latest run of each) under a verdict and counts ("Alguns checks não foram bem-sucedidos: 2 falhando, 3 bem-sucedidos"), failing and running groups open, successful and skipped collapsed, each check with its short result and a link to where it reports. The CI signal above it can never say something else. Not mirrored: the \"Required\" badge (needs branch-protection access the app does not have).
- [x] The drawer follows its PR: when the PR is merged or closed (live event, a reload that no longer lists it, or the user's own merge) the drawer closes and a dismissible notice says why, so nobody is left able to approve or merge a PR that is gone.
- [x] "Silenciar até CI passar" is disabled, with an explanation, once CI has passed or when no CI reports (it would never wake); the server refuses it too.
- [x] Changed-files preview: every file from GitHub as a collapsible card, owned files first with "Seu", loading and error states (#14).
- [x] Review state in the drawer: a "Revisões" section lists each reviewer's latest decision (approved, requested changes, commented, dismissed), current ones first and reviews of an older commit marked, from the stored reviews. The author's own comments are not reviews; it shows for every PR, whoever owns the files (#24).

## 9. Frontend platform

- [x] Angular 22 standalone, Signals, modular `common` / `features` / `layout` (ADR-0009).
- [x] pt-BR / en-US i18n, with a gate on keys used in templates (ADR-0008).
- [x] No hardcoded display text: store, mapper and templates render keys (with params and plurals); the ADR-0008 gate fails on literal text (#17).
- [x] Timeline follows the locale: activity carries its type and params (title, method, package...), never a sentence; the client phrases it.
- [x] No demo data in production; sample data only in test fixtures (#10).
- [x] Layout rhythm: a spacing scale (`--space-1` to `--space-6`) used by the views, component hosts that take no box of their own (so the canvas and drawer gaps apply between a view's header, filters and lists), ruled section headings, group headers with a trailing rule, and the dependency tree's caption as a callout attached to the tree. Checked by rendering the built app against mock data before and after.
- [x] A seat lists every open PR of its repository (it used to cut the list at three), scrolling inside the card once it is long, and the seats of a row keep one height.
- [x] An unanalysed Dependabot PR shows only its "Sem análise" verdict and the reason: no dependency tree, impact or notes that would claim "no imports" about code nobody read.
- [x] A PR is identified in the SPA by its unique id; GitHub's number is only for display and GitHub URLs. The SPA used to key PRs by number and send it to routes that take the database id, so actions could reach another PR (and numbers repeat across repositories); the activity timeline now points at PRs by id too.
- [ ] **P2** Browser end-to-end tests of the main flows (sign-in, import, drawer, merge).

## 10. Backend platform & operations

- [x] Go 1.27 + chi, goose forward-only migrations + sqlc, full-jitter backoff (ADR-0003, 0004, 0006).
- [x] Docker images, dev compose, production runbook (#1, #5).
- [x] The panel's nginx accepts webhook deliveries up to 5 MB, like the API: its 1 MB default refused a large `push` delivery with 413 before it reached the API. The production runbook was rechecked against the code and rewritten step by step.
- [ ] **P2** Bound concurrent GitHub requests per token: nothing caps them today (rate-limit headers are honoured since #29, see [PR state](#3-pr-state-from-github)).
- [x] Several API replicas (ADR-0012): each user's sync is a `sync_jobs` row (an atomic claim keeps one per user, the running replica heartbeats every second and picks up a cancel asked anywhere, a silent one reads as `sync_interrupted` and can run again, a run that lost its claim overwrites nothing), and an operation cancel that lands on another replica reaches the one serving it through `NOTIFY`; a user can only cancel their own operations. The `adrcheck` gate asks every locked in-memory struct for a `replica-local:` note.
- [x] Continuous integration: `.github/workflows/ci.yml` runs `make check` on PRs and `main` (#40; see [Business & distribution](#12-business--distribution)).
- [ ] **P2** Gates from the plan not in the Makefile: `golangci-lint run`, frontend coverage.
- [x] Cloud-agnostic install guide ([`INSTALL.md`](../INSTALL.md)): the GitHub App checklist, the configuration, and three paths with ready files: one machine with Docker Compose and Caddy (`deploy/compose/`, run end to end locally on 2026-11-16: healthy services, HTTPS, migrations, 401 for a forged webhook), plain Kubernetes manifests (`deploy/kubernetes/`, validated against the schemas, not applied to a live cluster yet), and the requirements for any managed container platform. The build image is pinned to Go 1.27.4, and the SPA's title is "PR Follower" (it was "Frontend").
- [x] Security scanning (`make security`, [`security.md`](security.md)): secrets in the whole history (Gitleaks), known vulnerabilities in Go and frontend dependencies (OSV-Scanner), code patterns (Semgrep) and workflows (zizmor), from pinned images; CI runs it on PRs, `main` and weekly, and Dependabot keeps dependencies, base images and pinned actions current. The first full scan (2026-11-16) led to Go 1.27.4 (`GO-2026-6859`), actions pinned to commit SHAs, and a refusal to start on https with the public development webhook secret.
- [ ] **P2** Before launch ([`security.md`](security.md#before-launch)): a Nuclei scan of the first real deployment, a Content-Security-Policy on the panel checked in a browser, `govulncheck` with the project's toolchain, private vulnerability reporting enabled.

## 11. Engineering guidelines

- [x] ADR-0001 to ADR-0013, each with an executable gate (`make adr-check`).
- [x] `AGENTS.md` / `CLAUDE.md` for agents; implementation plan; production runbook; this roadmap.

## 12. Business & distribution

Decisions and open questions: [`business-and-distribution.md`](business-and-distribution.md).

- [x] Business model: open source + services (2026-11-13).
- [ ] **P2** Define the paid services (which, for whom, price). Needs its own session before a public launch.
- [ ] **P2** Choose and add the open-source `LICENSE`, together with the services.
- [x] Distribution model: CI on every PR, semver tags publish images to GHCR with a GitHub Release, `main` deploys to staging, production after approval (2026-11-13).
- [x] CI workflow running `make check` on PRs and `main` (`.github/workflows/ci.yml`; Postgres-backed tests included).
- [ ] **P2** Required CI checks on `main`: rulesets configured but not enforced while the repository is private on GitHub Free; enforced once it goes public ([going public](business-and-distribution.md#3-going-public)).
- [ ] **P2** Go public at launch: `LICENSE`, secrets check of the git history, then verify the rulesets are enforced.
- [ ] **P2** Release workflow: `vX.Y.Z` tag builds and publishes both images to GHCR and creates the GitHub Release.
- [ ] **P2** Staging environment (own GitHub App and database) with automatic deploys from `main`; production deploys of tagged releases behind a manual approval.
