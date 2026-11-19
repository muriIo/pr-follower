# Upgrade guides

Every release has an **upgrade guide** here, `vX.Y.Z.md`: the step-by-step an operator follows to move a running deployment from the previous release to this one. The release workflow refuses a release without it and appends it to the GitHub Release notes.

Changes waiting for the next release collect their steps in [`UNRELEASED.md`](UNRELEASED.md). A pull request that changes anything an operator must act on adds its steps there in the same PR: a new or changed environment variable, a GitHub App permission or event, a change to `deploy/` (probes, mounts, resources), a database migration, a new route a proxy or health check needs, a behaviour users will notice. `/bump-version` renames the file to the release's version when the release is cut.

## What a guide contains

A guide is **self-sufficient**: an operator holding only this file and their own deployment can upgrade, without reading the diff or another document. Links to the runbook explain *why*; the steps themselves are all here.

1. **Title and summary:** `# Upgrading to vX.Y.Z` and one line naming the release it upgrades from, then what the upgrade does for the operator in two or three bullets.
2. **Before you start:** whether the release migrates the database (and so whether rolling back needs the backup), the backup step, and the exact image references to verify and pin (filled in from the release notes' digests).
3. **Required steps,** numbered, in the order to do them, with the exact snippet for each deploy path the project ships: Docker Compose (`deploy/compose/`), Kubernetes (`deploy/kubernetes/`), and ECS or any other platform (the property to set). Every new or changed variable with its value or how to generate it, and whether it is a secret. Every GitHub App permission or event change, with who must accept it on each installation.
4. **Optional steps:** improvements an operator may adopt, each self-contained.
5. **Behaviour changes:** what users or operators will see differently, with no action needed.
6. **Check that it worked:** commands with their expected output.
7. **Roll back:** what going back to the previous release takes.

A step reads the same whether the operator runs one replica or several, and never assumes they used the repository's files unchanged: name the setting, then show the snippet.
