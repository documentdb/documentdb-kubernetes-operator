# Copilot Agent Instructions: DocumentDB on AKS Edge

## Overview

This guide helps AI agents assist users in deploying DocumentDB on AKS Edge Essentials (K3s on Windows) with Azure Arc for portal visibility.

**End Result:**
- AKS Edge cluster running on Windows machine
- DocumentDB operator + instance deployed
- Cluster visible in Azure Portal via Azure Arc
- User can view/manage workloads from Azure Portal

**Estimated Time:** 45-60 minutes

**Target Machine:** Windows 10/11 Pro with Hyper-V (e.g., Lenovo ThinkPad)

---

## Critical Requirements

> **⚠️ MUST USE Windows PowerShell 5.1 (NOT PowerShell 7)**
>
> AKS Edge requires Windows PowerShell 5.1 due to DISM module compatibility.
> PowerShell 7 will fail with "Class not registered" errors.
>
> How to identify:
> - Windows PowerShell 5.1: Blue icon, title "Windows PowerShell"
> - PowerShell 7: Black icon, title "pwsh" or "PowerShell 7"
>
> **Tell user:** "Right-click on 'Windows PowerShell' (not PowerShell 7) and select 'Run as administrator'"

---

## Agent Behavior

### Running PowerShell Commands

**If running from WSL terminal**, use `powershell.exe` to execute Windows commands:

```bash
# Pattern for single commands
powershell.exe -Command "Get-Command -Module AksEdge"

# Pattern for multi-line scripts
powershell.exe -Command '
  $RESOURCE_GROUP = "aks-edge-rg"
  az group create --name $RESOURCE_GROUP --location eastus
'
```

**If running from Windows PowerShell terminal**, run commands directly.

**kubectl and helm** work in both environments (WSL shares kubeconfig with Windows via AKS Edge).

### Important: Administrator Privileges

**Phase 1-3 (AKS Edge installation, Azure setup, and deployment) require Administrator privileges in Windows PowerShell 5.1.**

The agent should instruct the user to:
1. Open **Windows PowerShell 5.1 as Administrator** (NOT PowerShell 7)
2. Run the AKS Edge commands directly there
3. Then continue with Kubernetes commands (kubectl/helm work in same terminal)

---

## Pre-flight Checks

Before starting, verify the environment.

**These can run from WSL:**
```bash
# Check if Hyper-V is enabled
powershell.exe -Command "Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V | Select-Object State"

# Check available RAM (need 16GB+)
powershell.exe -Command "[math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB, 2)"

# Check available disk space (need 40GB+)
powershell.exe -Command "[math]::Round((Get-PSDrive C).Free / 1GB, 2)"
```

**If Hyper-V is not enabled (requires Admin PowerShell on Windows):**
```powershell
# Run in Admin PowerShell, then restart Windows
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All
Restart-Computer
```

---

## Execution Instructions

### Phase 1: Install AKS Edge Essentials

> **⚠️ REQUIRES ADMIN:** Tell user to open **Windows PowerShell 5.1 as Administrator** (NOT PowerShell 7).

**Tell user:** \"Right-click on 'Windows PowerShell' and select 'Run as administrator', then run:\"

```powershell
# Download and install AKS Edge MSI  
$msiUrl = \"https://aka.ms/aks-edge/k3s-msi\"
$msiPath = \"$env:TEMP\\AksEdge.msi\"
Invoke-WebRequest -Uri $msiUrl -OutFile $msiPath -UseBasicParsing

# Install
Start-Process msiexec.exe -ArgumentList \"/i `\"$msiPath`\" /qn /l*v `\"$env:TEMP\\aksedge-install.log`\"\" -Wait -Verb RunAs

# Import and verify
Import-Module AksEdge
Get-Command -Module AksEdge
```

**Success check:** Output shows AksEdge commands like `New-AksEdgeDeployment`

---

### Phase 1.5: Install Host Features

```powershell
# Install required Windows features for AKS Edge
# This disables sleep mode and configures other requirements
Install-AksEdgeHostFeatures

# You may need to restart after this command
```

**Success check:** Command completes without errors

---

### Phase 2: Configure Azure Access

> **⚠️ CRITICAL:** AKS Edge **requires** Azure Arc. Both az CLI and Az PowerShell must be configured.

**Tell user:** \"Now set up Azure authentication:\"

```powershell
# Install Az PowerShell module if not present
Install-Module -Name Az -Scope CurrentUser -Repository PSGallery -Force

# Login via Azure CLI
az login

# Get subscription info
$SUBSCRIPTION_ID = (az account show --query id -o tsv)
$TENANT_ID = (az account show --query tenantId -o tsv)
az account set --subscription $SUBSCRIPTION_ID

# CRITICAL: Also connect Az PowerShell module
# AKS Edge deployment uses Az PowerShell internally, not just az CLI!
Connect-AzAccount
Set-AzContext -SubscriptionId $SUBSCRIPTION_ID

# Verify BOTH contexts match
Write-Host \"CLI Subscription: $SUBSCRIPTION_ID\"
Write-Host \"PowerShell Subscription: $((Get-AzContext).Subscription.Id)\"
# These MUST match!

# Register required Azure providers
az provider register --namespace Microsoft.Kubernetes
az provider register --namespace Microsoft.KubernetesConfiguration
az provider register --namespace Microsoft.ExtendedLocation

# Check registration (wait until \"Registered\")
az provider show --namespace Microsoft.ExtendedLocation --query \"registrationState\" -o tsv
```

**Success check:** Both subscriptions match, providers show \"Registered\"

---

### Phase 2.5: Create Resource Group

```powershell
# Variables - customize these
$RESOURCE_GROUP = \"aks-edge-rg\"
$LOCATION = \"westus2\"  # Choose a supported region
$CLUSTER_NAME = \"aks-edge-$(Get-Random -Maximum 9999)\"

# Create resource group
az group create --name $RESOURCE_GROUP --location $LOCATION
```

---

### Phase 3: Create AKS Edge Cluster

> **⚠️ REQUIRES ADMIN:** Continue in the same Admin Windows PowerShell 5.1 window.

**IMPORTANT: Network IP Conflict Check**

Before deploying, check the user's network to avoid IP conflicts:

```powershell
# Find user's current network IPs
Get-NetIPAddress -AddressFamily IPv4 | Where-Object {$_.InterfaceAlias -notlike '*Loopback*' -and $_.InterfaceAlias -notlike '*vEthernet*'} | Select-Object IPAddress, InterfaceAlias
```

**Tell user:** \"If your Wi-Fi is on 10.0.0.x, we'll use 192.168.200.0/24 for AKS Edge (or vice versa) to avoid conflicts.\"

Then create and deploy:

```powershell
# Get current Azure context values
$SUBSCRIPTION_ID = (az account show --query id -o tsv)
$TENANT_ID = (az account show --query tenantId -o tsv)

# Create deployment configuration
# CHANGE the Network IPs if they conflict with user's host network!
$deployConfig = @\"
{
    \"SchemaVersion\": \"1.14\",
    \"Version\": \"1.0\",
    \"DeploymentType\": \"SingleMachineCluster\",
    \"Init\": {
        \"ServiceIPRangeStart\": \"10.43.0.10\",
        \"ServiceIPRangeSize\": 10
    },
    \"Network\": {
        \"ControlPlaneEndpointIp\": \"192.168.200.2\",
        \"NetworkPlugin\": \"flannel\",
        \"Ip4AddressPrefix\": \"192.168.200.0/24\",
        \"Ip4GatewayAddress\": \"192.168.200.1\",
        \"DnsServers\": [\"8.8.8.8\", \"8.8.4.4\"]
    },
    \"User\": {
        \"AcceptEula\": true,
        \"AcceptOptionalTelemetry\": false
    },
    \"Machines\": [
        {
            \"LinuxNode\": {
                \"CpuCount\": 4,
                \"MemoryInMB\": 8192,
                \"DataSizeInGB\": 40
            }
        }
    ],
    \"Arc\": {
        \"ClusterName\": \"$CLUSTER_NAME\",
        \"Location\": \"$LOCATION\",
        \"ResourceGroupName\": \"$RESOURCE_GROUP\",
        \"SubscriptionId\": \"$SUBSCRIPTION_ID\",
        \"TenantId\": \"$TENANT_ID\"
    }
}
\"@

# Save configuration
$deployConfig | Out-File -FilePath \"$env:TEMP\\aksedge-config.json\" -Encoding utf8

# Verify configuration has correct subscription values
Get-Content \"$env:TEMP\\aksedge-config.json\"

# Deploy cluster (takes 10-15 minutes, includes Arc connection)
New-AksEdgeDeployment -JsonConfigFilePath \"$env:TEMP\\aksedge-config.json\"
```

After deployment, verify:
```powershell
# Verify deployment
Get-AksEdgeDeploymentInfo

# Test kubectl
kubectl get nodes
```

**Success check:** `kubectl get nodes` shows 1 node in Ready state, Arc connection shows in Azure Portal

---

### Phase 4: Install Storage Provisioner

AKS Edge doesn't include a storage provisioner by default. Install local-path-provisioner:

```powershell
# Install local-path-provisioner
kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/v0.0.26/deploy/local-path-storage.yaml

# IMPORTANT: AKS Edge has /opt as read-only, reconfigure to use /tmp
kubectl patch configmap local-path-config -n local-path-storage --type merge -p '{\"data\":{\"config.json\":\"{\\\"nodePathMap\\\":[{\\\"node\\\":\\\"DEFAULT_PATH_FOR_NON_LISTED_NODES\\\",\\\"paths\\\":[\\\"/tmp/local-path-provisioner\\\"]}]}\"}}'

# Restart provisioner to pick up config
kubectl rollout restart deployment local-path-provisioner -n local-path-storage

# Set as default storage class
kubectl patch storageclass local-path -p '{\"metadata\": {\"annotations\":{\"storageclass.kubernetes.io/is-default-class\":\"true\"}}}'

# Verify
kubectl get storageclass
```

**Success check:** `local-path (default)` shown in storageclass list

---

### Phase 5: Install cert-manager

**Tell user:** \"Installing cert-manager for TLS certificate management\"

```powershell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s

# Verify
kubectl get pods -n cert-manager
```

**Success check:** 3 pods running in cert-manager namespace

---

### Phase 6: Install DocumentDB Operator

**Tell user:** \"Installing the DocumentDB operator via Helm\"

```powershell
# Install Helm if not present
winget install Helm.Helm
# Refresh PATH
$env:Path = [System.Environment]::GetEnvironmentVariable(\"Path\",\"Machine\") + \";\" + [System.Environment]::GetEnvironmentVariable(\"Path\",\"User\")

# Navigate to the Helm chart (adjust path as needed)
cd C:\\path\\to\\documentdb-kubernetes-operator\\operator\\documentdb-helm-chart

# Install operator
helm install documentdb-operator . `
  --namespace documentdb-operator `
  --create-namespace `
  --wait `
  --timeout 10m

# Verify
kubectl get pods -n documentdb-operator
```

**Success check:** documentdb-operator pod running

---

### Phase 7: Deploy DocumentDB Instance

**Tell user:** \"Now deploying a DocumentDB instance\"

```powershell
# Create namespace
kubectl create namespace app-namespace

# Create credentials secret
kubectl create secret generic documentdb-credentials `
  --namespace app-namespace `
  --from-literal=username=docdbuser `
  --from-literal=password=YourSecurePassword123!

# Deploy DocumentDB (use here-string in PowerShell)
@\"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: demo-documentdb
  namespace: app-namespace
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDBImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  gatewayImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 5Gi
  exposeViaService:
    serviceType: ClusterIP
  logLevel: info
  sidecarInjectorPluginName: cnpg-i-sidecar-injector.documentdb.io
\"@ | kubectl apply -f -

# Watch pod creation (Ctrl+C to exit when ready)
kubectl get pods -n app-namespace -w
```

**Wait for:** Pod `demo-documentdb-1` to show `2/2 Running` (or `1/2` if gateway crashes - see Known Limitations)

```powershell
# Verify DocumentDB status
kubectl get documentdb -n app-namespace
```

**Success check:** Status shows \"Cluster in healthy state\"

> **Note:** Due to IPv6 limitations on AKS Edge, the gateway container may crash loop.
> The PostgreSQL container will run fine. See Known Limitations section.

---

### Phase 8: Create Portal Access Token

# Verify connection
### Phase 8: Create Portal Access Token

**Tell user:** \"Creating a token so you can view Kubernetes resources in Azure Portal\"

```powershell
# Create service account
kubectl create serviceaccount arc-portal-viewer -n default
kubectl create clusterrolebinding arc-portal-viewer-binding `
  --clusterrole=cluster-admin `
  --serviceaccount=default:arc-portal-viewer

# Generate token (valid for 1 year)
kubectl create token arc-portal-viewer -n default --duration=8760h
```

**Tell user:** \"Copy and save this token - you'll need it in Azure Portal\"

---

### Phase 9: View in Azure Portal

**Guide user through:**

1. Open browser: https://portal.azure.com
2. Navigate to: **Azure Arc** → **Kubernetes clusters**
3. Click on your cluster (name from `$CLUSTER_NAME`)
4. Go to: **Kubernetes resources** → **Workloads**
5. Click: **Sign in with service account token**
6. Paste the token from Phase 8
7. Change namespace filter to: `app-namespace`
8. View the `demo-documentdb-1` pod!

**Success check:** User can see their DocumentDB pod in Azure Portal

---

## Known Limitations

### IPv6 Not Supported on AKS Edge

AKS Edge's Linux VM does not support IPv6 by default. The DocumentDB gateway may crash with:
```
Address family not supported by protocol
```

The PostgreSQL container runs fine, and the DocumentDB status shows "healthy", but the gateway container may crash loop.

**Root cause:** The gateway binary tries to create an IPv6 listening socket which fails without kernel IPv6 support.

**Fix:** Pending one-line change in the gateway binary (`pg_documentdb_gw` in [microsoft/documentdb](https://github.com/microsoft/documentdb)) to fallback to IPv4 when IPv6 fails.

---

## Troubleshooting Guide

| Issue | Solution |
|-------|----------|
| \"Class not registered\" error | Use Windows PowerShell 5.1, NOT PowerShell 7 |
| Invalid Azure Arc parameters | Run `Connect-AzAccount` and `Set-AzContext` to set Az PowerShell context |
| Network validation fails | Check host IP, use non-overlapping subnet (e.g., 192.168.200.0/24) |
| ImagePullBackOff / DNS fails | Network IP conflict - redeploy with different subnet |
| Pods stuck in Pending | Install local-path-provisioner (Phase 4) |
| PVC stuck in Pending | Patch configmap to use /tmp instead of /opt (Phase 4) |
| Gateway CrashLoopBackOff | Known IPv6 limitation - pending gateway fix |
| Arc connection timeout | Ensure outbound HTTPS is allowed |
| Hyper-V not available | Enable Hyper-V in Windows Features, restart |
| Token expired in portal | Generate new: `kubectl create token arc-portal-viewer -n default --duration=8760h` |

---

## Cleanup Instructions

When user is done:

**From Windows PowerShell 5.1 (Admin):**
```powershell
# 1. Delete DocumentDB
kubectl delete documentdb demo-documentdb -n app-namespace
kubectl delete namespace app-namespace

# 2. Uninstall operator
helm uninstall documentdb-operator -n documentdb-operator
kubectl delete namespace documentdb-operator

# 3. Uninstall cert-manager
kubectl delete -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml

# 4. Uninstall storage provisioner
kubectl delete -f https://raw.githubusercontent.com/rancher/local-path-provisioner/v0.0.26/deploy/local-path-storage.yaml

# 5. Delete Azure resource group (removes Arc registration)
az group delete --name aks-edge-rg --yes --no-wait

# 6. Remove AKS Edge cluster
Import-Module AksEdge
Remove-AksEdgeDeployment -Force

# 7. (Optional) Uninstall AKS Edge
winget uninstall Microsoft.AKSEdge
```

---

## Quick Reference

| Phase | Environment | Command to Verify |
|-------|-------------|-------------------|
| 1. AKS Edge install | Win PS 5.1 Admin | `Get-Command -Module AksEdge` |
| 1.5. Host features | Win PS 5.1 Admin | `Install-AksEdgeHostFeatures` completes |
| 2. Azure setup | Win PS 5.1 Admin | Both CLI and PowerShell subscriptions match |
| 3. Cluster create | Win PS 5.1 Admin | `kubectl get nodes` |
| 4. Storage | Any | `kubectl get storageclass` shows local-path (default) |
| 5. cert-manager | Any | `kubectl get pods -n cert-manager` |
| 6. Operator | Any | `kubectl get pods -n documentdb-operator` |
| 7. DocumentDB | Any | `kubectl get documentdb -n app-namespace` |
| 8. Portal token | Any | Token output saved |
| 9. Portal view | Browser | See pods in Azure Portal |

---

## Files in This Directory

| File | Purpose |
|------|---------|
| `README.md` | Full documentation for users |
| `AGENT-INSTRUCTIONS.md` | This file - instructions for Copilot agents |
