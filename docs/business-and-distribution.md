# PR Follower — Business model & distribution

How the project makes money and how it reaches users. Decisions are dated; open questions stay listed here until they are settled. Open items are also tracked in [`roadmap.md`](roadmap.md#12-business--distribution).

## 1. Business model

**Decided (2026-11-13): open source + services.** The product (backend, frontend, deploy recipes) is published as open source and anyone can self-host it. Revenue comes from services offered around it, not from gating the code.

Open:

- [ ] **Define the services.** Which services, for whom, and at what price. Not discussed yet. Schedule a dedicated session for this before any public launch.
- [ ] **Choose the license.** The repository has no `LICENSE` file yet. The license decides what others may do with the code (for example, a permissive license versus a copyleft one that requires hosted forks to publish their changes), so it should be chosen alongside the services.

## 2. Distribution

**Decided (2026-11-13):**

```
PR ──▶ CI (make check) ──▶ merge to main ──▶ images :sha ──▶ staging (automatic)
                                                    │
                       tag vX.Y.Z ──▶ images :X.Y.Z + GitHub Release ──▶ approval ──▶ production
```

1. **CI on GitHub Actions.** `make check` runs on every pull request and on every push to `main`. A PR merges only when it passes (required status check on `main`). Implemented in `.github/workflows/ci.yml` as two parallel jobs, `backend` and `frontend`; the backend job runs a Postgres 17 service so the tests that need a real database run instead of skipping.
2. **Releases.** Semantic version tags (`vX.Y.Z`, starting at `v0.x.y` until the first public launch). A tag builds both images (`pr-follower-api`, `pr-follower-panel`) and publishes them to GitHub Container Registry (`ghcr.io`) tagged with the version and the commit SHA. A GitHub Release is created with notes generated from the Conventional Commits since the previous tag. Cut a release with the `/bump-version` skill (`.claude/skills/bump-version/`): it proposes the version from those commits (minor or patch until launch; major only from `1.0.0` on), summarizes them, and tags only after approval.
3. **Staging (homologation).** Every merge to `main` builds images tagged with the commit SHA and deploys them to staging automatically. Production gets only tagged releases, after a manual approval (a GitHub Environment with required reviewers).

Open:

- [ ] Where staging and production run (the AWS shape in [`aws-setup.md`](aws-setup.md) or another host) and how the deploy job reaches them.
- [ ] Staging needs its own GitHub App (webhook URL, callback) and its own database, so test data never touches production.
- [ ] Mark both CI jobs as required status checks on `main` (repository settings, branch protection).
- [ ] Implement the remaining workflows: `release.yml`, `deploy.yml` in `.github/workflows/`, then update [`production-setup.md`](production-setup.md) (§7 deploys commit SHAs today).
