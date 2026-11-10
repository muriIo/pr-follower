# Production Setup

Step-by-step runbook for putting PR Follower on a public domain. Written 2026-11-10 against the code at that date; the sign-in model is [ADR-0010](adr/0010-github-only-identity-and-installation-scoped-access.md).

Throughout, `app.example.com` stands for your production domain.

## 0. Before you start

Production readiness depends on open items in the Status list of [`implementation-plan.md`](implementation-plan.md). Check these first:

- [ ] **Demo seed endpoint.** `POST /api/v1/demo/seed` writes fake repositories and PRs for whoever calls it. Remove it or gate it to non-production before going public.
- [ ] **Acting on GitHub.** Approve / merge / comment do not yet use the user's token, and installation tokens (App ID + private key) are not wired. Until then those actions do not reach GitHub.

Sign-in, sessions, token encryption and webhook verification are production-ready.

## 1. Decide the shape

PR Follower ships as two images plus Postgres:

```
browser ──https──▶ TLS proxy / load balancer ──▶ panel (nginx, :8080) ──/api/*──▶ api (Go, :8080) ──▶ Postgres
```

- The **panel** image serves the Angular app and proxies `/api/*` to the API (`API_UPSTREAM`), with an unbuffered location for the live event stream. The browser only ever talks to one origin, which is what makes the session cookie work.
- Terminate **TLS in front of the panel** (load balancer, Caddy, Traefik, a cloud ingress…). The panel itself listens on plain HTTP 8080.
- Run **one API replica** for now. The live event hub and the operation-cancellation registry are in memory, so with several replicas a user only sees events and can only cancel operations on the replica they happen to hit.
- Use a **managed Postgres** (17) with backups. Migrations run automatically when the API starts.

## 2. Create the production GitHub App

GitHub → **Settings → Developer settings → GitHub Apps → New GitHub App** (or the organization's settings if the app should belong to an org).

| Field | Value |
|---|---|
| GitHub App name | `PR Follower` (unique on GitHub, max 34 chars) |
| Homepage URL | `https://app.example.com` |
| Callback URL | `https://app.example.com/api/v1/auth/github/callback` |
| Expire user authorization tokens | ✅ on |
| Request user authorization (OAuth) during installation | ❌ **off**: it skips our login state check and disables the Setup URL |
| Enable Device Flow | ❌ off |
| Setup URL | `https://app.example.com/` |
| Redirect on update | ✅ on |
| Webhook: Active | ✅ on |
| Webhook URL | `https://app.example.com/api/v1/webhooks/github` |
| Webhook secret | output of `openssl rand -hex 32` (keep it for step 4) |
| SSL verification | ✅ enabled |

**Repository permissions:** Pull requests *Read & write*, Contents *Read & write*, Checks *Read-only*, Commit statuses *Read-only*, Metadata *Read-only* (automatic). Everything else *No access*.

**Organization permissions:** Members *Read-only* (user teams for CODEOWNERS).

**Subscribe to events:** Pull request, Pull request review, Check suite, Check run, Issue comment. (`installation` and `installation_repositories` are always delivered.)

**Where can this GitHub App be installed?** **Any account**, so anyone can install it on their user or organization.

Click **Create GitHub App**.

## 3. Collect the app credentials

On the app's **General** page:

1. Copy **Client ID** → `GITHUB_CLIENT_ID`.
2. **Generate a new client secret** → `GITHUB_CLIENT_SECRET` (shown once).
3. Copy **App ID** → `GITHUB_APP_ID`, and the app's URL name (the `<slug>` in `https://github.com/apps/<slug>`) → `GITHUB_APP_SLUG`.
4. **Generate a private key** → download the `.pem`. Store it in your secret manager; never commit it. (Needed once installation tokens are wired.)
5. Optionally upload a logo, so the authorization and install pages look trustworthy.

## 4. Prepare the secrets

Generate the token encryption key once and store it in your secret manager:

```bash
openssl rand -base64 32   # TOKEN_ENCRYPTION_KEY
```

API environment:

| Variable | Value | Notes |
|---|---|---|
| `PUBLIC_URL` | `https://app.example.com` | Builds the GitHub callback URL; `https://` turns on `Secure` cookies. No trailing slash. |
| `DATABASE_URL` | `postgres://USER:PASS@HOST:5432/pr_follower?sslmode=require` | Use `sslmode=require` (or `verify-full`) for managed Postgres. |
| `GITHUB_CLIENT_ID` | from step 3 | Without it sign-in is disabled (`auth_error=not_configured`). |
| `GITHUB_CLIENT_SECRET` | from step 3 | Secret. |
| `GITHUB_APP_ID` | from step 3 | |
| `GITHUB_APP_SLUG` | from step 3 | Builds the "install on GitHub / add account" links on the import screen. |
| `GITHUB_WEBHOOK_SECRET` | from step 2 | Secret. **Must be set**: the default is the public dev value. |
| `TOKEN_ENCRYPTION_KEY` | from above | Secret. **Must be set**: without it a random key is used and every sign-in is lost on restart. |
| `ALLOWED_ORIGIN` | `https://app.example.com` | CORS; the app is same-origin, so this only narrows the default `*`. |
| `PORT` | `8080` | Default. |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | `25` / `10` | Defaults; lower them if your Postgres plan has few connections. |

Panel environment:

| Variable | Value |
|---|---|
| `API_UPSTREAM` | Internal URL of the API, e.g. `http://api:8080` (default) |

## 5. Build and publish the images

From the repo root (or in CI):

```bash
make docker-build                  # builds pr-follower-api and pr-follower-panel
docker tag pr-follower-api   REGISTRY/pr-follower-api:$(git rev-parse --short HEAD)
docker tag pr-follower-panel REGISTRY/pr-follower-panel:$(git rev-parse --short HEAD)
docker push REGISTRY/pr-follower-api:$(git rev-parse --short HEAD)
docker push REGISTRY/pr-follower-panel:$(git rev-parse --short HEAD)
```

Deploy by immutable tag, not `latest`. Both images run as non-root users.

## 6. Deploy

Any container platform works. A minimal single-host example (TLS proxy not shown; point it at `panel:8080`):

```yaml
services:
  api:
    image: REGISTRY/pr-follower-api:TAG
    env_file: api.env            # the variables from step 4, from your secret manager
    restart: unless-stopped
  panel:
    image: REGISTRY/pr-follower-panel:TAG
    environment:
      API_UPSTREAM: http://api:8080
    ports:
      - "127.0.0.1:8080:8080"    # only the TLS proxy reaches it
    depends_on: [api]
    restart: unless-stopped
```

Expose only the panel. The API needs no public port: the browser and GitHub webhooks both arrive through the panel's `/api/*` proxy.

TLS proxy requirements:
- Forward to the panel on HTTP 8080 and pass `X-Forwarded-Proto: https`.
- Do not buffer responses for `/api/v1/events/stream` and allow idle connections of at least 1 hour (live updates use Server-Sent Events).

## 7. Point DNS and verify

1. Point `app.example.com` at the TLS proxy and confirm a valid certificate.
2. `curl -s https://app.example.com/healthz` → `ok` (panel). The API's own `/healthz` returns `{"status":"ok"}` on the internal network.
3. `curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com/api/v1/workspace` → `401` (API reachable and protected).
4. Open `https://app.example.com`, click **Entrar com GitHub**, authorize the app. You should land in the workspace with your avatar.
5. In the browser dev tools, the `pf_session` cookie is `HttpOnly`, `Secure`, `SameSite=Lax`.
6. In the app, open **Seats → + Importar repositório**, follow **+ Adicionar conta do GitHub**, install the app on a test repository, and check that GitHub returns you to the import screen with that account selected. Follow the repository. In the app's **Advanced → Recent Deliveries**, the `installation` delivery shows a `2xx` response. A `401` means `GITHUB_WEBHOOK_SECRET` differs from the app's webhook secret.
7. Open a PR on the test repository: the delivery succeeds and the event appears in **Linha do Tempo**.
8. On GitHub, remove the test repository from the installation (**Configure** on the app's installation page). Its Seat shows "Sem acesso" within seconds; add it back and the warning clears.

## 8. Operate

- **Rotate the client secret:** generate a new one on the app page, deploy it, then delete the old one. Signed-in users are unaffected.
- **Rotate the webhook secret:** update the app and `GITHUB_WEBHOOK_SECRET` together; deliveries in between fail and can be redelivered from **Recent Deliveries**.
- **Rotate `TOKEN_ENCRYPTION_KEY`:** stored GitHub tokens become unreadable and are dropped, so users sign in again. Sessions themselves stay valid until they need a GitHub call. Rotate on suspected leak, not routinely.
- **Revoke everyone:** truncate the `sessions` table; every user must sign in again.
- **Backups:** the database holds users, encrypted tokens, sessions and triage state. Restore needs the same `TOKEN_ENCRYPTION_KEY`, or users simply sign in again.
- **Upgrades:** deploy a new API tag; forward-only migrations (ADR-0004) run on start. Roll back by deploying the previous tag only if no new migration shipped; otherwise fix forward.

## Troubleshooting

The sign-in screen shows why a login failed (`?auth_error=`):

| Code | Meaning | Fix |
|---|---|---|
| `not_configured` | API has no `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | Set both and restart the API. |
| `state_mismatch` | The login cookie was missing or did not match | Check `PUBLIC_URL` matches the domain users open; "Request user authorization during installation" must be off; the TLS proxy must not strip cookies. |
| `access_denied` | The user cancelled on GitHub | Nothing to fix. |
| `login_failed` | GitHub rejected the code exchange or profile read | Check the client secret, and that the app's Callback URL is exactly `PUBLIC_URL` + `/api/v1/auth/github/callback`. API logs show the cause. |

GitHub shows **"The redirect_uri is not associated with this application"** on its own page when the Callback URL in the app settings differs from `PUBLIC_URL` + `/api/v1/auth/github/callback`.
