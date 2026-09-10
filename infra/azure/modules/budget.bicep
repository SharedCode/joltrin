@description('App name prefix, used to name the budget.')
param appName string

param alertEmail string

@description('Monthly spend threshold in USD. The 100% alert is the "something is wrong, go look" signal for a deployment sized to run cheaply.')
param monthlyBudgetUsd int = 25

@description('Budget start date, first of the current month. utcNow() is only valid as a param default in Bicep, hence this indirection.')
param budgetStartDate string = utcNow('yyyy-MM-01')

resource costBudget 'Microsoft.Consumption/budgets@2023-11-01' = {
  name: '${appName}-monthly-budget'
  properties: {
    category: 'Cost'
    amount: monthlyBudgetUsd
    timeGrain: 'Monthly'
    timePeriod: {
      startDate: budgetStartDate
    }
    notifications: {
      alert50pct: {
        enabled: true
        operator: 'GreaterThanOrEqualTo'
        threshold: 50
        contactEmails: [
          alertEmail
        ]
        thresholdType: 'Actual'
      }
      alert75pct: {
        enabled: true
        operator: 'GreaterThanOrEqualTo'
        threshold: 75
        contactEmails: [
          alertEmail
        ]
        thresholdType: 'Actual'
      }
      alert90pct: {
        enabled: true
        operator: 'GreaterThanOrEqualTo'
        threshold: 90
        contactEmails: [
          alertEmail
        ]
        thresholdType: 'Actual'
      }
      alert100pct: {
        enabled: true
        operator: 'GreaterThanOrEqualTo'
        threshold: 100
        contactEmails: [
          alertEmail
        ]
        thresholdType: 'Actual'
      }
      forecast100pct: {
        enabled: true
        operator: 'GreaterThanOrEqualTo'
        threshold: 100
        contactEmails: [
          alertEmail
        ]
        thresholdType: 'Forecasted'
      }
    }
  }
}
