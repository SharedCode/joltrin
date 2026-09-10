@description('User-assigned managed identity name.')
param name string

param location string

@description('Name of the existing ACR to grant AcrPull on.')
param acrName string

var acrPullRoleId = '7f951dda-4ed3-4680-a7ca-43fe172d538d' // built-in AcrPull role

resource identity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: name
  location: location
}

resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' existing = {
  name: acrName
}

// Grants the Container App's identity pull-only access to the registry.
// No admin user, no shared key - this is the only way the app can pull its image.
resource acrPullAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(acr.id, identity.id, acrPullRoleId)
  scope: acr
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', acrPullRoleId)
    principalId: identity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

output id string = identity.id
output principalId string = identity.properties.principalId
output clientId string = identity.properties.clientId
