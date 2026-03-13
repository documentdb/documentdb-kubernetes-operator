# DocumentDB on Azure Kubernetes Service (AKS)

This directory contains comprehensive automation scripts for deploying DocumentDB on Azure Kubernetes Service (AKS) with production-ready configurations.

## 🚀 Quick Start

### Prerequisites
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli) installed and configured
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/) installed
- [Helm](https://helm.sh/docs/intro/install/) v3.0+ installed
- [mongosh](https://www.mongodb.com/try/download/shell) (MongoDB Shell) for testing connections
- Azure subscription with appropriate permissions

### Basic Usage

```bash
# Login to Azure
az login

# Create cluster with DocumentDB instance (recommended)
cd scripts
./create-cluster.sh --deploy-instance

# Clean up when done
./delete-cluster.sh
```

## 📋 Features

### ✅ **Automated AKS Setup**
- Complete AKS cluster with managed node pools
- Azure CNI networking with network policies
- Cluster autoscaler (2-5 nodes)
- Monitoring addon enabled
- Managed identity integration

### ✅ **Storage & Networking**
- Azure Disk CSI driver (uses StandardSSD_LRS by default)
- Azure File CSI driver for shared storage
- Azure Load Balancer (Standard SKU)
- Optional Premium SSD storage class for production

### ✅ **DocumentDB Integration**
- Enhanced DocumentDB operator with Azure support
- Automatic Azure LoadBalancer annotations
- Environment-specific configuration (`environment: aks`)
- Uses AKS default StandardSSD_LRS storage (Premium SSD optional)

### ✅ **Production Features**
- cert-manager for TLS certificate management
- Comprehensive resource cleanup
- Multi-environment support
- Resource tagging and organization

## 🛠️ Scripts Overview

### `create-cluster.sh`
Creates a complete AKS environment with all dependencies.

```bash
# Options
./create-cluster.sh [OPTIONS]

# Key options:
--deploy-instance      # Deploy DocumentDB instance (recommended)
--install-operator     # Install operator only (no instance)
--create-storage-class # Create Premium SSD storage class (optional)
--skip-storage-class   # Use AKS default StandardSSD_LRS (default)
--cluster-name NAME    # Custom cluster name
--resource-group RG    # Custom resource group
--location LOCATION    # Azure region
--github-username USER # GitHub username for operator
--github-token TOKEN   # GitHub token for operator
```

### `delete-cluster.sh`
Comprehensively removes all Azure resources and stops billing.

```bash
# Safe deletion with confirmation
./delete-cluster.sh

# Force deletion without prompts
./delete-cluster.sh --force

# Custom resource group
./delete-cluster.sh --resource-group my-rg
```

## 🏗️ Architecture

### **Azure Resources Created:**
- **AKS Cluster**: Managed Kubernetes with Azure CNI
- **Node Pool**: Standard_D4s_v5 VMs (3 nodes) with autoscaling (2-5)
- **Load Balancer**: Standard SKU for public access
- **Managed Identity**: For secure Azure resource access
- **Storage**: Standard SSD by default (Premium SSD optional)
- **Networking**: Virtual network with security policies

### **Kubernetes Components:**
- **DocumentDB Operator**: Version 0.1.3 with Azure features
- **CNPG**: CloudNative PostgreSQL for data persistence
- **cert-manager**: Certificate lifecycle management
- **Azure CSI Drivers**: Disk and File storage integration

## 🔧 Configuration

### **Default Settings:**
```bash
CLUSTER_NAME="documentdb-cluster"
RESOURCE_GROUP="documentdb-rg"
LOCATION="westus2"
NODE_COUNT=3
NODE_SIZE="Standard_D4s_v5"
KUBERNETES_VERSION="1.34.3"
OPERATOR_CHART_VERSION="0.1.3"
```

### **Storage Configuration:**
By default, uses AKS built-in StandardSSD_LRS storage. For production, optionally create Premium SSD:

```bash
# Use AKS default (StandardSSD_LRS) - recommended for development
./create-cluster.sh --deploy-instance

# Use Premium SSD - recommended for production
./create-cluster.sh --deploy-instance --create-storage-class
```

Custom Premium SSD storage class (created with `--create-storage-class`):
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: documentdb-storage
provisioner: disk.csi.azure.com
parameters:
  skuName: Premium_LRS    # Premium SSD
  kind: Managed          # Azure Managed Disks
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Retain
```

### **Azure LoadBalancer Annotations:**
The enhanced operator automatically applies Azure-specific annotations:

```yaml
annotations:
  service.beta.kubernetes.io/azure-load-balancer-internal: "false"
  service.beta.kubernetes.io/azure-load-balancer-mode: "auto"
  service.beta.kubernetes.io/azure-pip-name: "documentdb-pip"
```

## 📖 Usage Examples

### **Complete Deployment:**
```bash
# Development setup (uses AKS default StandardSSD_LRS)
./create-cluster.sh --deploy-instance

# Production setup (uses Premium SSD)
./create-cluster.sh --deploy-instance --create-storage-class

# With enhanced operator features
export GITHUB_USERNAME="your-username"
export GITHUB_TOKEN="your-token"
./create-cluster.sh --deploy-instance
```

### **Step-by-Step Deployment:**
```bash
# 1. Create basic cluster
./create-cluster.sh

# 2. Install operator separately
./create-cluster.sh --install-operator

# 3. Deploy DocumentDB instance manually
kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: default
spec:
  environment: aks
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 20Gi
      # storageClass omitted - uses AKS default (StandardSSD_LRS)
      # Or specify: storageClass: documentdb-storage  # for Premium SSD
  exposeViaService:
    serviceType: LoadBalancer
EOF
```

### **Custom Configuration:**
```bash
# Custom cluster in different region
./create-cluster.sh \
  --cluster-name "prod-documentdb" \
  --resource-group "prod-rg" \
  --location "West US 2" \
  --deploy-instance
```

## 🔍 Monitoring & Troubleshooting

### **Check Cluster Status:**
```bash
# Verify cluster
kubectl get nodes
kubectl get pods --all-namespaces

# Check DocumentDB
kubectl get documentdb -A
kubectl get pvc -A

# Monitor LoadBalancer
kubectl get svc -A -w
```

### **Access DocumentDB:**
```bash
# Get connection string (easiest method)
kubectl get dbs -A -o wide
# Output includes full connection string with status

# Get external IP only
kubectl get svc documentdb-service-sample-documentdb -n documentdb-instance-ns

# Get credentials from secret
kubectl get secret documentdb-credentials -n documentdb-instance-ns -o jsonpath='{.data.username}' | base64 -d
kubectl get secret documentdb-credentials -n documentdb-instance-ns -o jsonpath='{.data.password}' | base64 -d

# Connection string format
mongodb://username:password@EXTERNAL-IP:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0
```

### **Common Issues:**

**Issue: LoadBalancer pending**
```bash
# Check Azure quota and subnet configuration
az aks show --resource-group RESOURCE_GROUP --name CLUSTER_NAME --query networkProfile
```

**Issue: PVC binding failures**
```bash
# Check storage class and CSI drivers
kubectl get storageclass
kubectl get pods -n kube-system | grep csi-azuredisk
```

**Issue: Operator not starting**
```bash
# Check operator logs
kubectl logs -n documentdb-operator deployment/documentdb-operator
```

## 💰 Cost Management

### **Estimated Monthly Costs (West US 2):**
- **AKS Cluster**: ~$73/month (managed control plane)
- **3x Standard_D4s_v5 VMs**: ~$420/month
- **Standard SSD Storage (10GB)**: ~$1/month
- **Standard Load Balancer**: ~$18/month
- **Total**: ~$512/month

### **Cost Optimization:**
```bash
# Use smaller VMs for development
NODE_SIZE="Standard_B2s"  # ~$30/month per node

# Reduce node count
NODE_COUNT=1

# Use Standard SSD (default) instead of Premium
# storageClass omitted in DocumentDB spec uses AKS default (StandardSSD_LRS)
```

### **Cleanup:**
```bash
# Always clean up when done to avoid charges
./delete-cluster.sh --force
```

## 🔐 Security

### **Built-in Security Features:**
- Azure managed identity (no service principal needed)
- Network policies enabled
- Encryption at rest for storage
- TLS for all communications
- Azure RBAC integration

### **Additional Security:**
```bash
# Enable Azure Policy for AKS
az aks enable-addons --resource-group RESOURCE_GROUP --name CLUSTER_NAME --addons azure-policy

# Enable Azure Key Vault integration
az aks enable-addons --resource-group RESOURCE_GROUP --name CLUSTER_NAME --addons azure-keyvault-secrets-provider
```

## 📚 Additional Resources

- [AKS Documentation](https://docs.microsoft.com/en-us/azure/aks/)
- [Azure CNI Networking](https://docs.microsoft.com/en-us/azure/aks/configure-azure-cni)
- [Azure Load Balancer](https://docs.microsoft.com/en-us/azure/load-balancer/)
- [DocumentDB Operator GitHub](https://github.com/documentdb/documentdb-operator)
- [MongoDB Shell (mongosh)](https://www.mongodb.com/try/download/shell)

## 🆘 Support

For issues specific to:
- **AKS**: Check Azure support documentation
- **DocumentDB Operator**: Open issues on the GitHub repository
- **Scripts**: Review logs and check prerequisites

---

**⚠️  Remember to run `./delete-cluster.sh` when done to avoid Azure charges!**