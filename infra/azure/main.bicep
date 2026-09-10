// Azure Container Apps deployment for tools/httpserver (the joltrin commercial
// billing/Data Manager surface). Deploy at resource-group scope:
//
//   az group create -n <rg> -l <location>
//   az deployment group create -g <rg> -f infra/azure/main.bicep \
//     -p infra/azure/main.parameters.json
//
// Design intent (see PR description for the full tradeoff writeup):
//   - Single Container App revision, pinned to minReplicas=maxReplicas=1.
//     joltrin's embedded B-Tree engine has no documented multi-process
//     write-safety guarantee, and the deployment target is least-cost, so
//     this deploy does not add Azure Cache for Redis to unlock horizontal
//     scaling. CPU/memory/concurrency limits are enforced as guardrails
//     against runaway cost and restart loops, not as autoscaling fan-out.
//   - Azure Monitor native metric alerts (not Managed Prometheus/Grafana)
//     watch CPU%, memory%, and replica restarts, because a managed
//     Prometheus/Grafana stack adds meaningful monthly cost that a
//     single-replica personal-scale deployment doesn't need.
//   - Billing secrets (Stripe keys) live in Key Vault and are injected via
//     secret references, never as plaintext env vars or in source control.

@description('Azure region for all resources.')
param location string = resourceGroup().location

@description('Short environment/app name used as a resource-name prefix.')
param appName string = 'joltrin'

@description('Container image reference, e.g. <acr-login-server>/joltrin:<tag>. Defaults to a public placeholder so the infra can be deployed before the ACR has a real image in it (first-run bootstrap); CI updates the running revision separately via `az containerapp update` once the real image is pushed.')
param containerImage string = 'mcr.microsoft.com/k8se/quickstart:latest'

@description('Email address that receives budget and health alerts.')
param alertEmail string

@description('Monthly cost budget in USD before the 100% alert fires.')
param monthlyBudgetUsd int = 25

@secure()
@description('Stripe secret key. Stored in Key Vault, never in source control or plain env vars.')
param stripeSecretKey string

@secure()
@description('Stripe webhook signing secret.')
param stripeWebhookSecret string

@description('Stripe publishable key (not secret, but kept alongside the others for consistency).')
param stripePublishableKey string = ''

var resourceToken = uniqueString(resourceGroup().id, appName)
var logAnalyticsName = '${appName}-logs-${resourceToken}'
var acrName = replace('${appName}acr${resourceToken}', '-', '')
var kvName = take('${appName}-kv-${resourceToken}', 24)
var envName = '${appName}-env-${resourceToken}'
var containerAppName = '${appName}-app'
var uamiName = '${appName}-acr-pull-identity'
var actionGroupName = '${appName}-alerts'

module logAnalytics 'modules/log-analytics.bicep' = {
  name: 'logAnalytics'
  params: {
    name: logAnalyticsName
    location: location
    retentionInDays: 30 // caps ingestion cost; do not raise without reviewing Log Analytics billing
  }
}

module acr 'modules/container-registry.bicep' = {
  name: 'containerRegistry'
  params: {
    name: acrName
    location: location
  }
}

module identity 'modules/identity.bicep' = {
  name: 'acrPullIdentity'
  params: {
    name: uamiName
    location: location
    acrName: acr.outputs.name
  }
}

module keyVault 'modules/key-vault.bicep' = {
  name: 'keyVault'
  params: {
    name: kvName
    location: location
    principalIdForAccess: identity.outputs.principalId
    stripeSecretKey: stripeSecretKey
    stripeWebhookSecret: stripeWebhookSecret
    stripePublishableKey: stripePublishableKey
  }
}

module containerAppsEnv 'modules/container-apps-environment.bicep' = {
  name: 'containerAppsEnvironment'
  params: {
    name: envName
    location: location
    logAnalyticsCustomerId: logAnalytics.outputs.customerId
    logAnalyticsSharedKey: logAnalytics.outputs.primarySharedKey
  }
}

module containerApp 'modules/container-app.bicep' = {
  name: 'containerApp'
  params: {
    name: containerAppName
    location: location
    environmentId: containerAppsEnv.outputs.id
    containerImage: containerImage
    acrLoginServer: acr.outputs.loginServer
    userAssignedIdentityId: identity.outputs.id
    userAssignedIdentityClientId: identity.outputs.clientId
    keyVaultUri: keyVault.outputs.uri
  }
}

module budget 'modules/budget.bicep' = {
  name: 'costBudget'
  params: {
    appName: appName
    alertEmail: alertEmail
    monthlyBudgetUsd: monthlyBudgetUsd
  }
}

module alerts 'modules/monitor-alerts.bicep' = {
  name: 'monitorAlerts'
  params: {
    appName: appName
    location: location
    actionGroupName: actionGroupName
    alertEmail: alertEmail
    containerAppId: containerApp.outputs.id
  }
}

output containerAppFqdn string = containerApp.outputs.fqdn
output acrLoginServer string = acr.outputs.loginServer
output keyVaultUri string = keyVault.outputs.uri
