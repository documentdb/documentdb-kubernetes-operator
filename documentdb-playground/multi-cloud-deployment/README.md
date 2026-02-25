# Multi-Cloud DocumentDB Deployment

This directory contains templates and scripts for deploying DocumentDB across multiple cloud providers (Azure AKS, Google GKE, and AWS EKS) with cross-cloud replication using Istio service mesh and AKS Fleet for resource propagation.

## Architecture

- **Fleet Hub**: Deployed in East US 2 (for resource propagation)
- **Multi-Cloud Clusters**: 
  - **AKS**: Single member cluster in eastus2
  - **GKE**: Cluster in us-central1-a
  - **EKS**: Cluster in us-west-2
- **Network**: 
  - AKS: Uses default Azure CNI
  - GKE: Default GKE networking
  - EKS: Default EKS networking with internet-facing NLB for cross-cloud connectivity
- **Service Mesh**: Istio multi-cluster mesh for cross-cloud service discovery
- **VM Size**: Standard_DS3_v2 for AKS, e2-standard-4 for GKE, m5.large for EKS (configurable)
- **Node Count**: 1 nodes per cluster for cost optimization
- **Kubernetes Version**: Uses region default GA version (configurable)
- **DocumentDB**: Multi-cloud deployment with primary/replica architecture and Istio-based replication

## Prerequisites

- **Azure**: Azure CLI installed and logged in (`az login`)
- **GCP**: Google Cloud SDK installed and logged in (`gcloud auth login`)
  - gke-gcloud-auth-plugin: `sudo apt-get install google-cloud-cli-gke-gcloud-auth-plugin`
- **AWS**: AWS CLI installed and configured (`aws configure`)
  - eksctl installed for EKS cluster management
- **Kubernetes Tools**:
  - kubectl installed
  - kubelogin for Azure AD authentication: `az aks install-cli`
  - Helm 3.x installed
- **Other Tools**:
  - jq for JSON processing: `brew install jq` (macOS) or `apt-get install jq` (Linux)
  - openssl for password generation
- **Permissions**:
  - Azure: Contributor access to the subscription
  - GCP: Container Admin, Compute Network Admin, and Service Account User roles
  - AWS: Sufficient IAM permissions to create EKS clusters and IAM roles
- **Quotas**: Sufficient quota in target regions for clusters

## Quick Start

### Deploy Infrastructure

```bash
./deploy.sh
```

This single script will:
1. **Deploy Infrastructure**:
   - Create Azure resource group
   - Deploy AKS Fleet resource
   - Deploy AKS member cluster
   - Deploy GKE cluster 
   - Deploy EKS cluster with EBS CSI driver and AWS Load Balancer Controller
2. **Configure Multi-Cloud Mesh**:
   - Join GKE and EKS clusters to the AKS Fleet
   - Install cert-manager on all clusters
   - Set up Istio multi-cluster service mesh with shared root CA
   - Configure cross-cloud networking with east-west gateways
3. **Deploy DocumentDB Operator**:
   - Install DocumentDB operator on hub cluster
   - Propagate base resources (CRDs, RBAC) to all member clusters via Fleet
4. **Set Up Access**:
   - Configure kubectl contexts for all clusters
   - Set up RBAC access for Fleet

### Deploy DocumentDB Database

After the infrastructure is deployed:

```bash
# With auto-generated password
./deploy-documentdb.sh

# With custom password
./deploy-documentdb.sh "MySecureP@ssw0rd"

# Disable Azure DNS creation (for testing)
ENABLE_AZURE_DNS=false ./deploy-documentdb.sh
```

This will:
- Create cluster identification ConfigMaps on each member cluster
- Select a primary cluster (defaults to EKS cluster)
- Deploy DocumentDB with Istio-based cross-cloud replication
- Create Azure DNS zone with records for each cluster (if enabled)
- Create SRV record for primary connection string
- Provide connection information and failover commands

## Configuration

### Infrastructure Configuration

Edit `parameters.bicepparam` to customize AKS deployment:
- Hub cluster name (used for fleet naming)
- Hub region (fleet location)
- Member cluster name and region
- VM sizes
- Node counts
- Kubernetes version

Or use environment variables for all clouds:

```bash
# Azure AKS
export RESOURCE_GROUP="my-multi-cloud-rg"
export RG_LOCATION="eastus2"
export HUB_REGION="eastus2"
export AKS_CLUSTER_NAME="azure-documentdb"
export AKS_REGION="eastus2"
export HUB_VM_SIZE="Standard_D4s_v3"

# Google GKE
export PROJECT_ID="my-gcp-project-id"
export GCP_USER="user@example.com"
export ZONE="us-central1-a"
export GKE_CLUSTER_NAME="gcp-documentdb"

# AWS EKS
export EKS_CLUSTER_NAME="aws-documentdb"
export EKS_REGION="us-west-2"

# DocumentDB Operator
export VERSION="200"  # Operator version
export VALUES_FILE="/path/to/custom/values.yaml"  # Optional Operator images

./deploy.sh
```

### DocumentDB Configuration

Edit `documentdb-cluster.yaml` to customize:
- Database size and instances
- Replication settings (primary cluster, HA mode)
- Cross-cloud networking strategy (Istio)
- Storage class per environment
- Service exposure type
- Log levels

The template uses placeholders replaced at runtime:
- `{{DOCUMENTDB_PASSWORD}}`: The database password
- `{{PRIMARY_CLUSTER}}`: The selected primary cluster
- `{{CLUSTER_LIST}}`: YAML list of all clusters with their environments
- `{{GATEWAY_IMAGE}}`: Image to be used for the documentdb gateway
- `{{DOCUMENTDB_IMAGE}}`: Image to be used for the documentdb postgres backend

### Azure DNS Configuration

```bash
export ENABLE_AZURE_DNS="true"  # Enable/disable DNS creation
export AZURE_DNS_ZONE_NAME="my-documentdb-zone"  # DNS zone name (default: resource group name)
export AZURE_DNS_PARENT_ZONE_RESOURCE_ID="/subscriptions/.../dnszones/parent.zone"
```

## kubectl Contexts

After deployment, contexts are automatically configured for:
- `hub`: AKS Fleet hub cluster
- `azure-documentdb`: AKS member cluster (default name)
- `gcp-documentdb`: GKE cluster (default name)
- `aws-documentdb`: EKS cluster (default name)

## Management

### Check Deployment Status

```bash
# Check operator status on hub
kubectl --context hub get deploy -n documentdb-operator

# Check DocumentDB base resources propagation
kubectl --context hub get clusterresourceplacement documentdb-base -o wide

# Check DocumentDB cluster resources propagation
kubectl --context hub get clusterresourceplacement documentdb-crp -o wide

# View specific cluster
kubectl --context <cluster-name> get documentdb,pods -n documentdb-preview-ns
```

### Connect to Database

#### Via Port-Forward (for testing)

```bash
# Connect to primary cluster
kubectl --context <primary-cluster> port-forward \
  -n documentdb-preview-ns svc/documentdb-service-<cluster-name> 10260:10260

mongosh localhost:10260 -u docdb -p <password> \
  --authenticationMechanism SCRAM-SHA-256 --tls --tlsAllowInvalidCertificates
```

#### Via Azure DNS (production)

When `ENABLE_AZURE_DNS=true`, use the MongoDB SRV connection string:

```bash
mongosh "mongodb+srv://docdb:<password>@<zone-name>.<parent-zone>/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256"
```

Example:
```bash
mongosh "mongodb+srv://docdb:mypassword@documentdb-aks-fleet-rg.multi-cloud.pgmongo-dev.cosmos.windows-int.net/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256"
```

### Observability and Telemetry

The `telemetry` folder contains configuration files for setting up a comprehensive observability stack across your multi-cloud DocumentDB deployment:

#### Components

- **Prometheus**: Metrics collection and storage
- **Grafana**: Visualization and dashboards
- **OpenTelemetry Collector**: Unified telemetry collection (metrics, logs, traces)

#### Deploy Telemetry Stack

```bash
cd telemetry
./deploy-telemetry.sh
```

This script will:
1. Deploy OpenTelemetry Collector on all clusters
2. Install Prometheus on the azure-documentdb cluster
2. Install Grafana on the azure-documentdb cluster
4. Configure Prometheus to scrape DocumentDB metrics

#### Access Grafana Dashboard

```bash
# Port-forward to Grafana
kubectl --context hub port-forward -n monitoring svc/grafana 3000:80

# Open browser to http://localhost:3000
# Default credentials: admin/admin (change on first login)
```

From there you can import dashboard.json

#### Configuration Files

- **`deploy-telemetry.sh`**: Automated deployment script for the entire observability stack
- **`prometheus-values.yaml`**: Prometheus Helm chart configuration
- **`grafana-values.yaml`**: Grafana Helm chart configuration with dashboard provisioning
- **`otel-collector.yaml`**: OpenTelemetry Collector configuration for metrics and logs
- **`dashboard.json`**: Pre-built Grafana dashboard for DocumentDB monitoring

#### Custom Configuration

Edit the values files to customize:
- Prometheus retention period and storage
- Grafana plugins and data sources
- OpenTelemetry Collector pipelines and exporters
- Dashboard refresh intervals and panels

### Failover Operations

Failover is performed using the DocumentDB kubectl plugin:

```bash
kubectl documentdb promote \
  --documentdb documentdb-preview \
  --namespace documentdb-preview-ns \
  --hub-context hub \
  --target-cluster <new-primary-cluster> \
  --cluster-context <new-primary-cluster>
```

## Fleet Management

```bash
# Show AKS fleet details
az fleet show --name <fleet-name> --resource-group $RESOURCE_GROUP

# List fleet members (includes Azure members only, not cross-cloud)
az fleet member list --fleet-name <fleet-name> --resource-group $RESOURCE_GROUP

# Check multi-cloud fleet membership (GKE and EKS)
kubectl --context hub get membercluster
```

## Multi-Cloud Mesh Management

### Verify Istio Installation

```bash
# Check Istio components on each cluster
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get pods -n istio-system
  echo
done

# Verify east-west gateway services
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get svc -n istio-system istio-eastwestgateway
  echo
done
```

### Verify Cross-Cloud Connectivity

```bash
# Check remote secrets (for service discovery)
kubectl --context azure-documentdb get secrets -n istio-system | grep "istio-remote-secret"
kubectl --context gcp-documentdb get secrets -n istio-system | grep "istio-remote-secret"
kubectl --context aws-documentdb get secrets -n istio-system | grep "istio-remote-secret"

# Verify mesh network configuration
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get namespace istio-system --show-labels
  echo
done
```

## DocumentDB Management

### Check Deployment Status

```bash
# Quick status across all clusters
for c in azure-documentdb gcp-documentdb aws-documentdb; do 
  echo "=== $c ==="
  kubectl --context $c get documentdb,pods -n documentdb-preview-ns 2>/dev/null || echo 'Not deployed yet'
  echo
done

# Check operator status on all clusters
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get deploy -n documentdb-operator
  kubectl --context $cluster get pods -n documentdb-operator
done
```

### Monitor Replication

```bash
# Monitor all DocumentDB instances
watch 'for c in azure-documentdb gcp-documentdb aws-documentdb; do \
  echo "=== $c ==="; \
  kubectl --context $c get documentdb,pods -n documentdb-preview-ns; \
  echo; \
done'

# Check DocumentDB service endpoints
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get svc -n documentdb-preview-ns
  echo
done
```

### Verify Cross-Cloud Replication

```bash
# Check WAL replica status in Istio mesh
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get pods -n documentdb-preview-ns -l component=wal-replica
  echo
done

# Verify Istio sidecar injection
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster get pods -n documentdb-preview-ns -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].name}{"\n"}{end}'
  echo
done
```

### Azure DNS Management

```bash
# List DNS records for DocumentDB
az network dns record-set list \
  --zone-name <zone-name> \
  --resource-group $RESOURCE_GROUP \
  --output table

# Show SRV record for MongoDB connection
az network dns record-set srv show \
  --name "_mongodb._tcp" \
  --zone-name <zone-name> \
  --resource-group $RESOURCE_GROUP

# Show A/CNAME records for each cluster
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  az network dns record-set a show --name $cluster --zone-name <zone-name> --resource-group $RESOURCE_GROUP 2>/dev/null || \
  az network dns record-set cname show --name $cluster --zone-name <zone-name> --resource-group $RESOURCE_GROUP 2>/dev/null || \
  echo "Record not found"
  echo
done
```

## RBAC Management

The deployment script automatically assigns the "Azure Kubernetes Fleet Manager RBAC Cluster Admin" role for AKS Fleet access. To manage RBAC:

```bash
# View current role assignment
az role assignment list --assignee $IDENTITY --scope $FLEET_ID

# Add another user
az role assignment create --role "Azure Kubernetes Fleet Manager RBAC Cluster Admin" \
  --assignee <user-id> --scope $FLEET_ID
```

For GCP and AWS, ensure you have appropriate IAM permissions configured via `gcloud` and `aws` CLI.

## Troubleshooting

### Authentication Issues

**Azure AKS:**
```bash
# Get fleet credentials
az fleet get-credentials --resource-group $RESOURCE_GROUP --name <fleet-name>

# If web authentication is blocked, use Azure CLI
kubelogin convert-kubeconfig -l azurecli
```

**Google GKE:**
```bash
# Refresh credentials
gcloud container clusters get-credentials <cluster-name> --zone <zone>

# Verify authentication
gcloud auth list
gcloud config get-value account
```

**AWS EKS:**
```bash
# Update kubeconfig
aws eks update-kubeconfig --name <cluster-name> --region <region>

# Verify IAM identity
aws sts get-caller-identity
```

### Istio Mesh Issues

```bash
# Verify Istio installation
istioctl --context <cluster-name> version

# Check proxy status
istioctl --context <cluster-name> proxy-status

# Verify mesh configuration
istioctl --context <cluster-name> analyze

# Check east-west gateway connectivity
kubectl --context <cluster-name> get svc -n istio-system istio-eastwestgateway

# Verify remote secrets
kubectl --context <cluster-name> get secrets -n istio-system | grep istio-remote-secret
```

### EKS-Specific Issues

**EBS CSI Driver:**
```bash
# Check CSI driver status
kubectl --context aws-documentdb get pods -n kube-system -l app=ebs-csi-controller

# Verify storage class
kubectl --context aws-documentdb get storageclass documentdb-storage
```

**AWS Load Balancer Controller:**
```bash
# Check controller status
kubectl --context aws-documentdb get pods -n kube-system -l app.kubernetes.io/name=aws-load-balancer-controller

# Verify subnet tags
VPC_ID=$(aws eks describe-cluster --name $EKS_CLUSTER_NAME --region $EKS_REGION --query 'cluster.resourcesVpcConfig.vpcId' --output text)
aws ec2 describe-subnets --filters "Name=vpc-id,Values=$VPC_ID" --query 'Subnets[].{ID:SubnetId,Tags:Tags}' --region $EKS_REGION
```

### DNS Issues

```bash
# Verify DNS zone exists
az network dns zone show --name <zone-name> --resource-group $RESOURCE_GROUP

# Check DNS records
az network dns record-set list --zone-name <zone-name> --resource-group $RESOURCE_GROUP

# Test DNS resolution
nslookup <cluster-name>.<zone-name>.<parent-zone>
nslookup _mongodb._tcp.<zone-name>.<parent-zone> -type=SRV
```

### Cross-Cloud Connectivity

```bash
# Deploy test pod with network tools
kubectl --context azure-documentdb run test-pod --image=nicolaka/netshoot -it --rm -- /bin/bash

# From within the pod, test connectivity to other clusters
# Using Istio service discovery
curl -v http://documentdb-service-gcp-documentdb.documentdb-preview-ns.svc.cluster.local:10260
curl -v http://documentdb-service-aws-documentdb.documentdb-preview-ns.svc.cluster.local:10260
```

### Debugging

```bash
# Check operator logs on member clusters
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  echo "=== $cluster ==="
  kubectl --context $cluster logs -n documentdb-operator deployment/documentdb-operator --tail=50
  echo
done

# View DocumentDB resource status
kubectl --context <cluster> describe documentdb documentdb-preview -n documentdb-preview-ns

# Check Istio sidecar logs
kubectl --context <cluster> logs -n documentdb-preview-ns <pod-name> -c istio-proxy
```

## Clean Up

```bash
# Delete DocumentDB resources from all clusters
kubectl --context hub delete clusterresourceplacement documentdb-crp
kubectl --context hub delete namespace documentdb-preview-ns

# Wait for namespace deletion to complete on all clusters
for cluster in azure-documentdb gcp-documentdb aws-documentdb; do
  kubectl --context $cluster wait --for=delete namespace/documentdb-preview-ns --timeout=60s || true
done

# Delete base operator resources
kubectl --context hub delete clusterresourceplacement documentdb-base

# Delete entire Azure resource group (includes AKS fleet and member)
az group delete --name $RESOURCE_GROUP --yes --no-wait

# Delete GKE cluster
gcloud container clusters delete $GKE_CLUSTER_NAME \
  --zone $ZONE \
  --project $PROJECT_ID \
  --quiet

# Delete EKS cluster (also deletes associated IAM roles and service accounts)
eksctl delete cluster --name $EKS_CLUSTER_NAME --region $EKS_REGION

# Delete Azure DNS zone (if created)
az network dns zone delete \
  --name <zone-name> \
  --resource-group $RESOURCE_GROUP \
  --yes

# Clean up local kubectl contexts
kubectl config delete-context hub
kubectl config delete-context azure-documentdb
kubectl config delete-context gcp-documentdb
kubectl config delete-context aws-documentdb
```

## Scripts

- **`deploy.sh`**: All-in-one multi-cloud deployment (AKS Fleet + GKE + EKS + cert-manager + Istio mesh + operator)
- **`deploy-documentdb.sh`**: Deploy multi-cloud DocumentDB with Istio-based replication and optional Azure DNS
- **`main.bicep`**: Bicep template for AKS Fleet and single member cluster
- **`parameters.bicepparam`**: Configuration parameters for AKS deployment
- **`documentdb-base.yaml`**: Fleet ClusterResourcePlacement for base resources (CRDs, RBAC, namespaces)
- **`documentdb-cluster.yaml`**: DocumentDB multi-cloud configuration template with Fleet ClusterResourcePlacement

## Key Features

- **Multi-Cloud Architecture**: Deploy across Azure AKS, Google GKE, and AWS EKS
- **Istio Service Mesh**: Cross-cloud service discovery and secure communication
- **Automated Mesh Setup**: Shared root CA, east-west gateways, and remote secrets
- **AKS Fleet Integration**: Resource propagation via ClusterResourcePlacement to all clouds
- **Cross-Cloud Replication**: DocumentDB replication using Istio for connectivity
- **Dynamic Discovery**: Automatically configures all clusters and generates failover commands
- **Azure DNS Integration**: Optional DNS zone creation with A/CNAME and SRV records for MongoDB
- **Cloud-Specific Configuration**: 
  - EKS: EBS CSI driver and AWS Load Balancer Controller
  - GKE: Default persistent disk provisioner
  - AKS: Azure Disk CSI driver
- **Parallel Deployment**: AKS, GKE, and EKS deployed concurrently for faster setup
- **Smart Defaults**: Sensible defaults with environment variable overrides

## Additional Resources

- [Azure AKS Fleet Documentation](https://learn.microsoft.com/en-us/azure/kubernetes-fleet/)
- [AKS Authentication Guide](https://learn.microsoft.com/en-us/azure/aks/kubelogin-authentication)
- [Fleet ClusterResourcePlacement API](https://learn.microsoft.com/en-us/azure/kubernetes-fleet/concepts-resource-propagation)
- [Istio Multi-Cluster Installation](https://istio.io/latest/docs/setup/install/multicluster/)
- [Istio Multi-Primary Multi-Network](https://istio.io/latest/docs/setup/install/multicluster/multi-primary_multi-network/)
- [Google GKE Documentation](https://cloud.google.com/kubernetes-engine/docs)
- [AWS EKS Documentation](https://docs.aws.amazon.com/eks/)
- [AWS Load Balancer Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/)
- [eksctl Documentation](https://eksctl.io/)
- [DocumentDB Kubernetes Operator Documentation](../../README.md)