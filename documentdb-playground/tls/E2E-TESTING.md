# End-to-End (E2E) Testing Guide

This guide provides comprehensive instructions for testing the DocumentDB Kubernetes Operator with TLS support from start to finish.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Quick E2E Test (Automated)](#quick-e2e-test-automated)
- [Manual Step-by-Step E2E Test](#manual-step-by-step-e2e-test)
- [Testing Individual Components](#testing-individual-components)
- [Validation and Verification](#validation-and-verification)
- [Cleanup](#cleanup)
- [Troubleshooting](#troubleshooting)

## Overview

The E2E testing process validates the entire DocumentDB TLS setup, including:

1. **Infrastructure Setup**: AKS cluster, Azure Key Vault, networking
2. **Prerequisites Installation**: cert-manager, Secrets Store CSI driver
3. **Operator Deployment**: DocumentDB operator with Helm
4. **TLS Modes**:
   - SelfSigned mode (cert-manager with self-signed issuer)
   - Provided mode (Azure Key Vault with CSI driver)
5. **Connectivity Validation**: MongoDB shell connections with TLS
6. **Data Operations**: CRUD operations to verify full functionality

## Prerequisites

### Required Tools

Ensure these tools are installed and accessible in your PATH:

```bash
# Check required tools
az --version          # Azure CLI
kubectl version       # Kubernetes CLI
helm version          # Helm 3.x
mongosh --version     # MongoDB Shell
openssl version       # OpenSSL
jq --version          # JSON processor (optional but helpful)
```

**Installation guides:**
- [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [mongosh](https://www.mongodb.com/docs/mongodb-shell/install/)
- [jq](https://stedolan.github.io/jq/download/)

### Azure Requirements

- **Azure subscription** with Owner or Contributor permissions
- **Authenticated session**: Run `az login` before starting
- **Subscription ID**: Know your Azure subscription ID

```bash
# Login to Azure
az login

# List subscriptions
az account list --output table

# Set default subscription
az account set --subscription <subscription-id>
```

### Resource Quotas

Ensure your subscription has sufficient quota for:
- **AKS cluster**: 2-4 nodes (Standard_D4s_v5 or similar)
- **Public IPs**: 1-2 for LoadBalancer services
- **Storage**: ~50GB for persistent volumes
- **Key Vault**: 1 instance

## Quick E2E Test (Automated)

The fastest way to run a complete E2E test is using the automated script.

### Single Command Test

```bash
cd documentdb-playground/tls/scripts

# Run complete E2E test
./create-cluster.sh \
  --suffix e2etest \
  --subscription-id <your-subscription-id>
```

This single command will:
1. ✅ Create AKS cluster with all addons
2. ✅ Install cert-manager and CSI driver
3. ✅ Create Azure Key Vault
4. ✅ Deploy DocumentDB operator
5. ✅ Test SelfSigned TLS mode
6. ✅ Test Provided TLS mode (Azure Key Vault)
7. ✅ Validate connectivity for both modes
8. ✅ Provide connection strings

**Expected Duration**: ~25-30 minutes

### Cleanup After Test

```bash
# Delete all resources
./delete-cluster.sh \
  --suffix e2etest \
  --subscription-id <your-subscription-id> \
  --all
```

**Expected Duration**: ~5-10 minutes

## Automated E2E Testing with Scripts

All E2E tests can be fully automated using the provided scripts. This section describes how to use them for comprehensive testing without manual intervention.

### Understanding the Test Scripts

The testing suite consists of several scripts that work together:

1. **`create-cluster.sh`** - Main entry point that orchestrates the entire setup
2. **`gateway-tls-e2e.sh`** - Core E2E script that tests both TLS modes
3. **`setup-selfsigned-gateway-tls.sh`** - Configures SelfSigned TLS mode
4. **`setup-documentdb-akv.sh`** - Sets up Azure Key Vault for certificates
5. **`documentdb-provided-mode-setup.sh`** - Configures Provided TLS mode
6. **`tls-connectivity-check.sh`** - Validates TLS connectivity and certificates
7. **`delete-cluster.sh`** - Cleanup and resource deletion

### Complete Automated Test Scenarios

#### Scenario 1: Full End-to-End Test (Recommended)

This is the complete automated test that validates everything from cluster creation to TLS validation.

```bash
cd documentdb-playground/tls/scripts

# Set your configuration
export SUFFIX="e2etest-$(date +%H%M)"
export SUBSCRIPTION_ID="<your-azure-subscription-id>"

# Run complete E2E test
./create-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID"
```

**What it automates:**
- ✅ Creates AKS cluster with all required addons (cert-manager, CSI driver)
- ✅ Deploys DocumentDB operator via Helm
- ✅ Creates Azure Key Vault with proper RBAC
- ✅ Deploys DocumentDB instance with SelfSigned TLS
- ✅ Validates SelfSigned TLS connectivity with mongosh
- ✅ Reconfigures to Provided TLS mode (Azure Key Vault)
- ✅ Validates Provided TLS connectivity with mongosh
- ✅ Outputs connection strings and status

**Expected output:**
```
Running end-to-end gateway TLS validation with:
  Resource Group: guanzhou-e2etest-1234-rg
  AKS Cluster:    guanzhou-e2etest-1234
  Location:       eastus2
  Key Vault:      ddb-issuer-e2etest-1234
  Namespace:      documentdb-preview-ns
  DocumentDB:     documentdb-preview

2025-11-04 10:00:00 :: Provision AKS cluster
✅ AKS cluster created successfully
2025-11-04 10:15:00 :: Deploy DocumentDB self-signed mode
✅ DocumentDB deployed with SelfSigned TLS
2025-11-04 10:18:00 :: Validate self-signed connectivity
✅ TLS handshake successful
✅ mongosh connection successful
2025-11-04 10:20:00 :: Prepare Azure Key Vault
✅ Key Vault created with certificate
2025-11-04 10:22:00 :: Switch cluster to provided TLS
✅ DocumentDB reconfigured for Provided TLS
2025-11-04 10:25:00 :: Validate provided-mode connectivity
✅ TLS handshake successful
✅ mongosh connection successful

End-to-end gateway TLS validation completed successfully.
```

**Duration**: ~25-30 minutes

#### Scenario 2: Test on Existing Cluster

If you already have an AKS cluster, test TLS setup without recreating infrastructure.

```bash
cd documentdb-playground/tls/scripts

# Ensure you have kubectl context set to your cluster
kubectl config current-context

# Run E2E test on existing cluster
./create-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --skip-cluster
```

**What it automates:**
- ✅ Uses existing AKS cluster (no cluster creation)
- ✅ Installs cert-manager if not present
- ✅ Creates Azure Key Vault
- ✅ Deploys DocumentDB operator
- ✅ Tests both SelfSigned and Provided TLS modes
- ✅ Validates connectivity for both modes

**Duration**: ~10-15 minutes

#### Scenario 3: Direct E2E Script Usage (Advanced)

For more control, use the core `gateway-tls-e2e.sh` script directly.

```bash
cd documentdb-playground/tls/scripts

./gateway-tls-e2e.sh \
  --suffix "e2etest" \
  --location "eastus2" \
  --resource-group "my-test-rg" \
  --aks-name "my-aks-cluster" \
  --keyvault "my-key-vault" \
  --namespace "test-ns" \
  --docdb-name "test-documentdb"
```

**Additional options:**
```bash
# Skip cluster creation (use existing)
./gateway-tls-e2e.sh --suffix test --skip-cluster

# Custom GitHub registry (for private operator images)
./gateway-tls-e2e.sh \
  --suffix test \
  --github-username "myusername" \
  --github-token "ghp_xxxxxxxxxxxx"
```

### Component-Level Automated Tests

Test individual components in isolation using helper scripts.

#### Test: SelfSigned TLS Mode Only

```bash
cd documentdb-playground/tls/scripts

# Setup and validate SelfSigned TLS
./setup-selfsigned-gateway-tls.sh \
  --namespace "test-ns" \
  --name "test-documentdb" \
  --username "admin" \
  --password "SecurePass123!"

# Validate connectivity
./tls-connectivity-check.sh \
  --mode selfsigned \
  --namespace "test-ns" \
  --docdb-name "test-documentdb"
```

**Validates:**
- cert-manager ClusterIssuer creation
- Certificate resource generation
- TLS secret availability
- Gateway pod TLS configuration
- mongosh connectivity with TLS

**Duration**: ~5 minutes

#### Test: Provided TLS Mode Only

```bash
cd documentdb-playground/tls/scripts

# Setup Azure Key Vault and certificate
./setup-documentdb-akv.sh \
  --resource-group "my-rg" \
  --location "eastus2" \
  --keyvault "my-key-vault" \
  --aks-name "my-aks-cluster" \
  --sni-host "10.0.0.1.sslip.io"

# Configure DocumentDB for Provided mode
./documentdb-provided-mode-setup.sh \
  --resource-group "my-rg" \
  --aks-name "my-aks-cluster" \
  --keyvault "my-key-vault" \
  --cert-name "documentdb-gateway" \
  --namespace "test-ns" \
  --docdb-name "test-documentdb" \
  --provided-secret "documentdb-tls" \
  --user-assigned-client "<kubelet-identity-client-id>"

# Validate Provided mode connectivity
./tls-connectivity-check.sh \
  --mode provided \
  --namespace "test-ns" \
  --docdb-name "test-documentdb" \
  --provided-secret "documentdb-tls" \
  --sni-host "10.0.0.1.sslip.io"
```

**Validates:**
- Azure Key Vault certificate creation
- RBAC role assignments
- SecretProviderClass configuration
- CSI driver secret synchronization
- Gateway pod with provided certificate
- mongosh connectivity with custom certificate

**Duration**: ~8 minutes

#### Test: TLS Connectivity Only

Validate existing TLS setup without making changes.

```bash
cd documentdb-playground/tls/scripts

# Check SelfSigned mode
./tls-connectivity-check.sh \
  --mode selfsigned \
  --namespace "my-ns" \
  --docdb-name "my-documentdb"

# Check Provided mode
./tls-connectivity-check.sh \
  --mode provided \
  --namespace "my-ns" \
  --docdb-name "my-documentdb" \
  --provided-secret "my-tls-secret" \
  --sni-host "example.com"
```

**Validates:**
- TLS status readiness
- Service endpoint availability
- Certificate validity
- TLS handshake success
- mongosh connection with TLS

**Duration**: ~2 minutes

### Automated Cleanup Tests

Test cleanup functionality with different retention policies.

```bash
cd documentdb-playground/tls/scripts

# Complete cleanup (delete everything)
./delete-cluster.sh \
  --suffix "e2etest" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --all

# Keep cluster, delete only DocumentDB
./delete-cluster.sh \
  --suffix "e2etest" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --keep-cluster

# Delete cluster, preserve Key Vault
./delete-cluster.sh \
  --suffix "e2etest" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --keep-keyvault
```

### Continuous Integration Test Script

For CI/CD pipelines, use this automated test script:

```bash
#!/bin/bash
# ci-automated-test.sh

set -e

# Configuration
SUFFIX="ci-$(date +%Y%m%d%H%M%S)"
SUBSCRIPTION_ID="${AZURE_SUBSCRIPTION_ID}"
LOCATION="${AZURE_LOCATION:-eastus2}"

echo "=== Starting CI E2E Test ==="
echo "Suffix: $SUFFIX"
echo "Subscription: $SUBSCRIPTION_ID"
echo "Location: $LOCATION"
echo ""

# Navigate to scripts directory
cd "$(dirname "$0")/documentdb-playground/tls/scripts"

# Run E2E test
echo "=== Running E2E Test ==="
./create-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --location "$LOCATION" || {
  echo "❌ E2E test failed"
  exit 1
}

echo ""
echo "=== Running Validation ==="

# Additional validation
export NS="documentdb-preview-ns"
export DOCDB_NAME="documentdb-preview"

# Check TLS status
TLS_READY=$(kubectl get documentdb "$DOCDB_NAME" -n "$NS" \
  -o jsonpath='{.status.tls.ready}' 2>/dev/null || echo "false")

if [ "$TLS_READY" != "true" ]; then
  echo "❌ TLS not ready"
  exit 1
fi

echo "✅ TLS status verified"

# Check DocumentDB status
DOCDB_STATUS=$(kubectl get documentdb "$DOCDB_NAME" -n "$NS" \
  -o jsonpath='{.status.status}' 2>/dev/null || echo "unknown")

if [[ ! "$DOCDB_STATUS" =~ "healthy" ]]; then
  echo "❌ DocumentDB not healthy: $DOCDB_STATUS"
  exit 1
fi

echo "✅ DocumentDB status verified"

# Cleanup
echo ""
echo "=== Cleaning Up ==="
./delete-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --all || {
  echo "⚠️  Cleanup failed (manual cleanup may be required)"
}

echo ""
echo "=== CI E2E Test Complete ==="
exit 0
```

**Usage in CI/CD:**
```yaml
# Example GitHub Actions workflow
- name: Run E2E Tests
  env:
    AZURE_SUBSCRIPTION_ID: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
    AZURE_LOCATION: eastus2
  run: |
    az login --service-principal -u ${{ secrets.AZURE_CLIENT_ID }} \
      -p ${{ secrets.AZURE_CLIENT_SECRET }} \
      --tenant ${{ secrets.AZURE_TENANT_ID }}
    ./ci-automated-test.sh
```

### Automated Test Matrix

Run multiple test scenarios to ensure compatibility:

```bash
#!/bin/bash
# test-matrix.sh

set -e

SUBSCRIPTION_ID="<your-subscription-id>"
SCENARIOS=(
  "eastus2:SelfSigned"
  "westus2:Provided"
  "centralus:Both"
)

for scenario in "${SCENARIOS[@]}"; do
  LOCATION="${scenario%%:*}"
  MODE="${scenario##*:}"
  SUFFIX="matrix-$(echo $LOCATION | tr -d ' ')-$(date +%H%M)"
  
  echo "=== Testing: Location=$LOCATION, Mode=$MODE ==="
  
  case "$MODE" in
    SelfSigned)
      ./setup-selfsigned-gateway-tls.sh \
        --namespace "test-$SUFFIX" \
        --name "documentdb-$SUFFIX"
      ./tls-connectivity-check.sh \
        --mode selfsigned \
        --namespace "test-$SUFFIX" \
        --docdb-name "documentdb-$SUFFIX"
      ;;
    Provided)
      # Full Provided mode test
      ./create-cluster.sh --suffix "$SUFFIX" --subscription-id "$SUBSCRIPTION_ID"
      ;;
    Both)
      # Full E2E test (both modes)
      ./create-cluster.sh --suffix "$SUFFIX" --subscription-id "$SUBSCRIPTION_ID"
      ;;
  esac
  
  # Cleanup
  ./delete-cluster.sh --suffix "$SUFFIX" --subscription-id "$SUBSCRIPTION_ID" --all
  
  echo "✅ Test complete: $scenario"
  echo ""
done

echo "=== All test scenarios completed ==="
```

### Script Output and Logging

All scripts provide detailed output:

- **Timestamps**: Each major step is timestamped
- **Status indicators**: ✅ (success), ❌ (error), ⚠️ (warning)
- **Progress tracking**: Clear indication of current step
- **Error messages**: Detailed error information for debugging
- **Connection strings**: Ready-to-use mongosh connection strings

**Example output:**
```
2025-11-04 10:00:00 :: Creating AKS cluster
✅ Resource group created
✅ AKS cluster created
✅ Azure CSI drivers installed
✅ cert-manager installed

2025-11-04 10:15:00 :: Deploying DocumentDB
✅ Namespace created
✅ Credentials secret created
✅ DocumentDB deployed
⏳ Waiting for TLS readiness...
✅ TLS ready (took 120 seconds)

2025-11-04 10:18:00 :: Validating connectivity
✅ Service endpoint: 10.0.0.1:10260
✅ TLS handshake successful
✅ mongosh connection successful

Connection string:
mongodb://<username>:<password>@ssw0rd123@10.0.0.1:10260/?tls=true&tlsAllowInvalidCertificates=true
```

### Debugging Automated Tests

If automated tests fail, scripts provide debug options:

```bash
# Enable verbose output
bash -x ./create-cluster.sh --suffix test --subscription-id <id>

# Check script exit codes
./create-cluster.sh --suffix test --subscription-id <id>
echo "Exit code: $?"

# Capture output for analysis
./create-cluster.sh --suffix test --subscription-id <id> 2>&1 | tee test-output.log
```

---

## Manual Step-by-Step E2E Test

For detailed understanding or debugging, follow these manual steps.

### Step 1: Set Environment Variables

```bash
# Set your unique identifier
export SUFFIX="e2etest-$(whoami)"
export SUBSCRIPTION_ID="<your-azure-subscription-id>"
export LOCATION="eastus2"
export RG="documentdb-${SUFFIX}-rg"
export AKS_NAME="documentdb-${SUFFIX}-aks"
export KV_NAME="ddb-kv-${SUFFIX}"
export NS="documentdb-e2e-ns"
export DOCDB_NAME="documentdb-e2e"

# Display configuration
echo "Configuration:"
echo "  Suffix: $SUFFIX"
echo "  Resource Group: $RG"
echo "  AKS Cluster: $AKS_NAME"
echo "  Key Vault: $KV_NAME"
echo "  Namespace: $NS"
echo "  DocumentDB: $DOCDB_NAME"
```

### Step 2: Run Comprehensive E2E Script

The `gateway-tls-e2e.sh` script orchestrates all components:

```bash
cd documentdb-playground/tls/scripts

./gateway-tls-e2e.sh \
  --suffix "$SUFFIX" \
  --location "$LOCATION" \
  --resource-group "$RG" \
  --aks-name "$AKS_NAME" \
  --keyvault "$KV_NAME" \
  --namespace "$NS" \
  --docdb-name "$DOCDB_NAME"
```

**What it does:**
- Creates all Azure infrastructure
- Installs all prerequisites
- Deploys operator and DocumentDB
- Tests both TLS modes
- Validates connectivity

### Step 3: Verify Each Component

After the script completes, verify each component:

```bash
# 1. Check AKS cluster
az aks show --resource-group "$RG" --name "$AKS_NAME" --output table

# 2. Verify kubectl context
kubectl config current-context

# 3. Check cert-manager
kubectl get pods -n cert-manager
kubectl get clusterissuer

# 4. Check CSI driver
kubectl get pods -n kube-system | grep secrets-store

# 5. Verify Key Vault
az keyvault show --name "$KV_NAME" --output table

# 6. Check DocumentDB operator
kubectl get pods -n "$NS"
kubectl get documentdb -n "$NS"

# 7. Verify TLS status
kubectl get documentdb "$DOCDB_NAME" -n "$NS" -o jsonpath='{.status.tls}' | jq
```

### Step 4: Test Connectivity

```bash
# Get DocumentDB service
kubectl get svc -n "$NS"

# Get external IP (for LoadBalancer)
EXTERNAL_IP=$(kubectl get svc -n "$NS" -l "app.kubernetes.io/name=documentdb" -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}')

# Get credentials
USERNAME=$(kubectl get secret documentdb-credentials -n "$NS" -o jsonpath='{.data.username}' | base64 -d)
PASSWORD=$(kubectl get secret documentdb-credentials -n "$NS" -o jsonpath='{.data.password}' | base64 -d)

# Connect with mongosh (SelfSigned mode allows invalid certs)
mongosh "mongodb://${USERNAME}:${PASSWORD}@${EXTERNAL_IP}:10260/?tls=true&tlsAllowInvalidCertificates=true"
```

### Step 5: Test CRUD Operations

Once connected via mongosh:

```javascript
// Create database
use e2etest

// Insert documents
db.users.insertMany([
  { name: "Alice", email: "alice@example.com", role: "admin" },
  { name: "Bob", email: "bob@example.com", role: "user" },
  { name: "Charlie", email: "charlie@example.com", role: "user" }
])

// Read documents
db.users.find()
db.users.find({ role: "user" })

// Update document
db.users.updateOne(
  { name: "Bob" },
  { $set: { role: "admin" } }
)

// Verify update
db.users.find({ name: "Bob" })

// Delete document
db.users.deleteOne({ name: "Charlie" })

// Verify deletion
db.users.count()

// Clean up
db.users.drop()
```

## Testing Individual Components

You can test individual components separately using the provided scripts.

### Test 1: SelfSigned TLS Mode Only

```bash
cd documentdb-playground/tls/scripts

# Assumes cluster already exists
./setup-selfsigned-gateway-tls.sh \
  --namespace "$NS" \
  --docdb-name "$DOCDB_NAME"
```

**Validates:**
- cert-manager ClusterIssuer creation
- Certificate resource creation
- TLS secret generation
- Gateway pod TLS configuration

### Test 2: Provided TLS Mode (Azure Key Vault)

```bash
cd documentdb-playground/tls/scripts

# Setup Azure Key Vault and certificates
./setup-documentdb-akv.sh \
  --suffix "$SUFFIX" \
  --keyvault "$KV_NAME" \
  --resource-group "$RG" \
  --namespace "$NS"

# Configure DocumentDB for Provided mode
./documentdb-provided-mode-setup.sh \
  --namespace "$NS" \
  --docdb-name "$DOCDB_NAME" \
  --secret-name documentdb-provided-tls
```

**Validates:**
- Azure Key Vault certificate creation
- SecretProviderClass configuration
- CSI driver secret synchronization
- Gateway pod with provided certificate

### Test 3: TLS Connectivity

```bash
cd documentdb-playground/tls/scripts

# Run comprehensive connectivity tests
./tls-connectivity-check.sh \
  --namespace "$NS" \
  --docdb-name "$DOCDB_NAME"
```

**Validates:**
- TLS handshake
- Certificate validation
- MongoDB protocol over TLS
- Connection string generation

### Test 4: Full Cluster Creation

Use the comprehensive cluster creation script:

```bash
cd documentdb-playground/tls/scripts

# Creates everything from scratch
./create-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID"
```

## Validation and Verification

### Automated Validation Checklist

Run these commands to verify a successful E2E test:

```bash
#!/bin/bash

echo "=== E2E Validation Checklist ==="
echo ""

# 1. AKS Cluster
echo "1. AKS Cluster Status:"
az aks show --resource-group "$RG" --name "$AKS_NAME" --query "powerState.code" -o tsv
echo ""

# 2. cert-manager
echo "2. cert-manager Pods:"
kubectl get pods -n cert-manager --no-headers | wc -l
echo "   Expected: 3 (cert-manager, cainjector, webhook)"
echo ""

# 3. DocumentDB Operator
echo "3. DocumentDB Operator:"
kubectl get deployment -n "$NS" -l app.kubernetes.io/name=documentdb-operator --no-headers | wc -l
echo "   Expected: 1"
echo ""

# 4. DocumentDB Instance
echo "4. DocumentDB Instance Status:"
kubectl get documentdb "$DOCDB_NAME" -n "$NS" -o jsonpath='{.status.status}'
echo ""
echo "   Expected: Cluster in healthy state"
echo ""

# 5. TLS Configuration
echo "5. TLS Status:"
kubectl get documentdb "$DOCDB_NAME" -n "$NS" -o jsonpath='{.status.tls.ready}'
echo ""
echo "   Expected: true"
echo ""

# 6. Certificates
echo "6. Certificates:"
kubectl get certificates -n "$NS" --no-headers | wc -l
echo "   Expected: At least 1"
echo ""

# 7. TLS Secrets
echo "7. TLS Secrets:"
kubectl get secrets -n "$NS" | grep -c "tls"
echo "   Expected: At least 1"
echo ""

# 8. Service External IP
echo "8. LoadBalancer Service:"
kubectl get svc -n "$NS" -o jsonpath='{.items[?(@.spec.type=="LoadBalancer")].status.loadBalancer.ingress[0].ip}'
echo ""
echo "   Expected: IP address"
echo ""

# 9. Pod Readiness
echo "9. All Pods Ready:"
TOTAL_PODS=$(kubectl get pods -n "$NS" --no-headers | wc -l)
READY_PODS=$(kubectl get pods -n "$NS" --field-selector=status.phase=Running --no-headers | wc -l)
echo "   Ready: $READY_PODS / $TOTAL_PODS"
echo ""

# 10. Azure Key Vault
echo "10. Azure Key Vault:"
az keyvault show --name "$KV_NAME" --query "properties.provisioningState" -o tsv 2>/dev/null || echo "Not found or not accessible"
echo "    Expected: Succeeded"
echo ""

echo "=== Validation Complete ==="
```

### Manual Verification Steps

1. **Check DocumentDB CRD**:
   ```bash
   kubectl get crd dbs.documentdb.io -o yaml | grep -A 5 "tls"
   ```

2. **Inspect TLS Configuration**:
   ```bash
   kubectl get documentdb "$DOCDB_NAME" -n "$NS" -o yaml | grep -A 20 "tls:"
   ```

3. **Verify Certificate Details**:
   ```bash
   kubectl get certificate -n "$NS" -o yaml
   kubectl describe certificate -n "$NS"
   ```

4. **Check TLS Secret Contents**:
   ```bash
   # List keys in TLS secret
   kubectl get secret <tls-secret-name> -n "$NS" -o jsonpath='{.data}' | jq 'keys'
   
   # View certificate
   kubectl get secret <tls-secret-name> -n "$NS" -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout
   ```

5. **Test TLS Handshake**:
   ```bash
   EXTERNAL_IP=$(kubectl get svc -n "$NS" -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}')
   openssl s_client -connect "$EXTERNAL_IP:10260" -servername "$DOCDB_NAME"
   ```

## Cleanup

### Quick Cleanup (All Resources)

```bash
cd documentdb-playground/tls/scripts

./delete-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --all
```

### Selective Cleanup

**Keep cluster, delete DocumentDB only:**
```bash
./delete-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --keep-cluster
```

**Delete cluster, preserve Key Vault:**
```bash
./delete-cluster.sh \
  --suffix "$SUFFIX" \
  --subscription-id "$SUBSCRIPTION_ID" \
  --keep-keyvault
```

### Manual Cleanup

If scripts fail, manually delete resources:

```bash
# Delete DocumentDB
kubectl delete documentdb "$DOCDB_NAME" -n "$NS"

# Delete namespace
kubectl delete namespace "$NS"

# Uninstall operator
helm uninstall documentdb-operator -n "$NS"

# Uninstall cert-manager
helm uninstall cert-manager -n cert-manager
kubectl delete namespace cert-manager

# Delete AKS cluster
az aks delete --resource-group "$RG" --name "$AKS_NAME" --yes --no-wait

# Delete resource group
az group delete --name "$RG" --yes --no-wait

# Delete Key Vault (if needed)
az keyvault delete --name "$KV_NAME"
az keyvault purge --name "$KV_NAME"
```

## Troubleshooting

### Common Issues and Solutions

#### Issue: AKS Cluster Creation Fails

**Symptoms:**
- `az aks create` command fails
- Error about quota limits

**Solutions:**
```bash
# Check quota
az vm list-usage --location "$LOCATION" --output table

# Try different region
export LOCATION="westus2"

# Try smaller node size
# Edit script to use Standard_D2s_v5 instead of Standard_D4s_v5
```

#### Issue: cert-manager Not Ready

**Symptoms:**
- Certificate stuck in "Pending" state
- cert-manager pods not running

**Solutions:**
```bash
# Check cert-manager pods
kubectl get pods -n cert-manager

# Check cert-manager logs
kubectl logs -n cert-manager deployment/cert-manager

# Reinstall cert-manager
helm uninstall cert-manager -n cert-manager
kubectl delete namespace cert-manager
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set installCRDs=true
```

#### Issue: TLS Certificate Not Ready

**Symptoms:**
- `status.tls.ready` is `false`
- Certificate resource shows errors

**Solutions:**
```bash
# Check certificate status
kubectl describe certificate -n "$NS"

# Check cert-manager issuer
kubectl describe clusterissuer

# Check cert-manager controller logs
kubectl logs -n cert-manager deployment/cert-manager | grep -i error

# Delete and recreate certificate
kubectl delete certificate <cert-name> -n "$NS"
# Certificate should be recreated automatically by operator
```

#### Issue: Cannot Connect with mongosh

**Symptoms:**
- Connection timeout
- TLS handshake failure

**Solutions:**
```bash
# Verify service has external IP
kubectl get svc -n "$NS"

# Check pod logs
kubectl logs -n "$NS" <pod-name> -c gateway-sidecar

# Test without TLS first
mongosh "mongodb://${USERNAME}:${PASSWORD}@${EXTERNAL_IP}:10260/?tls=false"

# For self-signed certificates, allow invalid certificates
mongosh "mongodb://${USERNAME}:${PASSWORD}@${EXTERNAL_IP}:10260/?tls=true&tlsAllowInvalidCertificates=true"

# Check TLS handshake
openssl s_client -connect "${EXTERNAL_IP}:10260"
```

#### Issue: Azure Key Vault Access Denied

**Symptoms:**
- CSI driver cannot sync secrets
- "Access denied" errors in pod logs

**Solutions:**
```bash
# Check managed identity binding
kubectl get azureidentitybinding -n "$NS"

# Verify Key Vault access policies
az keyvault show --name "$KV_NAME" --query "properties.accessPolicies"

# Check CSI driver logs
kubectl logs -n kube-system -l app=secrets-store-csi-driver | grep -i error

# Verify pod identity
kubectl describe pod <pod-name> -n "$NS" | grep -i identity
```

### Debug Mode

Run scripts with debug output:

```bash
# Enable bash debug mode
bash -x ./create-cluster.sh --suffix test --subscription-id <id>

# Or set in script
set -x  # Enable debug
set +x  # Disable debug
```

### Getting Help

If you encounter issues not covered here:

1. **Check logs**:
   ```bash
   kubectl logs -n "$NS" <pod-name> --all-containers
   ```

2. **Describe resources**:
   ```bash
   kubectl describe documentdb "$DOCDB_NAME" -n "$NS"
   kubectl describe certificate -n "$NS"
   ```

3. **Export configuration**:
   ```bash
   kubectl get documentdb "$DOCDB_NAME" -n "$NS" -o yaml > documentdb-config.yaml
   ```

4. **Create GitHub issue**:
   - Repository: https://github.com/microsoft/documentdb-kubernetes-operator
   - Include: logs, configurations, error messages, steps to reproduce

## E2E Test Matrix

Test different combinations to ensure compatibility:

| Test Case | AKS Version | TLS Mode | Storage | Expected Result |
|-----------|-------------|----------|---------|-----------------|
| Basic     | 1.28+       | SelfSigned | Default | ✅ Pass |
| Provided  | 1.28+       | Provided (AKV) | Default | ✅ Pass |
| CertManager | 1.28+     | CertManager | Default | ✅ Pass |
| Custom Storage | 1.28+  | SelfSigned | Premium | ✅ Pass |
| Multi-Instance | 1.28+  | SelfSigned | Default | ✅ Pass |

## Continuous Testing

For CI/CD integration, use the automated script:

```bash
#!/bin/bash
# ci-e2e-test.sh

set -e

SUFFIX="ci-$(date +%Y%m%d%H%M)"
SUBSCRIPTION_ID="$AZURE_SUBSCRIPTION_ID"

# Create and test
./create-cluster.sh --suffix "$SUFFIX" --subscription-id "$SUBSCRIPTION_ID"

# Run validation
# (Add validation commands here)

# Cleanup
./delete-cluster.sh --suffix "$SUFFIX" --subscription-id "$SUBSCRIPTION_ID" --all
```

## Additional Resources

- [TLS Setup Guide](README.md) - Main TLS configuration documentation
- [Manual Provided Mode Setup](MANUAL-PROVIDED-MODE-SETUP.md) - Detailed step-by-step guide for Provided TLS with Azure Key Vault
- [Advanced Configuration](../../docs/operator-public-documentation/v1/advanced-configuration/README.md) - Production configurations
- [Project Documentation](../../docs/operator-public-documentation/index.md) - Full operator documentation
- [GitHub Repository](https://github.com/microsoft/documentdb-kubernetes-operator) - Source code and issues

---

**Last Updated**: November 2025  
**Tested On**: AKS 1.28+, cert-manager 1.13+, DocumentDB Operator 0.1.1+
