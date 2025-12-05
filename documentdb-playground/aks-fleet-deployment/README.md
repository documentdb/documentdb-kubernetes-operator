# AKS Multi-Cluster Deployment with KubeFleet

This directory contains templates for deploying AKS clusters across different
Azure regions with full mesh VNet peering, managed by the open-source KubeFleet
multi-cluster orchestration system, along with DocumentDB operator and
multi-region database deployment capabilities.

## Architecture

- **Hub Cluster**: West US 3 cluster serves as both hub and member (dual role)
- **Member Clusters**: Dynamically discovered and deployed across available regions (westus3, uksouth, eastus2)
- **Fleet Management**: KubeFleet open-source project for multi-cluster orchestration
- **Network**: Full mesh VNet peering between all clusters
- **VM Size**: Standard_DS2_v2 (configurable)
- **Node Count**: 2 nodes per cluster 
- **Kubernetes Version**: Uses region default GA version (configurable)
- **DocumentDB**: Multi-region deployment with primary/replica architecture

## Prerequisites

- Azure CLI installed and logged in
- Sufficient quota in target regions for AKS clusters
- Contributor access to the subscription
- kubelogin for Azure AD authentication: `az aks install-cli`
- Helm 3.x installed
- jq for JSON processing: `brew install jq` (macOS) or `apt-get install jq` (Linux)
- docker (for building KubeFleet agent images)
- git (for cloning KubeFleet repository)
- base64 utility

## Deployment

### 1. Deploy AKS Fleet Infrastructure

```bash
./deploy-fleet-bicep.sh

# With customer resource group name
# This will need to be set for all other scripts as well
export RESOURCE_GROUP=<resource group name>
./deploy-fleet-bicep.sh
```

The script will:
1. Create a resource group (default if not set: `documentdb-aks-fleet-rg`)
2. Create VNets with non-overlapping IP ranges (including hub VNet at 10.0.0.0/16)
3. Deploy clusters in each region
4. Configure full mesh VNet peering between all clusters (hub + members)
5. Install KubeFleet hub-agent on the westus3 cluster
6. Install KubeFleet member-agent on all member clusters (including westus3)
7. Set up kubectl aliases for easy cluster access

### 2. Install cert-manager

Install cert-manager on all member clusters:

```bash
./install-cert-manager.sh
```

This script will:
- Add the jetstack Helm repository
- Install cert-manager with CRDs on each member cluster
- Wait for deployments to be ready
- Install cert-manager CRDs on the hub
- Display pod status for verification

### 3. Install DocumentDB Operator

Deploy the DocumentDB operator using Fleet:

```bash
./install-documentdb-operator.sh
```

This script will:
- Package the local DocumentDB chart
- Deploy the operator via Helm
- Verify deployment across all member clusters
- Show operator pod status on each cluster

### 4. Deploy Multi-Region DocumentDB

Deploy a multi-region DocumentDB cluster with replication:

```bash
# With auto-generated password
./deploy-multi-region.sh

# With custom password
./deploy-multi-region.sh "MySecureP@ssw0rd"

# With environment variable
export DOCUMENTDB_PASSWORD="MySecureP@ssw0rd"
./deploy-multi-region.sh
```

This will:
- **Dynamically discover** all member clusters in the resource group
- **Create cluster identification ConfigMaps** on each cluster for tracking
- **Select a primary cluster** (prefers eastus2, or uses first available)
- **Deploy DocumentDB** with cross-region replication
- **Configure replicas** in all other discovered regions
- **Provide connection information** and failover commands

### Key Features of Multi-Region Deployment

- **Dynamic Cluster Discovery**: No hardcoded cluster names - automatically finds all member clusters
- **Smart Primary Selection**: Automatically selects the best primary cluster
- **Cluster Identification**: Creates ConfigMaps to identify each cluster with name and region
- **Resource Management**: Handles existing resources with options to delete, update, or cancel
- **Generated Failover Commands**: Provides ready-to-use commands for failover to any region

## Configuration

### Fleet Configuration

Edit `parameters.bicepparam` to customize:
- Hub cluster name (used for fleet naming)
- Hub region (fleet location)
- Member regions
- VM sizes
- Node counts
- Kubernetes version

### DocumentDB Configuration

Edit `multi-region.yaml` to customize:
- Database size and instances
- Primary region selection (handled dynamically by script)
- Replication settings
- Service exposure type
- Log levels

The template uses placeholders that are replaced at runtime:
- `${DOCUMENTDB_PASSWORD}`: The database password
- `${PRIMARY_CLUSTER}`: The selected primary cluster
- `${CLUSTER_LIST}`: The list of all discovered clusters

### Network Configuration

Default VNet ranges:
- West US 3 VNet: 10.1.0.0/16
- UK South VNet: 10.2.0.0/16
- East US 2 VNet: 10.3.0.0/16

All VNets are peered in a full mesh topology for direct cluster-to-cluster communication.

## kubectl Aliases

After deployment, these aliases are available:
- `k-westus3`: Access to West US 3 cluster
- `k-uksouth`: Access to UK South cluster
- `k-eastus2`: Access to East US 2 cluster

Load aliases:
```bash
source ~/.bashrc
```

## Fleet Management

```bash
# List member clusters
k-westus3 get membercluster

# Show member cluster details
k-westus3 describe membercluster <cluster-name>

# Check ClusterResourcePlacement status
k-westus3 get clusterresourceplacement

# View placement details
k-westus3 describe clusterresourceplacement documentdb-crp

# Check KubeFleet hub agent status
k-westus3 get pods -n fleet-system-hub
```

## DocumentDB Management

### Check Deployment Status

```bash
# Check operator status on all clusters
for cluster in $(az aks list -g $RESOURCE_GROUP -o json | jq -r '.[] | select(.name|startswith("member-")) | .name'); do
  echo "=== $cluster ==="
  kubectl --context $cluster get deploy -n documentdb-operator
  kubectl --context $cluster get pods -n documentdb-operator
done
```

### Connect to Database

```bash
# Port forward to primary (default eastus2)
k-eastus2 port-forward \
  -n documentdb-preview-ns svc/documentdb-preview 10260:10260

# Get connection string (auto-displayed after deployment)
k-eastus2 get documentdb -n documentdb-preview-ns -A -o json | \
  jq ".items[0].status.connectionString"
```

### Failover Operations

The deployment script generates failover commands for each region:

```bash
# Example: Failover to westus3
k-westus3 patch documentdb documentdb-preview -n documentdb-preview-ns \
  --type='merge' -p '{"spec":{"clusterReplication":{"primary":"member-westus3-xxx"}}}'
```

### Monitor Replication

```bash
# Watch ClusterResourcePlacement status
watch 'k-westus3 get clusterresourceplacement documentdb-crp -o wide'

# Monitor all DocumentDB instances
watch 'for c in $(az aks list -g $RESOURCE_GROUP -o json | jq -r ".[] | select(.name|startswith(\"member-\")) | .name"); do \
  echo "=== $c ==="; \
  kubectl --context $c get documentdb,pods -n documentdb-preview-ns; \
  echo; \
done'
```

## KubeFleet Management

KubeFleet uses standard Kubernetes RBAC on the hub cluster. To manage access:

```bash
# View KubeFleet components
k-westus3 get all -n fleet-system

# Check hub agent logs
k-westus3 logs -n fleet-system -l app=hub-agent

# Check member agent logs (on any member cluster)
k-westus3 logs -n fleet-system -l app=member-agent
```

## Network Verification

Test connectivity between clusters:

```bash
# Deploy a test pod in one cluster
kubectl --context member-westus3-xxx run test-pod --image=nicolaka/netshoot -it --rm -- /bin/bash

# From within the pod, ping services in other clusters
```

Verify VNet peering:

```bash
az network vnet peering list --resource-group $RESOURCE_GROUP \
  --vnet-name member-westus3-vnet --output table
```

## Backup and Restore
### Backup

Create a one-time backup:
```bash
kubectl --context hub apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: backup-documentdb
  namespace: documentdb-preview-ns
spec:
  cluster:
    name: documentdb-preview
EOF
```

Create automatic backups on a schedule:
```bash
kubectl --context hub apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: ScheduledBackup
metadata:
  name: scheduled-backup
  namespace: documentdb-preview-ns
spec:
  cluster:
    name: documentdb-preview
  schedule: "0 2 * * *" # Daily at 2 AM
EOF
```

Backups will be created on the primary cluster. 

### Restore

Step 1: Identify Available Backups
```bash
PRIMARY_CLUSTER=$(k-westus3 get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.primary}')

kubectl --context $PRIMARY_CLUSTER get backups -n documentdb-preview-ns
```

Step 2: Modify multi-region.yaml for Restore

Important: Restores must be to a new DocumentDB resource with a different name.

Edit `./multi-region.yaml` and change:
1. The DocumentDB resource name (e.g., documentdb-preview-restore)
2. Add the bootstrap section with backup reference

Example:
```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview-restore  # New name, different from original
  namespace: documentdb-preview-ns
spec:
  ...
  bootstrap:
    recovery:
      backup:
        name: scheduled-backup-xxxxxx  # Name of the backup to restore from
```

Step 3: Deploy the Restored Cluster

Run the deployment script:
```bash
./deploy-multi-region.sh "${DOCUMENTDB_PASSWORD}"
```

## Troubleshooting

### Authentication Issues

If you encounter authentication issues:

```bash
# Get credentials for hub cluster
az aks get-credentials --resource-group $RESOURCE_GROUP --name $HUB_CLUSTER --overwrite-existing

# If web authentication is blocked, switch to Azure CLI authentication
kubelogin convert-kubeconfig -l azurecli
```

### DocumentDB Operator Crashes

If the operator crashes with "exec format error":
1. Check the image architecture matches your nodes (amd64)
2. Review `operator/documentdb-helm-chart/values.yaml` for correct image settings
3. Ensure `forceArch: amd64` is set in values.yaml

### KubeFleet Resource Propagation

If resources aren't propagating to member clusters:

```bash
# Check ClusterResourcePlacement status
k-westus3 get clusterresourceplacement documentdb-crp -o yaml

# Check for placement conditions
k-westus3 describe clusterresourceplacement documentdb-crp

# Verify member clusters are joined
k-westus3 get membercluster

# Check hub agent logs
k-westus3 logs -n fleet-system -l app=hub-agent --tail=100
```

### Cluster List Formatting Issues

If the cluster list in the YAML is not formatted correctly:
1. Check that `jq` is installed: `which jq`
2. Verify clusters are discovered: `az aks list -g $RESOURCE_GROUP -o json | jq -r '.[] | select(.name|startswith("member-")) | .name'`
3. Review the generated YAML preview shown during deployment

### Debugging

Use different kubectl verbosity levels:

```bash
kubectl get pods --v=4  # Debug level
kubectl get pods --v=6  # Shows API requests
kubectl get pods --v=8  # Detailed API troubleshooting
```

## Clean Up

To delete all resources:

```bash
# Delete DocumentDB resources first
k-westus3 delete -f multi-region.yaml


# Delete the entire resource group
az group delete --name $RESOURCE_GROUP --yes --no-wait
```

## Scripts in this Directory

- `deploy-fleet-bicep.sh`: Main deployment script (deploys AKS clusters, installs KubeFleet)
- `install-cert-manager.sh`: Installs cert-manager on all member clusters
- `install-documentdb-operator.sh`: Deploys DocumentDB operator via KubeFleet
- `deploy-multi-region.sh`: Deploys multi-region DocumentDB with dynamic cluster discovery
- `main.bicep`: Bicep template for cluster deployment
- `parameters.bicepparam`: Parameter file for Bicep deployment
- `multi-region.yaml`: Multi-region DocumentDB configuration template with placeholders

## Key Improvements in Latest Version

1. **Dynamic Cluster Discovery**: No hardcoded cluster names - automatically discovers all deployed clusters
2. **Cluster Identification**: Creates ConfigMaps to track cluster identity and region
3. **Smart Primary Selection**: Automatically selects optimal primary cluster
4. **Resource Conflict Handling**: Detects existing resources and provides options
5. **Generated Commands**: Produces ready-to-use failover and monitoring commands
6. **Better Error Handling**: Improved validation and error messages
7. **Connection String Display**: Shows actual DocumentDB connection string from status

## Additional Resources

- [KubeFleet Documentation](https://kubefleet.dev/docs/)
- [KubeFleet Getting Started](https://kubefleet.dev/docs/getting-started/on-prem/)
- [KubeFleet GitHub Repository](https://github.com/kubefleet-dev/kubefleet)
- [AKS Authentication Guide](https://learn.microsoft.com/en-us/azure/aks/kubelogin-authentication)
- [DocumentDB Kubernetes Operator Documentation](../../README.md)