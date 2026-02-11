# DocumentDB Kubernetes Operator - Azure Extension

Deploy DocumentDB Kubernetes Operator on any Kubernetes cluster using Azure extensions.

## Overview

This extension allows you to:
- Install DocumentDB Operator on **AKS** clusters (Azure-native)
- Install DocumentDB Operator on **any Kubernetes cluster** via Azure Arc (on-premises, edge, multi-cloud)
- View and manage the extension in Azure Portal
- Monitor extension health and status from Azure
- Unified billing across all cluster types (Phase 2)

### Supported Cluster Types

| Cluster Type | `--cluster-type` | Arc Agent Required? |
|--------------|------------------|--------------------|
| AKS (Azure) | `managedClusters` | No |
| EKS (AWS) | `connectedClusters` | Yes |
| GKE (GCP) | `connectedClusters` | Yes |
| On-premises | `connectedClusters` | Yes |

## Prerequisites

### For AKS Clusters

- Azure subscription
- AKS cluster (v1.26+)
- Azure CLI with `aks` and `k8s-extension` extensions

```bash
az extension add --name aks-preview
az extension add --name k8s-extension
```

### For Non-AKS Clusters (Arc-enabled)

- Azure subscription
- Kubernetes cluster (v1.26+)
- Azure CLI with `connectedk8s` and `k8s-extension` extensions
- `kubectl` configured to access your cluster

```bash
az extension add --name connectedk8s
az extension add --name k8s-extension
```

## Installation

### Option 1: AKS Clusters (No Arc Agent Needed)

```bash
# Login to Azure
az login
az account set --subscription <your-subscription-id>

# Install extension directly on AKS
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-aks-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \
  --release-train stable
```

Verify installation:
```bash
# Check extension status
az k8s-extension show \
  --name documentdb-operator \
  --cluster-name my-aks-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters

# Check pods
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
```

### Option 2: Arc-enabled Clusters (EKS, GKE, On-premises)

#### Step 1: Connect Your Cluster to Azure Arc (One-time)

```bash
# Login to Azure
az login
az account set --subscription <your-subscription-id>

# Create resource group (if needed)
az group create --name my-arc-rg --location eastus

# Connect cluster to Azure Arc
az connectedk8s connect \
  --name my-cluster \
  --resource-group my-arc-rg
```

Verify Arc agent is running:
```bash
kubectl get pods -n azure-arc
```

#### Step 2: Install DocumentDB Extension

```bash
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-cluster \
  --resource-group my-arc-rg \
  --cluster-type connectedClusters \
  --release-train stable
```

#### Step 3: Verify Installation

```bash
# Check extension status
az k8s-extension show \
  --name documentdb-operator \
  --cluster-name my-cluster \
  --resource-group my-arc-rg \
  --cluster-type connectedClusters

# Check pods in cluster
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
```

## Configuration Options

> **Note:** For all examples below, use `--cluster-type managedClusters` for AKS or `--cluster-type connectedClusters` for Arc-enabled clusters.

### Basic Configuration

```bash
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \  # or connectedClusters
  --configuration-settings documentDbVersion=0.1.3 \
  --configuration-settings replicaCount=1
```

### Enable WAL Replica Feature

```bash
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \  # or connectedClusters
  --configuration-settings walReplica=true
```

### Private Registry Authentication

If using a private container registry:

```bash
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \  # or connectedClusters
  --configuration-protected-settings registry.username=<username> \
  --configuration-protected-settings registry.password=<password>
```

## Managing the Extension

### Check Extension Status

```bash
# For AKS
az k8s-extension show \
  --name documentdb-operator \
  --cluster-name my-aks-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \
  --output table

# For Arc-enabled clusters
az k8s-extension show \
  --name documentdb-operator \
  --cluster-name my-arc-cluster \
  --resource-group my-rg \
  --cluster-type connectedClusters \
  --output table
```

### Upgrade Extension

```bash
az k8s-extension update \
  --name documentdb-operator \
  --cluster-name my-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \  # or connectedClusters
  --version 0.1.4
```

### Uninstall Extension

```bash
az k8s-extension delete \
  --name documentdb-operator \
  --cluster-name my-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \  # or connectedClusters
  --yes
```

### Query All Installations (Cross-Cluster)

Use Azure Resource Graph to find all DocumentDB installations across your subscriptions:

```bash
az graph query -q "
  resources
  | where type == 'microsoft.kubernetesconfiguration/extensions'
  | where properties.extensionType == 'Microsoft.DocumentDB.Operator'
  | extend clusterType = case(
      id contains 'managedClusters', 'AKS',
      id contains 'connectedClusters', 'Arc',
      'Unknown')
  | project subscriptionId, resourceGroup, 
      clusterName=split(id,'/')[8], clusterType, 
      version=properties.version
"
```

## Deploying DocumentDB Instances

After the operator is installed, deploy DocumentDB instances:

```yaml
# documentdb-instance.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-ns
---
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-ns
type: Opaque
stringData:
  username: docdbadmin
  password: YourSecurePassword123!
---
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: documentdb-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDBImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  gatewayImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
```

Apply the configuration:
```bash
kubectl apply -f documentdb-instance.yaml
```

## Azure Portal

Once installed, you can view and manage the extension in Azure Portal:

### For AKS Clusters
1. Navigate to **Kubernetes services**
2. Select your AKS cluster
3. Go to **Settings** > **Extensions**
4. Find **documentdb-operator**

### For Arc-enabled Clusters
1. Navigate to **Azure Arc** > **Kubernetes clusters**
2. Select your cluster
3. Go to **Extensions**
4. Find **documentdb-operator**

## Troubleshooting

### Extension Installation Fails

```bash
# Check extension status (use appropriate --cluster-type)
az k8s-extension show --name documentdb-operator \
  --cluster-name my-cluster --resource-group my-rg \
  --cluster-type managedClusters  # or connectedClusters

# For Arc-enabled clusters: Check Arc agent logs
kubectl logs -n azure-arc -l app.kubernetes.io/name=clusterconnect-agent

# Check operator logs
kubectl logs -n documentdb-operator -l app.kubernetes.io/name=documentdb-operator
```

### Pods Not Starting

```bash
# Check pod status
kubectl get pods -n documentdb-operator -o wide

# Describe pod for events
kubectl describe pod -n documentdb-operator <pod-name>

# Check CNPG operator
kubectl get pods -n cnpg-system
```

### Connectivity Issues

Ensure outbound connectivity to:
- `ghcr.io` (port 443) - Container images

**Additional for Arc-enabled clusters:**
- `*.servicebus.windows.net` (port 443)
- `*.guestconfiguration.azure.com` (port 443)

## Support

- [DocumentDB Operator Documentation](https://documentdb.io/documentdb-kubernetes-operator/preview/)
- [AKS Cluster Extensions](https://learn.microsoft.com/en-us/azure/aks/cluster-extensions)
- [Azure Arc Documentation](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/)
- [GitHub Issues](https://github.com/microsoft/documentdb-kubernetes-operator/issues)
