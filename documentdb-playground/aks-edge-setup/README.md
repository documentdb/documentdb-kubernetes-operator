# DocumentDB on AKS Edge Essentials

Deploy DocumentDB on your **Windows machine** using **AKS Edge Essentials** and manage it via **Azure Portal** with Azure Arc integration.

**Goal:** Run a Kubernetes cluster on your Windows laptop/workstation and see the cluster + DocumentDB resources in Azure Portal.

## What is AKS Edge Essentials?

**AKS Edge Essentials** is essentially **K3s running in a lightweight Linux VM on Windows**, packaged with Microsoft tooling for easy deployment. It's a simple way to get Kubernetes running on any Windows 10/11 Pro machine.

### How It Works

```
┌─────────────────────────────────────────┐
│      Windows Machine (e.g. ThinkPad)    │
│  ┌───────────────────────────────────┐  │
│  │   Hyper-V Lightweight Linux VM    │  │
│  │  ┌─────────────────────────────┐  │  │
│  │  │    K3s Kubernetes Cluster   │  │  │
│  │  │   • Same K3s you know       │  │  │
│  │  │   • flannel networking      │  │  │
│  │  │   • local-path storage      │  │  │
│  │  └─────────────────────────────┘  │  │
│  └───────────────────────────────────┘  │
│  + Microsoft PowerShell tooling         │
│  + Azure Arc for portal visibility      │
└─────────────────────────────────────────┘
```

**In short:** If you've used K3s on Linux, AKS Edge is the same thing - just wrapped in a Hyper-V VM with Windows-friendly management commands.

| Feature | AKS Edge Essentials | Standard AKS | AKS-HCI |
|---------|---------------------|--------------|---------|  
| **Deployment Target** | Any Windows PC | Azure cloud | On-premises HCI |
| **Host OS** | Windows 10/11 Pro | N/A (managed) | Azure Stack HCI |
| **Under the Hood** | K3s in Hyper-V Linux VM | Managed K8s | K8s on HCI |
| **Resource Requirements** | 8GB RAM | N/A | 64GB+ RAM |
| **Azure Arc Integration** | Yes (required) | Built-in | Built-in |

> **TL;DR:** AKS Edge = K3s + Hyper-V VM + PowerShell wrapper. Azure Arc connection is now **required** for deployment.

### Why This Setup?

- **On-prem Kubernetes**: Run K8s on your Windows workstation without cloud costs
- **Azure Portal Visibility**: See your cluster and workloads in Azure Portal via Arc
- **Dev/Test Environment**: Test DocumentDB operator locally before cloud deployment
- **Hybrid Management**: Manage on-prem and cloud clusters from the same portal

## What You'll Build

| Component | Description |
|-----------|-------------|
| **AKS Edge Cluster** | K3s cluster on your Windows machine |
| **Azure Arc** | Connects cluster to Azure for portal visibility |
| **cert-manager** | TLS certificate management |
| **DocumentDB Operator** | Kubernetes operator for DocumentDB lifecycle |
| **DocumentDB Instance** | Single-node MongoDB-compatible database |

**Estimated Time:** 30-45 minutes

**Hardware Requirements:** 
- Windows 10/11 Pro with Hyper-V support
- 16GB+ RAM recommended
- 40GB+ available disk space
- CPU with virtualization enabled (Intel VT-x / AMD-V)

## Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│              Windows Machine (e.g. Lenovo ThinkPad P16s)               │
│  ┌─────────────────────────────────────────────────────────────────┐  │
│  │                      AKS Edge Essentials                         │  │
│  │  ┌──────────────────────────────────────────────────────────┐   │  │
│  │  │                   K3s Kubernetes Cluster                  │   │  │
│  │  │                                                           │   │  │
│  │  │  ┌─────────────────┐  ┌────────────────────────────────┐ │   │  │
│  │  │  │  cert-manager   │  │   documentdb-operator          │ │   │  │
│  │  │  └─────────────────┘  │   cnpg-cloudnative-pg          │ │   │  │
│  │  │                       └────────────────────────────────┘ │   │  │
│  │  │  ┌────────────────────────────────────────────────────┐  │   │  │
│  │  │  │           DocumentDB Instance (app-namespace)       │  │   │  │
│  │  │  │  • demo-documentdb-1 pod (Gateway + PostgreSQL)    │  │   │  │
│  │  │  │  • MongoDB-compatible API on port 10260            │  │   │  │
│  │  │  └────────────────────────────────────────────────────┘  │   │  │
│  │  └──────────────────────────────────────────────────────────┘   │  │
│  └─────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────┘
                                    │
                         Azure Arc Connection
                                    │
                                    ▼
                    ┌───────────────────────────────┐
                    │        Azure Portal           │
                    │  • View cluster & workloads   │
                    │  • See DocumentDB pods        │
                    │  • Monitor from anywhere      │
                    └───────────────────────────────┘
```

## Prerequisites

### Hardware Requirements

| Resource | Minimum | Recommended |
|----------|---------|-------------|
| CPU | 4 cores | 8+ cores |
| RAM | 8GB | 16GB+ |
| Disk | 40GB | 100GB+ SSD |
| Network | 1 NIC | 2 NICs (for external access) |

### Software Requirements

| Tool | Version | Purpose |
|------|---------|---------|
| Windows | 10/11 Pro | Host OS |
| Hyper-V | Enabled | Virtual machine support |
| AKS Edge Essentials | 1.0+ | Kubernetes distribution |
| kubectl | 1.26+ | Cluster management |
| Helm | 3.12+ | Package deployment |
| Azure CLI | 2.50+ | Azure Arc setup |
| Az PowerShell Module | 6.0+ | AKS Edge Azure integration |

### Important Notes

> **⚠️ CRITICAL: Use Windows PowerShell 5.1, NOT PowerShell 7**
>
> AKS Edge requires Windows PowerShell 5.1 (ships with Windows) due to DISM module compatibility issues.
> PowerShell 7 will fail with "Class not registered" errors.
>
> To open Windows PowerShell 5.1:
> - Search for "Windows PowerShell" (not "PowerShell 7" or "pwsh")
> - Right-click → "Run as administrator"

### Enable Hyper-V (Windows PowerShell 5.1 as Administrator)

```powershell
# Enable Hyper-V feature
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All

# Restart required after enabling
Restart-Computer
```

## Step-by-Step Guide

### Phase 1: Install AKS Edge Essentials

#### Option A: MSI Installer (Recommended)

1. **Download AKS Edge Essentials** from Microsoft:
   - Visit: https://aka.ms/aks-edge/msi
   - Download the MSI installer

2. **Install via PowerShell (Administrator)**:

```powershell
# Install AKS Edge Essentials MSI
# Replace with actual path to downloaded MSI
Start-Process msiexec.exe -ArgumentList '/i "C:\Downloads\AksEdge-K3s-1.28.3-1.7.639.0.msi" /qn' -Wait

# Import the AKS Edge module
Import-Module AksEdge

# Verify installation
Get-Command -Module AksEdge
```

#### Option B: Winget Installation

```powershell
# Install via winget
winget install --id Microsoft.AKSEdge -e --accept-source-agreements --accept-package-agreements

# Import module
Import-Module AksEdge
```

### Phase 1.5: Install Host Features

This step is required before creating the cluster:

```powershell
# Install required Windows features for AKS Edge
# This disables sleep mode and configures other requirements
Install-AksEdgeHostFeatures

# You may need to restart after this
```

### Phase 2: Configure Azure Access

AKS Edge **requires** Azure Arc connection. Set up both az CLI and Az PowerShell:

```powershell
# Install Az PowerShell module if not present
Install-Module -Name Az -Scope CurrentUser -Repository PSGallery -Force

# Login via Azure CLI
az login

# Set subscription
$SUBSCRIPTION_ID = (az account show --query id -o tsv)
$TENANT_ID = (az account show --query tenantId -o tsv)
az account set --subscription $SUBSCRIPTION_ID

# IMPORTANT: Also connect Az PowerShell module
# The AKS Edge deployment uses Az PowerShell, not az CLI!
Connect-AzAccount
Set-AzContext -SubscriptionId $SUBSCRIPTION_ID

# Verify both contexts match
Write-Host "CLI Subscription: $SUBSCRIPTION_ID"
Write-Host "PowerShell Subscription: $((Get-AzContext).Subscription.Id)"

# Register required Azure providers
az provider register --namespace Microsoft.Kubernetes
az provider register --namespace Microsoft.KubernetesConfiguration
az provider register --namespace Microsoft.ExtendedLocation

# Wait for registration (check status - should show "Registered")
az provider show --namespace Microsoft.ExtendedLocation --query "registrationState" -o tsv
```

### Phase 2.5: Create Resource Group

```powershell
# Variables - customize these
$RESOURCE_GROUP = "aks-edge-rg"
$LOCATION = "westus2"  # Choose a supported region
$CLUSTER_NAME = "aks-edge-$(Get-Random -Maximum 9999)"

# Create resource group
az group create --name $RESOURCE_GROUP --location $LOCATION
```

### Phase 3: Create AKS Edge Cluster

> **⚠️ IMPORTANT:** Choose a network subnet that does NOT conflict with your host network.
>
> Check your current IP: `Get-NetIPAddress -AddressFamily IPv4 | Where-Object {$_.InterfaceAlias -notlike '*Loopback*'}`
>
> If your Wi-Fi is on `10.0.0.x`, use `192.168.200.0/24` for AKS Edge (or vice versa).

#### Single Machine Deployment (Simplest)

```powershell
# Get current Azure context values
$SUBSCRIPTION_ID = (az account show --query id -o tsv)
$TENANT_ID = (az account show --query tenantId -o tsv)

# Create deployment configuration JSON
# CHANGE the Network IPs if they conflict with your host network!
$deployConfig = @"
{
    "SchemaVersion": "1.14",
    "Version": "1.0",
    "DeploymentType": "SingleMachineCluster",
    "Init": {
        "ServiceIPRangeStart": "10.43.0.10",
        "ServiceIPRangeSize": 10
    },
    "Network": {
        "ControlPlaneEndpointIp": "192.168.200.2",
        "NetworkPlugin": "flannel",
        "Ip4AddressPrefix": "192.168.200.0/24",
        "Ip4GatewayAddress": "192.168.200.1",
        "DnsServers": ["8.8.8.8", "8.8.4.4"]
    },
    "User": {
        "AcceptEula": true,
        "AcceptOptionalTelemetry": false
    },
    "Machines": [
        {
            "LinuxNode": {
                "CpuCount": 4,
                "MemoryInMB": 8192,
                "DataSizeInGB": 40
            }
        }
    ],
    "Arc": {
        "ClusterName": "$CLUSTER_NAME",
        "Location": "$LOCATION",
        "ResourceGroupName": "$RESOURCE_GROUP",
        "SubscriptionId": "$SUBSCRIPTION_ID",
        "TenantId": "$TENANT_ID"
    }
}
"@

# Save configuration
$deployConfig | Out-File -FilePath "$env:TEMP\aksedge-config.json" -Encoding utf8

# Verify configuration has correct values
Get-Content "$env:TEMP\aksedge-config.json"

# Validate configuration
Test-AksEdgeNetworkParameters -JsonConfigFilePath "$env:TEMP\aksedge-config.json"

# Deploy AKS Edge cluster (takes 10-15 minutes)
New-AksEdgeDeployment -JsonConfigFilePath "$env:TEMP\aksedge-config.json"
```

#### Verify Cluster Creation

```powershell
# Check cluster status
Get-AksEdgeDeploymentInfo

# Verify kubectl access
kubectl get nodes

# Expected output:
# NAME               STATUS   ROLES                       AGE   VERSION
# <hostname>-ledge   Ready    control-plane,etcd,master   5m    v1.31.x+k3s
```

### Phase 4: Install Storage Provisioner

AKS Edge doesn't include a storage provisioner by default. Install local-path-provisioner:

```powershell
# Install local-path-provisioner
kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/v0.0.26/deploy/local-path-storage.yaml

# IMPORTANT: AKS Edge has /opt as read-only, reconfigure to use /tmp
kubectl patch configmap local-path-config -n local-path-storage --type merge -p '{"data":{"config.json":"{\"nodePathMap\":[{\"node\":\"DEFAULT_PATH_FOR_NON_LISTED_NODES\",\"paths\":[\"/tmp/local-path-provisioner\"]}]}"}}'      

# Restart provisioner to pick up config
kubectl rollout restart deployment local-path-provisioner -n local-path-storage

# Set as default storage class
kubectl patch storageclass local-path -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'

# Verify
kubectl get storageclass
# Should show: local-path (default)
```

### Phase 5: Install cert-manager

```powershell
# Apply cert-manager CRDs and components
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s

# Verify installation
kubectl get pods -n cert-manager

# Expected output:
# NAME                                      READY   STATUS    RESTARTS   AGE
# cert-manager-xxxxxx                       1/1     Running   0          2m
# cert-manager-cainjector-xxxxxx            1/1     Running   0          2m
# cert-manager-webhook-xxxxxx               1/1     Running   0          2m
```

### Phase 6: Install DocumentDB Operator

Install Helm if not present, then install the operator:

```powershell
# Install Helm if not present
winget install Helm.Helm
# Refresh PATH
$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")

# Navigate to the DocumentDB Helm chart
cd \path\to\documentdb-kubernetes-operator\operator\documentdb-helm-chart

# Install DocumentDB operator
helm install documentdb-operator . `
  --namespace documentdb-operator `
  --create-namespace `
  --wait `
  --timeout 10m

# Verify operator installation
kubectl get pods -n documentdb-operator

# Expected output:
# NAME                                          READY   STATUS    RESTARTS   AGE
# documentdb-operator-xxxxxx                    1/1     Running   0          2m
```

### Phase 7: Deploy DocumentDB Instance

#### Create Namespace and Credentials

```powershell
# Create application namespace
kubectl create namespace app-namespace

# Create credentials secret
kubectl create secret generic documentdb-credentials `
  --namespace app-namespace `
  --from-literal=username=docdbuser `
  --from-literal=password=YourSecurePassword123!
```

#### Deploy DocumentDB Custom Resource

Create `documentdb-instance.yaml`:

```yaml
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
```

Apply the configuration:

```powershell
# Apply DocumentDB instance
kubectl apply -f documentdb-instance.yaml

# Watch pod creation
kubectl get pods -n app-namespace -w

# Wait for pod to be ready (2/2 containers)
# Expected: demo-documentdb-1   2/2   Running   0   3m
```

### Phase 7: Connect to Azure Arc

Connect your cluster to Azure so you can see everything in Azure Portal.

#### Install Azure CLI Extensions

```powershell
# Install Azure CLI extensions
az extension add --name connectedk8s --upgrade --yes
az extension add --name k8s-extension --upgrade --yes

# Login to Azure
az login
```

#### Connect Cluster to Azure Arc

```powershell
# Variables
$RESOURCE_GROUP = "aks-edge-rg"
$LOCATION = "eastus"
$ARC_CLUSTER_NAME = "aks-edge-documentdb"

# Create resource group
az group create --name $RESOURCE_GROUP --location $LOCATION

# Connect cluster to Azure Arc
az connectedk8s connect `
  --name $ARC_CLUSTER_NAME `
  --resource-group $RESOURCE_GROUP `
  --location $LOCATION `
  --tags "environment=onprem" "purpose=documentdb"

# Verify connection
az connectedk8s show --name $ARC_CLUSTER_NAME --resource-group $RESOURCE_GROUP `
  --query "{name:name, connectivityStatus:connectivityStatus}" -o table

# Expected: connectivityStatus = Connected
```

#### Create Service Account Token for Portal Access

```powershell
# Create service account for portal access
kubectl create serviceaccount arc-portal-viewer -n default
kubectl create clusterrolebinding arc-portal-viewer-binding `
  --clusterrole=cluster-admin `
  --serviceaccount=default:arc-portal-viewer

# Generate token (valid for 1 year)
kubectl create token arc-portal-viewer -n default --duration=8760h
```

> **Save this token** - you'll need it to view Kubernetes resources in Azure Portal.

### Phase 8: Verification

#### Verify DocumentDB Status

```powershell
# Check DocumentDB resource status
kubectl get documentdb -n app-namespace

# Expected output:
# NAME              STATUS                      AGE
# demo-documentdb   Cluster in healthy state    5m

# Get detailed status
kubectl describe documentdb demo-documentdb -n app-namespace
```

#### Verify Pod Health

```powershell
# Check pod status
kubectl get pods -n app-namespace

# Expected output:
# NAME                  READY   STATUS    RESTARTS   AGE
# demo-documentdb-1     2/2     Running   0          5m

# Check pod logs
kubectl logs demo-documentdb-1 -n app-namespace -c postgres
```

#### Test MongoDB Connection

```powershell
# Port-forward to access DocumentDB locally
kubectl port-forward svc/demo-documentdb-rw -n app-namespace 10260:10260

# In another terminal, test with mongosh
mongosh "mongodb://docdbuser:YourSecurePassword123!@localhost:10260/?authMechanism=SCRAM-SHA-256&tls=false&directConnection=true"

# Or test with simple curl (get server status)
# MongoDB wire protocol uses binary, so curl won't work directly
# Use mongosh or a MongoDB client library
```

#### Quick Health Check

```powershell
# View all resources in app-namespace
kubectl get all -n app-namespace

# Check persistent volume claims
kubectl get pvc -n app-namespace

# View services
kubectl get svc -n app-namespace
```

#### View Resources in Azure Portal

This is the main goal - see your on-prem cluster and DocumentDB in Azure Portal:

1. Navigate to: **Azure Portal** → **Azure Arc** → **Kubernetes clusters**
2. Select your cluster: `aks-edge-documentdb`
3. Go to **Kubernetes resources** → **Workloads**
4. Click **Sign in with service account token**
5. Paste the token from Phase 7
6. Select namespace: `app-namespace`
7. View your `demo-documentdb-1` pod running on your Windows machine!

**Portal Links:**
- Arc Clusters: https://portal.azure.com/#view/Microsoft_Azure_HybridCompute/AzureArcCenterBlade/~/kubernetesServices

## Known Limitations

### IPv6 Not Supported

AKS Edge's Linux VM does not support IPv6 by default. The DocumentDB gateway may crash with:
```
Address family not supported by protocol
```

The PostgreSQL container runs fine, and the DocumentDB status may show "healthy", but the MongoDB-compatible gateway container may crash loop.

**Root cause:** The gateway binary tries to create an IPv6 listening socket (`[::]:10260`) which fails when the kernel doesn't have IPv6 support.

**Fix:** A one-line change in the gateway binary (`pg_documentdb_gw`) to fallback to IPv4 (`0.0.0.0:10260`) when IPv6 socket creation fails. This fix needs to be made in the [microsoft/documentdb](https://github.com/microsoft/documentdb) repository.

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|----------|
| **"Class not registered" error** | Using PowerShell 7 | Use Windows PowerShell 5.1 instead |
| **Invalid Azure Arc parameters** | Az PowerShell context not set | Run `Connect-AzAccount` and `Set-AzContext` |
| **Network validation fails** | IP conflict with host | Use different subnet (e.g., 192.168.200.0/24) |
| **ImagePullBackOff / DNS fails** | Network IP conflict | Check host IP and use non-overlapping subnet |
| **Pods stuck in Pending** | No storage class | Install local-path-provisioner (Phase 4) |
| **PVC stuck in Pending** | /opt is read-only | Patch configmap to use /tmp (Phase 4) |
| **Gateway CrashLoopBackOff** | IPv6 not supported | Known limitation - pending gateway fix |
| **Arc connection timeout** | Network/firewall issues | Ensure outbound HTTPS allowed |
| **Hyper-V not available** | Not enabled or unsupported CPU | Enable Hyper-V in Windows Features |
| **Deployment fails** | Insufficient resources | Increase CPU/RAM in config, ensure 40GB+ disk |
| **cert-manager webhook timeout** | DNS issues | Check DNS resolution, verify network settings |

### Useful Diagnostic Commands

```powershell
# AKS Edge status
Get-AksEdgeDeploymentInfo

# Check node network config
Invoke-AksEdgeNodeCommand -Command "cat /etc/resolv.conf"

# Node status
kubectl get nodes -o wide

# All pods across namespaces
kubectl get pods -A

# Events for troubleshooting
kubectl get events -n app-namespace --sort-by='.lastTimestamp'

# Pod logs
kubectl logs demo-documentdb-1 -n app-namespace --all-containers

# DocumentDB operator logs
kubectl logs -n documentdb-operator -l app.kubernetes.io/name=documentdb-operator

# Describe stuck pod
kubectl describe pod <pod-name> -n app-namespace

# Check storage
kubectl get pvc -n app-namespace
kubectl get storageclass
```

## Cleanup

### Remove DocumentDB Instance

```powershell
# Delete DocumentDB resource
kubectl delete documentdb demo-documentdb -n app-namespace

# Delete namespace
kubectl delete namespace app-namespace

# Uninstall DocumentDB operator
helm uninstall documentdb-operator -n documentdb-operator
kubectl delete namespace documentdb-operator

# Uninstall cert-manager
kubectl delete -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
```

### Disconnect from Azure Arc

```powershell
# Disconnect from Arc
az connectedk8s delete --name aks-edge-documentdb --resource-group aks-edge-rg --yes

# Delete resource group (removes Arc registration fully)
az group delete --name aks-edge-rg --yes --no-wait
```

### Remove AKS Edge Cluster

```powershell
# Remove AKS Edge deployment
Remove-AksEdgeDeployment -Force

# Verify removal
Get-AksEdgeDeploymentInfo
# Should show "No deployment found"
```

### Uninstall AKS Edge Essentials

```powershell
# Uninstall via winget
winget uninstall Microsoft.AKSEdge

# Or via Programs and Features / MSI
# Control Panel → Programs → Uninstall AKS Edge Essentials
```

## Success Criteria

| Check | Expected Result |
|-------|-----------------|
| AKS Edge cluster | Running with 1+ nodes |
| kubectl access | Can list nodes and pods |
| cert-manager | 3 pods running in cert-manager namespace |
| DocumentDB operator | 2 pods running in documentdb-operator namespace |
| DocumentDB instance | Status "Cluster in healthy state" |
| Pod health | demo-documentdb-1 showing 2/2 Ready |
| Arc connection | "Connected" status in Azure Portal |
| Portal visibility | Can see pods in Azure Arc → Kubernetes resources |

## Related Documentation

- [Azure AKS Edge Essentials Documentation](https://learn.microsoft.com/en-us/azure/aks/hybrid/aks-edge-overview)
- [DocumentDB on Standard AKS](../aks-setup/) - Cloud AKS deployment
- [Arc Hybrid Setup with Fleet](../arc-hybrid-setup-with-fleet/) - Multi-cluster management
- [TLS Configuration](../tls/) - Certificate setup options
- [Monitoring](../telemetry/) - OpenTelemetry and Prometheus integration

## Appendix: Configuration Reference

### Full Deployment Configuration Schema

```json
{
    "SchemaVersion": "1.14",
    "Version": "1.0",
    "DeploymentType": "SingleMachineCluster",
    "Init": {
        "ServiceIPRangeStart": "10.43.0.10",
        "ServiceIPRangeSize": 10
    },
    "Network": {
        "ControlPlaneEndpointIp": "192.168.1.100",
        "NetworkPlugin": "flannel",
        "Ip4AddressPrefix": "192.168.1.0/24",
        "Ip4GatewayAddress": "192.168.1.1",
        "DnsServers": ["8.8.8.8", "8.8.4.4"],
        "InternetDisabled": false
    },
    "User": {
        "AcceptEula": true,
        "AcceptOptionalTelemetry": false
    },
    "Machines": [
        {
            "LinuxNode": {
                "CpuCount": 4,
                "MemoryInMB": 8192,
                "DataSizeInGB": 40,
                "LogSizeInGB": 1
            }
        }
    ]
}
```

### Multi-Node Deployment (Advanced)

For higher availability, deploy with worker nodes:

```json
{
    "SchemaVersion": "1.14",
    "Version": "1.0", 
    "DeploymentType": "ScalableCluster",
    "Init": {
        "ServiceIPRangeStart": "10.43.0.10",
        "ServiceIPRangeSize": 20
    },
    "Network": {
        "ControlPlaneEndpointIp": "192.168.1.100",
        "NetworkPlugin": "flannel"
    },
    "User": {
        "AcceptEula": true
    },
    "Machines": [
        {
            "LinuxNode": {
                "CpuCount": 4,
                "MemoryInMB": 8192,
                "DataSizeInGB": 40
            }
        },
        {
            "LinuxNode": {
                "ControlPlane": false,
                "CpuCount": 4,
                "MemoryInMB": 8192,
                "DataSizeInGB": 40
            }
        }
    ]
}
```

### DocumentDB Resource Sizing

| Deployment Size | CPU | Memory | Storage | Use Case |
|-----------------|-----|--------|---------|----------|
| **Minimal** | 2 cores | 4GB | 5GB | Development/testing |
| **Standard** | 4 cores | 8GB | 20GB | Production workloads |
| **Enhanced** | 8 cores | 16GB | 50GB | High-throughput |

Adjust DocumentDB instance spec based on your needs:

```yaml
spec:
  nodeCount: 1
  resource:
    storage:
      pvcSize: 20Gi    # Adjust based on data volume
    limits:
      cpu: "2"
      memory: "4Gi"
    requests:
      cpu: "1"
      memory: "2Gi"
```
