@description('Azure Container Registry name (must be globally unique, alphanumeric only).')
param name string

param location string

// Basic SKU: cheapest tier, sufficient for a single low-traffic app image.
// Admin credentials are disabled - the Container App pulls exclusively via
// its user-assigned managed identity (AcrPull role), never a shared key.
resource acr 'Microsoft.ContainerRegistry/registries@2023-11-01-preview' = {
  name: name
  location: location
  sku: {
    name: 'Basic'
  }
  properties: {
    adminUserEnabled: false
    publicNetworkAccess: 'Enabled'
  }
}

output name string = acr.name
output id string = acr.id
output loginServer string = acr.properties.loginServer
