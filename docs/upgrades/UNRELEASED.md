# Upgrading to vX.Y.Z

From **v0.1.0**. `/bump-version` replaces `vX.Y.Z` and `X.Y.Z` in this file with the release's version when it cuts the release.

- The API now has a health check that reaches the database through the panel, `GET /api/v1/healthz`, for readiness probes and uptime monitoring.
- API replicas can start together: migrations take a database lock. A single replica with a `Recreate` strategy is no longer needed.
- Optional: sign-in limited to members of your GitHub organizations, a read-only root filesystem for both containers, and `sslmode=verify-full` to the database.

## Before you start

- **No database migration** in this release: going back to v0.1.0 afterwards needs no backup restore. Take your usual backup anyway.
- **Image tags have no `v`:** release `vX.Y.Z` is `:X.Y.Z`.
- Take both image digests from the release notes and verify each one before deploying it (`gh` must be signed in: `gh auth login`, or `GH_TOKEN`):

  ```bash
  for pair in api@sha256:API_DIGEST panel@sha256:PANEL_DIGEST; do
    gh attestation verify "oci://ghcr.io/muriio/pr-follower-${pair%%@*}@${pair#*@}" \
      --repo muriIo/pr-follower \
      --signer-workflow muriIo/pr-follower/.github/workflows/release.yml \
      --source-ref refs/tags/vX.Y.Z --deny-self-hosted-runners
  done
  ```

  Each must print a successful verification and exit 0. Deploy exactly these digests.

## Required steps

### 1. Point the API's health checks at the right endpoints

The API now answers two health checks. Keep the restart check on `/healthz`; route traffic with `/api/v1/healthz`.

| Path | Answers | Use it for |
|---|---|---|
| `GET /healthz` (port 8080 of the api) | `200 {"status":"ok"}` while the process runs; never touches the database | liveness: restarting a stuck container |
| `GET /api/v1/healthz` (the api directly, or `https://<your domain>/api/v1/healthz` through the panel) | `200 {"status":"ok"}`, or `503 {"status":"unavailable"}` when the database does not answer within 2 s | readiness, the Compose health check, external uptime monitoring |

A starting API may now wait up to **5 minutes** for another replica's migration before it listens, so give it that long to start.

**Kubernetes** (`deploy/kubernetes/api.yaml`, container `api`): replace the `readinessProbe` and `livenessProbe` with these three probes (and drop `initialDelaySeconds` from liveness):

```yaml
startupProbe:
  httpGet: { path: /healthz, port: http }
  periodSeconds: 10
  failureThreshold: 36        # up to 6 minutes to start
readinessProbe:
  httpGet: { path: /api/v1/healthz, port: http }
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 3
livenessProbe:
  httpGet: { path: /healthz, port: http }
  periodSeconds: 10
```

With the database down, every API pod then leaves the Service and the panel answers `502` until it is back; the pods are not restarted.

**Docker Compose** (`deploy/compose/compose.yml`, service `api`):

```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/api/v1/healthz"]
  interval: 10s
  timeout: 3s
  retries: 3
  start_period: 6m
```

**ECS:** keep the api container's `healthCheck` on `/healthz` (an unhealthy essential container replaces the whole task, which a database outage must not do), and set its `"startPeriod": 300`. Set the service's health-check grace period to 360 s: `aws ecs update-service --cluster <cluster> --service <service> --health-check-grace-period-seconds 360`.

**Uptime monitoring:** point it at `https://<your domain>/api/v1/healthz`, expecting `200`. The panel's `https://<your domain>/healthz` only says nginx is up.

### 2. Deploy the new version

Set the same version on both images, pinned by the digests you verified:

- **Kubernetes** (`kustomization.yaml`):

  ```yaml
  images:
    - name: ghcr.io/muriio/pr-follower-api
      newTag: "X.Y.Z"
      digest: "sha256:API_DIGEST"
    - name: ghcr.io/muriio/pr-follower-panel
      newTag: "X.Y.Z"
      digest: "sha256:PANEL_DIGEST"
  ```

- **Docker Compose:** `PR_FOLLOWER_VERSION=X.Y.Z` in `.env`, then `docker compose pull && docker compose up -d`.
- **ECS:** a new task definition revision with `ghcr.io/muriio/pr-follower-api:X.Y.Z@sha256:API_DIGEST` and `ghcr.io/muriio/pr-follower-panel:X.Y.Z@sha256:PANEL_DIGEST` (or your ECR copies of those digests), then update the service.

## Optional steps

### Run several API replicas again

If you run one API replica with a `Recreate` strategy because migrations used to race, you can return to rolling updates: the first replica to start now takes a Postgres advisory lock and migrates, the others wait for it. On Kubernetes set `replicas: 2` and remove `strategy: { type: Recreate }`; budget `DB_MAX_OPEN_CONNS` + 2 database connections per replica. While a replica waits, `SELECT pid, granted FROM pg_locks WHERE locktype = 'advisory';` shows it.

### Read-only root filesystem

Both containers work with a read-only root filesystem, given these writable paths:

| Container | Writable path | Why |
|---|---|---|
| panel | `/tmp` | nginx's pid and temp files; webhook bodies over 8 KB are buffered there |
| panel | `/etc/nginx/conf.d`, writable by UID 101 | the entrypoint renders `default.conf` from the template at every start |
| api | `/tmp` | nothing today; keeps Go's temp directory usable |

Kubernetes, in each container's `securityContext` add `readOnlyRootFilesystem: true`, and mount `emptyDir` volumes:

```yaml
# panel
volumeMounts:
  - { name: tmp, mountPath: /tmp }
  - { name: conf, mountPath: /etc/nginx/conf.d }
# … and in the pod spec
volumes:
  - { name: tmp, emptyDir: { sizeLimit: 64Mi } }
  - { name: conf, emptyDir: { sizeLimit: 1Mi } }
# api: the same tmp volume mounted at /tmp
```

Docker Compose:

```yaml
api:
  read_only: true
  tmpfs: [/tmp]
panel:
  read_only: true
  tmpfs:
    - /tmp
    - /etc/nginx/conf.d:uid=101,gid=101
```

### Limit sign-in to your GitHub organizations

New optional variable **`ALLOWED_GITHUB_ORGS`** (not a secret): comma-separated organization logins, e.g. `acme` or `acme,acme-labs`. Only members of at least one of them can sign in, and users who leave lose access within 15 minutes. Empty or unset: sign-in stays open to any GitHub account, as in v0.1.0.

1. On the GitHub App's **Permissions & events** page, check that **Organization permissions → Members** is *Read-only*. If you add it now, each organization owner must accept the new permission (GitHub emails them; they approve it on the installation's settings page). Until then every sign-in fails closed with "could not verify".
2. Install the app on each organization you list.
3. Set `ALLOWED_GITHUB_ORGS` in the API's environment, next to `GITHUB_APP_ID` and the private key (the API refuses to start with the allowlist but without them).
4. Restart every API replica. It logs `sign-in restricted to github organization members`; within 15 minutes, `revoked access of a user who left the allowed github organizations` for each non-member who had access.

### Verify the database server's certificate

`sslmode=require` encrypts but accepts any certificate. `verify-full` also checks the server is your database. Mount your provider's CA file into the api container, readable by UID 10001 (on AWS RDS: `https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem`, or the per-region bundle), and change `DATABASE_URL` to end in `?sslmode=verify-full&sslrootcert=/path/to/ca.pem`, with the host exactly as your provider names the endpoint. Restart the API; an `x509` error in its log means the file or the host name is wrong.

### Keep plain settings out of the Secret (Kubernetes)

`api.yaml` now also reads an optional ConfigMap, `pr-follower-config`. The secret variables (`DATABASE_URL`, `GITHUB_CLIENT_SECRET`, `GITHUB_WEBHOOK_SECRET`, `TOKEN_ENCRYPTION_KEY`, the private key) stay in Secrets; the rest (`PUBLIC_URL`, `ALLOWED_ORIGIN`, `GITHUB_CLIENT_ID`, `GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `ALLOWED_GITHUB_ORGS`, `DB_MAX_*`) may move to it:

```yaml
envFrom:
  - secretRef: { name: pr-follower }
  - configMapRef: { name: pr-follower-config, optional: true }
```

Whatever store you use, the API reads its variables and key file once, at start: after rotating a secret, restart it (`kubectl rollout restart deploy/<api deployment>`).

## Behaviour changes

- Claiming or snoozing a PR now requires following its repository; for a repository the user does not follow, the API answers `404`, as for a PR that does not exist. Users who follow the repository see no difference.
- The panel's browser icon is the PR Follower mark.

## Check that it worked

```bash
curl -s https://<your domain>/healthz            # ok
curl -s https://<your domain>/api/v1/healthz     # {"status":"ok"}
```

Then open the panel and sign in. On the GitHub App's **Advanced → Recent Deliveries**, new deliveries answer `202`; redeliver any that failed during the deploy.

## Roll back

No migration shipped since v0.1.0: deploy the v0.1.0 digests again. If you changed the probes in step 1, put them back too (v0.1.0 has no `/api/v1/healthz`, so a readiness probe or Compose health check on it would never pass).
