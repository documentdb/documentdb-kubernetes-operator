# Provided TLS Mode: Manual Step-by-Step Guide

This guide shows how to manually configure and validate Provided TLS mode end-to-end using Azure Key Vault and the Secrets Store CSI Driver with managed identity.

> **Note**: For automated setup, see [E2E-TESTING.md](E2E-TESTING.md). This guide is for users who want to understand each step in detail or troubleshoot issues.

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Step 0: Azure and AKS Setup](#step-0-azure-and-aks-setup)
- [Step 1: Deploy DocumentDB with LoadBalancer](#step-1-deploy-documentdb-with-loadbalancer)
- [Step 2: Prepare Azure Key Vault](#step-2-prepare-azure-key-vault)
- [Step 3: Create Server Certificate](#step-3-create-server-certificate)
- [Step 4: Configure SecretProviderClass](#step-4-configure-secretproviderclass)
- [Step 5: Switch to Provided TLS Mode](#step-5-switch-to-provided-tls-mode)
- [Step 6: Validate Connectivity](#step-6-validate-connectivity)
- [Troubleshooting](#troubleshooting)
- [Cleanup](#cleanup)

## Overview

### What You'll Do

1. Create or reuse a DocumentDB cluster exposed via LoadBalancer
2. Mint a server certificate in Azure Key Vault for the service's SNI host (`<LB-IP>.sslip.io`)
3. Use a SecretProviderClass to sync the AKV certificate into a Kubernetes TLS secret
4. Switch DocumentDB to `spec.tls.gateway.mode: Provided` and point it at that secret
5. Connect with mongosh using the provided certificate

### Architecture

```
Azure Key Vault (Certificate)
      ↓
SecretProviderClass (CSI Driver)
      ↓
Kubernetes TLS Secret
      ↓
DocumentDB Gateway (TLS Enabled)
      ↓
mongosh Client (TLS Connection)
```

## Prerequisites

### Required Permissions

- **Azure subscription** with Owner or Contributor permissions
- Ability to create resource groups, AKS clusters, and Key Vaults
- RBAC permissions to assign roles

### Required Tools

Ensure these tools are installed on your machine:

```bash
# Check versions
az --version          # Azure CLI
kubectl version       # Kubernetes CLI
helm version          # Helm 3.x
mongosh --version     # MongoDB Shell
openssl version       # OpenSSL
```

**Installation guides:**
- [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [mongosh](https://www.mongodb.com/docs/mongodb-shell/install/)

### Login to Azure

```bash
# Login
az login

# List subscriptions
az account list --output table

# Set subscription
az account set --subscription <subscription-id>
```

## Environment Variables

Set these variables for your environment:

```bash
export SUFFIX="$(date +%m%d%H)"
export SUBSCRIPTION_ID="<your-azure-subscription-id>"
export LOCATION="eastus2"
export RG="documentdb-aks-${SUFFIX}-rg"
export AKS_NAME="documentdb-aks-${SUFFIX}"
export KV_NAME="${USER}-AKV-${SUFFIX}"
export NS="documentdb-preview-ns"
export DOCDB_NAME="documentdb-preview"
export CERT_NAME="documentdb-gateway"
export SECRET_NAME="documentdb-provided-tls"

# Display configuration
echo "Configuration:"
echo "  Suffix: $SUFFIX"
echo "  Subscription: $SUBSCRIPTION_ID"
echo "  Location: $LOCATION"
echo "  Resource Group: $RG"
echo "  AKS Cluster: $AKS_NAME"
echo "  Key Vault: $KV_NAME"
echo "  Namespace: $NS"
echo "  DocumentDB: $DOCDB_NAME"
echo "  Certificate: $CERT_NAME"
echo "  Secret: $SECRET_NAME"
```

## Step 0: Azure and AKS Setup

### 0.1 Create Resource Group

```bash
az group create -n "$RG" -l "$LOCATION"
```

### 0.2 Create AKS Cluster

Create AKS with managed identity:

```bash
az aks create \
  -g "$RG" \
  -n "$AKS_NAME" \
  -l "$LOCATION" \
  --enable-managed-identity \
  --node-count 3 \
  -s Standard_d8s_v5 \
  --generate-ssh-keys
```

**Expected duration**: ~10-15 minutes

### 0.3 Get Cluster Credentials

```bash
az aks get-credentials -g "$RG" -n "$AKS_NAME" --overwrite-existing
```

### 0.4 Verify Cluster Connectivity

```bash
kubectl config current-context
kubectl cluster-info
kubectl get nodes -o wide
```

**Expected output**: All nodes should be in `Ready` state.

### 0.5 Install cert-manager

Install cert-manager with CRDs:

```bash
# Add Helm repository
helm repo add jetstack https://charts.jetstack.io
helm repo update

# Create namespace
kubectl create namespace cert-manager --dry-run=client -o yaml | kubectl apply -f -

# Install cert-manager
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --set installCRDs=true

# Verify installation
kubectl -n cert-manager get pods
```

**Expected output**: 3 pods running (cert-manager, cainjector, webhook)

**Troubleshooting**: If you see "Kubernetes cluster unreachable" errors:
```bash
az account set --subscription "$SUBSCRIPTION_ID"
az aks get-credentials -g "$RG" -n "$AKS_NAME" --overwrite-existing
kubectl cluster-info && kubectl get nodes
```

### 0.6 Install Secrets Store CSI Driver with Azure Provider

> **Important**: Do not mix with the AKS managed add-on. If the add-on is enabled, disable it first or skip this Helm installation.

Check for existing add-on:
```bash
kubectl -n kube-system get ds | grep -E 'aks-secrets-store-provider-azure' && \
  echo "⚠️  AKS add-on detected; disable it or skip Helm" || \
  echo "✅ No add-on detected; proceed with Helm"
```

Install the CSI driver with Azure provider:
```bash
# Add Helm repository
helm repo add csi-azure https://azure.github.io/secrets-store-csi-driver-provider-azure/charts
helm repo update

# Install with secret sync enabled
helm upgrade --install csi-azure-provider \
  csi-azure/csi-secrets-store-provider-azure \
  -n kube-system \
  --set "secrets-store-csi-driver.syncSecret.enabled=true"

# Verify installation
kubectl -n kube-system get pods -l app=secrets-store-csi-driver
kubectl -n kube-system get pods -l app=csi-secrets-store-provider-azure

# Wait for pods to be ready
kubectl -n kube-system wait --for=condition=Ready \
  pod -l app=secrets-store-csi-driver --timeout=120s
kubectl -n kube-system wait --for=condition=Ready \
  pod -l app=csi-secrets-store-provider-azure --timeout=120s

# Verify DaemonSets
kubectl -n kube-system get ds -l app=secrets-store-csi-driver -o wide
kubectl -n kube-system get ds -l app=csi-secrets-store-provider-azure -o wide
```

**Important**: The SecretProviderClass `spec.provider` must be `azure` (lowercase), and any pod mounting it must set `volumeAttributes.secretProviderClass` to match the SecretProviderClass name.

### 0.7 Deploy DocumentDB Operator

> **Note**: This guide assumes you're using the DocumentDB operator Helm chart from the repository.

```bash
# Create namespace
kubectl create namespace documentdb-operator --dry-run=client -o yaml | kubectl apply -f -

# Install operator (adjust image references as needed)
cd /path/to/operator/documentdb-helm-chart
helm upgrade --install documentdb-operator . \
  -n documentdb-operator

# Verify installation
kubectl -n documentdb-operator get pods
```

## Step 1: Deploy DocumentDB with LoadBalancer

### 1.1 Create Namespace and Credentials

```bash
# Create namespace
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

# Create credentials secret
kubectl -n "$NS" create secret generic documentdb-credentials \
  --from-literal=username="docdbuser" \
  --from-literal=password="P@ssw0rd123" \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 1.2 Deploy DocumentDB with SelfSigned TLS (Temporary)

We'll start with SelfSigned mode to get a LoadBalancer IP, then switch to Provided mode.

Create the DocumentDB manifest:
```bash
cat > /tmp/documentdb-selfsigned.yaml <<'EOF'
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  version: "16"
  instances: 1
  storage:
    size: 10Gi
  exposeViaService:
    serviceType: LoadBalancer
  tls:
    mode: SelfSigned
EOF

kubectl apply -f /tmp/documentdb-selfsigned.yaml
```

**Explanation**:
- `version: "16"`: PostgreSQL version (DocumentDB is built on PostgreSQL)
- `instances: 1`: Single instance for testing
- `exposeViaService.serviceType: LoadBalancer`: Exposes service externally
- `tls.mode: SelfSigned`: Temporary mode, will switch to Provided later

### 1.3 Wait for Service and Capture IP

Monitor service creation:
```bash
kubectl -n "$NS" get svc -w
```

Press `Ctrl+C` when the `EXTERNAL-IP` appears, then capture it:
```bash
export SVC_IP=$(kubectl -n "$NS" get svc documentdb-service-"$DOCDB_NAME" \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
export SNI_HOST="${SVC_IP}.sslip.io"

echo "Service IP: $SVC_IP"
echo "SNI Host: $SNI_HOST"
```

**What is sslip.io?** It's a DNS service that maps `<IP>.sslip.io` to `<IP>`, providing a hostname for the IP address. This is necessary because TLS certificates require a hostname, not just an IP.

### 1.4 Verify DocumentDB Status

```bash
kubectl -n "$NS" get documentdb "$DOCDB_NAME"
kubectl -n "$NS" get documentdb "$DOCDB_NAME" -o jsonpath='{.status.tls}' | jq
```

**Expected output**:
- Status should show "Cluster in healthy state"
- TLS status should show `ready: true` and `mode: SelfSigned`

## Step 2: Prepare Azure Key Vault

### 2.1 Create Key Vault

Create a Key Vault with RBAC authorization:

```bash
az keyvault create \
  -g "$RG" \
  -n "$KV_NAME" \
  -l "$LOCATION" \
  --enable-rbac-authorization true
```

### 2.2 Assign RBAC Roles

**For your user account** (to create/import certificates):
```bash
# Get your user object ID
USER_OBJECT_ID=$(az ad signed-in-user show --query id -o tsv)

# Assign Key Vault Certificates Officer role
az role assignment create \
  --role "Key Vault Certificates Officer" \
  --assignee "$USER_OBJECT_ID" \
  --scope "/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RG/providers/Microsoft.KeyVault/vaults/$KV_NAME"
```

**For AKS kubelet identity** (to read secrets):
```bash
# Get kubelet managed identity
KUBELET_OBJECT_ID=$(az aks show \
  -g "$RG" \
  -n "$AKS_NAME" \
  --query identityProfile.kubeletidentity.objectId \
  -o tsv)

# Assign Key Vault Secrets User role
az role assignment create \
  --role "Key Vault Secrets User" \
  --assignee "$KUBELET_OBJECT_ID" \
  --scope "/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RG/providers/Microsoft.KeyVault/vaults/$KV_NAME"
```

**Verify role assignments**:
```bash
az role assignment list --scope "/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RG/providers/Microsoft.KeyVault/vaults/$KV_NAME" --output table
```

## Step 3: Create Server Certificate

You have two options for creating a certificate:

### Option A: Import Existing PFX Certificate

If you already have a certificate for `$SNI_HOST`:

```bash
export PFX_PATH=/path/to/cert_${SNI_HOST}.pfx
export PFX_PASSWORD="<pfx-password>"

az keyvault certificate import \
  --vault-name "$KV_NAME" \
  -n "$CERT_NAME" \
  --file "$PFX_PATH" \
  --password "$PFX_PASSWORD"
```

**Important**: The certificate's Subject Alternative Name (SAN) must include `$SNI_HOST` (e.g., `20.1.2.3.sslip.io`). If it doesn't, strict hostname verification will fail.

### Option B: Create Self-Signed Certificate in Key Vault (Recommended for Testing)

Create a certificate policy with proper SAN configuration:

```bash
cat > /tmp/akv-cert-policy.json <<EOF
{
  "issuerParameters": { "name": "Self" },
  "x509CertificateProperties": {
    "subject": "CN=${SNI_HOST}",
    "subjectAlternativeNames": { 
      "dnsNames": [ "${SNI_HOST}" ] 
    },
    "keyUsage": [ "digitalSignature", "keyEncipherment" ],
    "validityInMonths": 12
  },
  "keyProperties": {
    "exportable": true,
    "keyType": "RSA",
    "keySize": 2048,
    "reuseKey": false
  },
  "secretProperties": { 
    "contentType": "application/x-pem-file" 
  }
}
EOF
```

Create the certificate:
```bash
az keyvault certificate create \
  --vault-name "$KV_NAME" \
  -n "$CERT_NAME" \
  --policy @/tmp/akv-cert-policy.json
```

**Wait for certificate creation**:
```bash
az keyvault certificate show \
  --vault-name "$KV_NAME" \
  -n "$CERT_NAME" \
  --query "attributes.enabled" \
  -o tsv
```

**Why SAN is important**: The Subject Alternative Name (SAN) field must match the hostname (`$SNI_HOST`) used in the connection string. This enables strict TLS validation without `tlsAllowInvalidHostnames=true`.

**For production**: Use a custom domain or a pre-allocated Azure Public IP with a DNS label (e.g., `<label>.<region>.cloudapp.azure.com`) and create a certificate for that stable name.

### Verify Certificate

```bash
az keyvault certificate show \
  --vault-name "$KV_NAME" \
  -n "$CERT_NAME" \
  --query "{enabled: attributes.enabled, created: attributes.created, expires: attributes.expires}" \
  -o table
```

## Step 4: Configure SecretProviderClass

### 4.1 Create SecretProviderClass

The SecretProviderClass tells the CSI driver how to sync the certificate from Azure Key Vault to a Kubernetes secret.

```bash
cat > /tmp/azure-secret-provider-class.yaml <<EOF
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: documentdb-azure-tls
  namespace: ${NS}
spec:
  provider: azure
  secretObjects:
  - secretName: ${SECRET_NAME}
    type: kubernetes.io/tls
    data:
    - objectName: "tls.crt"
      key: tls.crt
    - objectName: "tls.key"
      key: tls.key
  parameters:
    usePodIdentity: "false"
    useVMManagedIdentity: "true"
    keyvaultName: "${KV_NAME}"
    tenantId: "$(az account show --query tenantId -o tsv)"
    cloudName: "AzurePublicCloud"
    syncSecret: "true"
    objects: |
      array:
        - |
          objectName: "${CERT_NAME}"
          objectType: "secret"
          objectAlias: "tls.crt"
          objectVersion: ""
        - |
          objectName: "${CERT_NAME}"
          objectType: "secret"
          objectAlias: "tls.key"
          objectVersion: ""
EOF

kubectl apply -f /tmp/azure-secret-provider-class.yaml
```

**Key configuration points**:
- `provider: azure`: Must be lowercase "azure"
- `useVMManagedIdentity: "true"`: Use kubelet managed identity
- `syncSecret: "true"`: Enable secret sync to Kubernetes
- `objectAlias`: Maps the Key Vault object to the required filenames (`tls.crt` and `tls.key`)
- `objectType: "secret"`: Certificates are stored as secrets in Key Vault

### 4.2 Handle Multiple Managed Identities (If Needed)

If your nodes have multiple user-assigned identities, you may see IMDS errors. Set the kubelet identity explicitly:

```bash
KUBELET_CLIENT_ID=$(az aks show \
  -g "$RG" \
  -n "$AKS_NAME" \
  --query identityProfile.kubeletidentity.clientId \
  -o tsv | tr -d '\r')

kubectl -n "$NS" patch secretproviderclass documentdb-azure-tls \
  --type merge \
  -p "{\"spec\":{\"parameters\":{\"userAssignedIdentityID\":\"$KUBELET_CLIENT_ID\"}}}"
```

### 4.3 Create Certificate Puller Pod

The CSI driver only syncs secrets when a pod mounts the SecretProviderClass. Create a simple "puller" pod:

```bash
cat > /tmp/busybox-cert-puller.yaml <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cert-puller
  namespace: ${NS}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cert-puller
  template:
    metadata:
      labels:
        app: cert-puller
    spec:
      containers:
      - name: bb
        image: busybox
        command: ["sh", "-c", "sleep 3600"]
        volumeMounts:
        - name: secrets-store-inline
          mountPath: "/mnt/secrets-store"
          readOnly: true
      volumes:
      - name: secrets-store-inline
        csi:
          driver: secrets-store.csi.k8s.io
          readOnly: true
          volumeAttributes:
            secretProviderClass: "documentdb-azure-tls"
EOF

kubectl apply -f /tmp/busybox-cert-puller.yaml
```

**Wait for the pod to start**:
```bash
kubectl -n "$NS" get pods -l app=cert-puller -w
```

### 4.4 Verify Secret Sync

Check that the secret was created:
```bash
kubectl -n "$NS" get secret "$SECRET_NAME"
```

Verify secret type and keys:
```bash
# Check type (should be kubernetes.io/tls)
kubectl -n "$NS" get secret "$SECRET_NAME" -o jsonpath='{.type}{"\n"}'

# Verify tls.crt exists (show first 20 chars of base64)
kubectl -n "$NS" get secret "$SECRET_NAME" \
  -o jsonpath='{.data.tls\.crt}{"\n"}' | head -c 20; echo

# Verify tls.key exists (show first 20 chars of base64)
kubectl -n "$NS" get secret "$SECRET_NAME" \
  -o jsonpath='{.data.tls\.key}{"\n"}' | head -c 20; echo
```

**Expected output**:
- Type: `kubernetes.io/tls`
- Both `tls.crt` and `tls.key` should have base64-encoded data

**Decode and inspect certificate**:
```bash
kubectl -n "$NS" get secret "$SECRET_NAME" \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout | head -30
```

Look for:
- Subject: `CN = <SNI_HOST>`
- Subject Alternative Name: `DNS:<SNI_HOST>`
- Validity dates

## Step 5: Switch to Provided TLS Mode

### 5.1 Patch DocumentDB Resource

Update the DocumentDB CR to use Provided TLS mode:

```bash
kubectl -n "$NS" patch documentdb "$DOCDB_NAME" --type merge -p "$(cat <<JSON
{
  "spec": {
    "tls": {
      "mode": "Provided",
      "provided": {
        "secretName": "$SECRET_NAME"
      }
    }
  }
}
JSON
)"
```

### 5.2 Verify Configuration

Check the DocumentDB resource:
```bash
kubectl -n "$NS" get documentdb "$DOCDB_NAME" -o yaml | grep -A 10 "tls:"
```

**Expected output**:
```yaml
tls:
  mode: Provided
  provided:
    secretName: documentdb-provided-tls
```

### 5.3 Verify Status

Check that TLS is ready:
```bash
kubectl -n "$NS" get documentdb "$DOCDB_NAME" -o jsonpath='{.status.tls}' | jq
```

**Expected output**:
```json
{
  "ready": true,
  "mode": "Provided",
  "secretName": "documentdb-provided-tls"
}
```

**Monitor the operator**:
```bash
kubectl -n documentdb-operator logs -l app.kubernetes.io/name=documentdb-operator --tail=50 -f
```

Look for messages about TLS configuration updates.

### 5.4 Verify Gateway Pod

The gateway container should be restarted with the new TLS configuration:

```bash
# List pods
kubectl -n "$NS" get pods

# Check gateway container in CNPG cluster pod
kubectl -n "$NS" describe pod <cluster-pod-name> | grep -A 20 "gateway"
```

## Step 6: Validate Connectivity

### 6.1 Get Credentials

```bash
export DOCDB_USER=$(kubectl -n "$NS" get secret documentdb-credentials \
  -o jsonpath='{.data.username}' | base64 -d)
export DOCDB_PASS=$(kubectl -n "$NS" get secret documentdb-credentials \
  -o jsonpath='{.data.password}' | base64 -d)

echo "Username: $DOCDB_USER"
echo "Password: $DOCDB_PASS"
```

### 6.2 Build CA File from Server

For self-signed certificates or ad-hoc testing, extract the certificate chain:

```bash
openssl s_client -connect "$SNI_HOST:10260" -servername "$SNI_HOST" -showcerts </dev/null \
  2>/dev/null | awk '/BEGIN CERTIFICATE/,/END CERTIFICATE/ {print}' > /tmp/ca.crt

# Verify CA file
openssl x509 -in /tmp/ca.crt -text -noout | grep -E "Subject:|Issuer:|DNS:"
```

### 6.3 Test Connection - Strict TLS

With SAN properly configured, strict TLS should work:

```bash
mongosh "mongodb://$SNI_HOST:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0" \
  --tlsCAFile /tmp/ca.crt \
  -u "$DOCDB_USER" \
  -p "$DOCDB_PASS" \
  --eval 'db.runCommand({ ping: 1 })'
```

**Expected output**: `{ ok: 1 }`

### 6.4 Test Connection - Relaxed Hostname Verification

If SAN doesn't match, use relaxed verification temporarily:

```bash
mongosh "mongodb://$SNI_HOST:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0&tlsAllowInvalidHostnames=true" \
  --tlsCAFile /tmp/ca.crt \
  -u "$DOCDB_USER" \
  -p "$DOCDB_PASS" \
  --eval 'db.runCommand({ ping: 1 })'
```

### 6.5 Perform CRUD Operations

Once connected, test database operations:

```javascript
// Connect interactively
mongosh "mongodb://$SNI_HOST:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0" \
  --tlsCAFile /tmp/ca.crt \
  -u "$DOCDB_USER" \
  -p "$DOCDB_PASS"

// Inside mongosh:
use testdb

// Insert
db.products.insertMany([
  { name: "Laptop", price: 999.99, category: "Electronics" },
  { name: "Mouse", price: 29.99, category: "Electronics" },
  { name: "Desk", price: 299.99, category: "Furniture" }
])

// Read
db.products.find()
db.products.find({ category: "Electronics" })

// Update
db.products.updateOne(
  { name: "Laptop" },
  { $set: { price: 899.99 } }
)

// Verify update
db.products.find({ name: "Laptop" })

// Delete
db.products.deleteOne({ name: "Mouse" })

// Count
db.products.count()

// Cleanup
db.products.drop()
```

## Troubleshooting

### Secret Not Created

**Symptom**: Secret `$SECRET_NAME` doesn't exist after creating cert-puller pod.

**Diagnosis**:
```bash
# Check puller pod status
kubectl -n "$NS" get pods -l app=cert-puller
kubectl -n "$NS" describe pod -l app=cert-puller

# Check SecretProviderClass
kubectl -n "$NS" describe secretproviderclass documentdb-azure-tls

# Check CSI driver logs
kubectl -n kube-system logs -l app=csi-secrets-store-provider-azure --tail=50
```

**Solutions**:
1. Ensure puller pod is running
2. Verify `secretProviderClass` name matches exactly
3. Check pod events for mount errors
4. Verify RBAC permissions on Key Vault

### Provider Not Found Error

**Symptom**: Error message `provider not found: provider "azure"`

**Diagnosis**:
```bash
# Check Azure provider pods
kubectl -n kube-system get pods -l app=csi-secrets-store-provider-azure

# Check provider registration
kubectl -n kube-system logs -l app=csi-secrets-store-provider-azure | grep -i "provider registered"
```

**Solutions**:
1. Ensure Azure provider is installed and running
2. Verify `spec.provider` in SecretProviderClass is `azure` (lowercase, exact case)
3. Verify puller pod's `volumeAttributes.secretProviderClass` matches SPC name
4. Restart CSI driver pods if needed:
   ```bash
   kubectl -n kube-system rollout restart daemonset csi-secrets-store-provider-azure
   ```

### Key Vault Permission Denied

**Symptom**: Errors about "Access denied" or "unauthorized" in pod events or logs.

**Diagnosis**:
```bash
# Check kubelet identity
az aks show -g "$RG" -n "$AKS_NAME" \
  --query identityProfile.kubeletidentity

# Check role assignments
az role assignment list \
  --scope "/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RG/providers/Microsoft.KeyVault/vaults/$KV_NAME" \
  --output table
```

**Solutions**:
1. Verify kubelet managed identity has "Key Vault Secrets User" role
2. Verify your user account has "Key Vault Certificates Officer" role
3. Wait a few minutes for RBAC to propagate
4. Check if RBAC authorization is enabled on Key Vault:
   ```bash
   az keyvault show -n "$KV_NAME" --query "properties.enableRbacAuthorization"
   ```

### DocumentDB TLS Not Ready

**Symptom**: `status.tls.ready` is `false` or TLS status shows errors.

**Diagnosis**:
```bash
# Check DocumentDB status
kubectl -n "$NS" get documentdb "$DOCDB_NAME" -o yaml | grep -A 20 "status:"

# Check operator logs
kubectl -n documentdb-operator logs -l app.kubernetes.io/name=documentdb-operator --tail=100
```

**Solutions**:
1. Verify secret type is `kubernetes.io/tls`
2. Verify secret contains both `tls.crt` and `tls.key`
3. Check `status.tls.message` for specific error
4. Restart operator if needed:
   ```bash
   kubectl -n documentdb-operator rollout restart deployment documentdb-operator
   ```

### Certificate SAN Mismatch

**Symptom**: TLS handshake errors or "hostname doesn't match certificate" errors.

**Diagnosis**:
```bash
# Check certificate SAN
kubectl -n "$NS" get secret "$SECRET_NAME" \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | \
  openssl x509 -text -noout | grep -A 2 "Subject Alternative Name"

# Compare with SNI host
echo "Expected: DNS:$SNI_HOST"
```

**Solutions**:
1. Ensure certificate SAN includes exact `$SNI_HOST` value
2. Recreate certificate with correct SAN if mismatch
3. Temporarily use `&tlsAllowInvalidHostnames=true` in connection string
4. For production, use stable DNS name instead of sslip.io

### IMDS Multiple Identity Error

**Symptom**: Error "Multiple user assigned identities exist" in pod events.

**Solution**:
```bash
# Get kubelet client ID
KUBELET_CLIENT_ID=$(az aks show \
  -g "$RG" \
  -n "$AKS_NAME" \
  --query identityProfile.kubeletidentity.clientId \
  -o tsv | tr -d '\r')

# Patch SecretProviderClass
kubectl -n "$NS" patch secretproviderclass documentdb-azure-tls \
  --type merge \
  -p "{\"spec\":{\"parameters\":{\"userAssignedIdentityID\":\"$KUBELET_CLIENT_ID\"}}}"

# Restart puller pod
kubectl -n "$NS" rollout restart deploy cert-puller
```

### Connection Timeout

**Symptom**: mongosh connection times out.

**Diagnosis**:
```bash
# Check service
kubectl -n "$NS" get svc

# Check if IP is assigned
echo "Service IP: $SVC_IP"

# Test network connectivity
curl -k https://$SNI_HOST:10260 || echo "Connection failed"
```

**Solutions**:
1. Verify LoadBalancer service has external IP
2. Check firewall rules
3. Verify gateway pod is running
4. Check gateway logs:
   ```bash
   kubectl -n "$NS" logs <cluster-pod-name> -c gateway-sidecar
   ```

## Cleanup

### Remove Test Resources

```bash
# Delete cert-puller pod
kubectl -n "$NS" delete deploy cert-puller

# Delete synced secret
kubectl -n "$NS" delete secret "$SECRET_NAME"

# Delete SecretProviderClass
kubectl -n "$NS" delete secretproviderclass documentdb-azure-tls

# Delete DocumentDB instance
kubectl -n "$NS" delete documentdb "$DOCDB_NAME"

# Delete credentials
kubectl -n "$NS" delete secret documentdb-credentials
```

### Remove Infrastructure (Optional)

```bash
# Delete namespace
kubectl delete namespace "$NS"

# Delete Key Vault
az keyvault delete --name "$KV_NAME"
az keyvault purge --name "$KV_NAME"

# Delete AKS cluster
az aks delete --resource-group "$RG" --name "$AKS_NAME" --yes --no-wait

# Delete resource group (removes everything)
az group delete --name "$RG" --yes --no-wait
```

## Advanced Topics

### Certificate Rotation

To rotate certificates automatically:

1. **Update SecretProviderClass** with rotation interval:
   ```yaml
   spec:
     parameters:
       rotationPollInterval: "2h"  # Check every 2 hours
   ```

2. **Update certificate in Key Vault** (new version)

3. **CSI driver syncs automatically** based on poll interval

4. **Restart pods** that load the secret if hot reload is not supported:
   ```bash
   kubectl -n "$NS" rollout restart deployment cert-puller
   ```

### Using User-Assigned Managed Identity

If using a user-assigned managed identity instead of kubelet identity:

1. Create user-assigned identity:
   ```bash
   az identity create -g "$RG" -n my-identity
   ```

2. Assign Key Vault permissions to the identity

3. Update SecretProviderClass:
   ```yaml
   parameters:
     userAssignedIdentityID: "<client-id-of-identity>"
   ```

### Using CA-Backed Certificates

For production with a public or corporate CA:

1. **Create CSR in Key Vault**:
   ```bash
   az keyvault certificate create \
     --vault-name "$KV_NAME" \
     -n "$CERT_NAME" \
     --policy @policy.json
   
   az keyvault certificate pending show \
     --vault-name "$KV_NAME" \
     -n "$CERT_NAME" \
     --query csr -o tsv > cert.csr
   ```

2. **Submit CSR to CA** and get signed certificate

3. **Merge signed certificate**:
   ```bash
   az keyvault certificate pending merge \
     --vault-name "$KV_NAME" \
     -n "$CERT_NAME" \
     --file signed-cert.cer
   ```

### Stable DNS Names

For production, use stable DNS instead of sslip.io:

1. **Reserve Public IP**:
   ```bash
   az network public-ip create \
     -g "$RG" \
     -n my-stable-ip \
     --dns-name my-documentdb \
     --allocation-method Static \
     --sku Standard
   ```

2. **Get FQDN**:
   ```bash
   az network public-ip show \
     -g "$RG" \
     -n my-stable-ip \
     --query dnsSettings.fqdn -o tsv
   ```

3. **Create certificate for FQDN**

4. **Configure LoadBalancer** to use reserved IP

## Additional Resources

- [Main TLS Setup Guide](README.md) - Overview of all TLS modes
- [E2E Testing Guide](E2E-TESTING.md) - Automated testing with scripts
- [Advanced Configuration](../../docs/operator-public-documentation/v1/advanced-configuration/README.md) - Production configurations
- [Azure Key Vault Documentation](https://learn.microsoft.com/en-us/azure/key-vault/)
- [Secrets Store CSI Driver](https://secrets-store-csi-driver.sigs.k8s.io/)
- [Azure Provider](https://azure.github.io/secrets-store-csi-driver-provider-azure/)

---

**Last Updated**: November 2025  
**Tested On**: AKS 1.28+, Secrets Store CSI Driver 1.4+, Azure Key Vault RBAC mode
