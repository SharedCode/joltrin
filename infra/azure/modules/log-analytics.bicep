@description('Log Analytics workspace name.')
param name string

param location string

@description('Data retention in days. Kept short deliberately: Log Analytics bills on ingestion and retained volume, and this workspace only needs enough history to debug a recent incident.')
param retentionInDays int = 30

resource workspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = {
  name: name
  location: location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
    retentionInDays: retentionInDays
    features: {
      disableLocalAuth: false
    }
  }
}

output customerId string = workspace.properties.customerId
output primarySharedKey string = workspace.listKeys().primarySharedKey
output id string = workspace.id
