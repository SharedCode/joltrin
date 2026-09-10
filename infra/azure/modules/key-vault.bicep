@description('Key Vault name.')
param name string

param location string

@description('Principal ID (managed identity) to grant Key Vault Secrets User access to.')
param principalIdForAccess string

@secure()
param stripeSecretKey string

@secure()
param stripeWebhookSecret string

param stripePublishableKey string = ''

var secretsUserRoleId = '4633458b-17de-408a-b874-0445c86b69e6' // built-in Key Vault Secrets User role

resource vault 'Microsoft.KeyVault/vaults@2023-07-01' = {
  name: name
  location: location
  properties: {
    sku: {
      family: 'A'
      name: 'standard'
    }
    tenantId: subscription().tenantId
    enableRbacAuthorization: true
    enableSoftDelete: true
    softDeleteRetentionInDays: 7
    publicNetworkAccess: 'Enabled'
  }
}

resource secretsUserAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(vault.id, principalIdForAccess, secretsUserRoleId)
  scope: vault
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', secretsUserRoleId)
    principalId: principalIdForAccess
    principalType: 'ServicePrincipal'
  }
}

resource secretStripeKey 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'stripe-secret-key'
  properties: {
    value: stripeSecretKey
  }
}

resource secretWebhookSecret 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'stripe-webhook-secret'
  properties: {
    value: stripeWebhookSecret
  }
}

resource secretPublishableKey 'Microsoft.KeyVault/vaults/secrets@2023-07-01' = {
  parent: vault
  name: 'stripe-publishable-key'
  properties: {
    value: empty(stripePublishableKey) ? 'unset' : stripePublishableKey
  }
}

output uri string = vault.properties.vaultUri
output name string = vault.name
