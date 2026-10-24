# Azure Container Apps deployment

Deploys `tools/httpserver` (the Data Manager UI + commercial Stripe billing
surface) to Azure Container Apps. Provisioned by `.github/workflows/deploy-azure.yml`
on push to `master`; this doc covers what it provisions and how to run it by hand.

## What this provisions

- **Resource Group** (created by the workflow before the Bicep deployment)
- **Azure Container Registry** (Basic SKU, admin user disabled - pulls only via managed identity)
- **User-assigned managed identity** with `AcrPull` on the registry and `Key Vault Secrets User` on the vault
- **Key Vault** (RBAC-authorized) holding the Stripe secret key, webhook secret, and publishable key
- **Log Analytics workspace** (30-day retention, caps ingestion cost)
- **Container Apps environment** wired to that workspace
- **Container App**, `minReplicas = 0`, `maxReplicas = 1` so it scales to zero when idle and costs close to nothing between requests (see the comment in `modules/container-app.bicep` for why it never scales past 1), 0.5 vCPU / 1Gi, liveness and startup probes on `/api/health`, readiness probe on `/api/ready`
- **Cost budget** with alerts at 50/75/90/100% of a monthly USD threshold
- **Azure Monitor metric alerts** (CPU%, memory%, restart-loop, no running replica, burst of 5xx responses) on the Container App, wired to an email action group. The app also serves Prometheus metrics at `/metrics`, including a request duration histogram

## Why pinned to 1 replica, and no Redis

There is no autoscaling. The app has an HTTP concurrency rule (50 concurrent requests), but the maximum replica count is 1, so that rule can never add a replica. It only sets how many requests the ingress queues against the single instance. Scaling out needs the billing store to be safe for several writers first.

joltrin's embedded B-Tree engine has no documented guarantee of safe
concurrent multi-process writes to the same data path. It does ship a
Redis-backed distributed-locking `L2Cache` (`adapters/redis`) that would make
horizontal scaling safe, but this deployment is optimized for lowest cost, so
it stays at most one replica instead of adding an Azure Cache for Redis instance.
Revisit `container-app.bicep`'s `minReplicas`/`maxReplicas` if that tradeoff
changes.

## Why Azure Monitor alerts, not Managed Prometheus/Grafana

Both are real, billed Azure resources (ingestion + workspace cost) that a
single-replica, personal-scale deployment doesn't need. Native platform
metric alerts on the Container App resource cost a few cents/month per rule
and cover the same guardrails (CPU, memory, restart loops).

## Operating the deployed service

This describes the code on `master`, so a reviewer can check each statement against it. A change reaches the running service only after the deploy workflow is approved, so the live service can be behind this text.

**What it is.** One Go binary, `tools/httpserver`, behind the Container Apps ingress (TLS only). It serves the Data Manager UI and the billing routes the site uses. The agent verification barrier is a different program and is not part of this deployment.

**Request path.** Ingress, then `net/http` with these layers in order: metrics, security headers, a 10 MiB body cap, then the handlers. Billing state (subscriptions, processed Stripe event ids, enterprise inquiries) is stored in Joltrin's own embedded store under `<database>/_billing`, on the Azure Files share mounted at the data path.

**Public and protected routes.**

- Public: the root page and `/login`, the sign-in routes under `/api/auth/` (login, refresh, provider list, OIDC), `/api/health`, `/api/ready`, `/metrics`, `/api/billing/public-checkout` (CORS limited to the marketing site, rate limited, 4 KiB body), `/api/billing/enterprise-contact` (CORS limited to the marketing site, rate limited, 16 KiB body, bounded fields, at most 10,000 stored), and `/api/billing/webhook` (Stripe signature checked, events deduplicated, 1 MiB body).
- `/api/billing/checkout/simulate` answers 404 unless Stripe is in simulation mode, so it cannot grant a paid tier with live keys.
- The other registered routes are wrapped in the sign-in check (`withAuth`).
- `/metrics` is open unless `JOLTRIN_METRICS_TOKEN` is set, in which case it needs `Authorization: Bearer <token>`.

**Configuration and secrets.** Stripe keys and the root password come from Key Vault through the app's managed identity. Other settings are flags and environment variables, listed in `modules/container-app.bicep`. No secret is in the repository.

**Health.** `/api/health` is liveness only and always answers 200 while the process runs. `/api/ready` answers 503 while the process is shutting down or when the data directory is missing, so a lost file share takes the instance out of rotation without a restart loop. Startup also uses `/api/health`.

**Shutdown.** On SIGTERM the server marks itself not ready, waits 5 seconds, then gives requests in flight up to 20 seconds to finish. Container Apps allows 30. A listen failure exits non-zero.

**Metrics.** `/metrics` has request totals by status class, in-flight requests, a latency histogram, goroutines and heap. Health, readiness and `/metrics` requests are counted separately as `sop_http_probe_requests_total` and are left out of the request totals and the histogram, so those numbers describe real requests.

**Alerts.** Azure Monitor alerts for CPU, memory, restart loops, no running replica and bursts of 5xx responses, sent to one email address. None of them has been seen to fire.

**Deploying.** A push to `master` that touches this folder, `tools/httpserver`, `governance` or the Dockerfile runs `deploy-azure.yml`: infrastructure, then build, scan and push, then a roll to the new image that waits for approval on the `production` environment. Between the infrastructure step and the approval, the previous revision keeps serving. That was seen once, when a run waited about 15 hours at the gate.

**Rolling back (not rehearsed).** The registry keeps one tag per build, in the form `<version>-<commit>`. Pointing the app at an earlier tag should roll it back:

```bash
az containerapp revision list -n joltrin-app -g joltrin-prod-rg -o table
az containerapp update -n joltrin-app -g joltrin-prod-rg --image <registry>.azurecr.io/joltrin:<earlier-tag>
```

**Troubleshooting.** Not ready: check that the file share is mounted (`/api/ready` says `data_unavailable`). 429 responses on billing routes: the per-client limit is working. Restart loop: `az containerapp logs show -n joltrin-app -g joltrin-prod-rg --tail 100`.

**Known limits.**

- One replica, so a restart or deploy can cause a short outage, and there is no high availability.
- The rate limit identifies a caller by the rightmost `X-Forwarded-For` entry, which is the one Container Apps supplies and documents as the only trusted one (IPv6 grouped by /64). That is only correct behind that ingress. If the server were exposed directly, a client could set that entry too.
- I found no throttling on `/api/auth/login`. The root account needs a password of at least 16 characters.
- Uptime history starts when `.github/workflows/uptime.yml` begins running, and there is no SLO. One alert path was exercised on purpose (see `docs/INCIDENTS.md`), and the five real alerts have not fired.
- Rollback to an earlier image was rehearsed once on 2026-10-12 (see `docs/INCIDENTS.md`). Restore of the data share has not been, and the file share's snapshot policy has not been checked.
- The billing store is single writer, which is why the app is pinned to one replica.

## Deploying by hand

```bash
az group create -n joltrin-prod-rg -l eastus

az deployment group create \
  -g joltrin-prod-rg \
  -f infra/azure/main.bicep \
  -p infra/azure/main.parameters.json \
  -p containerImage=<acr-login-server>/joltrin:<tag> \
  -p alertEmail=<your-email> \
  -p stripeSecretKey=<from a secret manager, never a shell history file> \
  -p stripeWebhookSecret=<same>
```

Stripe values, and which ones are secret:

| Value | Where it goes | Secret |
| :--- | :--- | :--- |
| `stripeSecretKey`, `stripeWebhookSecret` | Key Vault, injected as secret references | Yes |
| `stripePublishableKey` | Key Vault | No |
| `stripeProPriceId`, `stripeEnterprisePriceId` | Plain env vars (`STRIPE_PRO_PRICE_ID`, `STRIPE_ENTERPRISE_PRICE_ID`) | No |
| `publicBaseUrl` | Plain env var (`JOLTRIN_PUBLIC_URL`), builds the absolute success and cancel URLs | No |

Every Stripe value defaults to empty. An empty secret is stored in Key Vault as the
literal `unset` (Key Vault rejects empty values) and the server treats it as empty,
so an unconfigured deployment stays in simulation mode. Pro checkout turns on only
when the secret key, webhook secret, Pro price ID, and public URL are all present.
Enterprise stays contact-sales until `stripeEnterprisePriceId` is a real price.

`GET /api/billing/plan` returns a `checkout` block that names any missing variable
(names only, never values), so you can see why checkout is off without reading logs.

`deploy-azure.yml` passes these from repository secrets (`STRIPE_SECRET_KEY`,
`STRIPE_WEBHOOK_SECRET`) and repository variables (`STRIPE_PUBLISHABLE_KEY`,
`STRIPE_PRO_PRICE_ID`, `STRIPE_ENTERPRISE_PRICE_ID`, `JOLTRIN_PUBLIC_URL`). Set the
secrets in GitHub rather than writing them to Key Vault by hand: every deploy
re-applies the Key Vault secrets from the workflow inputs and would overwrite a
value set manually.

The data directory `/var/lib/sop` (config.json, stores, billing state) is an Azure
Files share mounted into the container, so it survives restarts and new revisions.
The app stays at one replica because there must only ever be one writer. The mount
uses `nobrl` because Azure Files does not honor SMB byte-range locks for this
access pattern. Standard LRS files bill on used capacity, so an empty share is
close to free.

The first deploy provisions the ACR before an image exists in it; build and
push the image, then re-run `az deployment group create` with the resulting
`containerImage` value (this is exactly what `deploy-azure.yml` automates).

## Before the first real deploy

Verify the Azure Monitor metric names used in `modules/monitor-alerts.bicep`
against the live resource - Container Apps metric names have changed across
API versions before:

```bash
az monitor metrics list-definitions --resource <container-app-resource-id>
```

If they've drifted, update the `metricName` values in that file.
