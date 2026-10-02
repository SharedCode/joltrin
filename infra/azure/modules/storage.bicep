@description('Storage account name (3-24 lowercase letters and digits).')
param name string

param location string

@description('Azure Files share that holds the app data directory (config.json, stores, billing state).')
param shareName string = 'joltrin-data'

@description('Share quota in GiB. Standard files bill on used capacity, the quota is only a ceiling.')
param shareQuotaGiB int = 5

resource account 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: name
  location: location
  kind: 'StorageV2'
  sku: {
    name: 'Standard_LRS'
  }
  properties: {
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
    allowBlobPublicAccess: false
    // Container Apps mounts Azure Files with the account key, so shared key
    // access has to stay on. Nothing else in this stack uses the account.
    allowSharedKeyAccess: true
  }
}

resource fileService 'Microsoft.Storage/storageAccounts/fileServices@2023-05-01' = {
  parent: account
  name: 'default'
}

resource share 'Microsoft.Storage/storageAccounts/fileServices/shares@2023-05-01' = {
  parent: fileService
  name: shareName
  properties: {
    shareQuota: shareQuotaGiB
    enabledProtocols: 'SMB'
  }
}

output accountName string = account.name
output shareName string = share.name
