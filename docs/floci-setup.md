# Running the AWS setup locally on Floci

This guide runs PR Follower on your machine with the same shape as the AWS deployment: VPC, security groups, an Application Load Balancer with an ACM certificate, an ECS Fargate task with two containers, RDS for PostgreSQL 17, Secrets Manager, ECR, CloudWatch Logs and IAM. All of it comes from [Floci](https://github.com/floci-io/floci), a free local AWS emulator, and you sign in with a real GitHub App. The only internet-facing piece is the GitHub webhook, which smee.io relays to your machine. Use the setup to rehearse the AWS deployment and to test every flow end to end, with no AWS account and no cost.

The guide is self-contained: every step is written out here, including the GitHub App, the secrets, the smoke tests and the end-to-end checklist.

**Status:** written and run on 2026-11-11 with Floci 2.4.0, AWS CLI 2.28 and Docker Desktop 28 on WSL 2, in zsh. These command blocks were run as written, on alternate ports and with dummy GitHub values: §2 to §15, the first smoke-test block in §17, the drills in §20.1, §20.2, §20.5, §20.6 and §20.7, the log commands in §21 and the teardown in §22. An earlier run also covered a Floci restart (§18 step 8) and showed that the event stream passes through the ALB unbuffered and that a 75-second response is not cut off. **Not run:** anything that needs GitHub, smee.io or a browser: the real GitHub App (§6), smee.io delivery (§5, §16, the second block of §17), trusting the CA in a browser (§10.2), the flows in §18 and §19, and the interactive rollback in §20.3.

Commands work in **bash and zsh**. Prompts use `printf` followed by `read`, because zsh's `read -p` means something else. Lists are split with an unquoted `$(echo …)`, because zsh does not split an unquoted `$VAR`. Each section ends with a check to run before moving on.

## 1. What runs where

```
 GitHub ──webhook──▶ smee.io channel ──▶ smee client (your terminal)
                                               │ POST https://localhost:8443/api/v1/webhooks/github
 Browser ── https://localhost:8443 ────────────┤
                                               ▼
┌─ Docker ───────────────────────────────────────────────────────────────────────────┐
│ Floci container "pr-follower-floci" (AWS APIs on :4566)                            │
│   ALB listeners  :8443 HTTPS (ACM certificate signed by Floci's CA)                │
│                  :8000 HTTP, redirects to :8443                                    │
│        │ target group (ip), health check /healthz                                  │
│        ▼                                                                           │
│   ECS task, in a namespace filtered by its security group                          │
│     panel (nginx :8080) ──127.0.0.1:8081──▶ api ─────────────────▶ github.com      │
│                                              │                                     │
│   RDS proxy :7001 (TLS) ◀────────────────────┘                                     │
│        ▼                                                                           │
│   postgres:17.4 container                                                          │
│                                                                                    │
│ ECR (a registry:2 container) · Secrets Manager · CloudWatch Logs · IAM (in Floci)  │
└────────────────────────────────────────────────────────────────────────────────────┘
```

How close each piece is to AWS:

| AWS piece | On Floci | How real |
|---|---|---|
| VPC, subnets | API objects | Metadata only: no routing tables and no NAT gateway. Containers reach the internet through Docker. |
| Security groups | API objects, plus packet filtering for ECS tasks | With enforcement on (§3), the task's group really filters traffic. The ALB and the database are not filtered. |
| ACM certificate | Issued immediately, signed by Floci's local CA | Real TLS; you trust the CA once (§10). No DNS validation. |
| Application Load Balancer | An HTTP proxy inside Floci | Real forwarding, health checks, redirects, `X-Forwarded-*` headers and the idle timeout. The event stream (SSE) passes through unbuffered. |
| ECS on Fargate | Real Docker containers | Real images, environment, secrets, `awslogs` logs, rolling deploys and the circuit breaker with rollback. Container health checks are not run (§15). |
| RDS for PostgreSQL 17 | A `postgres:17.4` container behind a TLS proxy | Real Postgres, including `LISTEN/NOTIFY`. Snapshots are `pg_dumpall` copies. |
| Secrets Manager | In Floci | ECS really injects `valueFrom` secrets into the task's environment. |
| ECR | A `registry:2` container | Real `docker push` and pull; immutable tags are enforced. Lifecycle policies and scan-on-push are stored but do nothing. |
| CloudWatch Logs | In Floci | Container output streams there; `aws logs tail` works. |
| IAM | In Floci | Roles and policies are stored but **not enforced**. |
| Route 53 | Not used | The app lives at `localhost`. |

## 2. Prerequisites

- **Docker**: Docker Desktop (macOS, or Windows with WSL 2) or Docker Engine on Linux, with about 4 GB of memory to spare. Security-group enforcement (§3) needs a rootful Docker daemon with nftables. Docker Desktop on WSL 2 works (this guide was verified there); rootless Docker does not.
- **AWS CLI v2.13 or later** (`aws --version`). Earlier versions ignore `AWS_ENDPOINT_URL`. You need no AWS account.
- **Node.js 18 or later** and the smee client: `npm install -g smee-client`.
- `git`, `openssl` and `python3` (python3 only checks JSON).
- A GitHub account that can create a GitHub App, and a throwaway **test repository**. §7 lists what it needs.
- This repository at the commit you want to test, with `make check` green.
- Free ports **4566, 8443, 8000 and 7001–7010**. If you started Floci with its CLI (`floci start`), stop that instance with `floci stop`, because it holds 4566. This guide runs its own Floci with extra settings.

### 2.1 Working folder and variables

Everything this guide writes goes into one folder outside the repository: the Compose file, the task definition, the CA certificate and the IDs of the resources you create.

```bash
mkdir -p ~/pr-follower-floci
cat > ~/pr-follower-floci/env.sh <<'EOF'
# PR Follower on Floci. Source this in every terminal:  source ~/pr-follower-floci/env.sh
unset AWS_PROFILE AWS_SESSION_TOKEN               # never fall back to a real AWS profile
export AWS_ENDPOINT_URL=http://localhost:4566     # every aws command goes to Floci
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1 AWS_DEFAULT_REGION=us-east-1 AWS_PAGER=""
export APP=pr-follower
export WORK=$HOME/pr-follower-floci               # this folder
export REPO=$HOME/projects/pr-follower            # your clone of the repository (edit if elsewhere)
export PUBLIC_URL=https://localhost:8443          # the address the browser opens
export FLOCI_IP=172.31.250.10                     # Floci's fixed address on its Docker network

# save NAME...: keep variables for new terminals (appended to ids.sh, which is read below)
save() {
  local v
  for v in "$@"; do eval "printf 'export %s=\"%s\"\\n' \"\$v\" \"\$$v\"" >> "$WORK/ids.sh"; done
}
# Secrets Manager helpers. Values travel on stdin, never on the command line.
store_secret()  { aws secretsmanager create-secret    --name      "$APP/local/$1" --secret-string file:///dev/stdin --query Name --output text; }
update_secret() { aws secretsmanager put-secret-value --secret-id "$APP/local/$1" --secret-string file:///dev/stdin --query VersionId --output text; }
secret_value()  { aws secretsmanager get-secret-value --secret-id "$APP/local/$1" --query SecretString --output text; }
secret_arn()    { aws secretsmanager describe-secret  --secret-id "$APP/local/$1" --query ARN --output text; }
# curl that trusts Floci's CA, which signed the ALB's certificate
lcurl() { curl --cacert "$WORK/floci-ca.pem" "$@"; }
# psql_local [psql options]: psql inside Floci's Docker network, with the stored DATABASE_URL
psql_local() {
  local tty=
  [ -t 0 ] && tty=-t
  DATABASE_URL=$(secret_value database-url) docker run --rm -i $tty -e DATABASE_URL \
    --network pr-follower-net postgres:17-alpine sh -c 'exec psql "$DATABASE_URL" "$@"' psql "$@"
}
# wait_deployed: until every running task belongs to the service's current deployment and the
# ALB target is healthy. On Floci, `aws ecs wait services-stable` returns while the old task still runs.
wait_deployed() {
  local dep tasks others states
  dep=$(aws ecs describe-services --cluster $APP --services $APP --query 'services[0].deployments[0].id' --output text)
  while :; do
    tasks=$(aws ecs list-tasks --cluster $APP --service-name $APP --desired-status RUNNING --query 'taskArns' --output text)
    if [ -n "$tasks" ] && [ "$tasks" != None ]; then
      others=$(aws ecs describe-tasks --cluster $APP --tasks $(echo "$tasks") \
        --query "tasks[?startedBy!='$dep' || lastStatus!='RUNNING'].taskArn" --output text)
      [ -z "$others" ] && break
    fi
    sleep 3
  done
  while :; do
    states=$(aws elbv2 describe-target-health --target-group-arn $TG_ARN \
      --query 'TargetHealthDescriptions[].TargetHealth.State' --output text)
    case "$states" in *healthy*) case "$states" in *initial*|*unhealthy*) ;; *) break ;; esac ;; esac
    sleep 3
  done
  echo "deployed: $(aws ecs describe-services --cluster $APP --services $APP \
    --query 'services[0].taskDefinition' --output text | sed 's|.*/||')"
}

[ -f "$WORK/ids.sh" ] && . "$WORK/ids.sh"
true
EOF
source ~/pr-follower-floci/env.sh
```

**Safety:** an `aws` command run in a terminal where `env.sh` is not sourced goes to **real AWS**, using whatever credentials that shell has. Source the file in every new terminal, and check with:

```bash
aws sts get-caller-identity --query Account --output text   # must print 000000000000 (once Floci runs, §3)
```

If it prints your real account number, stop and run `source ~/pr-follower-floci/env.sh`.

## 3. Start Floci

**Why these settings:**

| Setting | Why |
|---|---|
| Docker socket mounted, `user: root` | Floci starts real containers for ECS, RDS and ECR through it. |
| Network `pr-follower-net` with a fixed subnet, and Floci at `172.31.250.10` | Floci's task containers, the database and the registry all join this network. The ALB runs inside Floci, so its traffic to the task comes from Floci's own address, and §9 allows exactly that address. |
| `FLOCI_NETWORK_SECURITY_GROUP_ENFORCEMENT_ENABLED` | Without it, each container of a task gets its own network namespace, and the panel could not reach the API on `127.0.0.1:8081`. With it, the containers of a task share one namespace, as on Fargate, and the task's security group really filters packets. |
| Ports 8443 and 8000 | The ALB's listeners bind inside the Floci container. Publishing them on the same port numbers keeps the browser address, the listener and `PUBLIC_URL` identical. |
| Ports 7001–7010, `FLOCI_SERVICES_RDS_ENDPOINT_HOST` | Each RDS instance gets a proxy port from this range. The endpoint RDS reports (`pr-follower-floci:7001`) is the address the task uses on the Docker network. The published ports also let you reach the database from your own machine. |
| `FLOCI_SERVICES_ECR_URI_STYLE: path` | Repository URIs become `localhost:4566/<account>/<region>/<repo>`. Docker treats `localhost` registries as plain HTTP. The default `*.localhost` host names fail on Docker's containerd image store (the Docker Desktop default). |
| `FLOCI_STORAGE_MODE: hybrid` and a volume | Resources and data survive a Floci or Docker restart. |
| `FLOCI_DOCKER_RESOURCE_NAMESPACE` | Floci's containers are named `floci-aws-pr-follower-…`, separate from any other Floci on the same Docker. |
| `floci/floci:2.4.0` | The version this guide was verified with. Upgrade on purpose, not by accident. |

```bash
cat > "$WORK/compose.yaml" <<'EOF'
name: pr-follower-floci
services:
  floci:
    image: floci/floci:2.4.0
    container_name: pr-follower-floci
    user: root
    ports:
      - "4566:4566"            # AWS APIs, and ECR push/pull
      - "8443:8443"            # ALB HTTPS listener
      - "8000:8000"            # ALB HTTP listener (redirects to 8443)
      - "7001-7010:7001-7010"  # RDS proxy ports
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - floci-data:/app/data
    networks:
      net:
        ipv4_address: 172.31.250.10
    environment:
      FLOCI_PORT: "4566"
      FLOCI_HOSTNAME: pr-follower-floci
      FLOCI_DOCKER_RESOURCE_NAMESPACE: pr-follower
      FLOCI_STORAGE_MODE: hybrid
      FLOCI_STORAGE_PERSISTENT_PATH: /app/data
      FLOCI_SERVICES_DOCKER_NETWORK: pr-follower-net
      FLOCI_SERVICES_ECS_DOCKER_NETWORK: pr-follower-net
      FLOCI_NETWORK_SECURITY_GROUP_ENFORCEMENT_ENABLED: "true"
      FLOCI_SERVICES_RDS_PROXY_BASE_PORT: "7001"
      FLOCI_SERVICES_RDS_PROXY_MAX_PORT: "7010"
      FLOCI_SERVICES_RDS_ENDPOINT_HOST: pr-follower-floci
      FLOCI_SERVICES_ECR_URI_STYLE: path
networks:
  net:
    name: pr-follower-net
    ipam:
      config:
        - subnet: 172.31.250.0/24
volumes:
  floci-data:
EOF
docker compose -f "$WORK/compose.yaml" up -d
until curl -sf http://localhost:4566/_floci/health >/dev/null; do sleep 2; done; echo "Floci is up"
```

If `up` fails with `Pool overlaps with other one on this address space`, another Docker network already uses `172.31.250.0/24`. Pick a free `/24` and change it in three places: the subnet and the `ipv4_address` in `compose.yaml`, and `FLOCI_IP` in `env.sh`.

**Check:** `aws sts get-caller-identity --query Account --output text` prints `000000000000`, and `curl -s http://localhost:4566/_floci/health | head -c 120` shows `"version":"2.4.0"`.

## 4. Generate the secrets

Two secrets are random values you generate yourself. Generate them straight into Floci's Secrets Manager, so they never appear on screen or in your shell history:

| Secret | How | Used for |
|---|---|---|
| Webhook secret | `openssl rand -hex 32` | The GitHub App's webhook (§6.4) and `GITHUB_WEBHOOK_SECRET` |
| Token encryption key | `openssl rand -base64 32` (exactly 32 bytes once decoded) | `TOKEN_ENCRYPTION_KEY`, which encrypts every stored GitHub token. Changing it signs everyone out (§20.7). |

```bash
openssl rand -hex 32    | tr -d '\n' | store_secret github-webhook-secret
openssl rand -base64 32 | tr -d '\n' | store_secret token-encryption-key
```

The database password is generated in §11, and GitHub provides the client secret and the private key in §6.

**Check:** `secret_value token-encryption-key | base64 -d | wc -c` prints `32`.

## 5. Create the smee channel

**Why smee:** GitHub can only deliver webhooks to a public URL. smee.io gives you a public channel URL; GitHub posts to it, and the smee client in your terminal receives each delivery and replays it against the local ALB. Nothing on your machine is exposed to the internet.

1. Open <https://smee.io/new> in a browser. It redirects to your channel, `https://smee.io/<random>`.
2. Keep that URL:

   ```bash
   printf 'smee channel URL: '; read -r SMEE_URL; export SMEE_URL; save SMEE_URL
   ```

Things to know about smee:

- **Anyone who knows the channel URL can read the deliveries**, which include repository names, PR titles and branch names. Treat the URL as a secret and use test repositories only.
- Anyone who knows the URL can also post to it, but the API rejects anything that is not signed with the webhook secret (`401`).
- Deliveries arrive only while the smee client runs (§16). Anything sent while it is stopped is lost; use **Redeliver** on GitHub.

## 6. Create the GitHub App

Create a **separate app for this setup**: an app has a single webhook URL and a single Setup URL, so sharing one with `make dev` would make the two setups fight over them.

On GitHub: **Settings → Developer settings → GitHub Apps → New GitHub App**. For an organization: **Organization → Settings → Developer settings → GitHub Apps**.

### 6.1 Basic information and sign-in

| Field | Value | Why |
|---|---|---|
| GitHub App name | e.g. `PR Follower Floci <you>`; unique on GitHub, max 34 characters | Shown when you install and authorize it. |
| Homepage URL | `https://localhost:8443` | Required. |
| Callback URL | `https://localhost:8443/api/v1/auth/github/callback` | Must equal `PUBLIC_URL` + `/api/v1/auth/github/callback` character for character. Add only this one. |
| Expire user authorization tokens | ✅ on | Users get 8-hour tokens and a refresh token; the API refreshes them. |
| Request user authorization (OAuth) during installation | ❌ **off** | When on, GitHub sends installers to the Callback URL without the app's login state (they land on `state_mismatch`), and the Setup URL is disabled. |
| Enable Device Flow | ❌ off | Not used. |

### 6.2 Post installation

| Field | Value | Why |
|---|---|---|
| Setup URL | `https://localhost:8443/` | GitHub returns installers here with `installation_id`, and the app opens the import screen on that account. |
| Redirect on update | ✅ on | The same return after someone adds or removes repositories on an installation. |

### 6.3 Webhook

| Field | Value |
|---|---|
| Active | ✅ on |
| Webhook URL | Your smee channel URL (`echo $SMEE_URL`) |
| Webhook secret | Print it with `secret_value github-webhook-secret` and paste it here |
| SSL verification | ✅ enabled (smee.io has a valid certificate) |

### 6.4 Permissions

**Repository permissions** (everything not listed: *No access*):

| Permission | Access | Used for |
|---|---|---|
| Pull requests | Read & write | Read PRs, their files and reviews; approve; `@dependabot` comments. |
| Contents | Read & write | Merge (write); CODEOWNERS, lockfiles and tarballs (read). |
| Checks | Read-only | Check runs (CI). |
| Commit statuses | Read-only | Legacy statuses (Jenkins, Vercel). |
| Metadata | Read-only | Mandatory; set automatically. |

**Organization permissions:** Members *Read-only*, to read your teams for team entries in CODEOWNERS. **Account permissions:** none.

### 6.5 Events

Subscribe to exactly these. Each one appears only after its permission above is set.

| Event | What the API does with it |
|---|---|
| Pull request | Opens, updates, closes and merges PRs; reads their changed files; schedules the Dependabot analysis. |
| Pull request review | Stores reviews, which decide "approved by me", "teammate approved" and "waiting on author". |
| Push | Re-reads CODEOWNERS when a push to the default branch touches it. |
| Status | Legacy commit statuses, listed beside check runs. |
| Check run | CI state of each check. |

`installation` and `installation_repositories` are always delivered to GitHub Apps; they keep repository access in sync. Leave *Check suite* and *Issue comment* off: the API acknowledges and ignores them.

### 6.6 Where it can be installed

Choose **Only on this account**: a test app does not need to be installable by anyone else. Click **Create GitHub App**.

GitHub immediately sends a `ping` to the webhook. Nobody is listening yet, so it goes nowhere; you redeliver it in §18.

### 6.7 Collect the credentials

On the app's **General** page:

1. **App ID**, **Client ID** (starts with `Iv`) and the app's URL name (the `<slug>` in `https://github.com/apps/<slug>`). These are not secret:

   ```bash
   printf 'App ID: '; read -r GH_APP_ID
   printf 'Client ID: '; read -r GH_CLIENT_ID
   printf 'App slug: '; read -r GH_APP_SLUG
   export GH_APP_ID GH_CLIENT_ID GH_APP_SLUG; save GH_APP_ID GH_CLIENT_ID GH_APP_SLUG
   ```

2. **Generate a new client secret.** GitHub shows it once; store it directly:

   ```bash
   printf 'Client secret: '; read -rs v; echo; printf '%s' "$v" | store_secret github-client-secret; unset v
   ```

3. **Private keys → Generate a private key.** The browser downloads `<slug>.<date>.private-key.pem`. Store it as one line, with `\n` written for each newline (the format `GITHUB_APP_PRIVATE_KEY` takes). On WSL, a file downloaded by the Windows browser is under `/mnt/c/Users/<you>/Downloads/`.

   ```bash
   printf 'Path to the downloaded .pem (full path, no ~): '; read -r PEM
   [ -f "$PEM" ] && awk 'NF {sub(/\r/, ""); printf "%s\\n", $0}' "$PEM" | store_secret github-app-private-key
   ```

   Then delete the downloaded file. Never copy it into `backend/` or `frontend/` (they are the image build contexts) or commit it. If you ever need the key again, generate a new one on GitHub.

**Check:** `aws secretsmanager list-secrets --query "SecretList[].Name" --output text` lists four secrets: `github-webhook-secret`, `token-encryption-key`, `github-client-secret` and `github-app-private-key`.

## 7. The test repository

Use a throwaway repository, never production data. To exercise every flow in §18 and §19 it needs:

- a `.github/CODEOWNERS` that assigns a path to you;
- a second GitHub account (or a teammate) who can open PRs and review;
- a `package.json` and `package-lock.json` (npm lockfile v2 or v3) with an outdated dependency, so Dependabot opens a PR. Enable Dependabot version updates on it (`.github/dependabot.yml` with the `npm` ecosystem), or use **Insights → Dependency graph → Dependabot → Check for updates**.

## 8. Network

**Why:** the same layout as AWS: two public subnets for the load balancer and two private subnets for the task and the database, in two Availability Zones. An ALB refuses to be created with fewer than two subnets in different zones, on Floci too. On Floci these are only records, but every later resource refers to them, as on AWS.

```bash
VPC_ID=$(aws ec2 create-vpc --cidr-block 10.0.0.0/16 \
  --tag-specifications "ResourceType=vpc,Tags=[{Key=Name,Value=$APP-vpc}]" --query Vpc.VpcId --output text)
mk_subnet() {   # mk_subnet NAME CIDR AZ
  aws ec2 create-subnet --vpc-id $VPC_ID --cidr-block $2 --availability-zone $3 \
    --tag-specifications "ResourceType=subnet,Tags=[{Key=Name,Value=$APP-subnet-$1}]" \
    --query Subnet.SubnetId --output text
}
mk_subnet public1  10.0.0.0/20   ${AWS_REGION}a
mk_subnet public2  10.0.16.0/20  ${AWS_REGION}b
mk_subnet private1 10.0.128.0/20 ${AWS_REGION}a
mk_subnet private2 10.0.144.0/20 ${AWS_REGION}b

PUBLIC_SUBNETS=$(aws ec2 describe-subnets \
  --filters Name=vpc-id,Values=$VPC_ID Name=tag:Name,Values='*public*' \
  --query 'Subnets[].SubnetId' --output text)
PRIVATE_SUBNETS=$(aws ec2 describe-subnets \
  --filters Name=vpc-id,Values=$VPC_ID Name=tag:Name,Values='*private*' \
  --query 'Subnets[].SubnetId' --output text)
PRIVATE_CSV=$(echo "$PRIVATE_SUBNETS" | tr -s '\t ' ',')     # subnet-a,subnet-b for ECS
export VPC_ID PUBLIC_SUBNETS PRIVATE_SUBNETS PRIVATE_CSV; save VPC_ID PUBLIC_SUBNETS PRIVATE_SUBNETS PRIVATE_CSV
echo "$VPC_ID | public: $PUBLIC_SUBNETS | private: $PRIVATE_CSV"
```

**Check:** two subnet IDs in each list.

## 9. Security groups

**Why:** each tier admits only the tier in front of it: the internet reaches the ALB, only the ALB reaches the task, only the task reaches the database. These are the AWS rules unchanged, plus one rule that exists only here.

That extra rule is needed because the ALB is a proxy inside the Floci container, so its traffic to the task comes from Floci's own address (`FLOCI_IP`), not from a member of the ALB's group. The task's security group is really enforced (§3), so without this rule the ALB's health checks time out and the target stays `unhealthy`. On AWS you do not add this rule.

```bash
ALB_SG=$(aws ec2 create-security-group --group-name $APP-alb --description "PR Follower ALB" \
  --vpc-id $VPC_ID --query GroupId --output text)
APP_SG=$(aws ec2 create-security-group --group-name $APP-app --description "PR Follower tasks" \
  --vpc-id $VPC_ID --query GroupId --output text)
DB_SG=$(aws ec2 create-security-group --group-name $APP-db --description "PR Follower database" \
  --vpc-id $VPC_ID --query GroupId --output text)
export ALB_SG APP_SG DB_SG; save ALB_SG APP_SG DB_SG

aws ec2 authorize-security-group-ingress --group-id $ALB_SG --ip-permissions \
  'IpProtocol=tcp,FromPort=8443,ToPort=8443,IpRanges=[{CidrIp=0.0.0.0/0}]' \
  'IpProtocol=tcp,FromPort=8000,ToPort=8000,IpRanges=[{CidrIp=0.0.0.0/0}]' >/dev/null
aws ec2 authorize-security-group-ingress --group-id $APP_SG --ip-permissions \
  "IpProtocol=tcp,FromPort=8080,ToPort=8080,UserIdGroupPairs=[{GroupId=$ALB_SG}]" >/dev/null
aws ec2 authorize-security-group-ingress --group-id $DB_SG --ip-permissions \
  "IpProtocol=tcp,FromPort=5432,ToPort=5432,UserIdGroupPairs=[{GroupId=$APP_SG}]" >/dev/null

# Floci only: the ALB proxy runs inside the Floci container
aws ec2 authorize-security-group-ingress --group-id $APP_SG --ip-permissions \
  "IpProtocol=tcp,FromPort=8080,ToPort=8080,IpRanges=[{CidrIp=$FLOCI_IP/32,Description=floci-alb-proxy}]" >/dev/null
```

Egress stays at the default (all outbound): the task must reach GitHub.

On Floci the ALB's group and the database's group are records only: the ALB proxy and the RDS proxy both run inside Floci, outside any filtered namespace. Only the task's group (`APP_SG`) filters packets.

**Check:** `aws ec2 describe-security-groups --group-ids $APP_SG --query 'SecurityGroups[0].IpPermissions[].[FromPort,UserIdGroupPairs[0].GroupId,IpRanges[0].CidrIp]' --output text` shows port 8080 twice: once from `$ALB_SG` and once from `172.31.250.10/32`.

## 10. Certificate (ACM) and trusting Floci's CA

**Why:** the ALB terminates TLS like the real one, so cookies get the `Secure` flag (`PUBLIC_URL` starts with `https://`), and the redirect, `X-Forwarded-Proto` and TLS settings are exercised as in production. Floci's ACM issues the certificate immediately and signs it with Floci's own local CA. Trust that CA once, and the browser and the smee client accept the certificate.

### 10.1 Request the certificate and fetch the CA

```bash
CERT_ARN=$(aws acm request-certificate --domain-name localhost --validation-method DNS \
  --query CertificateArn --output text)
export CERT_ARN; save CERT_ARN
aws acm describe-certificate --certificate-arn $CERT_ARN --query 'Certificate.[Status,DomainName,Issuer]' --output text
curl -s http://localhost:4566/_floci/ca.pem -o "$WORK/floci-ca.pem"
openssl x509 -in "$WORK/floci-ca.pem" -noout -subject
```

The CA's private key lives in Floci's data volume, and it can sign a certificate for **any** domain. Once your system trusts the CA, keep that volume to yourself, and remove the trust when you are done (§22). Deleting the volume creates a new CA; you then trust the new one and request the certificate again.

### 10.2 Trust it in your browser

- **Windows (including a browser used with WSL)**: run this from WSL. Windows asks for confirmation; click **Yes**. Chrome and Edge use the Windows store. Firefox, if it still warns: **Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import**.

  ```bash
  certutil.exe -user -addstore Root "$(wslpath -w "$WORK/floci-ca.pem")"
  ```

- **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$WORK/floci-ca.pem"`
- **Linux (Debian/Ubuntu):** `sudo cp "$WORK/floci-ca.pem" /usr/local/share/ca-certificates/floci-ca.crt && sudo update-ca-certificates`. For Chrome on Linux, also run `certutil -d sql:$HOME/.pki/nssdb -A -t C,, -n floci -i "$WORK/floci-ca.pem"` (package `libnss3-tools`).

Restart the browser afterwards. If you skip this, you can click through the browser's warning instead; the app still works, but you lose the check that the chain is right.

Command-line tools do not need any of this: `lcurl` (from `env.sh`) passes the CA to curl, and §16 gives it to the smee client.

**Check:** the `describe-certificate` line prints `ISSUED  localhost  CN=Floci Local CA`. The browser check comes in §17.

## 11. Database (RDS for PostgreSQL 17)

**Why:** the API needs PostgreSQL 17 with a direct connection, not one through a transaction pooler, because it holds a long-lived `LISTEN` connection for the live updates. Floci runs a real `postgres:17.4` container behind a proxy that terminates TLS. As on RDS, the URL uses `sslmode=require`. The proxy forwards `LISTEN/NOTIFY` unchanged.

```bash
aws rds create-db-subnet-group --db-subnet-group-name $APP \
  --db-subnet-group-description "PR Follower" --subnet-ids $(echo "$PRIVATE_SUBNETS") >/dev/null

PG_VERSION=$(aws rds describe-db-engine-versions --engine postgres \
  --query "DBEngineVersions[?starts_with(EngineVersion, '17.')].EngineVersion" --output text \
  | tr '\t' '\n' | sort -V | tail -1)
echo "PostgreSQL $PG_VERSION"

# No URL-special characters, so the password needs no escaping inside DATABASE_URL
DB_PASS=$(openssl rand -base64 32 | tr -d '/+=')
aws rds create-db-instance \
  --db-instance-identifier $APP \
  --engine postgres --engine-version "$PG_VERSION" \
  --db-instance-class db.t4g.micro \
  --allocated-storage 20 --max-allocated-storage 100 --storage-type gp3 --storage-encrypted \
  --master-username prfollower --master-user-password "$DB_PASS" \
  --db-name pr_follower \
  --db-subnet-group-name $APP --vpc-security-group-ids $DB_SG --no-publicly-accessible \
  --backup-retention-period 7 --deletion-protection \
  --copy-tags-to-snapshot >/dev/null
aws rds wait db-instance-available --db-instance-identifier $APP && echo "database available"

read -r DB_HOST DB_PORT < <(aws rds describe-db-instances --db-instance-identifier $APP \
  --query 'DBInstances[0].Endpoint.[Address,Port]' --output text)
echo "$DB_HOST:$DB_PORT"
printf '%s' "postgres://prfollower:$DB_PASS@$DB_HOST:$DB_PORT/pr_follower?sslmode=require" | store_secret database-url
unset DB_PASS
```

The flags are the production ones. On Floci, `--deletion-protection` really blocks `delete-db-instance`. Backup retention enables snapshots and the restore drill (§20.5). Storage, instance class and Multi-AZ are only recorded. The master user owns `pr_follower`, which is what the API needs: it creates its tables when it first starts.

The endpoint is `pr-follower-floci:7001`, Floci's name on its Docker network, which is where the task connects. From your own machine the same database is at `localhost:7001`.

**Check:** `psql_local -Atc 'select version()'` prints `PostgreSQL 17.4 …`. The first run pulls `postgres:17-alpine`.

## 12. Images (ECR)

**Why immutable tags:** if a tag can be overwritten, "what is running?" has no answer. With `IMMUTABLE`, pushing the same tag twice fails, and Floci enforces that. The commit SHA is the tag; for an uncommitted change, the tag gets a `-dirty<timestamp>` suffix, so each build stays unique.

```bash
for repo in api panel; do
  aws ecr create-repository --repository-name $APP-$repo \
    --image-tag-mutability IMMUTABLE --image-scanning-configuration scanOnPush=true \
    --query repository.repositoryUri --output text
  aws ecr put-lifecycle-policy --repository-name $APP-$repo --lifecycle-policy-text '{"rules":[{"rulePriority":1,"description":"keep 20","selection":{"tagStatus":"any","countType":"imageCountMoreThan","countNumber":20},"action":{"type":"expire"}}]}' >/dev/null
done

REGISTRY=$(aws ecr describe-repositories --repository-names $APP-api \
  --query 'repositories[0].repositoryUri' --output text)
REGISTRY=${REGISTRY%/$APP-api}                 # localhost:4566/000000000000/us-east-1
export REGISTRY; save REGISTRY
aws ecr get-login-password | docker login --username AWS --password-stdin ${REGISTRY%%/*}
```

Build and push. Run `make check` first, and commit if you want a clean tag.

```bash
export TAG=$(git -C "$REPO" rev-parse --short=12 HEAD)$(git -C "$REPO" diff --quiet HEAD || echo "-dirty$(date +%s)")
docker build -t $REGISTRY/$APP-api:$TAG   "$REPO/backend"
docker build -t $REGISTRY/$APP-panel:$TAG "$REPO/frontend"
docker push $REGISTRY/$APP-api:$TAG
docker push $REGISTRY/$APP-panel:$TAG
```

Locally there is no `--platform`: Floci runs a task on the Docker host's own architecture when the task definition names none, so this also works as is on Apple Silicon. The lifecycle policy and scan-on-push are only stored on Floci; they are here to rehearse the AWS commands.

**Check:** `aws ecr describe-images --repository-name $APP-api --query 'imageDetails[].imageTags' --output text` shows `$TAG`.

## 13. IAM and logging

**Why two roles exist, and why we create only one:** a Fargate task has an **execution role**, used by ECS itself to pull images, write logs and fetch secrets, and an optional **task role**, used by *your code* to call AWS. PR Follower never calls AWS, so it gets no task role.

```bash
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
cat > "$WORK/trust.json" <<'EOF'
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ecs-tasks.amazonaws.com"},"Action":"sts:AssumeRole"}]}
EOF
aws iam create-role --role-name $APP-execution --assume-role-policy-document file://$WORK/trust.json >/dev/null
aws iam attach-role-policy --role-name $APP-execution \
  --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy

# Least privilege: read only this app's secrets
cat > "$WORK/read-secrets.json" <<EOF
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"secretsmanager:GetSecretValue",
 "Resource":"arn:aws:secretsmanager:${AWS_REGION}:${ACCOUNT_ID}:secret:${APP}/local/*"}]}
EOF
aws iam put-role-policy --role-name $APP-execution --policy-name read-secrets \
  --policy-document file://$WORK/read-secrets.json
rm "$WORK/trust.json" "$WORK/read-secrets.json"

aws logs create-log-group --log-group-name /ecs/$APP
aws logs put-retention-policy --log-group-name /ecs/$APP --retention-in-days 30
```

**Floci stores these policies but does not enforce them.** A wrong secret ARN in the policy fails on AWS (`ResourceInitializationError`) but not here, so review the policy by eye before you use it on AWS.

**Check:** `aws iam list-attached-role-policies --role-name $APP-execution --query 'AttachedPolicies[].PolicyName' --output text` prints `AmazonECSTaskExecutionRolePolicy`.

## 14. Load balancer

**Why these settings:**

- **Idle timeout 300 s.** Approve and merge wait on GitHub for up to 300 s. The panel allows 300 s, so the load balancer must allow at least as much. The event stream sends a heartbeat every 25 s and never idles. On Floci, with this setting, a 75-second response passed through intact.
- **Health check on `/healthz`** of the panel: nginx answers `ok` itself without touching the API, so a slow GitHub or database cannot take the task out of rotation.
- **Deregistration delay 30 s** instead of 300 s, so a replaced task does not linger during a deploy.
- **Target type `ip`**, as Fargate's `awsvpc` networking requires.
- **Listeners on 8443 (HTTPS) and 8000 (HTTP, redirect only)**, standing in for 443 and 80 (§3).

```bash
LB_ARN=$(aws elbv2 create-load-balancer --name $APP --type application --scheme internet-facing \
  --subnets $(echo "$PUBLIC_SUBNETS") --security-groups $ALB_SG \
  --query 'LoadBalancers[0].LoadBalancerArn' --output text)
aws elbv2 modify-load-balancer-attributes --load-balancer-arn $LB_ARN \
  --attributes Key=idle_timeout.timeout_seconds,Value=300 >/dev/null

TG_ARN=$(aws elbv2 create-target-group --name $APP --protocol HTTP --port 8080 \
  --vpc-id $VPC_ID --target-type ip \
  --health-check-path /healthz --health-check-interval-seconds 15 \
  --healthy-threshold-count 2 --unhealthy-threshold-count 3 \
  --query 'TargetGroups[0].TargetGroupArn' --output text)
aws elbv2 modify-target-group-attributes --target-group-arn $TG_ARN \
  --attributes Key=deregistration_delay.timeout_seconds,Value=30 >/dev/null
export LB_ARN TG_ARN; save LB_ARN TG_ARN

aws elbv2 create-listener --load-balancer-arn $LB_ARN --protocol HTTPS --port 8443 \
  --certificates CertificateArn=$CERT_ARN --ssl-policy ELBSecurityPolicy-TLS13-1-2-2021-06 \
  --default-actions Type=forward,TargetGroupArn=$TG_ARN >/dev/null
aws elbv2 create-listener --load-balancer-arn $LB_ARN --protocol HTTP --port 8000 \
  --default-actions 'Type=redirect,RedirectConfig={Protocol=HTTPS,Port=8443,StatusCode=HTTP_301}' >/dev/null
```

**Check:** the listeners answer before any target exists:

```bash
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' http://localhost:8000/
#   301 https://localhost:8443/
lcurl -s -o /dev/null -w '%{http_code}\n' $PUBLIC_URL/healthz
#   503   (no target yet; the next section adds it)
```

## 15. ECS: cluster, task definition, service

**The task definition** is the heart of it, and it is the AWS one with a single change:

- `api` listens on **8081** (`PORT=8081`), because the panel image's nginx owns 8080 in the same namespace.
- `panel` proxies `/api/*` to `http://127.0.0.1:8081` (`API_UPSTREAM`). It is the only container that receives traffic from the ALB.
- Secrets come from Secrets Manager (`secrets`), the rest from plain `environment`.
- **1 vCPU / 2 GB**: the Dependabot analysis reads repository tarballs in memory, with up to two analyses at once.
- **The one change: `dependsOn` is `START` instead of `HEALTHY`.** Floci keeps the containers' `healthCheck` but does not run it, so a container never becomes `HEALTHY`. With `HEALTHY`, the panel would wait 60 s and the task would fail with `did not reach HEALTHY within 60 seconds`. `START` is safe here because nginx proxies to a literal `127.0.0.1` address and does not need the API up to start. Keep `HEALTHY` on AWS.

The task definition is written once as a template with an `@TAG@` placeholder. Every deploy fills in a tag (§20.1).

```bash
EXEC_ROLE_ARN=$(aws iam get-role --role-name $APP-execution --query Role.Arn --output text)
cat > "$WORK/taskdef.template.json" <<EOF
{
  "family": "$APP",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "1024",
  "memory": "2048",
  "executionRoleArn": "$EXEC_ROLE_ARN",
  "containerDefinitions": [
    {
      "name": "api",
      "image": "$REGISTRY/$APP-api:@TAG@",
      "essential": true,
      "portMappings": [{"containerPort": 8081, "protocol": "tcp"}],
      "environment": [
        {"name": "PORT", "value": "8081"},
        {"name": "PUBLIC_URL", "value": "$PUBLIC_URL"},
        {"name": "ALLOWED_ORIGIN", "value": "$PUBLIC_URL"},
        {"name": "GITHUB_CLIENT_ID", "value": "$GH_CLIENT_ID"},
        {"name": "GITHUB_APP_ID", "value": "$GH_APP_ID"},
        {"name": "GITHUB_APP_SLUG", "value": "$GH_APP_SLUG"}
      ],
      "secrets": [
        {"name": "DATABASE_URL", "valueFrom": "$(secret_arn database-url)"},
        {"name": "GITHUB_CLIENT_SECRET", "valueFrom": "$(secret_arn github-client-secret)"},
        {"name": "GITHUB_WEBHOOK_SECRET", "valueFrom": "$(secret_arn github-webhook-secret)"},
        {"name": "TOKEN_ENCRYPTION_KEY", "valueFrom": "$(secret_arn token-encryption-key)"},
        {"name": "GITHUB_APP_PRIVATE_KEY", "valueFrom": "$(secret_arn github-app-private-key)"}
      ],
      "healthCheck": {
        "command": ["CMD-SHELL", "wget -qO- http://127.0.0.1:8081/healthz >/dev/null || exit 1"],
        "interval": 10, "timeout": 3, "retries": 3, "startPeriod": 30
      },
      "stopTimeout": 20,
      "logConfiguration": {"logDriver": "awslogs", "options": {
        "awslogs-group": "/ecs/$APP", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "api"}}
    },
    {
      "name": "panel",
      "image": "$REGISTRY/$APP-panel:@TAG@",
      "essential": true,
      "portMappings": [{"containerPort": 8080, "protocol": "tcp"}],
      "environment": [{"name": "API_UPSTREAM", "value": "http://127.0.0.1:8081"}],
      "dependsOn": [{"containerName": "api", "condition": "START"}],
      "healthCheck": {
        "command": ["CMD-SHELL", "wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1"],
        "interval": 10, "timeout": 3, "retries": 3, "startPeriod": 10
      },
      "logConfiguration": {"logDriver": "awslogs", "options": {
        "awslogs-group": "/ecs/$APP", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "panel"}}
    }
  ]
}
EOF
sed "s/@TAG@/$TAG/g" "$WORK/taskdef.template.json" > "$WORK/taskdef.json"
python3 -m json.tool "$WORK/taskdef.json" >/dev/null && echo "taskdef.json is valid JSON"
grep -c 'arn:aws:secretsmanager' "$WORK/taskdef.json"     # 5: every secret resolved to an ARN
```

Create the cluster, register the definition and start the service, with the AWS settings:

```bash
aws ecs create-cluster --cluster-name $APP >/dev/null
aws ecs register-task-definition --cli-input-json file://$WORK/taskdef.json \
  --query 'taskDefinition.taskDefinitionArn' --output text

aws ecs create-service --cluster $APP --service-name $APP \
  --task-definition $APP --desired-count 1 --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[$PRIVATE_CSV],securityGroups=[$APP_SG],assignPublicIp=DISABLED}" \
  --load-balancers "targetGroupArn=$TG_ARN,containerName=panel,containerPort=8080" \
  --health-check-grace-period-seconds 60 \
  --deployment-configuration "maximumPercent=200,minimumHealthyPercent=100,deploymentCircuitBreaker={enable=true,rollback=true}" \
  --query 'service.serviceName' --output text
```

How the service is set:

- `desired-count 1`: one API replica only, because sync jobs and the cancel registry live in memory.
- `maximumPercent=200, minimumHealthyPercent=100`: a deploy starts the new task before stopping the old one, so there is no downtime. On Floci too, a forced redeploy answered every request.
- The **deployment circuit breaker with rollback** returns to the last working revision when new tasks keep failing (§20.2 tries it).
- `health-check-grace-period-seconds 60`: on AWS it gives the API time to run its migrations. Floci stores it but does not enforce it.

Watch it start. On AWS you would run `aws ecs wait services-stable`. On Floci that waiter returns too early: during a deploy, the old task still counts as running. `wait_deployed` (from `env.sh`) instead waits until every running task belongs to the current deployment and the ALB target is healthy. This guide uses it after every deploy.

```bash
wait_deployed
aws logs tail /ecs/$APP --since 10m
```

A good start prints `INFO PR Follower Go v1.27 server listening port=8081` and **no** `WARN` or `ERROR`. In particular, `GitHub App credentials not set` means `GITHUB_APP_ID` or the key is missing, and `TOKEN_ENCRYPTION_KEY not set` means the secret did not arrive.

**Check:** `lcurl -s $PUBLIC_URL/healthz` prints `ok`.

## 16. Start the smee client

Run it in a **second terminal** and leave it running while you test:

```bash
source ~/pr-follower-floci/env.sh
NODE_EXTRA_CA_CERTS="$WORK/floci-ca.pem" smee --url "$SMEE_URL" --target "$PUBLIC_URL/api/v1/webhooks/github"
```

`NODE_EXTRA_CA_CERTS` makes the client trust Floci's CA. Without it, every forward fails with `SELF_SIGNED_CERT_IN_CHAIN`.

**Where a delivery's result shows up.** With smee in between, GitHub's **Recent Deliveries** shows smee.io's response, which is a success as soon as smee.io accepts the delivery. The app's real answer is in this terminal, one line per delivery:

```
POST https://localhost:8443/api/v1/webhooks/github - 202
```

Every delivery must be `202`. Watch this terminal, not GitHub, and use **Redeliver** on GitHub to replay one.

## 17. Smoke tests

From the first terminal:

```bash
# HTTP redirects to HTTPS
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' http://localhost:8000/
#   301 https://localhost:8443/

# The panel is up, behind the ALB
lcurl -s $PUBLIC_URL/healthz; echo
#   ok

# The API is reachable through the panel, and protected
lcurl -s -o /dev/null -w '%{http_code}\n' $PUBLIC_URL/api/v1/auth/me
#   401

# Sign-in is configured, PUBLIC_URL is right, cookies are Secure
lcurl -s -o /dev/null -D - $PUBLIC_URL/api/v1/auth/github/login | grep -iE '^(location|set-cookie):'
#   location: https://github.com/login/oauth/authorize?client_id=Iv23...&redirect_uri=https%3A%2F%2Flocalhost%3A8443%2Fapi%2Fv1%2Fauth%2Fgithub%2Fcallback&state=...
#   set-cookie: pf_oauth=...; Path=/api/v1/auth/github; Max-Age=600; HttpOnly; Secure; SameSite=Lax

# Webhooks are reachable and refuse unsigned deliveries
lcurl -s -o /dev/null -w '%{http_code}\n' -X POST $PUBLIC_URL/api/v1/webhooks/github -d '{}'
#   401

# The webhook secret matches: a ping signed the way GitHub signs deliveries
body='{"zen":"smoke test"}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$(secret_value github-webhook-secret)" | sed 's/^.* //')
lcurl -s -w ' %{http_code}\n' -X POST $PUBLIC_URL/api/v1/webhooks/github \
  -H 'Content-Type: application/json' -H 'X-GitHub-Event: ping' \
  -H "X-Hub-Signature-256: sha256=$sig" --data "$body"
#   {"accepted":true} 202
```

- `location: /?auth_error=not_configured`: the client ID or client secret is missing.
- A `redirect_uri` on another origin: `PUBLIC_URL` is wrong.
- No `Secure` on the cookie: `PUBLIC_URL` does not start with `https://`.

Then test the whole relay with the same signed ping, sent to the smee channel instead of to the ALB:

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X POST "$SMEE_URL" \
  -H 'Content-Type: application/json' -H 'X-GitHub-Event: ping' \
  -H "X-Hub-Signature-256: sha256=$sig" --data "$body"
#   200   (smee.io accepted it); the smee terminal prints: POST https://localhost:8443/api/v1/webhooks/github - 202
```

Finally, in the browser, open `https://localhost:8443`. The sign-in screen loads with **no certificate warning**: the padlock shows a certificate for `localhost` issued by `Floci Local CA`.

## 18. End to end with GitHub

Keep the smee terminal in view: every delivery must print `- 202`.

1. **Redeliver the ping.** On the app's **Advanced → Recent Deliveries**, open the first `ping` and click **Redeliver**. smee prints `- 202`.
2. **Sign in.** Open `https://localhost:8443`, click **Entrar com GitHub** (*Sign in with GitHub*) and authorize the app. You land in the workspace with your avatar. In the browser's dev tools, the `pf_session` cookie is `HttpOnly`, `Secure`, `SameSite=Lax` and expires in 30 days.
3. **Install.** Open **Seats de Repositórios → + Importar repositório → + Adicionar conta do GitHub** and install the app on the test repository only. GitHub returns you to the import screen with that account selected; the `installation` delivery prints `202`.
4. **Import.** Follow the test repository. Its open PRs appear in its Seat.
5. **Webhook to live update.** Open **Linha do Tempo** and leave it open. Open a PR on the test repository: it appears in the timeline within seconds, without a reload, and its `pull_request` delivery prints `202`. This proves the relay, the ALB, the database's `LISTEN/NOTIFY` and the live stream through the ALB and nginx. In dev tools, the `events/stream` request stays open.
6. **Access removal.** On GitHub, remove the test repository from the installation (**Configure** on the installation page). Its Seat shows "Sem acesso" (*No access*) within seconds; add it back and the warning clears. Both `installation_repositories` deliveries print `202`.
7. **Restart the task.** `aws ecs update-service --cluster $APP --service $APP --force-new-deployment >/dev/null && wait_deployed`, then reload the page and click **Sincronizar** (*Sync*). You are still signed in and the sync finishes. If `TOKEN_ENCRYPTION_KEY` were missing, the restart would make your GitHub token unreadable and sign you out here.
8. **Restart Floci itself** (like a host reboot): `docker compose -f "$WORK/compose.yaml" restart`. Within about 30 s Floci recreates the task, the database comes back with its data, and the page works again without a new sign-in. The API logs one `activity listener disconnected; reconnecting`, which is expected.
9. **Sign out.** **Sair** returns you to the sign-in screen; signing in again works.

## 19. The remaining flows

### 19.1 Installation token and file reads

- Open a PR that touches a file you own. Its drawer shows the changed files, owned ones first with "Seu".
- **Pass:** files also appear for a PR opened while you are signed out of the app, because the webhook read used the app's own installation token. If files stay "not evaluated", check the app's *Contents* and *Pull requests* read access.

### 19.2 Dependabot analysis

- Wait for a Dependabot PR on the test repository (§7). It appears in the Radar; open it and click **Analisar agora** (*Analyze now*) if it is "Sem análise".
- **Pass:** a risk tier, a dependency path and a blast radius appear, and `aws logs tail /ecs/$APP --since 10m --filter-pattern dependabot` shows no `dependabot analysis failed`.
- A grouped update PR stays "Sem análise", with its own explanation and no button.

### 19.3 Acting as the user

Use PRs authored by the second account.

1. **Approve** from the drawer: GitHub shows the review under *your* name, not the app's. Approve again: nothing is repeated.
2. **Checks running:** push a commit while a workflow runs; approve and merge are disabled with the reason, and the API answers `409 checks_running`.
3. **`@dependabot rebase`** from the drawer on a Dependabot PR: GitHub shows the comment under your name and Dependabot reacts.
4. **Merge** (squash) a PR: it is merged and shows as merged in the timeline. Merging a PR that is already merged converges to success.
5. On your own PR, approve is not offered; merge is.
6. "Resource not accessible by integration" means the installation has not accepted the app's permissions: accept them on the installation page.

### 19.4 CODEOWNERS refresh on push

1. On the default branch, edit `.github/CODEOWNERS` (change an owner) and push. The `push` delivery prints `202`; Seats reflect the new rules and the timeline announces the change.
2. Delete the file and push: the repository becomes "no CODEOWNERS". Restore it.
3. **Large push:** push more than 20 commits at once, with a CODEOWNERS edit among the first ones (`for i in $(seq 25); do git commit --allow-empty -m "c$i"; done; git push`). **Pass:** the rules still refresh, even though GitHub truncates the commit list.

### 19.5 Commit statuses

- Create a status by hand: `gh api repos/OWNER/REPO/statuses/<head-sha> -f state=pending -f context=ci/test`, then the same with `-f state=success`.
- **Pass:** the `status` deliveries print `202`; the drawer's check list shows `ci/test`; pending keeps the PR from looking green, and success clears it.
- Import check: create a status on an open PR's head, then unfollow and import the repository again. The status must appear from the combined-status read.

### 19.6 Sync after downtime

1. Take the app down: `aws ecs update-service --cluster $APP --service $APP --desired-count 0 >/dev/null`. The ALB now answers `503`.
2. On GitHub: open a PR, merge another, and push a commit to a third. smee prints `503` for those deliveries, and that is the point: they are lost.
3. Bring it back: `aws ecs update-service --cluster $APP --service $APP --desired-count 1 >/dev/null && wait_deployed`.
4. Click **Sincronizar**. **Pass:** progress shows "Sincronizando n de m: owner/repo"; the new PR appears, the merged one closes, and the third shows the new head commit with fresh checks.
5. Cancel a sync midway: the panel stops, and `GET /api/v1/sync` reports it cancelled.
6. **Rate-limit headers:** `curl -si -H "Authorization: Bearer $(gh auth token)" https://api.github.com/rate_limit | grep -i x-ratelimit` shows `X-RateLimit-Remaining` and `X-RateLimit-Reset`, the headers the client reads.

## 20. Infrastructure drills

These rehearse the operations you will do on AWS, using the same commands.

### 20.1 Deploy a new version

```bash
export TAG=$(git -C "$REPO" rev-parse --short=12 HEAD)$(git -C "$REPO" diff --quiet HEAD || echo "-dirty$(date +%s)")
docker build -t $REGISTRY/$APP-api:$TAG   "$REPO/backend"
docker build -t $REGISTRY/$APP-panel:$TAG "$REPO/frontend"
docker push $REGISTRY/$APP-api:$TAG
docker push $REGISTRY/$APP-panel:$TAG

# Migration in this release? Take a snapshot first, as on AWS.
aws rds create-db-snapshot --db-instance-identifier $APP --db-snapshot-identifier $APP-pre-$TAG \
  --query DBSnapshot.Status --output text

sed "s/@TAG@/$TAG/g" "$WORK/taskdef.template.json" > "$WORK/taskdef.json"
aws ecs register-task-definition --cli-input-json file://$WORK/taskdef.json --query 'taskDefinition.revision' --output text
aws ecs update-service --cluster $APP --service $APP --task-definition $APP >/dev/null
wait_deployed
```

`--task-definition $APP`, with no revision, means the latest one. To watch for downtime during a deploy, run this in another terminal; every line should be `401`, the API answering:

```bash
while true; do lcurl -s -o /dev/null -w '%{http_code} ' -m 3 $PUBLIC_URL/api/v1/auth/me; sleep 1; done
```

A rerun of the §17 smoke tests completes the deploy. Deliveries that hit the swap (a non-`202` line in the smee terminal) can be redelivered from GitHub.

### 20.2 A deploy that fails rolls itself back

Deploy an image tag that does not exist:

```bash
sed "s/@TAG@/does-not-exist/g" "$WORK/taskdef.template.json" > "$WORK/taskdef-bad.json"
aws ecs register-task-definition --cli-input-json file://$WORK/taskdef-bad.json --query 'taskDefinition.revision' --output text
aws ecs update-service --cluster $APP --service $APP --task-definition $APP >/dev/null
sleep 30
aws ecs list-service-deployments --cluster $APP --service $APP --query 'serviceDeployments[0].status' --output text
#   ROLLBACK_SUCCESSFUL
aws ecs describe-services --cluster $APP --services $APP --query 'services[0].taskDefinition' --output text
#   ...:task-definition/pr-follower:<the previous revision>
```

The old task keeps serving the whole time. The rollback goes to the last revision that actually ran. That is why every deploy in this guide waits with `wait_deployed`: a revision that never got its task running does not count. Deregister the broken revision so that "latest" is a good one again:

```bash
aws ecs deregister-task-definition --task-definition \
  $(aws ecs list-task-definitions --family-prefix $APP --sort DESC --query 'taskDefinitionArns[0]' --output text) >/dev/null
```

Do not add `--max-items` to that listing: combined with `--query … --output text`, the CLI prints `None`.

### 20.3 Roll back by hand

```bash
aws ecs list-task-definitions --family-prefix $APP --sort DESC --query 'taskDefinitionArns[:5]' --output text | tr '\t' '\n'
printf 'Revision to roll back to: '; read -r REV
aws ecs update-service --cluster $APP --service $APP --task-definition "${APP}:$REV" >/dev/null
wait_deployed
```

Roll back by task definition only if no migration shipped since that revision. Migrations are forward-only; otherwise fix forward, or restore the pre-deploy snapshot (§20.5).

### 20.4 Restart Floci

Done in §18 step 8. A Docker Desktop restart is the same thing; see §21.

### 20.5 Restore the database from a snapshot

A restore always creates a **new** instance, with a new endpoint. You then point `DATABASE_URL` at it and redeploy, exactly as on AWS.

```bash
aws rds create-db-snapshot --db-instance-identifier $APP --db-snapshot-identifier $APP-drill >/dev/null
aws rds wait db-snapshot-available --db-snapshot-identifier $APP-drill && echo "snapshot ready"

aws rds restore-db-instance-from-db-snapshot --db-instance-identifier $APP-restored \
  --db-snapshot-identifier $APP-drill --db-subnet-group-name $APP --vpc-security-group-ids $DB_SG >/dev/null
aws rds wait db-instance-available --db-instance-identifier $APP-restored && echo "restored"

read -r NEW_HOST NEW_PORT < <(aws rds describe-db-instances --db-instance-identifier $APP-restored \
  --query 'DBInstances[0].Endpoint.[Address,Port]' --output text)
secret_value database-url | sed "s|@[^/]*/|@$NEW_HOST:$NEW_PORT/|" | update_secret database-url
aws ecs update-service --cluster $APP --service $APP --force-new-deployment >/dev/null
wait_deployed && echo "now on $NEW_HOST:$NEW_PORT"
```

**Pass:** you are still signed in and your follows are there. The restore needs the `TOKEN_ENCRYPTION_KEY` that was in use when the snapshot was taken; with another key, users sign in again. Recent deliveries that failed in between can be redelivered, and **Sincronizar** catches up the rest.

On Floci, a snapshot is a `pg_dumpall` taken when you request it, and point-in-time restore always returns the latest state. Rehearse the commands here; test real point-in-time recovery on AWS.

To go back to the original instance, point the secret at `$APP`'s endpoint (`pr-follower-floci:7001`) the same way and redeploy. Then delete the copy with `aws rds delete-db-instance --db-instance-identifier $APP-restored --skip-final-snapshot`.

### 20.6 Rotate the webhook secret

```bash
openssl rand -hex 32 | tr -d '\n' | update_secret github-webhook-secret
secret_value github-webhook-secret     # paste it on the app: General → Webhook secret → Save
aws ecs update-service --cluster $APP --service $APP --force-new-deployment >/dev/null
wait_deployed
```

ECS reads secrets only when a task **starts**, so a new value takes effect only after the redeploy. Deliveries between the GitHub change and the new task print `401` in smee; redeliver them. The client secret, the private key and the database password rotate the same way: write the new value with `update_secret`, change it on the other side (GitHub, or the database), then redeploy.

### 20.7 Rotate `TOKEN_ENCRYPTION_KEY`

```bash
openssl rand -base64 32 | tr -d '\n' | update_secret token-encryption-key
aws ecs update-service --cluster $APP --service $APP --force-new-deployment >/dev/null
wait_deployed
```

**Pass:** at your next GitHub-backed action (open a drawer, **Sincronizar**) you are signed out with "Seu acesso ao GitHub expirou" (*Your GitHub access expired*), because stored tokens became unreadable. Signing in again works. Rotate this key on a suspected leak, not routinely.

## 21. Day to day

- **After a reboot or a Docker Desktop restart:** `source ~/pr-follower-floci/env.sh && docker compose -f "$WORK/compose.yaml" up -d`. Floci brings back the database, the ALB and the service by itself; the app answers within about 30 s. Start the smee client again (§16).
- **Logs:**

  ```bash
  aws logs tail /ecs/$APP --follow --since 10m                            # both containers
  aws logs tail /ecs/$APP --follow --since 10m --log-stream-name-prefix api
  aws logs tail /ecs/$APP --follow --filter-pattern ERROR                 # errors only
  docker logs -f pr-follower-floci                                        # Floci itself
  aws ecs describe-services --cluster $APP --services $APP --query 'services[0].events[:5].message'
  ```

- **psql:** `psql_local` opens a session. Housekeeping SQL:

  ```sql
  -- Sign everyone out
  TRUNCATE sessions;
  -- Sign one user out
  DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE login = 'octocat');
  -- Purge expired sessions
  DELETE FROM sessions WHERE expires_at < extract(epoch FROM now());
  -- Latest migration applied
  SELECT max(version_id) FROM goose_db_version;
  ```

- **Containers Floci started:** `docker ps --filter name=floci-aws-pr-follower`.
- **A task that will not start:** `aws ecs describe-tasks --cluster $APP --tasks $(aws ecs list-tasks --cluster $APP --desired-status STOPPED --query 'taskArns[0]' --output text) --query 'tasks[0].stoppedReason'`.

## 22. Tear down

1. Stop Floci and delete its state. Floci removes the task and database containers when it stops, but by design it keeps the registry container and the database volumes:

   ```bash
   docker compose -f "$WORK/compose.yaml" down -v
   docker rm -f floci-aws-pr-follower-ecr-registry
   docker volume rm $(docker volume ls -q --filter name=floci-aws-pr-follower)
   docker network rm pr-follower-net
   docker rmi $(docker images --format '{{.Repository}}:{{.Tag}}' | grep "^localhost:4566/000000000000/")
   docker logout localhost:4566
   ```

   `down -v` may report that the network is still in use; the `docker network rm` at the end removes it.

2. Stop trusting Floci's CA. Windows: `certutil.exe -user -delstore Root "Floci Local CA"`. macOS: `sudo security delete-certificate -c "Floci Local CA" /Library/Keychains/System.keychain`. Linux: `sudo rm /usr/local/share/ca-certificates/floci-ca.crt && sudo update-ca-certificates --fresh`.
3. On GitHub: uninstall the app from the test repository, then delete it (**app → Advanced → Delete GitHub App**), or keep it for next time.
4. `rm -rf ~/pr-follower-floci`. Stop the smee client; an unused channel needs no cleanup.

To **start over** instead, do step 1, then continue from §3. The volume held the secrets, so store new ones (§4, §6.7) and paste the new webhook secret into the app. The volume also held the CA, so trust the new one (§10).

## 23. What this does not test

Test these on AWS itself:

| Area | On Floci | On AWS |
|---|---|---|
| Container health checks | Not run, so the panel depends on the API with `START` (§15), and the task's health stays `UNKNOWN`. | `HEALTHY`; a failing health check replaces the task. |
| Security groups | Enforced only around the task; one extra rule admits Floci's ALB proxy (§9). The database's group is not enforced. | All three groups enforced; there is no extra rule. |
| Network | No route tables and no NAT gateway; egress goes through Docker. | Private subnets reach GitHub through the NAT gateway. A wrong route table shows up only there. |
| IAM | Policies stored, not enforced. | The execution role must really be allowed to read each secret. |
| Certificate and DNS | Floci's CA, issued immediately; `localhost`. | ACM with DNS validation in Route 53, and a real domain. |
| RDS | TLS by Floci's proxy (not the RDS CA bundle), no Multi-AZ, snapshots are logical copies, no real point-in-time recovery. | The real engine and backups. |
| ECR | Lifecycle policies and scanning stored only. | Enforced and run. |
| Monitoring | CloudWatch alarms on ALB metrics are not covered here. | The alarms in the AWS guide. |
| Webhook delivery | Through smee; GitHub sees smee.io's answer. | GitHub calls the ALB directly; Recent Deliveries shows the app's own answer. |
| Grace periods | `health-check-grace-period-seconds` stored, not enforced. | Enforced. |
| Deploy waits | `aws ecs wait services-stable` returns while the old task still runs; use `wait_deployed` (§15). | The waiter waits for the new deployment. |

## 24. Troubleshooting

| Symptom | Likely cause | What to do |
|---|---|---|
| `aws` answers with a real account, or `InvalidClientTokenId` / `ExpiredToken` | `env.sh` is not sourced in this terminal, so the command went to real AWS. | `source ~/pr-follower-floci/env.sh`; `aws sts get-caller-identity` must print `000000000000`. |
| `docker compose up`: `port is already allocated` on 4566 | Another Floci (for example from `floci start`) or LocalStack is running. | `floci stop`, or stop the other container. |
| `docker compose up`: `Pool overlaps with other one on this address space` | Another Docker network uses `172.31.250.0/24`. | Pick a free `/24`; change it in `compose.yaml` (subnet and `ipv4_address`) and `FLOCI_IP` in `env.sh` (§3). |
| `docker push`: `lookup …dkr.ecr…localhost: no such host` or an HTTPS error | The repository URIs are in the `*.localhost` style. | Check `FLOCI_SERVICES_ECR_URI_STYLE: path` in `compose.yaml`, recreate Floci, and use the `repositoryUri` that `describe-repositories` returns (§12). |
| `docker push`: `tag invalid` or `already exists` | Immutable tags: this tag was pushed before. | Use a new `TAG` (§12 adds `-dirty<timestamp>` to uncommitted builds). |
| A deploy reports done, but the old version still answers; or the circuit breaker rolls back further than the previous revision | The deploy was awaited with `aws ecs wait services-stable`, which returns early on Floci. | Use `wait_deployed` after every `update-service` (§15). |
| Task stops: `did not reach HEALTHY within 60 seconds` | `dependsOn` uses `HEALTHY`, and Floci does not run health checks. | Use `START` locally (§15). |
| Task stops: `ResourceInitializationError: unable to pull secrets` | A `valueFrom` ARN points at a secret that does not exist. | `grep valueFrom "$WORK/taskdef.json"` and compare with `aws secretsmanager list-secrets`. |
| Task stops: `CannotPullContainerError` or `TaskFailedToStart` on the image | The tag was not pushed to Floci's ECR. | `aws ecr describe-images --repository-name $APP-api`. |
| API log: `failed to connect` to the database | `DATABASE_URL` does not point at the instance's endpoint. | `aws rds describe-db-instances --query 'DBInstances[].Endpoint'`; fix it with `update_secret database-url`, then redeploy. |
| Target `unhealthy` with `Target.Timeout` | `APP_SG` does not admit `FLOCI_IP` on 8080, or Floci's address changed. | §9 check; `docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' pr-follower-floci` must print `FLOCI_IP`. |
| ALB answers `503` | No registered target: the service is at 0 tasks, or the task keeps failing. | `aws ecs describe-services …` (§21) and the stopped reason. |
| ALB answers `502` right after a deploy or a Floci restart | The new task is still starting. | Wait a few seconds. |
| Browser: `NET::ERR_CERT_AUTHORITY_INVALID` | The CA is not trusted, or the volume was reset and a new CA was made. | §10.2 with the current `floci-ca.pem`; restart the browser. |
| smee: `SELF_SIGNED_CERT_IN_CHAIN` | The client started without the CA. | Start it as in §16, with `NODE_EXTRA_CA_CERTS`. |
| smee: `POST … - 401` | The webhook secret on GitHub differs from the one stored (§20.6). If only an occasional delivery gets `401` while others get `202`: smee rebuilds the JSON body before forwarding, and for some payloads the rebuilt body no longer matches GitHub's signature. | Compare with `secret_value github-webhook-secret` and set the same on the app. For the occasional case, Redeliver does not help; that event is covered by **Sincronizar**. |
| GitHub shows deliveries as successful, but nothing happens | GitHub sees smee.io's answer; the smee client is not running or points elsewhere. | Look at the smee terminal (§16). |
| GitHub: "The redirect_uri is not associated with this application" | The Callback URL differs from `PUBLIC_URL` + `/api/v1/auth/github/callback`. | Fix it on the app (§6.1). |
| Sign-in lands on `?auth_error=state_mismatch` | "Request user authorization (OAuth) during installation" is on, the attempt took more than 10 minutes, or you opened another address than `https://localhost:8443`. | §6.1; always use `PUBLIC_URL`. |
| Sign-in lands on `?auth_error=login_failed` | GitHub refused the code exchange: wrong client secret or Callback URL. | Check both; the API log shows the cause. |
| API log: `GitHub App credentials not set` | `GITHUB_APP_ID` or the private key did not reach the task. | Check `GH_APP_ID` in `ids.sh`, the `github-app-private-key` secret, and redeploy. |
| The timeline updates only on reload | The event stream is cut or buffered. | In dev tools, `events/stream` must stay open. Floci's ALB does not buffer it; look for something else in between (a VPN or a browser extension). |
| Nothing answers after a reboot | Floci is not running. | `docker compose -f "$WORK/compose.yaml" up -d` (§21). |

## Sources

Checked on 2026-11-11 against Floci 2.4.0 (documentation and source at <https://github.com/floci-io/floci>) and by running the steps:

- **ECS** (`docs/services/ecs.md`): tasks are real containers; `secrets` are resolved from Secrets Manager by ARN; `awslogs` streams to CloudWatch Logs; with security-group enforcement, a task's containers share one network namespace. The source (`EcsContainerManager`) shows that `dependsOn: HEALTHY` reads Docker's own health status and that the task definition's `healthCheck` is not applied to the container, hence `START`.
- **ELBv2** (`docs/services/elb.md`): HTTP and HTTPS listeners forward to `ip` targets, terminate TLS with ACM certificates, and ignore security groups for their own traffic.
- **RDS** (`docs/services/rds.md`): a real PostgreSQL container behind a TLS proxy, the proxy port range, `FLOCI_SERVICES_RDS_ENDPOINT_HOST`, snapshots as `pg_dumpall` copies.
- **ECR** (`docs/services/ecr.md`): a `registry:2` backing store, immutable tags, `FLOCI_SERVICES_ECR_URI_STYLE=path` for Docker's containerd image store.
- **ACM** (`docs/services/acm.md`): certificates are issued immediately and signed by Floci's local CA, served at `/_floci/ca.pem`.
- **smee-client 5.0.0** (source): forwards with `undici`, which honors `NODE_EXTRA_CA_CERTS`, and re-serializes the JSON body before posting it.
