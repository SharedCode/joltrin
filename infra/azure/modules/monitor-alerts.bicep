// Native Azure Monitor metric alerts on the Container App, watching the
// guardrails this deployment relies on instead of real horizontal scaling
// (see container-app.bicep for why replicas are pinned to 1). Deliberately
// NOT Managed Prometheus/Managed Grafana: that stack has real monthly cost
// (ingestion + workspace) that a single-replica, least-cost deployment
// doesn't need. Metric alert rules cost a few cents/month each.
//
// IMPORTANT: the exact metric names for Microsoft.App/containerApps below
// (cpuPercentage, memoryPercentage, restartCount) are the documented
// standard-metric names as of this writing, but Azure has renamed Container
// Apps metrics across API versions before. Before the first deploy, verify
// them against the live resource:
//   az monitor metrics list-definitions --resource <container-app-id>
// and adjust the `metricName` values below if they've drifted.

@description('App name prefix.')
param appName string

param location string

param actionGroupName string

param alertEmail string

@description('Resource ID of the Container App to monitor.')
param containerAppId string

resource actionGroup 'Microsoft.Insights/actionGroups@2023-01-01' = {
  name: actionGroupName
  location: 'global'
  properties: {
    groupShortName: take(actionGroupName, 12)
    enabled: true
    emailReceivers: [
      {
        name: 'primary'
        emailAddress: alertEmail
        useCommonAlertSchema: true
      }
    ]
  }
}

resource cpuAlert 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: '${appName}-cpu-high'
  location: 'global'
  properties: {
    severity: 2
    enabled: true
    scopes: [
      containerAppId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT15M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          name: 'HighCpu'
          metricName: 'CpuPercentage'
          metricNamespace: 'Microsoft.App/containerApps'
          operator: 'GreaterThanOrEqual'
          threshold: 85
          timeAggregation: 'Average'
        }
      ]
    }
    actions: [
      {
        actionGroupId: actionGroup.id
      }
    ]
  }
}

resource memoryAlert 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: '${appName}-memory-high'
  location: 'global'
  properties: {
    severity: 2
    enabled: true
    scopes: [
      containerAppId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT15M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          name: 'HighMemory'
          metricName: 'MemoryPercentage'
          metricNamespace: 'Microsoft.App/containerApps'
          operator: 'GreaterThanOrEqual'
          threshold: 85
          timeAggregation: 'Average'
        }
      ]
    }
    actions: [
      {
        actionGroupId: actionGroup.id
      }
    ]
  }
}

resource restartAlert 'Microsoft.Insights/metricAlerts@2018-03-01' = {
  name: '${appName}-restart-loop'
  location: 'global'
  properties: {
    severity: 1
    enabled: true
    scopes: [
      containerAppId
    ]
    evaluationFrequency: 'PT5M'
    windowSize: 'PT15M'
    criteria: {
      'odata.type': 'Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria'
      allOf: [
        {
          name: 'RestartLoop'
          metricName: 'RestartCount'
          metricNamespace: 'Microsoft.App/containerApps'
          operator: 'GreaterThanOrEqual'
          threshold: 3
          timeAggregation: 'Total'
        }
      ]
    }
    actions: [
      {
        actionGroupId: actionGroup.id
      }
    ]
  }
}
