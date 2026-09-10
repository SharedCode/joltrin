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
- **Container App**, pinned to `minReplicas = maxReplicas = 1` (see the comment in `modules/container-app.bicep` for why), 0.5 vCPU / 1Gi, liveness/readiness/startup probes on `/api/health`
- **Cost budget** with alerts at 50/75/90/100% of a monthly USD threshold
- **Azure Monitor metric alerts** (CPU%, memory%, restart-loop) on the Container App, wired to an email action group

## Why pinned to 1 replica, and no Redis

joltrin's embedded B-Tree engine has no documented guarantee of safe
concurrent multi-process writes to the same data path. It does ship a
Redis-backed distributed-locking `L2Cache` (`adapters/redis`) that would make
horizontal scaling safe, but this deployment is optimized for lowest cost, so
it stays single-replica instead of adding an Azure Cache for Redis instance.
Revisit `container-app.bicep`'s `minReplicas`/`maxReplicas` if that tradeoff
changes.

## Why Azure Monitor alerts, not Managed Prometheus/Grafana

Both are real, billed Azure resources (ingestion + workspace cost) that a
single-replica, personal-scale deployment doesn't need. Native platform
metric alerts on the Container App resource cost a few cents/month per rule
and cover the same guardrails (CPU, memory, restart loops).

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
