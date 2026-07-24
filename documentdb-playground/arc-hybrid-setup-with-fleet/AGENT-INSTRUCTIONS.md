# Copilot Agent Instructions: DocumentDB Fleet Hybrid Setup

## Overview

This guide helps AI agents assist users in setting up DocumentDB on a hybrid environment using Azure Fleet Manager. The setup creates visibility in Azure Portal for both cloud (AKS) and on-premises (Arc-enabled) clusters.

**End Result:**
- Fleet Hub with 2 member clusters
- AKS cluster with DocumentDB operator + instance
- Kind cluster (simulating on-prem) with Arc agent, DocumentDB operator + instance
- Full visibility in Azure Portal

**Estimated Time:** 30-45 minutes

## Agent Behavior

**CRITICAL:** This setup requires commands in **two different environments**:

| Environment | Used For | Commands |
|-------------|----------|----------|
| **WSL/Linux** | Kind, Docker, kubectl, Helm | `kind`, `docker`, `kubectl`, `helm` |
| **PowerShell (Windows)** | Azure CLI | `az` |

**Always tell the user which environment to run each command in.**

> **Why dual environments?** In corporate environments with Conditional Access Policies, `az login` fails in WSL. PowerShell bypasses this limitation.

---

## Environment Setup

### WSL Terminal
```bash
# Verify tools
kubectl version --client
helm version --short
kind --version
docker ps
```

### PowerShell Terminal
```powershell
# Verify and install extensions
az --version  # Must be 2.50.0+
az extension add --name fleet --upgrade --yes
az extension add --name connectedk8s --upgrade --yes
az extension add --name k8s-extension --upgrade --yes
```

**KUBECONFIG Setup (PowerShell):**
```powershell
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\.kube\config"
```

---

## Execution Instructions

### Phase 1: Create Fleet Hub + AKS (PowerShell)

**Tell user:** "Run these commands in PowerShell"

```powershell
# Variables - user can customize these
$RESOURCE_GROUP = "documentdb-fleet-rg"
$LOCATION = "westus2"
$FLEET_NAME = "documentdb-fleet"
$AKS_CLUSTER = "documentdb-aks"

# Create resource group
az group create --name $RESOURCE_GROUP --location $LOCATION

# Create Fleet hub (hubless mode)
az fleet create --resource-group $RESOURCE_GROUP --name $FLEET_NAME --location $LOCATION

# Create AKS cluster (~5-10 minutes)
az aks create `
  --resource-group $RESOURCE_GROUP `
  --name $AKS_CLUSTER `
  --node-count 2 `
  --node-vm-size Standard_D4s_v3 `
  --enable-managed-identity `
  --generate-ssh-keys

# Join AKS to Fleet
$AKS_ID = az aks show -g $RESOURCE_GROUP -n $AKS_CLUSTER --query id -o tsv
az fleet member create `
  --resource-group $RESOURCE_GROUP `
  --fleet-name $FLEET_NAME `
  --name $AKS_CLUSTER `
  --member-cluster-id $AKS_ID

# Get AKS credentials to WSL kubeconfig
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\.kube\config"
az aks get-credentials --resource-group $RESOURCE_GROUP --name $AKS_CLUSTER --overwrite-existing
```

**Success check:** `az fleet member list` shows `documentdb-aks`

### Phase 2: Install cert-manager on AKS (WSL)

**Tell user:** "Run these commands in WSL"

```bash
kubectl config use-context documentdb-aks
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
```

**Success check:** All cert-manager pods Running

### Phase 3: Create Kind Cluster (WSL)

**Tell user:** "Run these commands in WSL"

```bash
kind create cluster --name documentdb-onprem --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
EOF

kubectl config use-context kind-documentdb-onprem
kubectl get nodes
```

**Success check:** 3 nodes in Ready state

### Phase 4: Arc-Enable Kind Cluster (PowerShell)

**Tell user:** "Run these commands in PowerShell"

**IMPORTANT:** Verify kubectl context first!

```powershell
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\.kube\config"
kubectl config use-context kind-documentdb-onprem
kubectl get nodes  # MUST show "documentdb-onprem-*" nodes, NOT AKS nodes
```

Then Arc-enable:

```powershell
az connectedk8s connect `
  --name documentdb-onprem `
  --resource-group $RESOURCE_GROUP `
  --location $LOCATION `
  --tags "environment=onprem" "purpose=documentdb"

# Verify connection
az connectedk8s show --name documentdb-onprem --resource-group $RESOURCE_GROUP `
  --query "{name:name, connectivityStatus:connectivityStatus}" -o table

# Join to Fleet
$ARC_ID = az connectedk8s show -g $RESOURCE_GROUP -n documentdb-onprem --query id -o tsv
az fleet member create `
  --resource-group $RESOURCE_GROUP `
  --fleet-name $FLEET_NAME `
  --name documentdb-onprem `
  --member-cluster-id $ARC_ID
```

**Success check:** `connectivityStatus` = `Connected`, Fleet has 2 members

### Phase 5: Install cert-manager on Kind (WSL)

**Tell user:** "Run these commands in WSL"

```bash
kubectl config use-context kind-documentdb-onprem
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
```

### Phase 6: Deploy DocumentDB Operator (WSL)

**Tell user:** "Run these commands in WSL from the repo root"

```bash
cd operator/documentdb-helm-chart

# Kind cluster
kubectl config use-context kind-documentdb-onprem
helm install documentdb-operator . --namespace documentdb-operator --create-namespace --wait --timeout 10m
kubectl get pods -n documentdb-operator  # Verify Running

# AKS cluster
kubectl config use-context documentdb-aks
helm install documentdb-operator . --namespace documentdb-operator --create-namespace --wait --timeout 10m
kubectl get pods -n documentdb-operator  # Verify Running
```

**Success check:** `documentdb-operator-*` and `cnpg-*` pods Running on both clusters

### Phase 7: Create Service Account Token for Arc Portal (WSL)

**Tell user:** "Run these commands in WSL"

```bash
kubectl config use-context kind-documentdb-onprem

kubectl create serviceaccount arc-portal-viewer -n default
kubectl create clusterrolebinding arc-portal-viewer-binding \
  --clusterrole=cluster-admin \
  --serviceaccount=default:arc-portal-viewer

# Generate 1-year token
kubectl create token arc-portal-viewer -n default --duration=8760h
```

**Tell user:** "Save this token - you'll need it for Azure Portal"

### Phase 8: Deploy DocumentDB Instance (WSL)

**Tell user:** "Run these commands in WSL"

```bash
# Kind cluster
kubectl config use-context kind-documentdb-onprem
kubectl create namespace app-namespace
kubectl create secret generic documentdb-credentials \
  --namespace app-namespace \
  --from-literal=username=docdbuser \
  --from-literal=password=YourSecurePassword123!

kubectl apply -f documentdb-instance.yaml
kubectl get pods -n app-namespace -w  # Wait for 2/2 Running
kubectl get documentdb -n app-namespace  # Should show "Cluster in healthy state"

# AKS cluster
kubectl config use-context documentdb-aks
kubectl create namespace app-namespace
kubectl create secret generic documentdb-credentials \
  --namespace app-namespace \
  --from-literal=username=docdbuser \
  --from-literal=password=YourSecurePassword123!

kubectl apply -f documentdb-instance.yaml
kubectl get pods -n app-namespace -w
kubectl get documentdb -n app-namespace
```

**Success check:** `demo-documentdb-1` pod 2/2 Running, DocumentDB status "Cluster in healthy state"

### Phase 9: Verify in Azure Portal

**Tell user:** "Verify in Azure Portal"

1. **Fleet:** https://portal.azure.com/#view/Microsoft_Azure_Fleet/FleetMenuBlade
   - Should show 2 members: `documentdb-aks`, `documentdb-onprem`

2. **AKS:** Azure Portal → AKS → `documentdb-aks` → Workloads → Pods
   - Select namespace `app-namespace`
   - Should see `demo-documentdb-1`

3. **Arc:** Azure Portal → Arc → Kubernetes → `documentdb-onprem` → Kubernetes resources
   - Click "Sign in with service account token"
   - Paste token from Phase 7
   - Select namespace `app-namespace`
   - Should see `demo-documentdb-1`

---

## Error Handling

| Error | Cause | Solution |
|-------|-------|----------|
| `az login` fails in WSL | Conditional Access Policy | Use PowerShell for `az` commands |
| Kind context not in PowerShell | kubeconfig not shared | Set `$env:KUBECONFIG` to WSL path |
| Arc CRD conflicts | Wrong kubectl context | Verify context with `kubectl get nodes` before Arc connect |
| Arc connect timeout | Network issues | Retry, check firewall allows outbound to Azure |
| Arc portal "token required" | No service account | Run Phase 7 |
| Pods not visible in Portal | Wrong namespace | Select `app-namespace` from dropdown |

---

## Cleanup

**Tell user:** "Run cleanup when done to avoid charges"

**In WSL:**
```bash
kind delete cluster --name documentdb-onprem
```

**In PowerShell:**
```powershell
az group delete --name documentdb-fleet-rg --yes --no-wait
```

---

## Reference

- Full documentation: See `README.md` in this directory
- DocumentDB instance spec: See `documentdb-instance.yaml`
