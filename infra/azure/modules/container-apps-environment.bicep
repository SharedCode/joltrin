@description('Container Apps managed environment name.')
param name string

param location string

param logAnalyticsCustomerId string

@secure()
param logAnalyticsSharedKey string

resource env 'Microsoft.App/managedEnvironments@2023-11-02-preview' = {
  name: name
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logAnalyticsCustomerId
        sharedKey: logAnalyticsSharedKey
      }
    }
    // zoneRedundant defaults to false: zone redundancy duplicates
    // infrastructure across availability zones, which is a cost multiplier
    // this single-replica, least-cost deployment doesn't need.
  }
}

output id string = env.id
output name string = env.name
