# Upgrading to vX.Y.Z

From **v0.2.0**. `/bump-version` replaces `vX.Y.Z` and `X.Y.Z` in this file with the release's version when it cuts the release. Each pull request that changes something an operator must act on or will notice adds its steps below ([`README.md`](README.md)).

## Before you start

- **This release migrates the database on start** (`00013_notifications.sql`: the `notifications`, `notification_settings`, `notification_watches`, `notification_baselines` and `log_positions` tables). Take a backup before upgrading: going back to an earlier version afterwards needs it.

## Required steps

## Optional steps

## Behaviour changes

- **The merge button follows GitHub's merge state, and GitHub decides the merge.** These no longer leave a PR stuck on "Mesclagem bloqueada" until its next commit:
  - a review (approved, dismissed, changes requested), made in the panel or on GitHub;
  - a change in a commit's CI.
- **"Mesclagem bloqueada pela proteção da branch" is now a warning.** The merge button stays enabled. GitHub's branch-protection state can lag a review or check it has not recounted yet, so the merge is sent and GitHub accepts it or refuses it with the rule that applies. Conflicts, an out-of-date branch, a draft and running checks still disable the button.
- **After an approval made in the panel, the merge waits 10 seconds.** This covers the drawer, the quick "Squash" button and the batch merge. A countdown says the wait is GitHub's, so the merge is not refused for an approval GitHub has not counted yet.
- **"Sincronizar" reads each open PR's merge state from GitHub.** That costs one GitHub API call per open PR, against the signed-in user's rate limit.
- **The drawer explains what GitHub has not settled.** When GitHub has not worked out the merge state yet, the drawer says so. When GitHub refuses a merge, the panel shows GitHub's reason and updates the PR to the blocker GitHub reports.
- **A notification center.** A bell in the sidebar opens the user's notifications: a PR entering "Minha Vez", a Dependabot analysis that came out Alto Risco, and changes to PRs the user acted on (merged or closed by someone else, approval dismissed, CI red after the approval). With the browser's permission, the panel shows a system notification when it is not on screen; browsers allow that only for a panel served over HTTPS (or `localhost`). Nothing is configured: one API replica at a time creates them (a Postgres advisory lock), and every replica delivers them to its own open panels.
- **The notification center starts empty.** The upgrade notifies nobody about anything that happened before it, and a repository is recorded silently the first time the notifier sees it for a user.
- **Notifications are kept 30 days.** Every replica deletes older ones hourly.

## Check that it worked

- Sign in, open the bell in the sidebar, and open "Configurações": the browser permission state shows.
- Ask a teammate to request your review on a PR (or push to one you approved) in a repository you follow: within a few seconds it appears in the center, and as a system notification if the panel is in another tab.

## Roll back

- Restore the database backup taken before the upgrade, then deploy the previous version. The notifications created since the upgrade are lost with it.
