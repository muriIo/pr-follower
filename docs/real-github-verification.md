# Verifying against real GitHub

The automated tests use fakes for GitHub. This is the manual checklist for the roadmap items marked "Not verified against real GitHub yet". Run it once against a **dev GitHub App** and a **throwaway test repository** (never production data), then tick the matching roadmap items.

## 0. Setup

1. Create the dev app as in [`production-setup.md`](production-setup.md) §4–5, with the callback `http://localhost:4200/api/v1/auth/github/callback` and the Setup URL `http://localhost:4200/`. Subscribe to Pull request, Pull request review, Push, Status and Check run (production-setup §4.6).
2. Expose the webhook endpoint (`smee.io` or `ngrok http 8080`) and set the app's Webhook URL to `<tunnel>/api/v1/webhooks/github` with a secret equal to `GITHUB_WEBHOOK_SECRET`.
3. Fill `.env` with `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `GITHUB_WEBHOOK_SECRET` and `GITHUB_APP_PRIVATE_KEY`: the `.pem` as one line, made with the `awk` command in production-setup §6.2 (`.env` is git-ignored). `make dev` does not pass `GITHUB_APP_PRIVATE_KEY_FILE` to the API container.
4. `make dev`, then `docker compose -f docker-compose.local.yml logs -f api`. The API must **not** print `GitHub App credentials not set`.
5. Test repository, owned by you: a `.github/CODEOWNERS` assigning a path to you, a `package.json` + `package-lock.json` (npm v2/v3) with an outdated dependency, and a `.github/dependabot.yml` for `npm` so Dependabot opens a PR. A second GitHub account forks it and opens PRs **into your repository**: those are the "someone else's PR" of §3. Import your repository, not the fork (a fork runs no Dependabot).
6. Install the app on the test repository, sign in, import it. Use one browser profile per account. Keep **Recent Deliveries** (app → Advanced) open: every delivery must be `2xx`.

Useful: `docker exec pr-follower-local-postgres-1 psql -U prfollower pr_follower_local -c '<sql>'`.

## 1. Installation token and file reads (§5)

- Open a PR that touches a file you own. Its drawer shows the changed files, owned ones first with "Seu".
- API log shows no fallback to a follower token. If files stay "not evaluated", check the app's *Contents* and *Pull requests* read access.

**Pass:** files appear for a PR opened while you are signed out of the app (the webhook read used the app's token).

## 2. Dependabot analysis (§5, §6)

- Wait for a Dependabot PR (or Insights → Dependency graph → Dependabot → check for updates).
- It appears in the Radar; open it and click **Analisar agora** if it is "Sem análise".
- **Pass:** a risk tier, a dependency path and a blast radius appear; the log has no `dependabot analysis failed`.
- A grouped update PR must stay "Sem análise" with its own explanation and no button.

## 3. Acting as the user (§6)

Use PRs of the test repository authored by someone other than you.

For 3.2 add a slow workflow to the default branch (a fork PR runs the base branch's workflows): `.github/workflows/slow.yml` with one job that runs `sleep 300` `on: [pull_request]`. Pushing it with a `gh` token needs the `workflow` scope (`gh auth refresh -s workflow`), or create it in the web editor. A first-time contributor's run may wait for **Approve and run**.

1. **Approve** from the drawer: GitHub shows the review under *your* name, not the app's. Approve again: nothing is repeated.
2. **Checks running:** push a commit while a workflow runs; approve and merge are disabled with the reason, and `409 checks_running` comes from the API.
3. **`@dependabot rebase`** from the drawer: GitHub shows the comment under your name and Dependabot reacts.
4. **Merge** (squash) a PR: it is merged and shows as merged in the timeline. Merging a PR already merged converges to success.
5. Your own PR: approve is not offered; merge is.
6. If you get "Resource not accessible by integration", accept the new permissions on the installation page.

## 4. Push refresh of CODEOWNERS (§2)

1. On the default branch, edit `.github/CODEOWNERS` (change an owner) and push. The delivery for `push` is `2xx`; Seats reflect the new rules and the timeline announces the change.
2. Delete the file and push: the repository becomes "no CODEOWNERS". Restore it.
3. **Large push:** push more than 20 commits at once (`for i in $(seq 25); do git commit --allow-empty -m "c$i"; done; git push`) with a CODEOWNERS edit among the first ones. **Pass:** rules still refresh (GitHub truncates the commit list).
4. With the app credentials removed and a follower signed out, the delivery must fail (red), then pass on **Redeliver** after restoring them.

## 5. Commit statuses (§3)

- Create a status by hand: `gh api repos/OWNER/REPO/statuses/<head-sha> -f state=pending -f context=ci/test` then `-f state=success`.
- **Pass:** the `status` delivery is `2xx`; the check list in the drawer shows `ci/test`, pending keeps the PR from looking green, success clears it.
- Import check: create a status on an open PR's head, then unfollow and import the repository again. The status must appear from the combined-status read.
- If you have Vercel or Jenkins on a repository, repeat with a real one.

## 6. Sync after downtime and rate limits (§3)

1. `make dev` stack stopped (`docker compose -f docker-compose.local.yml stop api`). On GitHub: open a PR, merge another, push a commit to a third, then start the API again.
2. Click **Sincronizar**. **Pass:** progress shows "Sincronizando n de m: owner/repo", the new PR appears, the merged one closes, the third shows the new head commit and fresh checks.
3. Cancel a sync midway: the panel stops and `GET /api/v1/sync` reports it cancelled.
4. **Rate-limit headers:** `gh api rate_limit` shows what is left. Real exhaustion is slow to reach; instead confirm the headers the client reads with `curl -si -H "Authorization: Bearer $(gh auth token)" https://api.github.com/rate_limit | grep -i x-ratelimit`. `X-RateLimit-Remaining` and `X-RateLimit-Reset` must be present. Optional: lower the margin with many repositories until a sync answers `429 rate_limited` with `retry_at` and names the repositories left.

Watch the header while the API is stopped: it should leave "Ao vivo (SSE)". If it does not, the stream is dead and the panel misses live events after the restart (roadmap §7); reload the page before judging what the panel shows.

## 7. Recording the result

Last run: 2026-11-16, everything above except 4.4, 6.3 and a real Vercel/Jenkins status; results in the roadmap.


Tick the matching items in [`roadmap.md`](roadmap.md) (§2, §3, §5, §6 "Not verified" lines). Anything that failed becomes a bug there, with the delivery id or log line.
