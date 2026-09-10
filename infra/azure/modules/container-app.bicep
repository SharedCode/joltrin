@description('Container App name.')
param name string

param location string

param environmentId string

@description('Full image reference: <acr-login-server>/joltrin:<tag>.')
param containerImage string

param acrLoginServer string

param userAssignedIdentityId string
param userAssignedIdentityClientId string
param keyVaultUri string

// Pinned to 1 replica: joltrin's embedded B-Tree engine has no documented
// multi-process write-safety guarantee, and this deployment optimizes for
// lowest cost over horizontal scale. CPU/memory/concurrency limits below
// are guardrails against runaway cost and restart loops on the single
// replica, not an autoscaling fan-out policy. Revisit if/when the engine's
// Redis-backed distributed locking (adapters/redis) is wired in and this
// value is deliberately raised.
var minReplicas = 1
var maxReplicas = 1

resource containerApp 'Microsoft.App/containerApps@2023-11-02-preview' = {
  name: name
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${userAssignedIdentityId}': {}
    }
  }
  properties: {
    managedEnvironmentId: environmentId
    configuration: {
      activeRevisionsMode: 'Single'
      registries: [
        {
          server: acrLoginServer
          identity: userAssignedIdentityId
        }
      ]
      secrets: [
        {
          name: 'stripe-secret-key'
          keyVaultUrl: '${keyVaultUri}secrets/stripe-secret-key'
          identity: userAssignedIdentityId
        }
        {
          name: 'stripe-webhook-secret'
          keyVaultUrl: '${keyVaultUri}secrets/stripe-webhook-secret'
          identity: userAssignedIdentityId
        }
        {
          name: 'stripe-publishable-key'
          keyVaultUrl: '${keyVaultUri}secrets/stripe-publishable-key'
          identity: userAssignedIdentityId
        }
      ]
      ingress: {
        external: true
        targetPort: 8080
        transport: 'auto'
        allowInsecure: false
      }
    }
    template: {
      containers: [
        {
          name: 'joltrin-httpserver'
          image: containerImage
          // Matches the Dockerfile runtime stage's own values: EXPOSE 8080,
          // ENTRYPOINT sop-server, ENV datapath=/var/lib/sop.
          command: [
            'sop-server'
          ]
          args: [
            '-database'
            '/var/lib/sop'
            '-port'
            '8080'
            '-open-browser=false'
          ]
          env: [
            {
              name: 'STRIPE_SECRET_KEY'
              secretRef: 'stripe-secret-key'
            }
            {
              name: 'STRIPE_WEBHOOK_SECRET'
              secretRef: 'stripe-webhook-secret'
            }
            {
              name: 'STRIPE_PUBLISHABLE_KEY'
              secretRef: 'stripe-publishable-key'
            }
            {
              name: 'AZURE_CLIENT_ID'
              value: userAssignedIdentityClientId
            }
          ]
          // 0.5 vCPU / 1.0 GiB: matches the requested cost-containment
          // sizing. Combined GB-CPU pairing is one of ACA's valid
          // combinations (0.5 vCPU pairs with 1Gi).
          resources: {
            cpu: json('0.5')
            memory: '1Gi'
          }
          probes: [
            {
              type: 'Liveness'
              httpGet: {
                path: '/api/health'
                port: 8080
              }
              initialDelaySeconds: 10
              periodSeconds: 30
              failureThreshold: 3
            }
            {
              type: 'Readiness'
              httpGet: {
                path: '/api/health'
                port: 8080
              }
              initialDelaySeconds: 5
              periodSeconds: 15
              failureThreshold: 3
            }
            {
              type: 'Startup'
              httpGet: {
                path: '/api/health'
                port: 8080
              }
              initialDelaySeconds: 5
              periodSeconds: 5
              failureThreshold: 10
            }
          ]
        }
      ]
      scale: {
        minReplicas: minReplicas
        maxReplicas: maxReplicas
        rules: [
          {
            // Concurrency guardrail: with maxReplicas pinned to 1 this
            // does not trigger scale-out, but it documents and caps the
            // per-replica concurrent-request budget the ingress will
            // queue against rather than letting requests pile up
            // unbounded in front of a single instance.
            name: 'http-concurrency-guardrail'
            http: {
              metadata: {
                concurrentRequests: '50'
              }
            }
          }
        ]
      }
    }
  }
}

output id string = containerApp.id
output fqdn string = containerApp.properties.configuration.ingress.fqdn
