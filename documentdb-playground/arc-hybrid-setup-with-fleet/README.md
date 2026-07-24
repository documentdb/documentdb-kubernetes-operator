# DocumentDB Hybrid Setup with Azure Fleet Manager

Deploy DocumentDB across AKS + Arc-enabled on-prem clusters using **Azure Fleet Manager**.

**Why Fleet?**
- No extension registration required - deploy vanilla DocumentDB via Helm
- Azure Portal visibility for all clusters (AKS native, Arc-enabled for on-prem)
- Centralized cluster management from Fleet
- Unified view of all DocumentDB deployments for billing/tracking

## What You'll Build

| Component | Description |
|-----------|-------------|
| **Fleet Hub** | Azure Fleet Manager for centralized cluster management |
| **AKS Cluster** | 2-node Azure-managed Kubernetes cluster |
| **Kind Cluster** | 3-node local cluster (simulates on-prem) |
| **Arc Connection** | Azure Arc agent on Kind for portal visibility |
| **DocumentDB** | Operator + instance deployed on both clusters |

**Estimated Time:** 30-45 minutes

**Cost:** ~$5-10/day for AKS cluster (delete when done)

## Important: Dual Environment Workflow

**This setup requires commands in TWO environments:**

| Environment | Used For | Tools |
|-------------|----------|-------|
| **WSL/Linux** | Kind cluster, kubectl, Helm | `kind`, `docker`, `kubectl`, `helm` |
| **PowerShell (Windows)** | Azure CLI commands | `az` |

> **Note:** In corporate environments with Conditional Access Policies, `az login` may fail in WSL. Use PowerShell for all Azure CLI commands.

## Prerequisites

### Required Versions

| Tool | Minimum Version | Check Command |
|------|-----------------|---------------|
| Azure CLI | 2.50.0+ | `az --version` |
| kubectl | 1.26+ | `kubectl version --client` |
| Helm | 3.12+ | `helm version --short` |
| Kind | 0.20+ | `kind --version` |
| Docker | 20.10+ | `docker --version` |

### Verify Prerequisites

**In WSL:**
```bash
kubectl version --client
helm version --short
kind --version
docker ps
```

**In PowerShell:**
```powershell
az --version
az extension add --name fleet --upgrade --yes
az extension add --name connectedk8s --upgrade --yes
az extension add --name k8s-extension --upgrade --yes
```

## Quick Start

### Option A: Step-by-Step (Recommended)

Follow the detailed [Step-by-Step Guide](#step-by-step-guide) below.

### Option B: Using Scripts (WSL-only - if `az login` works in WSL)

If Azure CLI works in your WSL environment without Conditional Access issues:

```bash
cd documentdb-playground/arc-hybrid-setup-with-fleet

# 1. Create Fleet hub + AKS cluster
./scripts/bash/setup-fleet-hub.sh

# 2. Create Kind cluster + Arc-enable + join Fleet
./scripts/bash/setup-arc-member.sh

# 3. Deploy DocumentDB to all clusters
./scripts/bash/deploy-documentdb-fleet.sh

# 4. Verify setup
./scripts/bash/verify-portal.sh
```

### Option C: Using Scripts (Dual-Environment - PowerShell + WSL)

If `az login` fails in WSL due to Conditional Access Policy:

**In PowerShell:**
```powershell
Set-Location C:\temp
$Repo = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\...\arc-hybrid-setup-with-fleet"

powershell -NoProfile -ExecutionPolicy Bypass -File "$Repo\scripts\powershell\setup-fleet-hub.ps1" -Location "westus2"
powershell -NoProfile -ExecutionPolicy Bypass -File "$Repo\scripts\powershell\setup-arc-member.ps1" -Location "westus2"
```

> **Important:** Run PowerShell scripts from a local Windows working directory such as `C:\temp`. Running unsigned scripts directly from a UNC current directory may fail due to execution policy restrictions.

**In WSL (for Kind cluster and DocumentDB):**
```bash
cd documentdb-playground/arc-hybrid-setup-with-fleet
./scripts/bash/setup-arc-member.sh --skip-azure  # Kind only, skip az commands
./scripts/bash/deploy-documentdb-fleet.sh
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          Azure Portal                                    │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │                    Azure Fleet Manager                            │  │
│  │  • Fleet Hub (member management)                                 │  │
│  │  • AKS Member (managedClusters) ─── Portal visibility           │  │
│  │  • Arc Member (connectedClusters) ─ Portal visibility + token   │  │
│  └──────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
                              │
        Direct Helm deployment to each cluster
        (hubless Fleet - no CRP propagation)
                              │
        ┌─────────────────────┴─────────────────────┐
        ▼                                           ▼
┌───────────────────────┐               ┌───────────────────────┐
│   AKS Member Cluster  │               │  Arc-Enabled Member   │
│   (Azure-managed)     │               │    (On-Prem/Kind)     │
├───────────────────────┤               ├───────────────────────┤
│  cert-manager         │               │  cert-manager         │
│  DocumentDB Operator  │               │  DocumentDB Operator  │
│  (deployed by Helm)   │               │  Azure Arc Agents     │
├───────────────────────┤               ├───────────────────────┤
│  DocumentDB Cluster   │               │  DocumentDB Cluster   │
└───────────────────────┘               └───────────────────────┘
```

## Step-by-Step Guide

### Phase 1: Create Fleet Hub + AKS (PowerShell)

If the resource group already exists, use its current location. This guide assumes `westus2`.

```powershell
# Variables
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

> **Reruns:** If `documentdb-aks` already exists, skip `az aks create` or choose a new `AKS_CLUSTER` name. The helper script does not currently adopt an existing AKS cluster.

### Phase 2: Install cert-manager on AKS (WSL)

```bash
kubectl config use-context documentdb-aks
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
```

### Phase 3: Create Kind Cluster (WSL)

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

### Phase 4: Arc-Enable Kind Cluster (PowerShell)

```powershell
# Ensure using WSL kubeconfig
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\.kube\config"
kubectl config use-context kind-documentdb-onprem
kubectl get nodes  # Verify shows "documentdb-onprem-*" nodes

# Arc-enable the cluster
az connectedk8s connect `
  --name documentdb-onprem `
  --resource-group $RESOURCE_GROUP `
  --location $LOCATION `
  --tags "environment=onprem" "purpose=documentdb" "fleet=documentdb-fleet"

# Verify Arc connection
az connectedk8s show --name documentdb-onprem --resource-group $RESOURCE_GROUP `
  --query "{name:name, connectivityStatus:connectivityStatus}" -o table

# Join Arc cluster to Fleet
$ARC_ID = az connectedk8s show -g $RESOURCE_GROUP -n documentdb-onprem --query id -o tsv
az fleet member create `
  --resource-group $RESOURCE_GROUP `
  --fleet-name $FLEET_NAME `
  --name documentdb-onprem `
  --member-cluster-id $ARC_ID

# Verify Fleet members
az fleet member list --resource-group $RESOURCE_GROUP --fleet-name $FLEET_NAME -o table
```

If using the PowerShell helper script instead of the manual steps above, the script also installs cert-manager on the Arc-enabled cluster. It first tries the Azure Arc extension type `microsoft.certmanagement`, and if that extension type is unavailable for the connected cluster, it automatically falls back to the upstream cert-manager manifest.

### Phase 5: Install cert-manager on Kind (WSL)

If you used the manual steps in Phase 4, install cert-manager explicitly:

```bash
kubectl config use-context kind-documentdb-onprem
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
```

If you used `scripts/powershell/setup-arc-member.ps1` or `scripts/bash/setup-arc-member.sh`, this phase is already handled by the script and can be skipped.

### Phase 6: Deploy DocumentDB Operator to Both Clusters (WSL)

```bash
# Navigate to Helm chart directory (from repo root)
cd operator/documentdb-helm-chart

# Deploy to Kind cluster
kubectl config use-context kind-documentdb-onprem
helm install documentdb-operator . --namespace documentdb-operator --create-namespace --wait --timeout 10m

# Verify operator is running
kubectl get pods -n documentdb-operator
# Expected: documentdb-operator-xxx Running, cnpg-cloudnative-pg-xxx Running

# Deploy to AKS cluster  
kubectl config use-context documentdb-aks
helm install documentdb-operator . --namespace documentdb-operator --create-namespace --wait --timeout 10m

# Verify operator is running
kubectl get pods -n documentdb-operator
```

### Phase 7: Create Service Account Token for Arc Portal (WSL)

The Arc-enabled cluster requires a bearer token to view Kubernetes resources in Azure Portal:

```bash
kubectl config use-context kind-documentdb-onprem

# Create service account with cluster-admin access
kubectl create serviceaccount arc-portal-viewer -n default
kubectl create clusterrolebinding arc-portal-viewer-binding \
  --clusterrole=cluster-admin \
  --serviceaccount=default:arc-portal-viewer

# Generate token (valid for 1 year)
kubectl create token arc-portal-viewer -n default --duration=8760h
```

> **Important:** Copy and save the token output - you'll need it for Azure Portal.

### Phase 8: Deploy DocumentDB Instance (WSL)

Deploy a DocumentDB instance on both clusters:

```bash
# Create namespace and credentials on Kind cluster
kubectl config use-context kind-documentdb-onprem
kubectl create namespace app-namespace
kubectl create secret generic documentdb-credentials \
  --namespace app-namespace \
  --from-literal=username=docdbuser \
  --from-literal=password=YourSecurePassword123!

# Deploy DocumentDB instance
kubectl apply -f documentdb-instance.yaml

# Wait for pod to be ready
kubectl get pods -n app-namespace -w
# Wait until: demo-documentdb-1  2/2  Running

# Verify DocumentDB is healthy
kubectl get documentdb -n app-namespace
# Expected: STATUS = "Cluster in healthy state"
```

Repeat for AKS cluster:

```bash
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

### Phase 9: Verify in Azure Portal

**AKS Cluster:**
1. Navigate to: Azure Portal → AKS → `documentdb-aks` → **Workloads**
2. Select namespace: `app-namespace`
3. View `demo-documentdb-1` pod

**Arc-Enabled Cluster:**
1. Navigate to: Azure Portal → Arc → Kubernetes → `documentdb-onprem`
2. Go to **Kubernetes resources** → **Workloads**
3. Click **Sign in with service account token**
4. Paste the token from Phase 7
5. Select namespace: `app-namespace`
6. View `demo-documentdb-1` pod

**Fleet Manager:**
1. Navigate to: Azure Portal → Fleet Manager → `documentdb-fleet`
2. Go to **Members** to see both clusters

## Success Criteria

After completing all phases, verify:

| Check | Expected Result |
|-------|-----------------|
| Fleet members | 2 clusters (documentdb-aks, documentdb-onprem) |
| Arc connectivity | `Connected` status |
| Operator pods | Running on both clusters |
| DocumentDB status | "Cluster in healthy state" on both |
| Portal visibility | Can see pods in both AKS and Arc portals |

## Portal Links

| Resource | URL |
|----------|-----|
| Fleet Hub | https://portal.azure.com/#view/Microsoft_Azure_Fleet/FleetMenuBlade |
| Arc Clusters | https://portal.azure.com/#view/Microsoft_Azure_HybridCompute/AzureArcCenterBlade/~/kubernetesServices |
| AKS Clusters | https://portal.azure.com/#view/HubsExtension/BrowseResource/resourceType/Microsoft.ContainerService%2FmanagedClusters |

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|----------|
| `az login` fails in WSL | Conditional Access Policy | Use PowerShell for all `az` commands |
| `management.azure.com` times out | VPN route captures ARM traffic | Disconnect Azure VPN or fix routing, then retry from PowerShell |
| PowerShell script cannot run from `\\wsl.localhost\...` | Execution policy blocks unsigned UNC scripts | Run from `C:\temp` and invoke with `powershell -ExecutionPolicy Bypass -File ...` |
| Kind context not found | kubeconfig not shared | Set `$env:KUBECONFIG` to WSL path in PowerShell |
| Arc CRD conflicts | Previous Arc install | Delete Kind cluster with `kind delete cluster --name documentdb-onprem` and recreate |
| Arc cluster already exists but is disconnected | Stale Azure Arc registration | Delete `connectedk8s` resource and Fleet member, then recreate or use a new Arc cluster name |
| Arc connect timeout | Network issues | Retry; check firewall allows outbound to Azure |
| `microsoft.certmanagement` extension type unavailable | Extension not offered for this connected cluster | Allow script to fall back to the upstream cert-manager manifest |
| Arc cert-manager extension install fails with Helm ownership errors | cert-manager was already installed from raw manifests | Use a fresh cluster for extension validation, or remove the existing `cert-manager` resources before retrying the extension install |
| Arc "token required" | Missing service account | Create token per Phase 7 |
| Pods not visible in Portal | Wrong namespace selected | Change namespace filter to `app-namespace` |
| Helm not found in PowerShell | Helm not in PATH | Run Helm commands from WSL only |
| AKS creation fails | Quota exceeded or cluster already exists | Try different region, request quota increase, or choose a new AKS cluster name |

## Cleanup

When you're done, clean up all resources:

**Using cleanup script (WSL):**
```bash
./scripts/bash/cleanup.sh
```

**Or manually:**

*In WSL:*
```bash
kind delete cluster --name documentdb-onprem
```

*In PowerShell (or WSL if az works):*
```bash
# This deletes: Fleet hub, AKS cluster, Arc registration, all resources
az group delete --name documentdb-fleet-rg --yes --no-wait
```

## Files in This Directory

```
arc-hybrid-setup-with-fleet/
├── README.md                      # This guide
├── documentdb-instance.yaml       # Sample DocumentDB CR
└── scripts/
    ├── bash/                      # For WSL/Linux (use if az login works)
    │   ├── setup-fleet-hub.sh     # Creates Fleet + AKS + cert-manager
    │   ├── setup-arc-member.sh    # Creates Kind, Arc-enables, joins Fleet
    │   ├── deploy-documentdb-fleet.sh  # Deploys operator + instances
    │   ├── verify-portal.sh       # Verifies setup in Azure
    │   └── cleanup.sh             # Removes all resources
    └── powershell/                # For Windows (if az login fails in WSL)
        ├── setup-fleet-hub.ps1    # Creates Fleet + AKS
        └── setup-arc-member.ps1   # Arc-enables Kind + joins Fleet
```

### Script Features

| Script | What It Does |
|--------|-------------|
| `setup-fleet-hub.sh` | Creates resource group, Fleet hub, AKS cluster, joins AKS to Fleet, installs cert-manager |
| `setup-arc-member.sh` | Creates Kind cluster, Arc-enables it, joins to Fleet, installs cert-manager, creates portal token |
| `deploy-documentdb-fleet.sh` | Deploys DocumentDB operator and instances to all Fleet members |
| `verify-portal.sh` | Shows Fleet members, Arc status, and Azure Portal links |
| `cleanup.sh` | Deletes Kind cluster, Arc registration, Fleet, AKS, resource group |

## Related Documentation

- [Multi-cluster replication](../multi-cloud-deployment/) - Cross-cluster DocumentDB replication
- [TLS configuration](../tls/) - Certificate setup options
- [Monitoring](../telemetry/) - OpenTelemetry and Prometheus integration
- [Azure Arc Integration Plan](../../docs/designs/azure-arc/azure-arc-integration-plan.md) - Full Azure product integration roadmap
