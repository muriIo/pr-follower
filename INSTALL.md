# Installing PR Follower

PR Follower runs on **your** infrastructure, with **your** GitHub App: no data goes through anyone else's servers. It is two containers and a Postgres database, so it runs on any cloud, on-premises, or a single VM.

This guide takes about an hour the first time. For every detail behind a step (each permission, each variable, troubleshooting, operations) see the full runbook, [`docs/production-setup.md`](docs/production-setup.md).

## 0. What you are deploying

```
browser ───┐
           ├─ https ─▶ TLS proxy ─▶ panel (nginx :8080) ─ /api/* ─▶ api (Go :8080) ─▶ Postgres 17
GitHub ────┘           (public)     serves the web app               │
(webhooks)                                                            └─ https ─▶ GitHub
```

| Piece | Image | Notes |
|---|---|---|
| **panel** | `ghcr.io/muriio/pr-follower-panel:<version>` | Serves the web app and forwards `/api/*` to the API. The only thing the proxy talks to. |
| **api** | `ghcr.io/muriio/pr-follower-api:<version>` | Talks to Postgres and GitHub. Creates and migrates its tables when it starts. Any number of replicas. |
| **Postgres 17** | any | Managed, an operator, or the bundled container. Holds all state: back it up. |
| **TLS proxy** | any | Caddy, nginx, Traefik, an ingress or a cloud load balancer, with a **publicly trusted** certificate (GitHub refuses to deliver webhooks otherwise). |

Use the **same version** for both images. Versions are the [releases](https://github.com/muriIo/pr-follower/releases); the images are public, built for `linux/amd64` and `linux/arm64`, and signed: `gh attestation verify oci://ghcr.io/muriio/pr-follower-api:<version> --owner muriIo` checks that one came from this repository's release workflow ([runbook §7](docs/production-setup.md#7-get-the-images)). To build your own instead: `docker build -t <registry>/pr-follower-api:<tag> backend` and `docker build -t <registry>/pr-follower-panel:<tag> frontend`.

**You need:** a domain you control (e.g. `prfollower.example.com`), a place to run containers, Postgres 17, and owner access to the GitHub organization (or your own account) that will own the app.

## 1. Create your GitHub App

On GitHub: **Organization → Settings → Developer settings → GitHub Apps → New GitHub App** (or the same under your personal settings). Replace `prfollower.example.com` with your domain everywhere.

| Field | Value |
|---|---|
| Homepage URL | `https://prfollower.example.com` |
| Callback URL | `https://prfollower.example.com/api/v1/auth/github/callback` |
| Expire user authorization tokens | on |
| Request user authorization (OAuth) during installation | **off** |
| Setup URL | `https://prfollower.example.com/` with **Redirect on update** on |
| Webhook | active; URL `https://prfollower.example.com/api/v1/webhooks/github`; secret from §2 |

**Repository permissions:** Pull requests *read & write*, Contents *read & write*, Checks *read-only*, Commit statuses *read-only*, Metadata *read-only*. **Organization permissions:** Members *read-only* (team entries in CODEOWNERS, and the organization check of `ALLOWED_GITHUB_ORGS`).

**Events:** Pull request, Pull request review, Push, Status, Check run.

**Where it can be installed:** *Only on this account* for a single organization; *Any account* to let anyone sign in and install it. To let only your organization's members sign in at all, also set `ALLOWED_GITHUB_ORGS` (§3).

Then, on the app's page, collect: the **App ID**, the **Client ID**, a new **client secret**, the app's URL name (`https://github.com/apps/<slug>`), and a **private key** (a `.pem` download). Why each setting matters: [runbook §4–5](docs/production-setup.md#4-create-the-production-github-app).

## 2. Generate the secrets

```bash
openssl rand -hex 32                    # webhook secret: paste it into the app's webhook too
openssl rand -base64 32                 # token encryption key (encrypts stored GitHub tokens)
openssl rand -base64 32 | tr -d '/+='   # database password
```

Keep them in a secret manager. Changing the encryption key later signs every user out.

## 3. The configuration

The API reads its configuration from environment variables:

| Variable | Value |
|---|---|
| `PUBLIC_URL` | `https://prfollower.example.com` |
| `DATABASE_URL` | `postgres://prfollower:PASSWORD@HOST:5432/pr_follower?sslmode=require` (the role must **own** the database) |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | from the app |
| `GITHUB_APP_ID`, `GITHUB_APP_SLUG` | from the app |
| `GITHUB_APP_PRIVATE_KEY_FILE` | path of the mounted `.pem`, readable by UID 10001 (or `GITHUB_APP_PRIVATE_KEY` with its contents on one line) |
| `GITHUB_WEBHOOK_SECRET` | from §2. The API refuses to start on an `https` `PUBLIC_URL` without it. |
| `TOKEN_ENCRYPTION_KEY` | from §2 |
| `ALLOWED_ORIGIN` | `https://prfollower.example.com` |
| `ALLOWED_GITHUB_ORGS` | optional: `acme` (or `acme,acme-labs`). Only members of these organizations can sign in, and people who leave lose access within 15 minutes. Empty: any GitHub account can sign in. The app must be installed on each organization, with its *Members* permission accepted. [Runbook §13.11](docs/production-setup.md#1311-restricting-sign-in-to-your-organization). |

The panel needs only `API_UPSTREAM`, the API's internal address (default `http://api:8080`: scheme, host and port, no path). Every variable: [runbook §6](docs/production-setup.md#6-write-the-configuration).

## 4. Deploy: pick a path

### A. One machine with Docker Compose

The simplest path: any Linux VM or server with Docker, on any provider. [`deploy/compose/`](deploy/compose) runs Postgres, the API, the panel and [Caddy](https://caddyserver.com), which gets and renews the HTTPS certificate on its own.

1. Point your domain's DNS at the machine, and open ports 80 and 443.
2. Copy `deploy/compose/` to the machine, then:

   ```bash
   cp .env.example .env && chmod 600 .env     # fill in every value (§1-3)
   install -d secrets
   sudo install -m 0400 -o 10001 -g 10001 <your-app>.private-key.pem secrets/github-app.pem
   docker compose up -d
   docker compose ps                          # all services healthy after ~30 s
   ```

3. Using a managed Postgres instead of the bundled one: in `.env`, delete `COMPOSE_PROFILES` and set `DATABASE_URL`.

### B. Kubernetes

[`deploy/kubernetes/`](deploy/kubernetes) has plain manifests (a Kustomization): both deployments, their services and an ingress. Bring your own Postgres (managed, or an operator such as CloudNativePG).

```bash
kubectl create namespace pr-follower
kubectl -n pr-follower create secret generic pr-follower --from-env-file=api.env      # the variables of §3
kubectl -n pr-follower create secret generic pr-follower-github-app --from-file=github-app.pem=<your-app>.private-key.pem
# edit ingress.yaml (your domain, TLS) and the image tags in kustomization.yaml, then:
kubectl apply -k deploy/kubernetes
```

The ingress annotations are for ingress-nginx; with another controller, set the four requirements of path C below.

### C. Any managed container platform

Cloud Run, ECS/Fargate, Azure Container Apps, Fly.io, Render, Nomad, OpenShift…: run the two images and give them these properties.

| Requirement | Why |
|---|---|
| The **panel** is the only public service, on HTTPS with a publicly trusted certificate, behind one domain. | Browsers and GitHub must reach a single origin; the session cookie depends on it. |
| The **api** is reachable from the panel only (private service or internal network), and reaches the internet over HTTPS. | It calls `github.com`, `api.github.com` and `codeload.github.com`. |
| Request bodies up to **5 MB**. | GitHub webhook deliveries can exceed the common 1 MB default. |
| Request timeout of at least **300 s**, and no response buffering. | Merges wait on GitHub; the live updates are a long-lived event stream. |
| Health checks on `GET /healthz` (port 8080) for both. | |
| Secrets (§3) from the platform's secret store; the private key mounted as a file or set as `GITHUB_APP_PRIVATE_KEY`. | |
| Postgres 17, managed, with backups. Use a direct connection or a **session**-mode pooler. | The API keeps a `LISTEN` connection for live updates across replicas. |

A worked example on AWS: [`docs/aws-setup.md`](docs/aws-setup.md).

## 5. Check that it works

1. `https://prfollower.example.com/healthz` answers `ok`.
2. Open the domain and **sign in with GitHub**.
3. **Seats de Repositórios → + Importar repositório → + Adicionar conta do GitHub**: install the app on a test repository, then follow it.
4. Open a PR on that repository: it appears in the panel within seconds. On the app's **Advanced → Recent Deliveries** page, deliveries answer `202`.

Something off? [Runbook troubleshooting](docs/production-setup.md#troubleshooting).

## 6. Keep it running

- **Upgrade:** back up the database, set the new version (`PR_FOLLOWER_VERSION` in `.env`, or the image tags), and redeploy (`docker compose pull && docker compose up -d`). Migrations run on start and only move forward: going back to the previous version is safe only if the release you are leaving shipped no migration (its release notes say); otherwise upgrade to a fix, and keep restoring the backup as a last resort, since it loses what was written since ([runbook §13.2–13.3](docs/production-setup.md#132-upgrades)).
- **Back up** Postgres daily (`pg_dump`, or your provider's snapshots). The Compose bundle keeps it in the `pr-follower_postgres_data` volume.
- **Rotate** secrets, change domains, run more replicas: [runbook §13](docs/production-setup.md#13-operate).
- **Security issues:** report them privately ([`SECURITY.md`](SECURITY.md)).
