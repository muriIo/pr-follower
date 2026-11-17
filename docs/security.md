# Security scanning

What is scanned, how to run it, and what the scans found. Report a vulnerability through [`SECURITY.md`](../SECURITY.md), not here.

## The gate: `make security`

Four scanners, each from a pinned Docker image (versions at the bottom of the `Makefile`), so a local run and CI give the same answer. Every target fails on a finding. CI runs them on every PR, on `main`, and every Monday (`.github/workflows/security.yml`), because a new advisory can make unchanged dependencies vulnerable.

| Target | Scanner | Looks at |
|---|---|---|
| `make security-secrets` | [Gitleaks](https://github.com/gitleaks/gitleaks) | Every commit of every branch, not only the current tree. Matches are redacted. |
| `make security-deps` | [OSV-Scanner](https://github.com/google/osv-scanner) | Go modules (`backend/go.mod`) and frontend packages (`frontend/pnpm-lock.yaml`) against the OSV database. |
| `make security-code` | [Semgrep](https://github.com/semgrep/semgrep) | Go, TypeScript and Dockerfiles with the `p/default`, `p/golang`, `p/typescript` and `p/dockerfile` rule packs. `--metrics=off`: no code or telemetry leaves the machine. |
| `make security-workflows` | [zizmor](https://github.com/zizmorcore/zizmor) | `.github/workflows/`: unpinned actions, credentials left in checkouts, template injection. |

Dependabot (`.github/dependabot.yml`) keeps Go modules, frontend packages, the Dockerfiles' base images and the pinned action SHAs current, proposing a new release only after a 7-day cooldown (a hijacked package is usually caught and pulled within days).

### Reading a finding

- **A real issue:** fix it in the same PR.
- **A false positive:** suppress it on its line with `// nosemgrep: <rule-id>` and a comment saying why it is safe, and add it to the table below. Never disable a rule globally.
- **pnpm lockfile:** pnpm writes its own environment as a first YAML document in `pnpm-lock.yaml`, and OSV-Scanner reads only that one (16 packages instead of ~400). `security-deps` hands it the app's document alone; keep that if the target is rewritten.

## First full scan (2026-11-16)

Run on `main` at `96a0578` (after #48). Besides the four scanners above, TruffleHog read the whole history as a second secrets opinion.

| Area | Result | Action |
|---|---|---|
| Secrets in history (Gitleaks, 110 commits; TruffleHog) | Gitleaks: none. TruffleHog: 5 matches, all placeholder connection strings in the docs (`PASSWORD`, `$DB_PASSWORD`, `PASS`). | None needed. |
| Frontend dependencies (396 packages) | No known vulnerability. | None. |
| Go dependencies | `GO-2026-6859` (CVE-2026-97054): an HTTP/1 server crash in `net/http`'s `MaxBytesReader`, fixed in Go 1.27.4. The code does not call `MaxBytesReader` itself. | Go bumped from 1.27.3 to 1.27.4. |
| Code (Semgrep, 357 rules, 260 files) | 6 matches, all false positives (below). | Suppressed on their lines with the reason. |
| Workflows (zizmor) | `pnpm/action-setup` not pinned to a commit (high); `actions/checkout` keeping the token in `.git` (2× medium). | Every action pinned to a commit SHA; `persist-credentials: false`. |
| Configuration (manual review) | With `GITHUB_WEBHOOK_SECRET` unset, the server checked webhook signatures against `dev-webhook-secret`, a value published in this repository: anyone could sign a forged webhook against a deployment that forgot the variable. | The server refuses to start when `PUBLIC_URL` is https and the secret is unset or the development default. |
| Agent skill (`.claude/skills/bump-version`) | It reads commit messages and PR titles, which strangers write once the repository is public (a prompt-injection path). It is user-invoked only and tags only after approval. | It now says that this text is data to classify, never instructions. |

Checked by hand and found sound: session cookies are `HttpOnly`, `SameSite=Lax` and `Secure` on https; the panel sends `X-Frame-Options: DENY` (no clickjacking of the one-click merge), `X-Content-Type-Options: nosniff` and a referrer policy; CORS never allows credentials; GitHub tokens are encrypted at rest (an unset `TOKEN_ENCRYPTION_KEY` means a random key per start, not a known one); identity comes only from the session (ADR-0010); writes to GitHub use the signed-in user's own token, so GitHub enforces their permissions.

### Accepted false positives

| Rule | Where | Why it is safe |
|---|---|---|
| `cookie-missing-secure` | `backend/internal/adapter/http/auth.go` (3 cookies) | `Secure` is set from the configuration: on whenever `PUBLIC_URL` is https, off only for local http development. |
| `no-fprintf-to-responsewriter` | `backend/internal/adapter/sse/hub.go` (2) | The live event stream is `text/event-stream` carrying JSON; a browser never renders it as HTML. |
| `math-random-used` | `backend/internal/infrastructure/resilience/backoff.go` | Retry jitter only; nothing secret or guessable depends on it. |
| `GHSA-fc5q-39xc-vjpf` (OSV, picomatch ≤ 4.0.7 ReDoS) | [`osv-scanner.toml`](../osv-scanner.toml), until 2026-12-17 | No fixed release yet. Build-time only (Angular CLI, Vite, Vitest, Sass); not in the panel image, and it matches only globs from the build configuration, never user input. The ignore expires so the scan fails again to re-check for a fix. |

## Before launch

- [ ] **Dynamic scan of a running deployment.** [Nuclei](https://github.com/projectdiscovery/nuclei) against the first real deployment (with the owner's permission), for exposed paths, headers and TLS: `nuclei -u https://<host> -as`. An AI pentest ([Strix](https://github.com/usestrix/strix)) is optional; it needs an LLM API key and sends the app's responses to that provider.
- [ ] **Content-Security-Policy** on the panel (`frontend/nginx.conf.template`), checked in a browser so nothing breaks: `default-src 'self'`, avatars from `avatars.githubusercontent.com`, `frame-ancestors 'none'`.
- [ ] **`govulncheck`** with the project's Go toolchain to confirm which reported Go issues are reachable (OSV-Scanner's own call analysis needs a Go 1.27 toolchain in its image, so it is off in `security-deps`).
- [ ] **Private vulnerability reporting** enabled in the repository settings when it goes public, so [`SECURITY.md`](../SECURITY.md) works.
- [ ] Optional: [Claude Code Security Review](https://github.com/anthropics/claude-code-security-review) on every PR (needs an Anthropic API key as a repository secret).
