# PR Follower — Business model & distribution

How the project makes money and how it reaches users. Decisions are dated; open questions stay listed here until they are settled. Open items are also tracked in [`roadmap.md`](roadmap.md#12-business--distribution).

## 1. Business model

**Decided (2026-11-13): open source + services.** The product (backend, frontend, deploy recipes) is published as open source and anyone can self-host it. Revenue comes from services offered around it, not from gating the code.

**Decided (2026-11-16): a self-hosted, open-source portfolio project with optional income.**

- **Goal.** A strong open-source portfolio piece first; income is optional.
- **First user.** A company the maintainer works with as a contractor validates it on its own infrastructure, under an agreement between the two, with pt-BR as the default UI language. Every team, the first one included, runs its own deployment.
- **No hosted offer.** The maintainer takes no operations responsibility: no SaaS, no on-call, no customer code passing through infrastructure the maintainer runs.
- **Services.** End-to-end support and setup for teams that self-host, agreed per engagement (no fixed tiers or prices until someone asks). Plus sponsorship (GitHub Sponsors / Buy Me a Coffee through `.github/FUNDING.yml`) and stars.

**Decided (2026-11-17): Apache-2.0** ([`LICENSE`](../LICENSE), copyright in [`NOTICE`](../NOTICE)). The maintainer built it as a contractor with their own resources and holds the copyright; the first company's validation is an agreement between the two, not a claim on the code. With no hosted offer to protect, a copyleft license (AGPL) would only deter companies from adopting it, and source-available licenses (BSL, FSL) would weaken the open-source claim. Apache-2.0 is permissive like MIT and adds an explicit patent grant, contribution terms and a trademark clause, which companies' legal reviews prefer. Every dependency is MIT, BSD or Apache-2.0, so none constrains it.

## 2. Distribution

**Decided (2026-11-13):**

```
PR ──▶ CI (make check) ──▶ merge to main ──▶ images :sha ──▶ staging (automatic)
                                                    │
                       tag vX.Y.Z ──▶ images :X.Y.Z + GitHub Release ──▶ approval ──▶ production
```

1. **CI on GitHub Actions.** `make check` runs on every pull request and on every push to `main`. A PR merges only when it passes (required status check on `main`). Implemented in `.github/workflows/ci.yml` as two parallel jobs, `backend` and `frontend`; the backend job runs a Postgres 17 service so the tests that need a real database run instead of skipping.
2. **Releases.** Semantic version tags (`vX.Y.Z`, starting at `v0.x.y` until the first public launch). A tag builds both images (`pr-follower-api`, `pr-follower-panel`) and publishes them to GitHub Container Registry (`ghcr.io`) tagged with the version and the commit SHA. A GitHub Release is created with notes generated from the Conventional Commits since the previous tag. Cut a release with the `/bump-version` skill (`.claude/skills/bump-version/`): it proposes the version from those commits (minor or patch until launch; major only from `1.0.0` on), summarizes them, and tags only after approval.
3. **Deployments belong to whoever runs it (revised 2026-11-16).** This repository ships images and recipes, not environments. Each team, the first one included, deploys tagged images from GHCR with its own pipeline, GitHub App, database and secrets ([`production-setup.md`](production-setup.md), [`aws-setup.md`](aws-setup.md)). The automatic staging deploy and the production approval planned on 2026-11-13 are dropped from this repository: the maintainer runs no environment.

Open:

- [ ] The first deployment, at the first company: its own GitHub App, database and pipeline, pulling tagged images. A staging there, if wanted, is the company's (with its own GitHub App, so test data never touches production).
- [ ] Required CI checks on `main`: rulesets are configured (`main`: PR only, both CI jobs required, merge commits only, no force push or deletion; `release tags`: `v*` created only by the admin, never moved or deleted) but **not enforced**, because GitHub Free does not enforce rulesets on a private repository. They take effect when the repository goes public (§3).
- [ ] Implement `release.yml` in `.github/workflows/` (no `deploy.yml`: see 3. above), then update [`production-setup.md`](production-setup.md) to deploy tagged images (§7 deploys commit SHAs today).

## 3. Going public

**Decided (2026-11-13):** the repository stays private until launch and becomes public then; no paid GitHub plan in the meantime. Until then, the rulesets saved in the repository settings are not enforced, so `main` relies on the PR habit alone.

Before flipping the visibility:

- [x] `LICENSE` chosen and added (§1).
- [x] A README for self-hosters (what it does, screenshots, the quick start) and `SECURITY.md` (how to report a vulnerability).
- [ ] `CONTRIBUTING.md` and `.github/FUNDING.yml`.
- [ ] Git history checked for secrets (every commit, not only the current tree); rotate anything found before publishing.
- [ ] After flipping: confirm both rulesets are enforced (`gh api repos/<owner>/pr-follower/rules/branches/main` lists the rules) and that a direct push to `main` is rejected.
