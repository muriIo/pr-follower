# Upgrading to vX.Y.Z

From **v0.2.0**. `/bump-version` replaces `vX.Y.Z` and `X.Y.Z` in this file with the release's version when it cuts the release. Each pull request that changes something an operator must act on or will notice adds its steps below ([`README.md`](README.md)).

## Before you start

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

## Check that it worked

## Roll back
