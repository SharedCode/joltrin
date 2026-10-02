@description('Container Apps managed environment name.')
param name string

param location string

param logAnalyticsCustomerId string

@secure()
param logAnalyticsSharedKey string

param storageAccountName string
param fileShareName string

@description('Name the container app uses to reference the mounted share.')
param storageMountName string = 'joltrin-data'

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-05-01' existing = {
  name: storageAccountName
}

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

resource dataStorage 'Microsoft.App/managedEnvironments/storages@2023-11-02-preview' = {
  parent: env
  name: storageMountName
  properties: {
    azureFile: {
      accountName: storageAccountName
      accountKey: storageAccount.listKeys().keys[0].value
      shareName: fileShareName
      accessMode: 'ReadWrite'
    }
  }
}

output id string = env.id
output name string = env.name
output dataStorageName string = dataStorage.name
