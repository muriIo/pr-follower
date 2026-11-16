# Production Setup

Step-by-step runbook for putting PR Follower on a public domain: from nothing to a verified deploy, then how to run it. Last checked against the code on 2026-11-11 (`main` after #31). The sign-in model is [ADR-0010](adr/0010-github-only-identity-and-installation-scoped-access.md). Update this file in the PR that changes an env var, a route or the deploy shape.

Work through the sections in order: each one uses what the previous ones produced. Deploying on AWS? [`aws-setup.md`](aws-setup.md) maps these steps to AWS services (and explains the choices); it reuses §2, §4–5, §10–11 and §13 from here.

| Placeholder | Meaning |
|---|---|
| `app.example.com` | Your production domain. |
| `REGISTRY` | Your container registry, e.g. `ghcr.io/acme`. |
| `TAG` | The image tag you deploy: a commit SHA (§7). |
| `/opt/pr-follower` | The deploy directory of the single-host example (§9). |

## 0. Before you start

### What you need

- A domain or subdomain you control, and access to its DNS.
- A host or container platform that runs Linux images and reaches the internet over HTTPS.
- A TLS-terminating reverse proxy or load balancer (Caddy, nginx, Traefik, a cloud load balancer…) with a publicly trusted certificate: GitHub refuses to deliver webhooks to an untrusted one.
- PostgreSQL 17 with automated backups, preferably managed.
- A secret manager (1Password, Vault, a cloud secret manager…) for the secrets created in §2 and §5.
- On GitHub: an organization where you are an **owner** (recommended) or your personal account, to own the app; and a throwaway test repository for §11.
- On the build machine: Docker and this repository, with `make check` passing.

### Readiness

Open items in [`roadmap.md`](roadmap.md) that matter in production. None blocks a deploy, but know them before users arrive:

- [x] **Verified against real GitHub** (2026-11-16, [`real-github-verification.md`](real-github-verification.md)); what is still unverified is listed in the roadmap.
- **Several API replicas** are supported (§13.10, ADR-0012) but have not run as two processes behind one proxy yet.
- **No periodic resync.** Webhooks lost while the API was down are not fetched on their own: redeliver them (§13.1) or users click **Sincronizar**.
- **Expired sessions are never purged** automatically: §13.6 has the SQL.
- **Failed Dependabot analyses are only logged**: the Radar keeps showing "Sem análise".
- **CI** runs `make check` on every PR and on `main`; still run it before a build from a local checkout (§7).

Sign-in, sessions, token encryption and webhook verification are production-ready.

## 1. Understand the shape

```
browser ───┐
           ├─ https ─▶ TLS proxy ─ http ─▶ panel (nginx :8080) ─ /api/* ─▶ api (Go :8080) ─▶ Postgres 17
GitHub ────┘           (public)            serves the SPA                       │
(webhooks)                                                                      └─ https ─▶ github.com
                                                                                           api.github.com
                                                                                           codeload.github.com
```

- **panel** (`pr-follower-panel`, nginx, UID 101) serves the Angular app and proxies `/api/*` to the API (`API_UPSTREAM`), with an unbuffered location for the live event stream. Browsers and GitHub only ever talk to this one origin, which is what makes the session cookie work.
- **api** (`pr-follower-api`, Go, UID 10001) is the only component that talks to Postgres and GitHub. It needs no public port.
- **Postgres** holds all state (§13.7). Migrations are embedded in the API and run every time it starts, before it listens.
- **TLS** terminates in front of the panel; the panel listens on plain HTTP 8080.
- **Network.** The API needs outbound HTTPS to `github.com` (sign-in), `api.github.com` (REST) and `codeload.github.com` (repository tarballs). If you filter inbound traffic by source, allow GitHub's webhook ranges (`hooks` in `https://api.github.com/meta`) as well as your users.

The API also works in the background, in-process:

| Work | Behaviour |
|---|---|
| Dependabot analysis | 2 workers, up to 256 queued, 2 minutes per PR. Only with the GitHub App credentials. |
| Sync ("Sincronizar") | One background job per user, kept in Postgres (`sync_jobs`) so any replica reports or cancels it. The replica running it records a heartbeat every second; a job silent for 15 s (its replica stopped) is reported as interrupted and can be run again. |
| Activity (timeline, live stream) | Stored in Postgres. Replicas wake each other with `LISTEN/NOTIFY`, with a 5 s poll as a safety net. Events older than 7 days are pruned hourly. |
| Installation tokens | Exchanged with the app's private key, cached, renewed 5 minutes before they expire (they last 1 hour). |

**Any number of API replicas** can serve the same users: activity, sync jobs and cancel requests go through Postgres (ADR-0012). §13.10 lists what running more than one takes.

## 2. Generate the secrets

Generate these once, on a trusted machine, and put each one straight into the secret manager under a clear name.

| Secret | Command | Used in |
|---|---|---|
| Webhook secret | `openssl rand -hex 32` | The app's webhook (§4.4) and `GITHUB_WEBHOOK_SECRET` |
| Token encryption key | `openssl rand -base64 32` | `TOKEN_ENCRYPTION_KEY` |
| Database password | `openssl rand -base64 32 \| tr -d '/+='` | The Postgres role (§3) and `DATABASE_URL` |

The database password command drops `/`, `+` and `=`, so the password needs no escaping inside a URL.

The token encryption key must decode to exactly 32 bytes: `printf '%s' 'THE_KEY' | base64 -d | wc -c` prints `32`. It encrypts every stored GitHub token; losing or changing it signs every user out (§13.5).

GitHub produces the rest in §5: the client secret and the app's private key.

## 3. Prepare Postgres

1. Create a PostgreSQL **17** instance (the version the dev stack and tests use) on a private network the API can reach. Turn on automated backups with point-in-time recovery.
2. Create the role, and a database **owned by it**. Since Postgres 15 only the database owner may create tables in `public` by default, and the API creates its tables on first start:

   ```sql
   CREATE ROLE prfollower LOGIN PASSWORD 'the database password from §2';
   CREATE DATABASE pr_follower OWNER prfollower;
   ```

   A managed service's console does the same; what matters is that `prfollower` owns `pr_follower`.
3. Build the connection string:

   ```
   postgres://prfollower:PASSWORD@DB_HOST:5432/pr_follower?sslmode=require
   ```

   - `sslmode=require` encrypts the connection. `sslmode=verify-full&sslrootcert=/path/ca.pem` also checks the server's certificate (mount the provider's CA file into the API container).
   - Use a **direct** connection or a session-mode pooler. The API keeps one connection open for `LISTEN`, which a transaction-mode pooler (PgBouncer `pool_mode = transaction`) does not carry: live updates would then only arrive through the 5 s poll.
   - Budget connections: up to `DB_MAX_OPEN_CONNS` (default 25), plus 2 for `LISTEN`, per API replica. Lower `DB_MAX_OPEN_CONNS` on small plans.
4. Check it from a machine on that network:

   ```bash
   psql 'postgres://prfollower:PASSWORD@DB_HOST:5432/pr_follower?sslmode=require' -c 'select version()'
   ```

Don't run migrations by hand. The API applies the forward-only migrations ([ADR-0004](adr/0004-forward-only-goose-migrations-and-sqlc.md)) at start; `SELECT max(version_id) FROM goose_db_version;` shows the applied version afterwards.

## 4. Create the production GitHub App

Use a separate app for production. Keep your dev app (callback on `localhost`) for development, so the production client secret and private key never sit on a laptop.

### 4.1 Choose the owner

Create the app under the **organization** that runs PR Follower, so it does not depend on one person's account: **Organization → Settings → Developer settings → GitHub Apps → New GitHub App**. Org owners can make other members **App managers**. A personal app works the same way (**Settings → Developer settings → GitHub Apps → New GitHub App**).

### 4.2 Basic information and sign-in

| Field | Value | Why |
|---|---|---|
| GitHub App name | `PR Follower` (unique on GitHub, max 34 characters) | Shown when users install and authorize it. |
| Description | One line on what it does | Shown on the install page. |
| Homepage URL | `https://app.example.com` | Required. |
| Callback URL | `https://app.example.com/api/v1/auth/github/callback` | Must equal `PUBLIC_URL` + `/api/v1/auth/github/callback` character for character. Add only this one. |
| Expire user authorization tokens | ✅ on | Users get 8-hour tokens and a refresh token; the API refreshes them. |
| Request user authorization (OAuth) during installation | ❌ **off** | When on, GitHub sends installers to the Callback URL without our login state (they land on `state_mismatch`) and disables the Setup URL. |
| Enable Device Flow | ❌ off | Not used. |

### 4.3 Post installation

| Field | Value | Why |
|---|---|---|
| Setup URL | `https://app.example.com/` | GitHub returns installers here with `installation_id`, and the app opens the import screen on that account. |
| Redirect on update | ✅ on | The same return after someone adds or removes repositories on an installation. |

### 4.4 Webhook

| Field | Value |
|---|---|
| Active | ✅ on |
| Webhook URL | `https://app.example.com/api/v1/webhooks/github` |
| Webhook secret | The webhook secret from §2 |
| SSL verification | ✅ enabled |

The server does not need to exist yet. GitHub sends a `ping` when the app is created; it fails now, and you redeliver it in §11.

### 4.5 Permissions

**Repository permissions** (everything not listed: *No access*):

| Permission | Access | Used for |
|---|---|---|
| Pull requests | Read & write | Read PRs, their files and reviews; approve; `@dependabot` comments. |
| Contents | Read & write | Merge (write); CODEOWNERS, lockfiles and tarballs (read). |
| Checks | Read-only | Check runs (CI). |
| Commit statuses | Read-only | Legacy statuses (Jenkins, Vercel). |
| Metadata | Read-only | Mandatory; set automatically. |

**Organization permissions:** Members *Read-only*, to read the user's teams for team entries in CODEOWNERS. **Account permissions:** none.

Approve, merge and comment use the signed-in user's own token, so they are limited by these permissions *and* by what that user may do on the repository.

### 4.6 Events

Subscribe to exactly these (each one appears only once its permission above is set):

| Event | What the API does with it |
|---|---|
| Pull request | Opens, updates, closes and merges PRs; reads their changed files; schedules the Dependabot analysis. |
| Pull request review | Stores reviews, which decide "approved by me", "teammate approved" and "waiting on author". |
| Push | Re-reads CODEOWNERS when a push to the default branch touches it. |
| Status | Legacy commit statuses, listed beside check runs. |
| Check run | CI state of each check. |

`installation` and `installation_repositories` are always delivered to GitHub Apps; they keep repository access in sync. *Check suite* and *Issue comment* appear in the product plan but are not ingested yet: the API acknowledges and ignores them, so leave them off.

### 4.7 Where it can be installed

- **Any account**: anyone can install the app on their user or organization. This is the ADR-0010 model: sign-in is open to every GitHub account, and what each user sees is limited by where the app is installed.
- **Only on this account**: only the owning account can install it. Choose this for a private, single-organization instance. Anyone can still sign in, but other users find nothing to import.

You can change this later on the app's **Advanced** page.

Click **Create GitHub App**.

## 5. Collect the app credentials

On the app's **General** page:

1. **App ID** → `GITHUB_APP_ID`.
2. **Client ID** (starts with `Iv`) → `GITHUB_CLIENT_ID`.
3. **Generate a new client secret** → `GITHUB_CLIENT_SECRET`. GitHub shows it once: put it straight into the secret manager.
4. The app's URL name, the `<slug>` in its public page `https://github.com/apps/<slug>` → `GITHUB_APP_SLUG`.
5. **Private keys → Generate a private key.** The browser downloads `<slug>.<date>.private-key.pem`. Store it in the secret manager. Never copy it into `backend/` or `frontend/` (the image build contexts) or commit it, and delete the downloaded copy once it is stored.
6. Optional: confirm the key belongs to this app. GitHub shows each key's SHA-256 fingerprint under **Private keys**; this prints the same value:

   ```bash
   openssl rsa -in <slug>.<date>.private-key.pem -pubout -outform DER | openssl sha256 -binary | openssl base64
   ```

7. Optional: upload a logo under **Display information**, so the install and authorization pages look trustworthy.

## 6. Write the configuration

### 6.1 API environment

| Variable | Required | Value | Notes |
|---|---|---|---|
| `PUBLIC_URL` | yes | `https://app.example.com` | The origin users open. Builds the GitHub callback URL; `https://` makes the cookies `Secure`. A trailing slash is dropped. Default `http://localhost:4200`. |
| `DATABASE_URL` | yes | from §3 | Default: a local dev database. |
| `GITHUB_CLIENT_ID` | yes | from §5 | Without it (or the secret) sign-in is disabled: the login answers `auth_error=not_configured` and every API route `401`. |
| `GITHUB_CLIENT_SECRET` | yes | from §5 | Secret. |
| `GITHUB_APP_ID` | yes* | from §5 | With the private key, the API reads as the app (installation token), so webhook-time reads, CODEOWNERS refreshes and the Dependabot analysis do not depend on a signed-in follower. |
| `GITHUB_APP_PRIVATE_KEY_FILE` | yes* | `/run/secrets/github-app.pem` | Secret. Preferred: the mounted `.pem` (§6.2). Wins over `GITHUB_APP_PRIVATE_KEY`. A file the API cannot read stops it at start. |
| `GITHUB_APP_PRIVATE_KEY` | | the `.pem` contents | Secret. Alternative to the file for platforms that only take variables: one line, newlines written as `\n` (§6.2). A key that does not parse stops the API at start. |
| `GITHUB_APP_SLUG` | yes | from §5 | Builds the **+ Adicionar conta do GitHub** link (`https://github.com/apps/<slug>/installations/new`). Without it the import screen has no install link. |
| `GITHUB_WEBHOOK_SECRET` | yes | from §2 | Secret. The default is the public dev value `dev-webhook-secret`, with which anyone could forge deliveries. |
| `TOKEN_ENCRYPTION_KEY` | yes | from §2 | Secret. Standard base64 of 32 bytes. Unset: a random key on every start, so GitHub sign-ins do not survive a restart. Invalid: the API stops at start. |
| `ALLOWED_ORIGIN` | no | `https://app.example.com` | CORS. The app is same-origin, so this only narrows the default `*`. |
| `PORT` | no | `8080` | Default. |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | no | `25` / `10` | Defaults. Values that are not positive numbers fall back to them. |

\* Without `GITHUB_APP_ID` and a key the API still runs: it logs a warning, falls back to followers' tokens, and turns the Dependabot analysis off ("Sem análise" everywhere). Treat both as required in production.

For the single-host example, as `/opt/pr-follower/api.env` (mode `0600`, filled from the secret manager, never committed):

```dotenv
PUBLIC_URL=https://app.example.com
DATABASE_URL=postgres://prfollower:PASSWORD@DB_HOST:5432/pr_follower?sslmode=require
GITHUB_CLIENT_ID=Iv23...
GITHUB_CLIENT_SECRET=...
GITHUB_APP_ID=123456
GITHUB_APP_SLUG=pr-follower
GITHUB_APP_PRIVATE_KEY_FILE=/run/secrets/github-app.pem
GITHUB_WEBHOOK_SECRET=...
TOKEN_ENCRYPTION_KEY=...
ALLOWED_ORIGIN=https://app.example.com
```

One variable per line, unquoted, with no spaces around `=`.

### 6.2 The private key

The API runs as **UID 10001**, so the mounted key must be readable by that user. On a Linux Docker host:

```bash
sudo install -d -m 0755 /opt/pr-follower/secrets
sudo install -m 0400 -o 10001 -g 10001 <slug>.<date>.private-key.pem /opt/pr-follower/secrets/github-app.pem
```

On Kubernetes, mount it from a Secret with `defaultMode: 0440` and set the pod's `securityContext.fsGroup: 10001`.

If your platform only takes environment variables, set `GITHUB_APP_PRIVATE_KEY` to the key as one line instead:

```bash
awk 'NF {sub(/\r/, ""); printf "%s\\n", $0}' <slug>.<date>.private-key.pem
```

### 6.3 Panel environment

| Variable | Value | Notes |
|---|---|---|
| `API_UPSTREAM` | Internal URL of the API, e.g. `http://api:8080` (default) | Scheme, host and port only. A trailing slash or path makes nginx strip the `/api/` prefix and every API call fails. nginx resolves the host once, at start (§13.2). |

The panel image accepts request bodies up to 5 MB on `/api/` (GitHub deliveries); the API reads up to 5 MiB of a delivery.

## 7. Build and publish the images

From a clean checkout of the commit you want to ship:

```bash
git switch main && git pull --ff-only
git status --short          # must print nothing: the tag has to name exactly this code
make check                  # the only gate: no CI runs it yet
TAG=$(git rev-parse --short=12 HEAD)
docker build --platform linux/amd64 -t REGISTRY/pr-follower-api:$TAG   backend
docker build --platform linux/amd64 -t REGISTRY/pr-follower-panel:$TAG frontend
docker push REGISTRY/pr-follower-api:$TAG
docker push REGISTRY/pr-follower-panel:$TAG
```

- Match `--platform` to the server (`linux/arm64` on Graviton or Ampere). `make docker-build` builds the same two images for your machine's platform, tagged `latest`, which is fine for local tries but not for deploys.
- Give both images the **same tag** and deploy by that immutable tag, never `latest`.
- The API image is a static Go binary on Alpine 3.22 with CA certificates and the migrations embedded. The panel image is `nginx-unprivileged` 1.27 with the built SPA. Both run as non-root users.

## 8. Set up the TLS proxy

Whatever you use, it must:

1. Serve `app.example.com` with a publicly trusted certificate (full chain) and redirect HTTP to HTTPS.
2. Forward every path to the panel on HTTP 8080 and keep the `Host` header.
3. Accept request bodies of **at least 5 MB**. GitHub deliveries (a `push` listing many files) can exceed 1 MB, a common proxy default; a refused one shows `413` in Recent Deliveries.
4. Neither buffer nor compress `/api/v1/events/stream` (Server-Sent Events). The API sends a heartbeat every 25 s, so an idle timeout of 60 s or more keeps the stream open. A proxy that cuts streams after a maximum duration is tolerated: the browser reconnects and reloads.
5. Wait at least **300 s** for a response on `/api/*`, load balancer idle timeouts included: approve, merge and batch merge wait on GitHub, and the panel allows them 300 s.
6. Pass cookies through untouched and cache nothing under `/api/`.

The panel sends no HSTS header; add one at the proxy if you want it.

**Caddy** covers all of this with its defaults (automatic certificates, HTTP→HTTPS, immediate flushing of `text/event-stream`, no body limit, no response timeout):

```caddy
app.example.com {
	reverse_proxy 127.0.0.1:8080
	header Strict-Transport-Security "max-age=31536000"
}
```

**nginx** on the host needs the limits spelled out. A `location` that sets any `proxy_set_header` inherits none from the `server`, so each one lists them all:

```nginx
server {
    listen 80;
    server_name app.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name app.example.com;
    ssl_certificate     /etc/letsencrypt/live/app.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/app.example.com/privkey.pem;
    add_header Strict-Transport-Security "max-age=31536000" always;
    client_max_body_size 5m;                 # GitHub webhook deliveries

    location = /api/v1/events/stream {       # live updates (SSE)
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_buffering off;
        proxy_read_timeout 1h;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 300s;
    }
}
```

Point the proxy at `127.0.0.1:8080` when it runs on the host (the compose file in §9 publishes the panel on loopback only), or at `panel:8080` when it runs as a container on the same network.

## 9. Deploy

Any container platform works. This single-host example uses Docker Compose; the comments carry the requirements to translate to other platforms.

`/opt/pr-follower/compose.yml`:

```yaml
name: pr-follower

services:
  api:
    image: REGISTRY/pr-follower-api:TAG
    env_file: api.env                         # §6.1, mode 0600
    volumes:
      - ./secrets/github-app.pem:/run/secrets/github-app.pem:ro   # §6.2, readable by UID 10001
    restart: unless-stopped                   # the API exits if Postgres is unreachable at start
    stop_grace_period: 20s                    # it drains for up to 10 s on SIGTERM
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 3
      start_period: 30s
    logging:
      options: { max-size: 10m, max-file: "5" }

  panel:
    image: REGISTRY/pr-follower-panel:TAG     # same TAG as the api
    environment:
      API_UPSTREAM: http://api:8080           # §6.3
    ports:
      - "127.0.0.1:8080:8080"                 # only the TLS proxy on this host reaches it
    depends_on:
      api: { condition: service_healthy }     # nginx must resolve `api` when it starts
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 3
    logging:
      options: { max-size: 10m, max-file: "5" }
```

1. Put `compose.yml`, `api.env` and `secrets/github-app.pem` in `/opt/pr-follower` with the modes from §6.
2. Log in to the registry: `docker login REGISTRY`.
3. Start: `cd /opt/pr-follower && docker compose pull && docker compose up -d`.
4. Follow the API: `docker compose logs -f api`. A good start logs a single line, `INFO PR Follower Go v1.27 server listening port=8080`. Any `WARN` or `ERROR` at start means a missing or wrong setting: see [The API does not start](#the-api-does-not-start).
5. `docker compose ps` shows both services `healthy`.

Expose only the TLS proxy. The API and Postgres need no public port: browsers and GitHub webhooks both arrive through the panel's `/api/*` proxy.

## 10. Point DNS and smoke-test

1. Point `app.example.com` at the TLS proxy (A/AAAA or CNAME). Wait until `dig +short app.example.com` returns it and the proxy holds a valid certificate.
2. From your own machine:

   ```bash
   # HTTP redirects to HTTPS (Caddy answers 308)
   curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' http://app.example.com/
   #   301 https://app.example.com/

   # The panel is up
   curl -s https://app.example.com/healthz
   #   ok

   # The API is reachable through the panel, and protected
   curl -s -o /dev/null -w '%{http_code}\n' https://app.example.com/api/v1/auth/me
   #   401

   # Sign-in is configured, PUBLIC_URL is right, cookies are Secure
   curl -s -o /dev/null -D - https://app.example.com/api/v1/auth/github/login | grep -iE '^(location|set-cookie):'
   #   location: https://github.com/login/oauth/authorize?client_id=Iv23...&code_challenge=...&code_challenge_method=S256&redirect_uri=https%3A%2F%2Fapp.example.com%2Fapi%2Fv1%2Fauth%2Fgithub%2Fcallback&state=...
   #   set-cookie: pf_oauth=...; Path=/api/v1/auth/github; Max-Age=600; HttpOnly; Secure; SameSite=Lax

   # Webhooks are reachable and refuse unsigned deliveries
   curl -s -o /dev/null -w '%{http_code}\n' -X POST https://app.example.com/api/v1/webhooks/github -d '{}'
   #   401
   ```

   - `location: /?auth_error=not_configured`: `GITHUB_CLIENT_ID` or `GITHUB_CLIENT_SECRET` is missing.
   - A `redirect_uri` on another origin: `PUBLIC_URL` is wrong.
   - No `Secure` on the cookie: `PUBLIC_URL` does not start with `https://`.

3. Check the webhook secret with a `ping` signed the way GitHub signs deliveries:

   ```bash
   read -rs SECRET             # paste the webhook secret from the secret manager; it is not echoed
   body='{"zen":"smoke test"}'
   sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* //')
   curl -s -w '%{http_code}\n' -X POST https://app.example.com/api/v1/webhooks/github \
     -H 'Content-Type: application/json' -H 'X-GitHub-Event: ping' \
     -H "X-Hub-Signature-256: sha256=$sig" --data "$body"
   #   {"accepted":true}
   #   202
   unset SECRET
   ```

   `401` here means the deployed `GITHUB_WEBHOOK_SECRET` is not the secret you pasted.

## 11. Verify end to end with GitHub

Use the throwaway test repository. Keep the app's **Advanced → Recent Deliveries** open: every delivery must answer `202`.

1. **Redeliver the ping.** The `ping` GitHub sent when the app was created failed; open it and click **Redeliver**. Expect `202`.
2. **Sign in.** Open `https://app.example.com`, click **Entrar com GitHub** (*Sign in with GitHub*), authorize the app. You land in the workspace with your avatar. In the browser's dev tools, the `pf_session` cookie is `HttpOnly`, `Secure`, `SameSite=Lax` and expires in 30 days.
3. **Install.** Open **Seats de Repositórios → + Importar repositório → + Adicionar conta do GitHub** and install the app on the test repository only. GitHub returns you to the import screen with that account selected. The `installation` delivery answers `202`. A `401` means `GITHUB_WEBHOOK_SECRET` differs from the app's webhook secret.
4. **Import.** Follow the test repository. Its open PRs appear in its Seat.
5. **Webhook to live update.** Open **Linha do Tempo** and leave it open. Open a PR on the test repository: it appears in the timeline within seconds, without a reload, and its `pull_request` delivery answers `202`. This proves the deliveries, the database and the live stream through both proxies. In dev tools, the `events/stream` request stays open.
6. **Access removal.** On GitHub, remove the test repository from the installation (**Configure** on the installation page). Its Seat shows "Sem acesso" (*No access*) within seconds; add it back and the warning clears. Both `installation_repositories` deliveries answer `202`.
7. **Restart.** `docker compose restart api`, wait for `healthy`, reload the page and click **Sincronizar** (*Sync*). You are still signed in and the sync finishes. Had `TOKEN_ENCRYPTION_KEY` been unset, the restart would have made your GitHub token unreadable and signed you out here.
8. **Sign out.** **Sair** returns you to the sign-in screen; signing in again works.

Acting on PRs (approve, merge, `@dependabot` comments), the Dependabot analysis and the CODEOWNERS refresh are covered by [`real-github-verification.md`](real-github-verification.md); run it against this instance if you have not run it with a dev app.

## 12. Go-live checklist

- [ ] §10 and §11 pass, and every delivery in Recent Deliveries answers `202`.
- [ ] The API's start-up log has no `WARN` line (all of §6.1 is set; no ephemeral key, no missing app credentials).
- [ ] `PUBLIC_URL`, the Callback URL, the Setup URL and the Webhook URL share the same `https://` origin.
- [ ] Only the TLS proxy is reachable from the internet; the API and Postgres are not.
- [ ] The TLS proxy accepts 5 MB bodies, does not buffer the event stream and waits 300 s on `/api/*`.
- [ ] Every secret lives in the secret manager; `api.env` is `0600`; the downloaded `.pem` is deleted.
- [ ] Database backups are on, and a restore has been tried (§13.7).
- [ ] Someone watches Recent Deliveries and the API logs (§13.1).
- [ ] If the instance is for one organization only, the app is set to **Only on this account** (§4.7).

## 13. Operate

### 13.1 What to watch

- **Recent Deliveries** (app → **Advanced**). GitHub does not retry a failed delivery, and only deliveries from the last 3 days can be redelivered. Check after every deploy or outage and **Redeliver** the failures: ingestion is idempotent ([ADR-0011](adr/0011-idempotent-integrations.md)), so redelivering one that already arrived changes nothing. After a longer outage, users click **Sincronizar** to bring the repositories they follow back in line.
- **API logs.** Alert on any `ERROR`, and on `dependabot analysis failed`, `dependabot analysis queue full`, `activity listener disconnected`, `reading the activity log failed` and `pruning old activity failed` ([Troubleshooting](#troubleshooting)).
- **An uptime probe** on `https://app.example.com/api/v1/auth/me` expecting `401`: that answer needs the TLS proxy, the panel and the API all up. The panel's `/healthz` covers only the panel, and the API's own `/healthz` does not touch the database.
- **GitHub's emails** to installation owners about permission requests (§13.8).
- **Database size.** Activity is pruned after 7 days; expired sessions are not (§13.6).

### 13.2 Upgrades

1. See whether the release carries a migration:

   ```bash
   git diff --stat DEPLOYED_SHA HEAD -- backend/internal/infrastructure/database/migrations/
   ```

2. If it does, take a database snapshot first (or note the time for point-in-time recovery).
3. Build and push both images with the new tag (§7).
4. Set the new `TAG` on **both** services in `compose.yml`, then `docker compose pull && docker compose up -d`. Compose recreates the API (which migrates before it listens), then the panel.
5. Follow `docker compose logs -f api` until `server listening`, then rerun §10.
6. Redeliver the deliveries that failed during the restart (§13.1).

Users get the new SPA on their next page load (`index.html` is served `no-cache`), and their sessions survive.

nginx resolves `API_UPSTREAM` once, at start. If the API container is recreated or restarted without the panel and its address changes, the panel keeps answering `502`: `docker compose restart panel`. Deploying both with the same tag avoids it.

### 13.3 Rollback

Deploy the previous tag only if no migration shipped since it; otherwise fix forward. Migrations are forward-only, and an older binary does not know the newer schema. Restoring the pre-upgrade snapshot and deploying the old tag also works, but loses everything written since: keep it as a last resort.

### 13.4 Rotating secrets

| Secret | Steps | Effect on users |
|---|---|---|
| Client secret | **Generate a new client secret** on the app page; deploy it as `GITHUB_CLIENT_SECRET` and restart the API; sign in once to check; delete the old secret on GitHub. | None. |
| App private key | **Generate a private key** (an app can hold several); deploy it (§6.2) and restart the API; check the log has no `invalid GitHub App credentials` and that **Analisar agora** works on a Dependabot PR; delete the old key on GitHub. | None. Installation tokens already issued last at most an hour. |
| Webhook secret | Generate one (§2); set it on the app (**General → Webhook secret**) and as `GITHUB_WEBHOOK_SECRET`, restarting the API, as close together as you can; redeliver the deliveries that failed in between (they show `401`). | None. |
| Database password | Change it on the provider; update `DATABASE_URL`; restart the API. | A few seconds of downtime. |

### 13.5 Rotating `TOKEN_ENCRYPTION_KEY`

Stored GitHub tokens become unreadable and are treated as missing: each user is signed out at their next GitHub-backed action and signs in again. Rotate on a suspected leak, not routinely: generate a key (§2), replace it in the secret manager, restart the API.

### 13.6 Sessions and housekeeping

Sessions last 30 days. GitHub user tokens last 8 hours and are refreshed with a 6-month refresh token. Run these with `psql` on `DATABASE_URL`:

```sql
-- Sign everyone out
TRUNCATE sessions;

-- Sign one user out
DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE login = 'octocat');

-- Purge expired sessions (nothing does it automatically yet: schedule it weekly)
DELETE FROM sessions WHERE expires_at < extract(epoch FROM now());
```

A user can also revoke the app on GitHub (**Settings → Applications → Authorized GitHub Apps**): their token stops working and they are signed out at their next GitHub-backed action.

### 13.7 Backups and restore

The database holds users (with their GitHub tokens, encrypted), sessions, the repositories each user follows, PRs with their files, reviews, checks and statuses, Dependabot analyses, triage state (snooze, claim) and 7 days of activity. GitHub data comes back with a re-import or **Sincronizar**; follows and triage state exist only here.

To restore: restore the backup into a new instance, point `DATABASE_URL` at it, restart the API, redeliver recent failed deliveries, and have users click **Sincronizar**. The restore needs the `TOKEN_ENCRYPTION_KEY` that was in use when the backup was taken; with another key, users simply sign in again.

### 13.8 Changing the app's permissions or events

When a release needs a new permission, change it on the app page. GitHub asks every installation owner to accept it (by email and on the installation page). Until an owner accepts, their installation keeps the old permissions, and actions there fail with *Resource not accessible by integration*. New events that need no new permission start arriving at once. Deploy the code that uses them before or together with the change, and update §4.5–4.6.

### 13.9 Moving to another domain

Update `PUBLIC_URL`, `ALLOWED_ORIGIN` and the TLS proxy, and on the app the Homepage, Callback, Setup and Webhook URLs. Cookies belong to the old domain, so everyone signs in again. Keep the old domain redirecting to the new one.

### 13.10 Running more than one API replica

Every request may land on any replica: no sticky sessions are needed. Activity, sync jobs (`sync_jobs`) and operation cancels (`NOTIFY` on `pr_follower_operation_cancel`) are shared through Postgres (ADR-0012).

- Use direct Postgres connections: replicas wake each other with `LISTEN/NOTIFY` (a transaction-pooling proxy breaks it). Each replica holds two `LISTEN` connections (activity, cancels).
- A deploy that stops a replica mid-sync reports that sync as interrupted ("Sincronize de novo"); nothing it already did is lost.
- On upgrades, let one replica start (and migrate) before the others: migrations take no lock across processes.
- Budget `DB_MAX_OPEN_CONNS` + 2 connections per replica.

## Troubleshooting

### The API does not start

The API logs one `ERROR` line and exits:

| Log line | Cause | Fix |
|---|---|---|
| `failed to load configuration` … `read GITHUB_APP_PRIVATE_KEY_FILE` | The key file is missing at that path or not readable by UID 10001. | Mount it as in §6.2. |
| `failed to connect to database` | `DATABASE_URL` is wrong, Postgres is unreachable or refuses TLS, or a migration failed. It gives up after 4 quick attempts; the restart policy retries. | Check the URL, network and `sslmode`, and that the role owns the database (§3). |
| `invalid TOKEN_ENCRYPTION_KEY` | Not standard base64 of exactly 32 bytes. | Fix the copy first (§2 has the check). Generating a new key signs everyone out (§13.5). |
| `invalid GitHub App credentials` | The key is not a PEM RSA key, often because its newlines were lost. | Use the file (§6.2), or regenerate the one-line form. |
| `failed to start the activity hub` | The activity log cannot be read. | Check the database and the migration state. |

The API starts but warns:

| Log line | Effect |
|---|---|
| `GitHub sign-in disabled: set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET` | Login answers `not_configured`; every API route `401`. |
| `GitHub App credentials not set: background reads use the token of a signed-in follower` | Webhook-time reads need a signed-in follower; no Dependabot analysis. |
| `GitHub App credentials incomplete` | Only one of `GITHUB_APP_ID` and the key is set. |
| `TOKEN_ENCRYPTION_KEY not set: using an ephemeral key` | Every restart makes stored GitHub tokens unreadable and signs users out. |

### Sign-in

The sign-in screen shows why a login failed (`?auth_error=`):

| Code | Meaning | Fix |
|---|---|---|
| `not_configured` | The API has no `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET`. | Set both and restart the API. |
| `state_mismatch` | The login cookie was missing or did not match. | `PUBLIC_URL` must be the domain users open; "Request user authorization (OAuth) during installation" must be off; the proxy must not strip cookies; the attempt must finish within 10 minutes. |
| `access_denied` | The user cancelled on GitHub. | Nothing to fix. |
| `login_failed` | GitHub rejected the code exchange or the profile read. | Check the client secret, and that the Callback URL is exactly `PUBLIC_URL` + `/api/v1/auth/github/callback`. API logs show the cause. |

After a sign-out the app explains it: "Sua sessão expirou" when the session expired or was deleted (§13.6), "Seu acesso ao GitHub expirou" when the user's GitHub token expired, was revoked, or became unreadable (a changed or unset `TOKEN_ENCRYPTION_KEY`).

GitHub shows **"The redirect_uri is not associated with this application"** on its own page when the app's Callback URL differs from `PUBLIC_URL` + `/api/v1/auth/github/callback`.

### Webhook deliveries

What Recent Deliveries shows for a failed delivery:

| Response | Cause | Fix |
|---|---|---|
| `401` | Signature mismatch: `GITHUB_WEBHOOK_SECRET` differs from the app's webhook secret (or the delivery is over 5 MiB, which the API cuts short). | Set the same secret on both sides (§10 step 3 checks it), then redeliver. |
| `413` | A proxy refused the body. | Allow 5 MB bodies at the TLS proxy (§8). |
| `502` / `504` | The API is down, or the panel holds the address of an API container that was replaced. | `docker compose ps`; `docker compose restart panel` (§13.2). |
| `500` | The database or a GitHub read failed while storing the delivery. A `push` that touches CODEOWNERS fails here when nobody can read the file (no app credentials and no signed-in follower) instead of keeping stale rules. | Read the API log, fix the cause, redeliver. |
| *timed out* | The API took more than GitHub's 10 s. `pull_request` deliveries read the PR's changed files and `push` deliveries re-read CODEOWNERS, so a slow or rate-limited GitHub shows up here. | Redeliver. |
| SSL or connection error | DNS, firewall or the certificate chain. | Fix the proxy (§8); GitHub needs a trusted, complete chain while SSL verification is on. |

### Live updates

- **The timeline changes only on reload.** Something buffers `/api/v1/events/stream`. In dev tools the request should stay open; if it ends or receives nothing, turn off buffering and compression for that path at the TLS proxy (§8).
- **Changes arrive about 5 s late.** `LISTEN` is not working, usually behind a transaction-mode pooler, and the poll delivers them instead. Use a direct connection (§3).
- **The stream reconnects every few minutes.** The proxy caps connection length. It is harmless (the app reconnects and reloads), but raise the cap if you can.
- **`activity listener disconnected; reconnecting`** after a database restart or failover is expected; it reconnects by itself and catches up.

### PR data

- **A PR's files stay "not evaluated".** They have not been read yet. Check that the app has *Contents* and *Pull requests* read on that installation, and that the app credentials are set; otherwise a follower's token is needed.
- **Dependabot PRs stay "Sem análise".** The analysis reads lockfiles and repository tarballs as the GitHub App, so it needs `GITHUB_APP_ID` and the private key: without them the API warns at start, **Analisar agora** answers `503` "dependabot analysis needs the GitHub App credentials", and nothing is analysed. It also needs *Contents* read on the installation. Analyses run in the background (two at once, two minutes each); failures are logged as `dependabot analysis failed` with the PR id. Grouped updates and non-npm PRs stay "Sem análise" by design, with their own explanation.
- **Commit statuses missing** (Jenkins, Vercel and other CI that does not use check runs). They arrive as **Status** events, so the app must be subscribed to Status (§4.6) and have *Commit statuses* read. PRs stored before the subscription get theirs on their next status event, or with **Sincronizar**.
- **CODEOWNERS changes not picked up.** The rules are re-read when a push to the default branch touches `.github/CODEOWNERS`, `CODEOWNERS` or `docs/CODEOWNERS` (or when GitHub truncated a push at 20 commits), so the app must be subscribed to **Push**. The refresh reads as the GitHub App, or as a follower when the app credentials are not set.

### Actions on GitHub

Approve, merge and `@dependabot` comments are sent with the signed-in user's own token, so they are limited by what the app may do *and* by what that user may do.

- **"GitHub denied the action" with *Resource not accessible by integration*:** the installation owner has not accepted the app's *Pull requests* / *Contents* write permission. GitHub emailed them; they accept on the installation page (§13.8).
- **`409 checks_running`:** a check run or status of the head commit has not finished; approve and merge wait for it.
- **`429 rate_limited`** with a retry time: the user's GitHub rate limit (5,000 requests per hour) is spent. A sync stops there and says how many repositories it left and when to try again.
- **Approving your own PR** is refused: GitHub never allows it, and the app does not offer it. Merging it is allowed.
