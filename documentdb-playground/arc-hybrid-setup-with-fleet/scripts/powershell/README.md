# PowerShell Scripts for Arc + Fleet Setup

Use these scripts when Azure CLI doesn't work in WSL (Conditional Access Policy issues).

## Prerequisites

- Azure CLI 2.50.0+ with `fleet`, `connectedk8s`, `k8s-extension` extensions
- kubectl (Windows or via WSL)
- Logged into Azure: `az login`
- Kind cluster created in WSL (for `setup-arc-member.ps1`)

## Usage

### Step 1: Create Fleet + AKS (PowerShell)

```powershell
Set-Location C:\temp
$Repo = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\...\arc-hybrid-setup-with-fleet"
powershell -NoProfile -ExecutionPolicy Bypass -File "$Repo\scripts\powershell\setup-fleet-hub.ps1" -Location "westus2"
```

Use a local Windows working directory such as `C:\temp` before invoking the scripts. This avoids UNC current-directory and execution policy issues.

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
$ArcCluster = "documentdb-onprem"
powershell -NoProfile -ExecutionPolicy Bypass -File "$Repo\scripts\powershell\setup-arc-member.ps1" -Location "westus2" -ArcCluster $ArcCluster
```

If you create a non-default Kind cluster name such as `documentdb-onprem-v2`, pass the same name with `-ArcCluster`.

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
#   -Location        Azure region (match the resource group location)
#   -FleetName       Fleet hub name (default: documentdb-fleet)
#   -ArcCluster      Arc cluster name for setup-arc-member.ps1 (default: documentdb-onprem)
```

## Notes

- `setup-fleet-hub.ps1` expects to create a new AKS cluster. If `documentdb-aks` already exists, choose a new name or skip that step.
- `setup-arc-member.ps1` installs cert-manager automatically. It first tries the Azure Arc extension type `microsoft.certmanagement`, then falls back to the upstream cert-manager manifest if the extension type is unavailable.
- If cert-manager is already present from a raw manifest install, a later Arc extension install attempt can fail with Helm ownership errors. Validate the extension path on a fresh cluster, or remove the existing `cert-manager` resources before retrying.

## KUBECONFIG

These scripts automatically set KUBECONFIG to the WSL path:

```powershell
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$env:USERNAME\.kube\config"
```
