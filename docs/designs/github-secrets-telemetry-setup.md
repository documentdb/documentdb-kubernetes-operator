# GitHub Secrets Setup for Application Insights Telemetry

This document describes how to configure GitHub secrets for Application Insights telemetry collection in the DocumentDB Kubernetes Operator CI/CD pipeline.

## Overview

The DocumentDB Operator uses Application Insights to collect anonymous telemetry data about operator usage patterns. This helps the team understand:

- How many people use the operator
- Which cloud providers are most common (AKS, EKS, GKE)
- Common cluster configurations
- Error patterns and operational issues

To enable telemetry in CI/CD workflows, you need to configure a GitHub secret containing the Application Insights connection string.

## Prerequisites

1. An Azure Application Insights resource
2. Admin access to the GitHub repository (to create secrets)

## Step 1: Create Application Insights Resource

If you don't have an Application Insights resource, create one in Azure:

```bash
# Create a resource group (if needed)
az group create --name documentdb-telemetry-rg --location eastus2

# Create Application Insights resource
az monitor app-insights component create \
  --app documentdb-operator-telemetry \
  --location eastus2 \
  --resource-group documentdb-telemetry-rg \
  --kind web \
  --application-type web
```

## Step 2: Get the Connection String

Retrieve the connection string from your Application Insights resource:

```bash
az monitor app-insights component show \
  --app documentdb-operator-telemetry \
  --resource-group documentdb-telemetry-rg \
  --query connectionString \
  --output tsv
```

The connection string will look like:
```
InstrumentationKey=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx;IngestionEndpoint=https://eastus2-2.in.applicationinsights.azure.com/;LiveEndpoint=https://eastus2.livediagnostics.monitor.azure.com/;ApplicationId=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

## Step 3: Create GitHub Secret

### Via GitHub UI

1. Navigate to your repository on GitHub
2. Go to **Settings** → **Secrets and variables** → **Actions**
3. Click **New repository secret**
4. Set the following:
   - **Name**: `APPINSIGHTS_CONNECTION_STRING`
   - **Secret**: Paste the connection string from Step 2
5. Click **Add secret**

### Via GitHub CLI

```bash
# Authenticate with GitHub CLI (if not already)
gh auth login

# Set the secret
gh secret set APPINSIGHTS_CONNECTION_STRING --body "InstrumentationKey=xxx;IngestionEndpoint=https://..."
```

## Step 4: Use the Secret in GitHub Actions

Reference the secret in your GitHub Actions workflow:

```yaml
# .github/workflows/test-integration.yml
name: Integration Tests

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Build and Deploy Operator
        env:
          APPLICATIONINSIGHTS_CONNECTION_STRING: ${{ secrets.APPINSIGHTS_CONNECTION_STRING }}
        run: |
          # Build operator image with telemetry enabled
          make docker-build
          
          # Deploy with telemetry connection string
          helm install documentdb-operator ./operator/documentdb-helm-chart \
            --set telemetry.enabled=true \
            --set telemetry.connectionString="${APPLICATIONINSIGHTS_CONNECTION_STRING}"
```

## Step 5: Helm Chart Configuration

The operator Helm chart supports telemetry configuration via these values:

```yaml
# values.yaml
telemetry:
  # Enable/disable telemetry collection
  enabled: true
  
  # Option 1: Direct connection string (for CI/CD, injected from secrets)
  connectionString: ""
  
  # Option 2: Instrumentation key only
  instrumentationKey: ""
  
  # Option 3: Use an existing Kubernetes secret
  existingSecret: ""
```

### CI/CD Deployment Example

```bash
# Deploy with connection string from environment variable
helm upgrade --install documentdb-operator ./operator/documentdb-helm-chart \
  --namespace documentdb-operator \
  --create-namespace \
  --set telemetry.enabled=true \
  --set "telemetry.connectionString=${APPLICATIONINSIGHTS_CONNECTION_STRING}"
```

## Alternative: Using Kubernetes Secrets

For production deployments, you may want to store the connection string in a Kubernetes secret:

```yaml
# Create a secret with the connection string
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-telemetry-secret
  namespace: documentdb-operator
type: Opaque
stringData:
  APPLICATIONINSIGHTS_CONNECTION_STRING: "InstrumentationKey=xxx;IngestionEndpoint=https://..."
```

Then reference it in Helm:

```yaml
# values.yaml
telemetry:
  enabled: true
  existingSecret: "documentdb-telemetry-secret"
```

## Security Considerations

1. **Secret Rotation**: Rotate the Application Insights key periodically
2. **Scope**: Use repository-level secrets (not organization-level) for better isolation
3. **Access Control**: Limit who can view/edit repository secrets
4. **Environment-Specific**: Consider using different App Insights resources for dev/prod

## Verifying Telemetry Collection

After deployment, verify telemetry is being sent:

1. Check operator logs for telemetry transmission:
   ```bash
   kubectl logs -n documentdb-operator deployment/documentdb-operator | grep -i "telemetry\|appinsights"
   ```

2. Query Application Insights:
   ```kusto
   // In Azure Portal > Application Insights > Logs
   customEvents
   | where timestamp > ago(1h)
   | where name startswith "documentdb"
   | summarize count() by name
   ```

## Troubleshooting

### Telemetry Not Appearing

1. Verify the connection string is correct:
   ```bash
   echo $APPLICATIONINSIGHTS_CONNECTION_STRING | grep "InstrumentationKey="
   ```

2. Check if the operator has the environment variable:
   ```bash
   kubectl exec -n documentdb-operator deployment/documentdb-operator -- env | grep APPINSIGHTS
   ```

3. Check operator logs for errors:
   ```bash
   kubectl logs -n documentdb-operator deployment/documentdb-operator | grep -i error
   ```

### Ingestion Delays

Application Insights has a batching interval (default: 30 seconds). Events may take up to a few minutes to appear in the portal.

## Data Privacy

The telemetry system is designed with privacy in mind:

- **No PII**: No cluster names, namespaces, IP addresses, or user-provided identifiers
- **Hashed namespaces**: Namespace names are SHA-256 hashed
- **GUIDs for correlation**: Auto-generated GUIDs are used instead of resource names
- **Categorized errors**: Error messages are categorized, not raw strings

See [appinsights-metrics.md](./appinsights-metrics.md) for the complete list of collected telemetry.
