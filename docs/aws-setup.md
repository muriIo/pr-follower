# Deploying on AWS

A step-by-step walkthrough for running PR Follower on AWS, written as a study document: before each group of steps it says **which service we pick and why**, and what we rejected. It complements [`production-setup.md`](production-setup.md), which stays the reference for everything that is not AWS-specific (the GitHub App, the environment variables, the smoke tests, rotation). Where a step is identical there, this file points to it instead of repeating it.

Status: written 2026-11-11 against the code at that date and the AWS documentation (looked up through Context7; the sources are listed at the end). **The commands were checked for JSON and shell syntax but not run against a real AWS account.** Run it once in a throwaway account or region and fix what differs, the way [`real-github-verification.md`](real-github-verification.md) does for GitHub.

Commands are written for `bash` and the AWS CLI v2. Each section ends with a check you can run before moving on.

## 1. What we build, and why these services

Constraints that come from the code, not from taste:

| Fact about PR Follower | Consequence for AWS |
|---|---|
| The API keeps a **live SSE stream** open per browser and a long-lived Postgres `LISTEN` connection, and runs **background workers** (sync jobs, Dependabot analysis). | It needs an always-on process. Request-scoped compute (Lambda) does not fit. |
| **Any replica serves any request**: activity, sync jobs and cancels are shared through Postgres (ADR-0012, [`production-setup.md`](production-setup.md) §13.10). | No sticky sessions. This guide starts with one small task; more tasks scale it out. |
| Approve/merge wait on GitHub for up to **300 s**; the stream idles between 25 s heartbeats. | The load balancer's idle timeout must be raised above its 60 s default. |
| GitHub webhooks need a **public HTTPS endpoint** with a publicly trusted certificate, and answer within 10 s. | Public load balancer with an ACM certificate. |
| The API calls `github.com`, `api.github.com` and `codeload.github.com` over HTTPS. | Outbound internet from wherever the API runs. |
| Postgres 17 or newer, with `LISTEN/NOTIFY`, needs a direct (not transaction-pooled) connection. | Plain RDS for PostgreSQL, no pooler. |

The result:

```
                    Route 53 (A alias)
                          │
GitHub webhooks ──┐       ▼
Browsers ─────────┴─▶ Application Load Balancer :443 (ACM certificate, idle timeout 300 s)
                          │  public subnets, 2 AZs
          ────────────────┼───────────────────────────────────
                          ▼  private subnets
              ┌─ ECS Fargate task (1) ──────────────────────┐
              │  panel (nginx :8080) ──127.0.0.1:8081──▶ api │──▶ NAT gateway ──▶ github.com
              └───────────────────────┬─────────────────────┘
                                      ▼
                         RDS for PostgreSQL 17 (private)

 ECR (images) · Secrets Manager (secrets) · CloudWatch Logs (logs) · IAM (execution role)
```

### Service choices

| Need | We choose | Why | Considered and not chosen |
|---|---|---|---|
| Run the containers | **ECS on Fargate** | The images already exist; Fargate removes servers to patch. A task is the right unit: it gives the `awsvpc` network (own ENI and security group) and restarts a crashed container. | **Lambda / API Gateway**: no long-lived SSE, no background workers, no `LISTEN`. **EC2**: works, but you patch and size the host. **EKS**: a control plane to run for a single replica. If you already run EKS, use `deploy/kubernetes/` instead ([`INSTALL.md`](../INSTALL.md) path B) with `examples/ingress-alb.yaml` (AWS Load Balancer Controller) and `examples/external-secrets.yaml` (Parameter Store or Secrets Manager), and the RDS settings of §7 below. **App Runner**: simpler, but fewer knobs for timeouts and network egress; not evaluated here. |
| How the containers are packaged | **One task, two containers** (`api` + `panel`) | Containers of a task share `localhost`, so the panel proxies to `127.0.0.1:8081`. No service discovery, no internal load balancer, and the panel can never hold a stale address of a replaced API (the nginx caveat in `production-setup.md` §13.2). Scaling out adds whole tasks (an `api` + `panel` pair each), so the pair is never scaled apart. | Two ECS services plus Cloud Map/Service Connect: more moving parts for nothing. |
| Public entry | **Application Load Balancer** | Layer 7 HTTPS with ACM certificates, health checks and an adjustable **idle timeout** (default 60 s, range 1–4000 s). It forwards `X-Forwarded-Proto`. | **Network Load Balancer**: no TLS termination by ACM at layer 7 for this use, no HTTP health semantics. **CloudFront** in front: a second timeout to manage, nothing to cache (the app is dynamic). **API Gateway**: it is for APIs with request/response limits; wrong for streams. |
| Database | **RDS for PostgreSQL 17**, single instance, Multi-AZ later | Managed backups with point-in-time recovery, patching, encryption. `rds.force_ssl` is **on by default from PostgreSQL 15**, so connections are encrypted with `sslmode=require`. A plain instance gives a direct connection for `LISTEN`. | **Aurora PostgreSQL**: more capability (and cost and complexity) than this app needs; reconsider at scale. **RDS Proxy**: pools connections, which would be wrong for `LISTEN`. **Postgres on EC2/Fargate**: you own backups and upgrades. |
| Secrets | **Secrets Manager** | ECS injects a secret into a container's environment by ARN (`valueFrom`); the **task execution role** fetches it, the app code never calls AWS. Versioning and rotation hooks for later. | **SSM Parameter Store (SecureString)**: also supported by ECS, free in the standard tier. A legitimate cheaper choice; we pay about $0.40 per secret per month for rotation support. |
| Certificates | **ACM** (public certificate, DNS validation) | Free, renews itself, attaches to the ALB. | Let's Encrypt on a proxy container: you run renewal. |
| DNS | **Route 53** alias record | An alias to the ALB costs nothing per query and follows the ALB's addresses. | Any DNS provider works with a CNAME; ACM validation then needs a CNAME at that provider. |
| Images | **ECR**, immutable tags | Same account, no pull-rate limits, scan on push, and immutable tags enforce "deploy by tag, never `latest`". | Docker Hub / GHCR: work, but add credentials to manage. |
| Logs | **CloudWatch Logs** (`awslogs` driver) | Zero setup for ECS; the API logs structured text to stdout. | A log shipper sidecar: not needed at this size. |
| Outbound internet | **Private subnets + NAT gateway** | The tasks have no public IP, so nothing can reach them except the ALB. The NAT also carries ECR, Secrets Manager and CloudWatch traffic, since a Fargate task reaches these through its own network interface. | **Public subnets with public IPs** and a security group that only admits the ALB: saves the NAT cost (below) with similar inbound exposure. A valid trade for a small deployment. **VPC endpoints** for ECR/Secrets/Logs: cheaper at scale, but the GitHub calls still need a NAT. |

### Roughly what it costs

Order of magnitude for one region at on-demand list prices (US East; check the [AWS Pricing Calculator](https://calculator.aws/) for yours, São Paulo costs more):

| Item | About / month |
|---|---|
| NAT gateway (hourly) + data | $33 + $0.045/GB |
| Application Load Balancer | $16+ |
| Fargate, 1 vCPU / 2 GB, always on | $36 |
| RDS `db.t4g.micro` + 20 GB gp3 | $12 + $3 |
| Secrets Manager, 5 secrets | $2 |
| Route 53 zone, CloudWatch Logs, ECR | $1–5 |
| **Total** | **≈ $100** |

The NAT gateway is a third of it. The cheapest variant (tasks in public subnets, no NAT) is about $65.

## 2. Prerequisites

- An AWS account and an **IAM role or user with admin rights** for the setup (never the root user), and the [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) configured (`aws sts get-caller-identity` works).
- Docker, `jq`, `openssl`, and this repository at the commit you ship, with `make check` green.
- A domain whose DNS you control. This guide uses a Route 53 hosted zone; with another provider, create the records it prints by hand.
- The values generated in [`production-setup.md`](production-setup.md) §2: webhook secret, token encryption key, database password. Keep them in your password manager; this guide stores them in Secrets Manager.

Every command runs as written in **bash and zsh** (the macOS default). Where the two differ, the guide uses the form both accept: prompts are a `printf` followed by `read` (zsh's `read -p` means something else), lists of IDs are split with an unquoted `$(…)` (zsh does not split an unquoted `$VAR`), and a variable followed by `:` is written `${VAR}:` (zsh reads `$VAR:x` as a modifier).

Set the variables used throughout (put them in a file and `source` it in each new terminal):

```bash
export AWS_REGION=us-east-1        # pick the region closest to your users
export AWS_PAGER=""                # no pager for CLI output
export APP=pr-follower
export DOMAIN=app.example.com
export ZONE_NAME=example.com       # the Route 53 hosted zone that contains $DOMAIN
export ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export REGISTRY=$ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com
export TAG=$(git rev-parse --short=12 HEAD)
```

## 3. Network

**Why:** the ALB must sit in public subnets in at least two Availability Zones; the tasks and the database sit in private subnets that have no route from the internet. The NAT gateway gives the private tasks their way out.

Use the console wizard rather than a dozen `ec2` commands: **VPC → Create VPC → VPC and more**, with:

| Setting | Value |
|---|---|
| Name tag auto-generation | `pr-follower` |
| IPv4 CIDR | `10.0.0.0/16` |
| Availability Zones | 2 |
| Public subnets / private subnets | 2 / 2 |
| NAT gateways | **In 1 AZ** (one gateway; "1 per AZ" doubles the cost for resilience you can add later) |
| VPC endpoints | **S3 Gateway** (free, and keeps image-layer downloads off the NAT) |
| DNS hostnames / DNS resolution | both enabled |

Then collect the IDs (the wizard names things `pr-follower-vpc`, `pr-follower-subnet-public1-…`):

```bash
export VPC_ID=$(aws ec2 describe-vpcs --filters Name=tag:Name,Values=$APP-vpc \
  --query 'Vpcs[0].VpcId' --output text)
export PUBLIC_SUBNETS=$(aws ec2 describe-subnets \
  --filters Name=vpc-id,Values=$VPC_ID Name=tag:Name,Values='*public*' \
  --query 'Subnets[].SubnetId' --output text)
export PRIVATE_SUBNETS=$(aws ec2 describe-subnets \
  --filters Name=vpc-id,Values=$VPC_ID Name=tag:Name,Values='*private*' \
  --query 'Subnets[].SubnetId' --output text)
export PRIVATE_CSV=$(echo "$PRIVATE_SUBNETS" | tr -s '\t ' ',')   # subnet-a,subnet-b for ECS
echo "$VPC_ID | public: $PUBLIC_SUBNETS | private: $PRIVATE_CSV"
```

The lists come back tab-separated. Commands that take one argument per subnet use `$(echo "$PRIVATE_SUBNETS")`, which splits into separate arguments in both shells.

**Check:** two subnet IDs in each list.

## 4. Security groups

**Why:** each tier admits only the tier in front of it, by security-group reference rather than by IP range. The internet reaches the ALB; only the ALB reaches the task; only the task reaches the database.

```bash
ALB_SG=$(aws ec2 create-security-group --group-name $APP-alb --description "PR Follower ALB" \
  --vpc-id $VPC_ID --query GroupId --output text)
APP_SG=$(aws ec2 create-security-group --group-name $APP-app --description "PR Follower tasks" \
  --vpc-id $VPC_ID --query GroupId --output text)
DB_SG=$(aws ec2 create-security-group --group-name $APP-db --description "PR Follower database" \
  --vpc-id $VPC_ID --query GroupId --output text)

aws ec2 authorize-security-group-ingress --group-id $ALB_SG --ip-permissions \
  'IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=0.0.0.0/0}]' \
  'IpProtocol=tcp,FromPort=80,ToPort=80,IpRanges=[{CidrIp=0.0.0.0/0}]' >/dev/null
aws ec2 authorize-security-group-ingress --group-id $APP_SG --ip-permissions \
  "IpProtocol=tcp,FromPort=8080,ToPort=8080,UserIdGroupPairs=[{GroupId=$ALB_SG}]" >/dev/null
aws ec2 authorize-security-group-ingress --group-id $DB_SG --ip-permissions \
  "IpProtocol=tcp,FromPort=5432,ToPort=5432,UserIdGroupPairs=[{GroupId=$APP_SG}]" >/dev/null
```

Egress stays at the default (all outbound): the task must reach GitHub and the AWS APIs.

**Check:** `aws ec2 describe-security-groups --group-ids $DB_SG --query 'SecurityGroups[0].IpPermissions'` shows one rule whose source is `$APP_SG`.

## 5. Certificate (ACM) and DNS validation

**Why:** the ALB terminates TLS, and GitHub refuses to deliver webhooks to an untrusted certificate. ACM issues a public certificate for free and renews it as long as its validation record stays in DNS.

```bash
ZONE_ID=$(aws route53 list-hosted-zones-by-name --dns-name $ZONE_NAME \
  --query 'HostedZones[0].Id' --output text | sed 's|/hostedzone/||')
CERT_ARN=$(aws acm request-certificate --domain-name $DOMAIN --validation-method DNS \
  --query CertificateArn --output text)

# The validation record takes a few seconds to appear; retry this line if it prints "None".
sleep 10
read -r VAL_NAME VAL_VALUE < <(aws acm describe-certificate --certificate-arn $CERT_ARN \
  --query 'Certificate.DomainValidationOptions[0].ResourceRecord.[Name,Value]' --output text)

aws route53 change-resource-record-sets --hosted-zone-id $ZONE_ID --change-batch "{
  \"Changes\": [{\"Action\": \"UPSERT\", \"ResourceRecordSet\": {
    \"Name\": \"$VAL_NAME\", \"Type\": \"CNAME\", \"TTL\": 300,
    \"ResourceRecords\": [{\"Value\": \"$VAL_VALUE\"}]}}]}" >/dev/null

aws acm wait certificate-validated --certificate-arn $CERT_ARN && echo "certificate issued"
```

With DNS outside Route 53, create that CNAME at your provider instead of running `change-resource-record-sets`.

**Check:** `certificate issued`. Keep the validation CNAME forever: deleting it stops renewals.

## 6. The GitHub App

Follow [`production-setup.md`](production-setup.md) **§4** (create the app) and **§5** (collect its credentials) with `app.example.com` as `$DOMAIN`. Nothing there depends on AWS. Keep the **App ID**, **Client ID**, **client secret**, **slug** and the downloaded **`.pem`** at hand for §8.

The webhook delivers to the domain before the ALB exists, so the `ping` GitHub sends when the app is created fails. That is expected; you redeliver it in §14.

## 7. Database (RDS for PostgreSQL 17)

**Why single-AZ at first:** Multi-AZ doubles the instance cost to protect against a zone failure. Automated backups with point-in-time recovery already cover data loss; turn Multi-AZ on later with one command (§15.4).

**Why we set the master password ourselves:** RDS can generate and rotate the master password in a secret (`--manage-master-user-password`), but that secret is a JSON `{username, password}`, while the API reads one `DATABASE_URL`. A password we generate with no URL-special characters ([`production-setup.md`](production-setup.md) §2) lets us store the whole URL as one secret.

```bash
# Subnet group over the private subnets
aws rds create-db-subnet-group --db-subnet-group-name $APP \
  --db-subnet-group-description "PR Follower" --subnet-ids $(echo "$PRIVATE_SUBNETS") >/dev/null

# Pick the newest PostgreSQL 17 minor offered in your region (18 also works: use '18.' below)
PG_VERSION=$(aws rds describe-db-engine-versions --engine postgres \
  --query "DBEngineVersions[?starts_with(EngineVersion, '17.')].EngineVersion" --output text \
  | tr '\t' '\n' | sort -V | tail -1)
echo "PostgreSQL $PG_VERSION"

printf 'Database password (from production-setup §2): '; read -rs DB_PASS; echo
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

aws rds wait db-instance-available --db-instance-identifier $APP    # about 10 minutes
DB_HOST=$(aws rds describe-db-instances --db-instance-identifier $APP \
  --query 'DBInstances[0].Endpoint.Address' --output text)
echo "$DB_HOST"
```

What the flags buy: `--max-allocated-storage` lets storage grow by itself up to 100 GB; `--storage-encrypted` encrypts disk, snapshots and backups; `--no-publicly-accessible` plus the private subnets and `DB_SG` keep it unreachable from the internet; `--backup-retention-period 7` turns on automated backups and point-in-time recovery; `--deletion-protection` makes a mistaken delete fail. `t4g.micro` is enough for a handful of users; resize later with `modify-db-instance` (a short restart).

The master user owns `pr_follower`, which is what the API needs: it creates its tables at first start ([`production-setup.md`](production-setup.md) §3). TLS is required by the instance (`rds.force_ssl` defaults to 1 for PostgreSQL 15 and later). This guide's URL uses `sslmode=require`, which encrypts but accepts any certificate; `verify-full` below also checks that the server is your RDS instance.

**Stronger TLS: `sslmode=verify-full` (recommended).** The API must find RDS's CA bundle as a file: [`global-bundle.pem`](https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem) covers every commercial region; the per-region bundle (`https://truststore.pki.rds.amazonaws.com/$AWS_REGION/$AWS_REGION-bundle.pem`) trusts less. Then the URL in §8 becomes `…/pr_follower?sslmode=verify-full&sslrootcert=/rds-ca/rds-ca.pem`, with `$DB_HOST` the instance endpoint exactly as printed (the certificate names it; a CNAME of your own fails the check).

- **On EKS:** put the bundle in a ConfigMap and mount it into the api container (it is public, not a secret), readable by UID 10001.
- **On ECS (this guide):** Fargate has no ConfigMaps. Add a short-lived container to the task that downloads the bundle into a shared task volume before the API starts: in §12's task definition, `"volumes": [{"name": "rds-ca"}]`; a container `{"name": "rds-ca", "image": "public.ecr.aws/docker/library/alpine:3.22", "essential": false, "command": ["sh", "-c", "wget -qO /rds-ca/rds-ca.pem https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem && chmod 0444 /rds-ca/rds-ca.pem"], "mountPoints": [{"sourceVolume": "rds-ca", "containerPath": "/rds-ca"}]}`; and in `api`, `"mountPoints": [{"sourceVolume": "rds-ca", "containerPath": "/rds-ca", "readOnly": true}]` plus `{"containerName": "rds-ca", "condition": "SUCCESS"}` in its `dependsOn`. This recipe was not rehearsed on Floci; the first start of the API is its test (an `x509` error in the log means the file or the host name is wrong).

**Check:** `echo "$DB_HOST"` prints an endpoint like `pr-follower.xxxxxxxx.us-east-1.rds.amazonaws.com`. The database is private, so you cannot `psql` to it from your laptop; the first start of the API in §11 is the real test, and §15.5 shows how to open a `psql` session from inside the VPC.

## 8. Secrets (Secrets Manager)

**Why:** ECS reads each secret at task start through the **task execution role** and puts it into the container's environment. Secrets never appear in the task definition, in the image or in your shell history.

Store the sensitive values; the non-secret ones (`GITHUB_CLIENT_ID`, `GITHUB_APP_ID`, `GITHUB_APP_SLUG`) go straight into the task definition in §11.

```bash
put_secret() {   # put_secret NAME  (reads the value without echoing it)
  local v
  printf '%s: ' "$1"; read -rs v; echo
  [ -n "$v" ] || { echo "empty value, nothing stored" >&2; return 1; }
  # Through stdin, so the value never appears in the process list
  printf '%s' "$v" | aws secretsmanager create-secret --name "$APP/prod/$1" \
    --secret-string file:///dev/stdin --query Name --output text
}
put_secret github-client-secret      # from the GitHub App page (§6)
put_secret github-webhook-secret     # the webhook secret from production-setup §2, also set on the app
put_secret token-encryption-key      # openssl rand -base64 32, from production-setup §2

# DATABASE_URL: the password cannot contain URL-special characters (production-setup §2)
aws secretsmanager create-secret --name "$APP/prod/database-url" --query Name --output text \
  --secret-string "postgres://prfollower:$DB_PASS@$DB_HOST:5432/pr_follower?sslmode=require"
unset DB_PASS

# The GitHub App private key as ONE line with \n for newlines (production-setup §6.2)
printf 'Path to the downloaded .pem (full path, no ~): '; read -r PEM
[ -f "$PEM" ] || echo "not found: $PEM" >&2
aws secretsmanager create-secret --name "$APP/prod/github-app-private-key" --query Name --output text \
  --secret-string "$(awk 'NF {sub(/\r/, ""); printf "%s\\n", $0}' "$PEM")"
```

Store the `.pem` in your password manager, then delete the downloaded copy.

Collect the ARNs the task definition needs:

```bash
sm_arn() { aws secretsmanager describe-secret --secret-id "$APP/prod/$1" --query ARN --output text; }
export SEC_CLIENT_SECRET=$(sm_arn github-client-secret)
export SEC_WEBHOOK=$(sm_arn github-webhook-secret)
export SEC_TOKEN_KEY=$(sm_arn token-encryption-key)
export SEC_DATABASE_URL=$(sm_arn database-url)
export SEC_APP_KEY=$(sm_arn github-app-private-key)
```

**Check:** `aws secretsmanager list-secrets --query "SecretList[?starts_with(Name, '$APP/prod/')].Name"` lists five names.

## 9. Images (ECR)

**Why immutable tags:** a tag that can be overwritten makes "what is running?" unanswerable. With `IMMUTABLE`, pushing the same tag twice fails, which matches the rule to deploy by commit-SHA tag.

```bash
for repo in api panel; do
  aws ecr create-repository --repository-name $APP-$repo \
    --image-tag-mutability IMMUTABLE --image-scanning-configuration scanOnPush=true >/dev/null
done

# Keep the last 20 images of each repository
aws ecr put-lifecycle-policy --repository-name $APP-api --lifecycle-policy-text '{"rules":[{"rulePriority":1,"description":"keep 20","selection":{"tagStatus":"any","countType":"imageCountMoreThan","countNumber":20},"action":{"type":"expire"}}]}' >/dev/null
aws ecr put-lifecycle-policy --repository-name $APP-panel --lifecycle-policy-text '{"rules":[{"rulePriority":1,"description":"keep 20","selection":{"tagStatus":"any","countType":"imageCountMoreThan","countNumber":20},"action":{"type":"expire"}}]}' >/dev/null
```

To run a published release, copy its images into ECR instead of building. Take the digests from the release notes, verify them first ([`production-setup.md`](production-setup.md) §7.1), and copy **by digest**, so ECR receives exactly what you verified: `docker buildx imagetools create -t $REGISTRY/$APP-api:$TAG ghcr.io/muriio/pr-follower-api@$API_DIGEST`, and the same for the panel. `$TAG` is then the release name without the `v` (release `v0.1.0` → `0.1.0`); the copy keeps the digest, so the task definition can also name `$REGISTRY/$APP-api@$API_DIGEST`. To build your own, do it exactly as in §7.2, with `$REGISTRY` and `$TAG` from §2 (clean tree, `make check` first):

```bash
aws ecr get-login-password | docker login --username AWS --password-stdin $REGISTRY
docker build --platform linux/amd64 -t $REGISTRY/$APP-api:$TAG   backend
docker build --platform linux/amd64 -t $REGISTRY/$APP-panel:$TAG frontend
docker push $REGISTRY/$APP-api:$TAG
docker push $REGISTRY/$APP-panel:$TAG
```

`linux/amd64` matches the Fargate default (X86_64). On an Apple Silicon laptop this cross-builds under emulation; slow but correct. To run on Graviton instead (about 20% cheaper), build `linux/arm64` and set `"runtimePlatform": {"cpuArchitecture": "ARM64", "operatingSystemFamily": "LINUX"}` in the task definition.

**Check:** `aws ecr describe-images --repository-name $APP-api --query 'imageDetails[].imageTags'` shows `$TAG`.

## 10. IAM and logging

**Why two roles exist, and why we create only one.** A Fargate task has an **execution role** (used by ECS itself to pull images, write logs and fetch secrets) and an optional **task role** (used by *your code* to call AWS). PR Follower never calls AWS, so it gets no task role: a compromised container has no AWS permissions at all.

```bash
cat > trust.json <<'EOF'
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ecs-tasks.amazonaws.com"},"Action":"sts:AssumeRole"}]}
EOF
aws iam create-role --role-name $APP-execution --assume-role-policy-document file://trust.json >/dev/null
aws iam attach-role-policy --role-name $APP-execution \
  --policy-arn arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy

# Least privilege: read only this app's secrets
cat > read-secrets.json <<EOF
{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"secretsmanager:GetSecretValue",
 "Resource":"arn:aws:secretsmanager:${AWS_REGION}:${ACCOUNT_ID}:secret:${APP}/prod/*"}]}
EOF
aws iam put-role-policy --role-name $APP-execution --policy-name read-secrets \
  --policy-document file://read-secrets.json
export EXEC_ROLE_ARN=$(aws iam get-role --role-name $APP-execution --query Role.Arn --output text)

aws logs create-log-group --log-group-name /ecs/$APP
aws logs put-retention-policy --log-group-name /ecs/$APP --retention-in-days 30
rm trust.json read-secrets.json
```

`AmazonECSTaskExecutionRolePolicy` covers ECR pulls and CloudWatch writes; the inline policy adds the secrets. The log group's retention keeps logs from growing without bound.

## 11. Load balancer

**Why these settings:**

- **Idle timeout 300 s.** The ALB closes a connection after no data moves for the idle timeout (default 60 s, valid range 1–4000 s). Approve and merge wait on GitHub for up to 300 s, so the load balancer must outwait the panel's own 300 s limit. The SSE stream sends a heartbeat every 25 s, so it never idles. The timeout is an attribute of the **whole ALB**: on an ALB shared with other apps (an EKS IngressGroup, for instance), raising it raises it for all of them, so agree it with the ALB's owner.
- **Health check on `/healthz`** of the panel: it answers `ok` without touching the API, so a slow GitHub or database cannot take the task out of rotation.
- **Deregistration delay 30 s** instead of the default 300 s: during a deploy the old task would otherwise linger for five minutes, mostly serving long-lived SSE connections that the browser reopens anyway.
- **Target type `ip`**: required for Fargate's `awsvpc` networking.

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

aws elbv2 create-listener --load-balancer-arn $LB_ARN --protocol HTTPS --port 443 \
  --certificates CertificateArn=$CERT_ARN --ssl-policy ELBSecurityPolicy-TLS13-1-2-2021-06 \
  --default-actions Type=forward,TargetGroupArn=$TG_ARN >/dev/null
aws elbv2 create-listener --load-balancer-arn $LB_ARN --protocol HTTP --port 80 \
  --default-actions 'Type=redirect,RedirectConfig={Protocol=HTTPS,Port=443,StatusCode=HTTP_301}' >/dev/null
```

The TLS policy accepts TLS 1.2 and 1.3. HTTP on port 80 only redirects.

**Check:** `aws elbv2 describe-load-balancers --names $APP --query 'LoadBalancers[0].State.Code'` prints `active`. The target group has no targets yet; that is the next step.

## 12. ECS: cluster, task definition, service

**The task definition** is the heart of it. Two containers share the task's network namespace:

- `api` listens on **8081** (`PORT=8081`), because the panel image's nginx owns 8080 in the same namespace. Its container health check calls its own `/healthz`, which says the process is up without touching Postgres: on ECS an unhealthy essential container replaces the whole task, and a database outage must not do that. Its `startPeriod` of 300 s covers a start that waits for another task's migration lock (§12 bullets below). For monitoring the database path, use `https://$DOMAIN/api/v1/healthz` (§14).
- `panel` proxies `/api/*` to `http://127.0.0.1:8081` (`API_UPSTREAM`) and **waits for `api` to be healthy** before starting. It is the only container that receives traffic from the ALB (port 8080).
- Secrets come from Secrets Manager (`secrets`), the rest from plain `environment`.
- **1 vCPU / 2 GB** is a deliberate margin: the Dependabot analysis reads repository tarballs in memory (ADR-0005) with up to two analyses at once. If the task is OOM-killed (§16), raise the memory; if usage stays low you can drop to 0.5 vCPU / 1 GB.

First export the three non-secret values from the GitHub App's page (§6):

```bash
export GH_CLIENT_ID=Iv23xxxxxxxxxxxx   # Client ID
export GH_APP_ID=123456                # App ID
export GH_APP_SLUG=pr-follower         # the <slug> in https://github.com/apps/<slug>
```

Then write the definition (the shell fills in every `$VARIABLE`):

```bash
cat > taskdef.json <<EOF
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
      "image": "$REGISTRY/$APP-api:$TAG",
      "essential": true,
      "portMappings": [{"containerPort": 8081, "protocol": "tcp"}],
      "environment": [
        {"name": "PORT", "value": "8081"},
        {"name": "PUBLIC_URL", "value": "https://$DOMAIN"},
        {"name": "ALLOWED_ORIGIN", "value": "https://$DOMAIN"},
        {"name": "GITHUB_CLIENT_ID", "value": "$GH_CLIENT_ID"},
        {"name": "GITHUB_APP_ID", "value": "$GH_APP_ID"},
        {"name": "GITHUB_APP_SLUG", "value": "$GH_APP_SLUG"}
      ],
      "secrets": [
        {"name": "DATABASE_URL", "valueFrom": "$SEC_DATABASE_URL"},
        {"name": "GITHUB_CLIENT_SECRET", "valueFrom": "$SEC_CLIENT_SECRET"},
        {"name": "GITHUB_WEBHOOK_SECRET", "valueFrom": "$SEC_WEBHOOK"},
        {"name": "TOKEN_ENCRYPTION_KEY", "valueFrom": "$SEC_TOKEN_KEY"},
        {"name": "GITHUB_APP_PRIVATE_KEY", "valueFrom": "$SEC_APP_KEY"}
      ],
      "healthCheck": {
        "command": ["CMD-SHELL", "wget -qO- http://127.0.0.1:8081/healthz >/dev/null || exit 1"],
        "interval": 10, "timeout": 3, "retries": 3, "startPeriod": 300
      },
      "stopTimeout": 20,
      "logConfiguration": {"logDriver": "awslogs", "options": {
        "awslogs-group": "/ecs/$APP", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "api"}}
    },
    {
      "name": "panel",
      "image": "$REGISTRY/$APP-panel:$TAG",
      "essential": true,
      "portMappings": [{"containerPort": 8080, "protocol": "tcp"}],
      "environment": [{"name": "API_UPSTREAM", "value": "http://127.0.0.1:8081"}],
      "dependsOn": [{"containerName": "api", "condition": "HEALTHY"}],
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
python3 -m json.tool taskdef.json >/dev/null && echo "taskdef.json is valid JSON"
```

Optional: to let only members of your GitHub organization sign in, add `{"name": "ALLOWED_GITHUB_ORGS", "value": "acme"}` to the api's `environment` (comma-separated for several). The app must be installed on that organization with its *Members: read* permission accepted; leavers lose access within 15 minutes. See [`production-setup.md`](production-setup.md) §13.11, including what an existing deployment must do to adopt it.

Create the cluster, register the definition and start the service:

```bash
aws ecs create-cluster --cluster-name $APP >/dev/null
aws ecs register-task-definition --cli-input-json file://taskdef.json \
  --query 'taskDefinition.taskDefinitionArn' --output text

aws ecs create-service --cluster $APP --service-name $APP \
  --task-definition $APP --desired-count 1 --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={subnets=[$PRIVATE_CSV],securityGroups=[$APP_SG],assignPublicIp=DISABLED}" \
  --load-balancers "targetGroupArn=$TG_ARN,containerName=panel,containerPort=8080" \
  --health-check-grace-period-seconds 360 \
  --deployment-configuration "maximumPercent=200,minimumHealthyPercent=100,deploymentCircuitBreaker={enable=true,rollback=true}" \
  --query 'service.serviceName' --output text
```

How the service is set:

- `desired-count 1`: start with one task; raise it to scale out (production-setup §13.10).
- `maximumPercent=200, minimumHealthyPercent=100`: a deploy starts the **new** task, waits for it to be healthy, then stops the old one: no downtime. For a few seconds two API processes exist; that is fine: the new one migrates under a Postgres advisory lock (ADR-0016) while the old one keeps serving, activity is shared through Postgres, and a sync running on the old one is reported as interrupted and can be run again. Several tasks starting together also wait for each other's migration instead of racing it. If you would rather never overlap, use `maximumPercent=100, minimumHealthyPercent=0` and accept a brief outage per deploy.
- The **deployment circuit breaker with rollback** stops a deployment whose new tasks keep failing and returns to the last working task definition, which is exactly what you want after a bad migration or a mistyped secret.
- `health-check-grace-period-seconds 360`: the panel starts only once the API is healthy, and the API listens only after migrating, possibly after waiting up to 5 minutes for another task's migration lock. The grace period keeps the ALB from judging the target before that.
- **Read-only root filesystem** is not set in this task definition. To add it (`"readonlyRootFilesystem": true`), the containers need the writable paths listed in [`production-setup.md`](production-setup.md) §6.4 (`/tmp` on both; `/etc/nginx/conf.d`, writable by UID 101, on the panel); this guide has not rehearsed that on Fargate.

Watch it start:

```bash
aws ecs wait services-stable --cluster $APP --services $APP && echo "service stable"
aws logs tail /ecs/$APP --since 10m
```

A good start prints `INFO PR Follower Go v1.27 server listening port=8081` and **no** `WARN`/`ERROR` ([`production-setup.md`](production-setup.md) §9 and its Troubleshooting).

**Check:** `aws elbv2 describe-target-health --target-group-arn $TG_ARN --query 'TargetHealthDescriptions[].TargetHealth.State'` prints `["healthy"]`.

## 13. DNS record

```bash
read -r ALB_DNS ALB_ZONE < <(aws elbv2 describe-load-balancers --load-balancer-arns $LB_ARN \
  --query 'LoadBalancers[0].[DNSName,CanonicalHostedZoneId]' --output text)
aws route53 change-resource-record-sets --hosted-zone-id $ZONE_ID --change-batch "{
  \"Changes\": [{\"Action\": \"UPSERT\", \"ResourceRecordSet\": {
    \"Name\": \"$DOMAIN\", \"Type\": \"A\",
    \"AliasTarget\": {\"HostedZoneId\": \"$ALB_ZONE\", \"DNSName\": \"$ALB_DNS\", \"EvaluateTargetHealth\": true}}}]}" >/dev/null
```

The alias record has no TTL and no per-query charge. With another DNS provider, create a CNAME from `$DOMAIN` to `$ALB_DNS`.

**Check:** `curl -s https://$DOMAIN/healthz` prints `ok` once DNS propagates (the panel), and `curl -s https://$DOMAIN/api/v1/healthz` prints `{"status":"ok"}` (the API and RDS).

## 14. Verify

Run, unchanged, [`production-setup.md`](production-setup.md) **§10** (command-line smoke tests, including the signed `ping` that checks the webhook secret) and **§11** (end to end with GitHub: redeliver the `ping`, sign in, install, import, live timeline, access removal, restart, sign out). Two AWS-specific points while you do:

- For §11 step 7 ("restart"), use `aws ecs update-service --cluster $APP --service $APP --force-new-deployment`, then `aws ecs wait services-stable ...`.
- The live-timeline test in §11 step 5 is also the proof that the **ALB does not buffer the event stream**: if the timeline only updates on reload, see §16.

Then the **go-live checklist** in `production-setup.md` §12.

## 15. Operate on AWS

### 15.1 Deploy a new version

The task definition is the unit of deploy: a new tag means a new revision.

```bash
export TAG=$(git rev-parse --short=12 HEAD)          # clean tree, make check green
# 1. Migration in this release? Take a snapshot first.
git diff --stat DEPLOYED_SHA HEAD -- backend/internal/infrastructure/database/migrations/
aws rds create-db-snapshot --db-instance-identifier $APP --db-snapshot-identifier $APP-pre-$TAG

# 2. Build, push (§9), then register a revision with the new images and roll the service
aws ecs describe-task-definition --task-definition $APP --query taskDefinition \
 | jq --arg api "$REGISTRY/$APP-api:$TAG" --arg panel "$REGISTRY/$APP-panel:$TAG" '
     del(.taskDefinitionArn,.revision,.status,.requiresAttributes,.compatibilities,.registeredAt,.registeredBy)
     | (.containerDefinitions[] | select(.name=="api").image)   = $api
     | (.containerDefinitions[] | select(.name=="panel").image) = $panel' > taskdef-new.json
aws ecs register-task-definition --cli-input-json file://taskdef-new.json >/dev/null
aws ecs update-service --cluster $APP --service $APP --task-definition $APP >/dev/null
aws ecs wait services-stable --cluster $APP --services $APP
```

`--task-definition $APP` (no revision) means the latest active one. Then rerun `production-setup.md` §10 and **redeliver failed webhook deliveries** from the app's Recent Deliveries (§13.1 there): GitHub does not retry, and deliveries that arrive during the swap can fail.

### 15.2 Roll back

```bash
aws ecs list-task-definitions --family-prefix $APP --sort DESC --max-items 5
aws ecs update-service --cluster $APP --service $APP --task-definition "${APP}:PREVIOUS_REVISION"
```

The circuit breaker already does this automatically when a deployment cannot become healthy. As in [`production-setup.md`](production-setup.md) §13.3, roll back by task definition only if no migration shipped since; otherwise fix forward, or restore the pre-deploy snapshot as a last resort (§15.4).

### 15.3 Logs and alarms

```bash
aws logs tail /ecs/$APP --follow --since 10m                      # both containers
aws logs tail /ecs/$APP --follow --filter-pattern ERROR           # errors only
aws ecs describe-services --cluster $APP --services $APP --query 'services[0].events[:5].message'
```

Alert on the two things that matter first: no healthy target, and 5xx from the task.

```bash
SNS_ARN=$(aws sns create-topic --name $APP-alerts --query TopicArn --output text)
aws sns subscribe --topic-arn $SNS_ARN --protocol email --notification-endpoint you@example.com
# confirm the subscription from the email, then:

TG_DIM=${TG_ARN##*:}                       # targetgroup/pr-follower/xxxx
LB_DIM=${LB_ARN##*:loadbalancer/}          # app/pr-follower/xxxx
aws cloudwatch put-metric-alarm --alarm-name $APP-unhealthy --namespace AWS/ApplicationELB \
  --metric-name UnHealthyHostCount --dimensions Name=TargetGroup,Value=$TG_DIM Name=LoadBalancer,Value=$LB_DIM \
  --statistic Maximum --period 60 --evaluation-periods 3 --threshold 1 \
  --comparison-operator GreaterThanOrEqualToThreshold --treat-missing-data notBreaching \
  --alarm-actions $SNS_ARN
aws cloudwatch put-metric-alarm --alarm-name $APP-5xx --namespace AWS/ApplicationELB \
  --metric-name HTTPCode_Target_5XX_Count --dimensions Name=LoadBalancer,Value=$LB_DIM \
  --statistic Sum --period 300 --evaluation-periods 1 --threshold 5 \
  --comparison-operator GreaterThanOrEqualToThreshold --treat-missing-data notBreaching \
  --alarm-actions $SNS_ARN
```

These do not replace watching **Recent Deliveries** on GitHub ([`production-setup.md`](production-setup.md) §13.1): a webhook GitHub failed to deliver never reaches your metrics.

### 15.4 Backups, restore and resilience

- Automated backups with point-in-time recovery are on (7 days, §7). Manual snapshot: `aws rds create-db-snapshot ...` (they stay until deleted).
- **Restore** always creates a *new* instance: `aws rds restore-db-instance-to-point-in-time --source-db-instance-identifier $APP --target-db-instance-identifier $APP-restored --restore-time 2026-11-12T10:00:00Z --db-subnet-group-name $APP --vpc-security-group-ids $DB_SG` (or `restore-db-instance-from-db-snapshot`). Then update the `database-url` secret to the new host, `put-secret-value`, and `force-new-deployment`. The restore needs the `TOKEN_ENCRYPTION_KEY` that was in use, as in [`production-setup.md`](production-setup.md) §13.7.
- **Multi-AZ** when downtime from a zone failure is no longer acceptable: `aws rds modify-db-instance --db-instance-identifier $APP --multi-az --apply-immediately`. The endpoint does not change.
- **Second NAT gateway** (one per AZ) when losing the NAT's zone must not cut GitHub access; the wizard's "1 per AZ" option, or add one by hand.

### 15.5 `psql` against the private database (sessions, housekeeping)

The SQL in [`production-setup.md`](production-setup.md) §13.6 (sign everyone out, purge expired sessions) needs a client inside the VPC. A one-off Fargate task does it, with the same secret and no inbound access:

```bash
cat > psql-taskdef.json <<EOF
{ "family": "$APP-psql", "networkMode": "awsvpc", "requiresCompatibilities": ["FARGATE"],
  "cpu": "256", "memory": "512", "executionRoleArn": "$EXEC_ROLE_ARN",
  "containerDefinitions": [{ "name": "psql", "image": "public.ecr.aws/docker/library/postgres:17-alpine",
    "essential": true, "entryPoint": ["sh", "-c"], "command": ["sleep 5"],
    "secrets": [{"name": "DATABASE_URL", "valueFrom": "$SEC_DATABASE_URL"}],
    "logConfiguration": {"logDriver": "awslogs", "options": {
      "awslogs-group": "/ecs/$APP", "awslogs-region": "$AWS_REGION", "awslogs-stream-prefix": "psql"}} }]
}
EOF
aws ecs register-task-definition --cli-input-json file://psql-taskdef.json >/dev/null

run_sql() {   # run_sql "DELETE FROM sessions WHERE expires_at < extract(epoch FROM now());"
  aws ecs run-task --cluster $APP --launch-type FARGATE --task-definition $APP-psql \
    --network-configuration "awsvpcConfiguration={subnets=[$PRIVATE_CSV],securityGroups=[$APP_SG],assignPublicIp=DISABLED}" \
    --overrides "{\"containerOverrides\":[{\"name\":\"psql\",\"command\":[\"psql \\\"\$DATABASE_URL\\\" -c \\\"$1\\\"\"]}]}" \
    --query 'tasks[0].taskArn' --output text
}
run_sql "select max(version_id) from goose_db_version;"
aws logs tail /ecs/$APP --since 5m --filter-log-stream-name-prefix psql
```

The task runs under `APP_SG`, the only group the database admits, and ends when the command finishes. Use it sparingly and only with statements you have read.

### 15.6 Rotating secrets

The rotation procedures in [`production-setup.md`](production-setup.md) §13.4–13.5 apply unchanged; the AWS mechanics are: write the new value, then restart so the task re-reads it (ECS reads secrets only when a task **starts**).

```bash
aws secretsmanager put-secret-value --secret-id $APP/prod/github-client-secret --secret-string "NEW_VALUE"
aws ecs update-service --cluster $APP --service $APP --force-new-deployment
```

Do the same for `github-webhook-secret` (and set it on the GitHub App at the same moment), `database-url`, `github-app-private-key` and `token-encryption-key` (remember it signs everyone out).

### 15.7 Tear down

In this order (each step depends on the previous): disable `--deletion-protection` on the database and delete it with a final snapshot (`aws rds modify-db-instance --db-instance-identifier $APP --no-deletion-protection`, then `aws rds delete-db-instance --db-instance-identifier $APP --final-db-snapshot-identifier $APP-final`); scale and delete the ECS service and cluster; delete the listeners, load balancer and target group; delete the Route 53 records (alias and ACM validation) and the certificate; delete the ECR repositories, the log group, the secrets (`--force-delete-without-recovery` only if you mean it), the IAM role; delete the security groups; delete the VPC from the console (it removes the NAT gateway and subnets). The NAT gateway and ALB are billed hourly, so do not leave them behind.

## 16. Troubleshooting on AWS

| Symptom | Likely cause | What to do |
|---|---|---|
| Task stops at once with `ResourceInitializationError: unable to pull secrets or registry auth` | The execution role cannot read a secret (wrong ARN or policy), or the task has no route to Secrets Manager/ECR (NAT missing or route table wrong). | `aws ecs describe-tasks ... --query 'tasks[0].stoppedReason'`; check the role's `read-secrets` policy and that private subnets route `0.0.0.0/0` to the NAT. |
| `CannotPullContainerError` | The tag does not exist in ECR, or no route to ECR. | Check `aws ecr describe-images`, and the NAT/S3 endpoint. |
| Task starts, API exits: `failed to connect to database` | Wrong `database-url`, or `DB_SG` does not admit `APP_SG` on 5432. | §4 check; read the logs (`production-setup.md` Troubleshooting, "The API does not start"). |
| Target `unhealthy`, service keeps replacing tasks | `APP_SG` does not admit `ALB_SG` on 8080, or the panel is waiting for an unhealthy API. | `describe-target-health` shows the reason; read the `api` log stream. |
| `502` from the ALB right after a deploy | The new task is not healthy yet; the grace period or health check is too tight. | Wait, or raise `--health-check-grace-period-seconds`. |
| Approve/merge fail with `504` after 60 s | The ALB idle timeout was left at 60 s. | `describe-load-balancer-attributes` must show `idle_timeout.timeout_seconds` = 300 (§11). |
| The timeline updates only on reload | The stream is buffered or cut: confirm with dev tools (the `events/stream` request must stay open). | Check the ALB idle timeout above and that nothing (CDN, WAF) was put in front. |
| Task killed, `OutOfMemoryError: Container killed due to memory usage` in `stoppedReason` | An analysis of a large repository exceeded the memory. | Raise `memory` in the task definition (§12) and redeploy. |
| Webhook deliveries `502`/`504` in GitHub during a deploy | The swap window. | Redeliver (`production-setup.md` §13.1). |
| Rollback by the circuit breaker, "deployment failed" | The new task never became healthy: bad image, secret or migration. | `describe-services` events and the `api` log stream of the failed task. |

## Sources

Looked up with Context7 on 2026-11-11:

- **Application Load Balancer**: connection idle timeout attribute `idle_timeout.timeout_seconds`, default 60 s, valid range 1–4000 s ([ELB User Guide, "Edit attributes for your Application Load Balancer"](https://docs.aws.amazon.com/elasticloadbalancing/latest/application/edit-load-balancer-attributes.html)).
- **Amazon RDS for PostgreSQL**: `rds.force_ssl` defaults to 1 from PostgreSQL 15; `sslmode=verify-full` with the RDS CA bundle for certificate verification ([RDS User Guide, "Using SSL with a PostgreSQL DB instance"](https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/PostgreSQL.Concepts.General.SSL.html)).
- **Amazon ECS on Fargate**: image pulls, log delivery and Secrets Manager retrieval use the task's network interface and the task **execution role** ([ECS Developer Guide, "Fargate task networking"](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/fargate-task-networking.html)); the execution role needs `secretsmanager:GetSecretValue` on the secret.

Everything else (ECS `valueFrom` injection, deployment circuit breaker, target group attributes, `awsvpc` port sharing between containers of a task, prices) comes from general knowledge of the services and should be checked against the current documentation and pricing pages before you rely on it.
