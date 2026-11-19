# Plan: devops feedback from the first company deployment

Date: 2026-11-19 · Source: the devops team's notes after deploying v0.1.0 (11 items).

**Status: done (2026-11-19)**, in one branch rather than the five PRs listed under "Order of work": ADR-0016 and the migration lock, `GET /api/v1/healthz`, the read-only and probe changes in `deploy/`, every doc item, and the release flow that now ships an upgrade guide with each release (`docs/upgrades/`). The operator steps for adopting these changes are in `docs/upgrades/UNRELEASED.md`.

This plan checks each item against the code, decides what to change, says how to validate it, and adds a short **Learn** note where the item touches something worth understanding. Items are grouped into PRs at the end.

**Legend:** 🐛 the docs or manifests are wrong · 🔧 code change · 📝 docs only · 🎓 learning note

> **Validating docs: always use Context7.** Every library, CLI, or cloud claim in this plan or in the doc changes it leads to (goose's locker API, nginx-unprivileged paths, `gh attestation` flags, RDS CA bundle URL, AWS Load Balancer Controller annotations, External Secrets fields, Kubernetes probe fields) gets checked against current docs with `ctx7` before it's written down: `npx ctx7@latest library <name> "<topic>"`, then `npx ctx7@latest docs <id> "<topic>"`. Don't rely on memory or on this plan's wording; tools change between versions. If `ctx7` hits a quota, stop and say so (`npx ctx7@latest login` or `CONTEXT7_API_KEY`) instead of falling back to memory.

---

## Summary

| # | Item | Verdict after reading the code | Change | Size |
|---|------|-------------------------------|--------|------|
| 1 | Release `v0.1.0` vs image tag `0.1.0` | Confirmed: `docker/metadata-action` `type=semver,pattern={{version}}` strips the `v` | 📝 one sentence in INSTALL, runbook §7.1, `.env.example`, kustomization | XS |
| 2 | Postgres 17 "required", 18 worked | Confirmed: CI, dev stack and docs all say 17; nothing in the code needs 17 | 📝 "tested on 17, works on 18" | XS |
| 3 | `api.yaml` has `replicas: 2`, runbook says migrations take no lock | Confirmed: `runGooseForwardMigrations` uses the global `goose.UpContext`; `gooseMu` is a process-local mutex | 🔧 goose session locker (advisory lock) | M |
| 4 | Panel needs writable `/tmp` and `/etc/nginx/conf.d` | Confirmed: `nginx-unprivileged` keeps its temp paths and pid in `/tmp`; the entrypoint renders the template into `conf.d` | 🔧 manifests get `readOnlyRootFilesystem` + `emptyDir`s; 📝 hardening paragraph | S |
| 5 | Panel `/healthz` says nothing about the API | Confirmed: nginx answers `/healthz` itself; the API's `/healthz` is at the root, which the panel does not proxy | 🔧 `GET /api/v1/healthz` in the API (checks the database) | S |
| 6 | Creating the App sends a ping that fails | Expected GitHub behaviour | 📝 warn + "Redeliver" | XS |
| 7 | Hard to find where to create the App | Docs give a menu path only | 📝 direct URL; events appear after permissions | XS |
| 8 | Verify by digest + `--repo` + `--signer-workflow`; pin by digest | Our command (`--owner`) is the weakest form | 📝 stronger command; release notes print digests; 🎓 analysis below | S |
| 9 | RDS with `verify-full` + CA bundle | `aws-setup.md` uses `sslmode=require` only | 📝 make `verify-full` the recommended option | S |
| 10 | Kubernetes on AWS (ALB) | Only ingress-nginx annotations, and ingress-nginx is retired | 📝 ALB ingress example (+ a note on shared ALBs) | S |
| 11 | Secrets with External Secrets (GitOps) | Only `kubectl create secret` | 📝 ExternalSecret example | S |

Devops' manifests were shared for reference; what they confirmed is summarised in "What devops' manifests confirmed" below.

---

## 1. Image tag is the release version without the `v` 🐛📝

**What happens.** `release.yml` runs on a `v*.*.*` git tag and tags images with `docker/metadata-action` `type=semver,pattern={{version}}` and `{{major}}.{{minor}}`. Semver patterns drop the `v`, so release `v0.1.0` produces `:0.1.0`, `:0.1` and `:sha-<commit>`. Nobody wrote that down.

**Fix (docs).** One sentence wherever a version is asked for: *"The image tag is the release name without the leading `v` (release `v0.1.0` → image `:0.1.0`)."*

- `INSTALL.md` §0 (the "Use the same version" paragraph)
- `docs/production-setup.md` §7.1
- `deploy/compose/.env.example` (comment above `PR_FOLLOWER_VERSION`)
- `deploy/kubernetes/kustomization.yaml` (comment on `images:`)
- `docs/aws-setup.md` wherever it sets the tag

**Not chosen:** also publishing `:v0.1.0`. It doubles the tags for one sentence of docs, and `v`-tags in container registries are not the convention.

**Validate.** `docker buildx imagetools inspect ghcr.io/muriio/pr-follower-api:0.1.0` works; `:v0.1.0` fails with "not found".

---

## 2. Postgres: tested on 17, works on 18 📝

**What the code needs.** `LISTEN/NOTIFY`, `IF NOT EXISTS` DDL and ordinary SQL: nothing specific to 17. Devops ran 18.3 with migrations, `LISTEN`, sign-in, import and webhooks working.

**Fix (docs).** Replace "Postgres 17" with **"PostgreSQL 17 or newer (tested on 17; works on 18)"** in `INSTALL.md` (§0 diagram, table, "You need", path C table), `production-setup.md` §3, `aws-setup.md` (keep 17 as the version the guide creates, but say 18 works).

**Decided: no CI job on 18 yet.** The docs only say "tested on 17, works on 18"; CI stays on `postgres:17-alpine`. Revisit if 18 becomes the default somewhere or a migration uses version-specific SQL.

**Validate (optional, once).** Locally: `docker run -d -p 5434:5432 -e POSTGRES_PASSWORD=x postgres:18-alpine`, then `TEST_DATABASE_URL=postgres://postgres:x@localhost:5434/postgres?sslmode=disable go test ./internal/...`.

🎓 **Learn.** Pin a *minimum* version in docs, not an exact one, unless the code uses a feature that later versions removed. The honest wording is "tested on X; known to work on Y" and the way to make Y a promise is to test it in CI.

---

## 3. Migrations: take a lock instead of relying on one replica first 🐛🔧

**What happens.** `backend/internal/infrastructure/database/postgres.go` `runGooseForwardMigrations` calls the package-global `goose.UpContext`. `gooseMu` only serialises goroutines **inside one process**. Two replicas starting together on an empty database both read `goose_db_version`, both see "nothing applied", and both run `00001_…`. Today that mostly works because the DDL is `IF NOT EXISTS` (ADR-0011), but:

- a data migration (an `UPDATE`/`INSERT`) would run twice;
- `IF NOT EXISTS` doesn't make concurrent DDL safe: two transactions creating the same table at once can still fail with `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`, so one replica may crash-loop on its first start (then come back fine, which is noise at best);
- `goose_db_version` has no unique constraint on `version_id`, so both processes can record the same version twice;
- §13.10 tells people to start one replica first, while our own `api.yaml` ships `replicas: 2` with the default `RollingUpdate`. The manifest contradicts the runbook.

**Options.**

| Option | Pros | Cons |
|---|---|---|
| A. `api.yaml` → `replicas: 1`, `strategy: Recreate` (what devops did) | No code | Downtime on every deploy; doesn't help Compose `--scale`, ECS with 2 tasks, or anyone who raises replicas; contradicts ADR-0012 "any replica" |
| **B. goose session locker (recommended)** | Fixes the cause on every platform; keeps `replicas: 2`; goose already ships it (`github.com/pressly/goose/v3/lock`, we are on v3.30.0) | Code change, a real-Postgres test |
| C. A Kubernetes `Job` / init container that migrates | Common pattern | Platform-specific; still needs B for Compose/ECS; a new binary flag |

**Why option A doesn't generalise.** The race is "two API processes start at the same moment against a database with pending migrations", whatever starts them. `replicas: 1` + `Recreate` lives only in `deploy/kubernetes/api.yaml`; it doesn't change the API, so every other way of running it needs its own manual setup:

- **Kubernetes:** safe only while nobody sets `replicas: 3` or adds an HPA; then an upgrade starts several new pods together, all with the new migrations pending.
- **Compose:** no `Recreate` concept. One API container today is safe, but `docker compose up -d --scale api=2` (or `deploy.replicas: 2`), which the docs' "any number of replicas" invites, starts both at once.
- **ECS:** `desiredCount: 2` starts two tasks together on a first deploy or an upgrade. Approximating `Recreate` needs `minimumHealthyPercent: 0` / `maximumPercent: 100` and a count of 1: another platform-specific trick, with downtime.

With the lock, the fix is in the API itself: on any platform and any replica count, the first process takes `pg_advisory_lock`, the others wait, then find nothing left to apply.

**Correction: it's `replicas: 1` that prevents the race, not `Recreate`.** With one replica and the default `RollingUpdate`, only the new pod migrates on start (the old one already migrated when it started and never does again), so there's still one migrator. What `Recreate` adds is that **old code never runs against the new schema**. That matters only if a migration breaks the previous version, which forward-only, additive migrations (ADR-0004) shouldn't do. Ask devops whether that's why they chose it or whether it was just caution. Either way, `Recreate` costs downtime on every deploy for something the lock (plus additive migrations) already covers.

**Plan for B (test first, ADR-0002).**

1. **Red:** in `internal/infrastructure/database`, a test gated on `TEST_DATABASE_URL` (like `activity_signal_test.go`) that opens two separate `*sql.DB` to a fresh schema/database and runs the migrator from both at once (`errgroup`), then asserts both return `nil` and `goose_db_version` has each version exactly once. Run it a few times against the current code to watch it fail (or flake: race tests are probabilistic, so add `-count=20`).
2. **Green:** for the `postgres` dialect, switch to the Provider API:
   ```go
   locker, err := lock.NewPostgresSessionLocker() // pg_advisory_lock on a fixed id
   provider, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS,
       goose.WithSessionLocker(locker))
   _, err = provider.Up(ctx)
   ```
   Keep SQLite (tests) on the existing path or the Provider without a locker. The Provider has no global state, so `gooseMu` and the `SetBaseFS`/`SetDialect` globals can go. Before coding, check the exact option names with `ctx7` / the v3.30.0 source (`~/go/pkg/mod/github.com/pressly/goose/v3@v3.30.0/lock/`), and how long the locker waits by default (`lock.WithLockTimeout`), since a replica waiting on the lock must not be killed by its liveness probe.
3. **Cancellable (ADR-0007):** `provider.Up(ctx)` takes the start-up context; the lock wait must honour it.
4. **Manifest:** keep `replicas: 2`; replace `livenessProbe.initialDelaySeconds: 30` with a `startupProbe` (e.g. `failureThreshold: 30`, `periodSeconds: 10` = up to 5 min) so a replica waiting for another's migration is not restarted.
5. **Docs:** `production-setup.md` §13.10 and §13.2: "migrations take a Postgres advisory lock; replicas can start together". Remove "let one replica start first".
6. **ADR (decided: yes).** A new ADR (`NNNN-migrations-take-an-advisory-lock.md`, the six headings from `docs/adr/README.md`, added to the index table) that builds on ADR-0004, plus an `adrcheck` gate that fails if the global, lock-free goose API (`goose.Up`, `goose.UpContext`, …) appears in `backend/internal/`. Write the gate test first and watch it go red against today's `postgres.go`.

**Validate end to end.** `docker compose -f docker-compose.local.yml down -v`, then start two API processes against the empty DB at the same moment; both healthy, `SELECT version_id, count(*) FROM goose_db_version GROUP BY 1 HAVING count(*) > 1` is empty. In Postgres while one waits: `SELECT * FROM pg_locks WHERE locktype = 'advisory'`.

🎓 **Learn.** A `sync.Mutex` protects memory in one process; replicas are separate processes, so they need a lock that lives where they meet: the database. `pg_advisory_lock(id)` is a lock on an arbitrary number, held by a **session** (connection), released when that connection closes, so a crashed replica can't leave it stuck. That's why goose calls it a *session* locker. This is the same idea as ADR-0012 ("state another replica needs goes in Postgres"); a lock is state.

---

## 4. Read-only root filesystem: what each container must write 🐛🔧📝

**What happens.**

- **Panel** (`nginxinc/nginx-unprivileged`): the image keeps the pid file and every temp path (`client_body_temp`, `proxy_temp`, …) under `/tmp`, because it can't write to `/var/cache/nginx` as non-root. A webhook bigger than `client_body_buffer_size` (two memory pages by default: 8 KB on x86-64) is spooled to `/tmp/client_temp`: that's the log line devops saw, and it is normal. And the image entrypoint (`20-envsubst-on-templates.sh`) renders `/etc/nginx/templates/default.conf.template` into `/etc/nginx/conf.d/default.conf` at every start.
- **API**: a static Go binary; it writes nothing on disk. Devops ran it read-only with `/tmp` writable and saw no issue.

**Fix (manifests, so the secure default ships).**

- `deploy/kubernetes/panel.yaml`: `readOnlyRootFilesystem: true` + `emptyDir` on `/tmp` and on `/etc/nginx/conf.d`.
- `deploy/kubernetes/api.yaml`: `readOnlyRootFilesystem: true` + `emptyDir` on `/tmp` (cheap insurance: Go's `os.TempDir` and any future library).
- `deploy/compose/compose.yml`: `read_only: true` + `tmpfs: [/tmp, /etc/nginx/conf.d]` for the panel, `tmpfs: [/tmp]` for the API. Check that the Compose healthchecks still pass.
- Optional: `client_body_buffer_size 1m;` in `location /api/` so most deliveries stay in memory. Not required: `/tmp` must stay writable anyway (pid, proxy temp), so this is a performance nicety, not a fix.

**Fix (docs).** A short **Hardening** paragraph in `production-setup.md` (and a pointer from INSTALL path B/C): "Both images run as non-root and support a read-only root filesystem. The panel needs `/tmp` (nginx temp files: large webhook bodies are buffered there) and `/etc/nginx/conf.d` (the config is rendered from a template at start) writable; the API needs only `/tmp`." Plus the ECS equivalent in `aws-setup.md` (`readonlyRootFilesystem` + a bind-mount volume).

**Validate.**
```bash
docker run --rm --read-only --tmpfs /tmp --tmpfs /etc/nginx/conf.d -p 8081:8080 \
  -e API_UPSTREAM=http://host.docker.internal:8080 ghcr.io/muriio/pr-follower-panel:0.1.0
# then POST a 2 MB body to /api/v1/webhooks/github: expect the API's 401 (bad signature), not a 500
head -c 2000000 /dev/zero | tr '\0' a | curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST --data-binary @- http://localhost:8081/api/v1/webhooks/github
```
Repeat without `--tmpfs /tmp` to see the failure devops predicted (500 and `open() "/tmp/client_temp/…" failed (30: Read-only file system)`).

🎓 **Learn.** `readOnlyRootFilesystem` turns "an attacker who gets code execution can drop files" into "can't, except in the few places you allowed". The work is listing those places, which is why images should document them.

---

## 5. A health check that reaches the API 🐛🔧

**What happens.** `GET /healthz` on the public domain is answered by nginx (`location = /healthz` → `return 200 "ok"`) without touching the API. The API's own `/healthz` is mounted at the router root (`backend/internal/infrastructure/server/router.go:19`), and nginx only proxies `/api/`, so from outside it is unreachable (`/api/healthz` → 404 from the SPA fallback/API). The only external signal today is `GET /api/v1/auth/github/login` → 302.

**Fix (code).** Add `GET /api/v1/healthz` in the API, public (no session), which **pings the database** with a short timeout:

- `200 {"status":"ok"}` when the DB answers, `503 {"status":"unavailable"}` otherwise. No error details (it's public).
- Keep the root `/healthz` as is: it's the **liveness** probe (process alive). If liveness depended on the DB, a database blip would restart every API pod at once, which makes the outage worse.
- No nginx change: `location /api/` already forwards it.

Test first: a handler test in `internal/adapter/http` for both 200 and 503 (a fake pinger), and a router test that it needs no session. Wire the pinger through a small port so the handler doesn't import `database/sql` (ADR-0001).

**Docs.** INSTALL §5 step 1 and `production-setup.md` §10: check both `https://domain/healthz` (panel up) and `https://domain/api/v1/healthz` (API and database up). Suggest the second as the URL for external uptime monitoring. **Decided:** `api.yaml`'s `readinessProbe` moves to `/api/v1/healthz`, so a pod that can't reach the DB leaves the Service; liveness stays on `/healthz` (and the startup probe from item 3). Known trade-off: if the DB is down for every pod, all are unready and the panel answers 502 instead of the API's own errors. Say this in the runbook's troubleshooting, and keep the ping timeout short (~2 s) and `failureThreshold` ≥ 3 so a brief DB blip doesn't flap pods. Mirror it in Compose (`healthcheck`) and ECS (container health check) where it makes sense, and in the ALB target group only if devops wants it (an ALB with no healthy targets fails open, which is different from Kubernetes).

🎓 **Learn.** Three different questions: *liveness* "should this process be restarted?" (process only), *readiness* "should it get traffic now?" (may include dependencies), and *external/synthetic* "can a user get through the whole path?" (proxy → panel → API → DB). One endpoint can't answer all three well.

---

## 6. The ping at App creation fails, and that's expected 📝

GitHub sends a `ping` as soon as an App with an active webhook is created. INSTALL creates the App (§1) before deploying (§4), so the URL doesn't exist yet and that delivery fails.

**Fix.** In INSTALL §1 (after the table) and runbook §4.4: *"GitHub sends a `ping` the moment the app is created. It fails because nothing is deployed yet: that's expected. After §5, open the app's **Advanced → Recent Deliveries**, pick the ping, and click **Redeliver**: it should answer `202`."* This also becomes a nice first end-to-end check in §5.

(Reordering the guide so the App is created after the deploy doesn't work: the deploy needs the App's credentials.)

---

## 7. A direct link to create the App 📝

**Fix.** In INSTALL §1 and runbook §4: give the URLs, not only the menu path:

- Organization: `https://github.com/organizations/<org>/settings/apps/new`
- Personal account: `https://github.com/settings/apps/new`

And a note: *"`/settings/installations` lists apps installed on the org, not apps it owns. The **Subscribe to events** checkboxes only appear after the matching permission is set (e.g. *Pull request* appears once *Pull requests* is read or read & write), so set permissions before events."*

---

## 8. Image verification: by digest, with the exact signer 📝🎓

### What to change

`production-setup.md` §7.1, INSTALL §0 and the release notes template in `release.yml` (which already computes the digest):

```bash
# gh must be signed in (gh auth login, or GH_TOKEN): it fetches attestations from the GitHub API
DIGEST=$(docker buildx imagetools inspect ghcr.io/muriio/pr-follower-api:0.1.0 --format '{{json .Manifest}}' | …)  # or copy it from the release notes
gh attestation verify oci://ghcr.io/muriio/pr-follower-api@sha256:<digest> \
  --repo muriIo/pr-follower \
  --signer-workflow muriIo/pr-follower/.github/workflows/release.yml \
  --source-ref refs/tags/v0.1.0
```

Then deploy **that digest**: Kustomize `images: [{name: …, newTag: "0.1.0", digest: "sha256:…"}]`, Compose `image: ghcr.io/muriio/pr-follower-api:0.1.0@sha256:…`. Keep the tag next to the digest for humans; the runtime uses the digest.

Also: the release notes print the two digests and the full verify command (with `--repo`, `--signer-workflow`, `--source-ref`), so the operator copies, not composes. And state that `:X.Y` moves with every patch, so it's for "what's newest", never for deploys.

### 🎓 Analysis: digest pinning vs attestation (the dev-note)

They answer **different questions**, which is why the strong setup uses both.

**A tag** (`:0.1.0`, `:0.1`) is a *name* that points to an image, and registries let the owner move it. `:0.1` moves by design on every patch; `:0.1.0` *shouldn't* move, but nothing stops a re-push, a compromised token, or a mistake.

**A digest** (`@sha256:abc…`) is the hash of the image manifest (for our multi-arch images, the index that lists the amd64 and arm64 manifests, which in turn hash every layer). It is a *content address*: the same digest always means the same bytes, and the registry can't serve anything else under it.
- Answers: *"Is this exactly the image I looked at before?"*
- Doesn't answer: *"Who built it, and from what?"* A digest of a malicious image is just as stable.

**An attestation** (what `actions/attest` produces in `release.yml`) is a signed statement: *"the artifact with digest `sha256:abc…` was built by workflow `.github/workflows/release.yml` in repo `muriIo/pr-follower`, at ref `refs/tags/v0.1.0`, commit `…`, on a GitHub-hosted runner"* (SLSA provenance). It's signed through Sigstore with a short-lived certificate that GitHub's OIDC issues **to that workflow run**, and recorded in a transparency log. Nobody holds a long-lived signing key that could leak.
- Answers: *"Where did these bytes come from?"*
- Always about a digest: `gh attestation verify oci://…:0.1.0` first resolves the tag to a digest, then looks up attestations for that digest.

**Why `--owner muriIo` is weak.** It accepts an attestation signed by **any workflow in any repo** owned by `muriIo`. A different repo of mine, a test workflow, or a fork-like repo under the same account would pass. `--repo muriIo/pr-follower` narrows it to this repository; `--signer-workflow …/release.yml` narrows it to the one workflow that only runs on `v*` tags from `main`, so a CI or PR workflow in the same repo couldn't produce an accepted attestation. `--source-ref refs/tags/v0.1.0` pins it to that release. (`--deny-self-hosted-runners` is another available check; we only use GitHub-hosted runners, so it's free to add.)

**Why verify the digest, not the tag.** If you verify `:0.1.0` and later deploy `:0.1.0`, the tag is resolved twice; if it moved in between, you deployed something you never verified (a time-of-check/time-of-use gap). Verifying `@sha256:…` and deploying `@sha256:…` makes the check and the deploy refer to the same bytes by construction.

**Putting it together:**

| Practice | Protects against |
|---|---|
| Deploy by `:X.Y` | nothing; changes under you |
| Deploy by `:X.Y.Z` | accidental upgrades, but not a moved tag |
| Pin `@sha256` | the tag moving after you chose; drift between replicas/environments |
| Verify attestation `--owner` | images not built by *some* workflow of that owner |
| Verify `--repo` + `--signer-workflow` (+ `--source-ref`) **on the digest you pin** | images not built by *this* release workflow from *this* tag; plus the above |

**Going further (mention, don't do now):** verification at admission time in the cluster (Sigstore `policy-controller`, Kyverno `verifyImages`), so an unverified image can't run even if someone edits a manifest by hand. And mirroring into ECR by digest (runbook already mentions it) removes the GHCR dependency at pull time.

---

## 9. RDS: `sslmode=verify-full` with the CA bundle 📝

**Why.** `sslmode=require` encrypts but **doesn't check who answered**: anyone able to intercept traffic inside the network could present any certificate. `verify-full` checks that the certificate chains to a trusted CA **and** that its name matches the host in the URL.

**Fix.** In `aws-setup.md` §7 and `production-setup.md` §3, make it the recommended option:

```
postgres://prfollower:PASSWORD@DB_HOST:5432/pr_follower?sslmode=verify-full&sslrootcert=/run/secrets/rds/global-bundle.pem
```

- CA bundle: `https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem` (all regions), or the regional one. Confirm the URL against the AWS RDS docs before writing it down (use `ctx7`).
- Mount it as a file (ECS: a volume from the image or S3/Secrets; Kubernetes: a ConfigMap; it's public, not a secret). The API runs as UID 10001: the file must be readable by it.
- Note that pgx reads `sslrootcert` from the URL, so no code change. Check this is true for the driver path we use (`pgx` stdlib) with a local test against a Postgres with a self-signed cert, or trust devops' "it worked" and say so.
- Keep `require` as the minimal option ("encrypts, doesn't authenticate the server").

🎓 **Learn.** TLS gives two things: confidentiality (encryption) and authentication (you're talking to the right server). `require` gives the first only; without the second, an active attacker can still decrypt by being the server.

---

## 10. Kubernetes on AWS: an ALB ingress example 📝

**Fix.** Add `deploy/kubernetes/ingress-alb.yaml` (not in `kustomization.yaml` by default; the comment says how to swap it in) or a section in INSTALL path B / `aws-setup.md`:

```yaml
metadata:
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
    alb.ingress.kubernetes.io/target-type: ip            # pods get traffic directly (needs the VPC CNI)
    alb.ingress.kubernetes.io/listen-ports: '[{"HTTPS":443}]'
    alb.ingress.kubernetes.io/certificate-arn: arn:aws:acm:…
    alb.ingress.kubernetes.io/healthcheck-path: /healthz
    alb.ingress.kubernetes.io/load-balancer-attributes: idle_timeout.timeout_seconds=300
    # shared ALB: alb.ingress.kubernetes.io/group.name: <group>
spec:
  ingressClassName: alb
```

- Body size: ALB has no 5 MB limit to raise (the panel's nginx enforces `5m`). Response buffering: ALB streams, so SSE works.
- **Shared ALB warning (devops' "decision 2"):** with `group.name`, many Ingresses share one ALB, and `idle_timeout` is an attribute of the **load balancer**, not of a rule or target group. Setting 300 s for us sets it for every app in the group; conflicting values across Ingresses in a group are an error. Either a dedicated ALB, or agree the value with the platform team.
- Also update `ingress.yaml`'s comment: ingress-nginx is retired upstream; keep the example but say so, and point to the ALB file and Gateway API.
- Verify every annotation name against the AWS Load Balancer Controller docs (`ctx7`) before committing; placeholders only (`prfollower.example.com`, `arn:aws:acm:…`).

🎓 **Learn.** Why 300 s: an idle connection is one with no bytes moving. The SSE stream sends heartbeats, but a merge request waits on GitHub silently; any proxy in the path with a shorter idle timeout cuts it with a 504. Every hop (ALB, nginx, the API's server timeouts) needs ≥ the longest silent wait.

---

## 11. Secrets with External Secrets (GitOps) 📝

**Fix.** A note in INSTALL path B (and a full example in `production-setup.md` §6 or a `deploy/kubernetes/examples/external-secrets.yaml`) showing devops' shape:

- One `ExternalSecret` → Secret `pr-follower` with every §3 variable, extracted from one JSON secret in the store (`dataFrom: - extract: { key: pr-follower/api }`), consumed by `envFrom`.
- Another `ExternalSecret` → Secret `pr-follower-github-app` with key `github-app.pem` from the private key secret, mounted as a file (as `api.yaml` already does).
- Why two: the key is multi-line and is mounted as a file, not an env var; it rotates on a different schedule.
- Check field names against the External Secrets Operator docs (`ctx7`) for `external-secrets.io/v1`; placeholders only for store names and secret paths.

🎓 **Learn.** `kubectl create secret` puts the secret's value only in the cluster: nothing in git, which is good, but nothing reproducible either. GitOps wants everything in git; External Secrets lets git hold a *reference* ("secret `pr-follower/api` in AWS Secrets Manager") while the value stays in the secret manager, and the operator syncs and rotates it.

---

## What devops' manifests confirmed

Devops shared their manifests for reference only; nothing of their infrastructure goes into this repo. What carries over to our docs:

- **Item 1:** their version comment already says "the image tag has no `v`".
- **Item 8:** they pin both images by tag + digest (Kustomize `newTag` + `digest`), verified with `gh attestation verify` against `release.yml` at the release tag. Use that shape as our worked example: tag for humans, digest for the runtime, a comment recording what was verified.
- **Item 9:** `verify-full` with a regional RDS CA bundle mounted as a file. Document both the global and the regional bundle.
- **Item 10:** the 300 s idle timeout is set on a **shared** ALB, so it applies to every app on it. Our example shows the annotation with that warning, plus the HTTP→HTTPS redirect (`listen-ports` 80+443, `ssl-redirect`) and the two ways to get the certificate (explicit `certificate-arn`, or discovery from the host).
- **Item 11:** two `ExternalSecret`s on `external-secrets.io/v1` (env JSON via `dataFrom.extract`; the PEM via `data.remoteRef`, mounted as a file), backed by SSM Parameter Store. Our example says "Parameter Store or Secrets Manager".
- **New: config vs secrets.** Non-secret values (`PUBLIC_URL`, `ALLOWED_ORIGIN`, `GITHUB_CLIENT_ID`, `GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `DB_MAX_*`) can live in a ConfigMap; only `DATABASE_URL`, `GITHUB_CLIENT_SECRET`, `GITHUB_WEBHOOK_SECRET`, `TOKEN_ENCRYPTION_KEY` and the private key are secrets. Add a "secret?" column to INSTALL §3 / runbook §6.1 and split them in our Kubernetes example.
- **New: rotation needs a restart.** The API reads its env and key file once at start (`internal/infrastructure/config/config.go`), so a secret updated by External Secrets takes effect only after `kubectl rollout restart` (or a tool such as Reloader). Say it in item 11's example and runbook §13.4.
- **New: `namePrefix` and `API_UPSTREAM`.** A Kustomize `namePrefix` renames the API Service but not the `API_UPSTREAM` string in `panel.yaml`; add a comment there.
- **Item 3 check:** they run with `DB_MAX_OPEN_CONNS=5`; confirm start-up with the migration lock works with a pool that small.

Still to ask devops (item 3): was `Recreate` chosen to keep old code off the new schema, or out of caution?

## Order of work (PRs into `develop`)

| PR | Branch | Items | Why grouped |
|----|--------|-------|-------------|
| 1 | `fix/migration-advisory-lock` | 3 | Only real correctness bug; code + test + new ADR and `adrcheck` gate + manifest (startup probe) + runbook §13.10 |
| 2 | `feat/api-health-endpoint` | 5 | Code; readiness probe moves to it; changes the smoke tests in INSTALL §5 / runbook §10 |
| 3 | `fix/read-only-filesystem` | 4 | Manifests + compose + hardening docs, validated with `docker run --read-only` |
| 4 | `docs/install-feedback` | 1, 2, 6, 7, 8 (+ release notes in `release.yml`) | The INSTALL/runbook wording fixes; small and reviewable |
| 5 | `docs/aws-kubernetes-examples` | 9, 10, 11 | AWS/Kubernetes examples; best written with devops' real manifests |


Every PR: `make check` green; per the install-docs-in-sync rule, each change touches every doc that describes the same step (README, INSTALL, `.env.example`, production/aws/floci setup, compose, k8s). Tick the matching items in `docs/roadmap.md` (add them first if they aren't there).

## Decisions

1. Item 3: **new ADR + `adrcheck` gate** for the advisory lock (in PR 1).
2. Item 5: **readiness probe moves to `/api/v1/healthz`**; liveness stays on `/healthz` (in PR 2).
3. Item 2: **no CI on 18 yet**; docs say "tested on 17, works on 18".
4. Items 10–11: devops' manifests were used for reference only; the examples are written from scratch with placeholders.
