# PowerShell Scripts for Arc + Fleet Setup

Use these scripts when Azure CLI doesn't work in WSL (Conditional Access Policy issues).

## Prerequisites

- Azure CLI 2.50.0+ with `fleet`, `connectedk8s` extensions
- kubectl (Windows or via WSL)
- Logged into Azure: `az login`
- Kind cluster created in WSL (for `setup-arc-member.ps1`)

## Usage

### Step 1: Create Fleet + AKS (PowerShell)

```powershell
.\setup-fleet-hub.ps1
```

### Step 2: Create Kind Cluster (WSL)

```bash
kind create cluster --name documentdb-onprem --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
EOF
```

### Step 3: Arc-Enable Kind (PowerShell)

```powershell
.\setup-arc-member.ps1
```

### Step 4: Deploy DocumentDB (WSL)

```bash
cd ../bash
./deploy-documentdb-fleet.sh
```

## Parameters

```powershell
.\setup-fleet-hub.ps1 -Help

# Common parameters:
#   -ResourceGroup   Azure resource group (default: documentdb-fleet-rg)
#   -Location        Azure region (default: eastus)
#   -FleetName       Fleet hub name (default: documentdb-fleet)
```

## KUBECONFIG

These scripts automatically set KUBECONFIG to the WSL path:

```powershell
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\.kube\config"
```
