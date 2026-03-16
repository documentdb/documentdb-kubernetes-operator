---
title: Multi-Region Setup Guide
description: Step-by-step instructions for deploying DocumentDB across multiple Kubernetes clusters with replication, prerequisites, and configuration examples.
tags:
  - multi-region
  - setup
  - deployment
  - replication
---

# Multi-Region Setup Guide

This guide walks through deploying DocumentDB across multiple Kubernetes clusters with regional replication.

## Prerequisites

### Infrastructure Requirements

Before deploying DocumentDB in multi-region mode, ensure you have:

- **Multiple Kubernetes clusters:** 2+ clusters in different regions
- **Network connectivity:** Clusters can communicate over private network or internet
- **Storage:** CSI-compatible storage class in each cluster with snapshot support
- **Load balancing:** LoadBalancer or Ingress capability for external access (optional)

### Required Components

Install these components on **all** Kubernetes clusters:

#### 1. cert-manager

Required for TLS certificate management between clusters.

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set installCRDs=true
```

Verify installation:

```bash
kubectl get pods -n cert-manager
```

See [Get Started](../index.md#install-cert-manager) for detailed cert-manager setup.

#### 2. DocumentDB Operator

Install the operator on each Kubernetes cluster. If using KubeFleet (recommended), install once on the hub cluster and let it propagate to all member clusters. See [Fleet Setup](#with-fleet-management-recommended) below.

```bash
helm repo add documentdb https://documentdb.github.io/documentdb-kubernetes-operator
helm repo update
helm install documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator \
  --create-namespace
```

Verify on each cluster:

```bash
kubectl get pods -n documentdb-operator
```

#### Self Label

Each Kubernetes cluster participating in multi-region deployment must identify itself with a unique cluster name. Create a ConfigMap on each cluster:

```bash
# Run on each cluster - replace with actual cluster name
CLUSTER_NAME="member-eastus2-cluster"  # e.g., member-eastus2-cluster, member-westus3-cluster

kubectl create configmap cluster-identity \
  --namespace kube-system \
  --from-literal=cluster-name="${CLUSTER_NAME}"
```

Important: The cluster name in this ConfigMap must exactly match one of the member cluster names in the DocumentDB resource's spec.clusterReplication.clusterList[].name. See Cluster Identification for details.

This is needed since the DocumentDB CRD will be the same across primaries and replicas for ease of use, but the clusters need
to be able to identify where in the topology they lie.

### Network Configuration

#### VNet/VPC Peering (Single Cloud Provider)

For clusters within the same cloud provider, configure VNet or VPC peering:

=== "Azure (AKS)"

    Create VNet peering between all AKS cluster VNets:

    ```bash
    az network vnet peering create \
      --name peer-to-cluster2 \
      --resource-group cluster1-rg \
      --vnet-name cluster1-vnet \
      --remote-vnet /subscriptions/.../cluster2-vnet \
      --allow-vnet-access
    ```

    Repeat for all cluster pairs in a full mesh topology.

    See [AKS Fleet Deployment](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md) for automated Azure multi-region setup with VNet peering.

=== "AWS (EKS)"

    Create VPC peering connections between EKS cluster VPCs:

    ```bash
    aws ec2 create-vpc-peering-connection \
      --vpc-id vpc-cluster1 \
      --peer-vpc-id vpc-cluster2 \
      --peer-region us-west-2
    ```

    Update route tables to allow traffic between VPCs.

=== "GCP (GKE)"

    Enable VPC peering between GKE cluster networks:

    ```bash
    gcloud compute networks peerings create peer-to-cluster2 \
      --network=cluster1-network \
      --peer-network=cluster2-network
    ```

#### Networking management

Configure inter-cluster networking using `spec.clusterReplication.crossCloudNetworkingStrategy`:

**Valid options:**

- **None** (default): Direct service-to-service connections using standard Kubernetes service names for the PostgreSQL backend server
- **Istio**: Use Istio service mesh for cross-cluster connectivity
- **AzureFleet**: Use Azure Fleet Networking for cross-cluster communication (separate from KubeFleet)

**Example:**

```yaml
spec:
  clusterReplication:
    primary: member-eastus2-cluster
    crossCloudNetworkingStrategy: Istio  # or AzureFleet, None
    clusterList:
      - name: member-eastus2-cluster
      - name: member-westus3-cluster
```

## Deployment Options

Choose a deployment approach based on your infrastructure and operational preferences.

### With KubeFleet (Recommended)

KubeFleet systems simplify multi-region operations by:

- **Centralized control:** Define resources once, deploy everywhere
- **Automatic propagation:** Resources sync to member clusters automatically
- **Coordinated updates:** Roll out changes across regions consistently

#### Step 1: Deploy Fleet Infrastructure

Install KubeFleet or another fleet management system:

```bash
# Example: KubeFleet on hub cluster
kubectl apply -f https://github.com/kubefleet-dev/kubefleet/releases/latest/download/install.yaml
```

Configure member clusters to join the fleet. See the [AKS Fleet Deployment guide](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md) for a complete automated setup example.

#### Step 2: Install DocumentDB Operator via KubeFleet

Create a `ClusterResourcePlacement` to deploy the operator to all member clusters:

```yaml title="documentdb-operator-crp.yaml"
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: documentdb-operator-crp
  namespace: fleet-system
spec:
  resourceSelectors:
    - group: ""
      kind: Namespace
      name: documentdb-operator
      version: v1
  placement:
    strategy:
      type: PickAll  # Deploy to all member clusters
```

Apply to hub cluster:

```bash
kubectl --context hub apply -f documentdb-operator-crp.yaml
```

The fleet controller will install the operator on all member clusters automatically.

#### Step 3: Deploy Multi-Region DocumentDB

Create a DocumentDB resource with replication configuration:

```yaml title="multi-region-documentdb.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  nodeCount: 2
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: managed-csi  # Use appropriate storage class per cloud
  exposeViaService:
    serviceType: LoadBalancer
  tls:
    gateway:
      mode: SelfSigned
  clusterReplication:  # (1)!
    primary: member-eastus2-cluster  # (2)!
    clusterList:  # (3)!
      - name: member-westus3-cluster
      - name: member-uksouth-cluster
      - name: member-eastus2-cluster
  documentDbCredentialSecret: documentdb-secret
```

1. The `clusterReplication` section enables multi-region deployment
2. `primary` specifies which cluster accepts write operations
3. `clusterList` lists all member clusters that will host DocumentDB instances (including the primary)

Create the credentials secret:

```bash
kubectl create secret generic documentdb-secret \
  --namespace documentdb-preview-ns \
  --from-literal=password='YourSecurePassword123!'
```

Apply via KubeFleet to propagate to all clusters:

```yaml title="documentdb-crp.yaml"
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: documentdb-crp
  namespace: fleet-system
spec:
  resourceSelectors:
    - group: documentdb.io
      kind: DocumentDB
      name: documentdb-preview
      version: preview
    - group: ""
      version: v1
      kind: Secret
      name: documentdb-secret
  placement:
    strategy:
      type: PickAll
```

Apply both resources:

```bash
kubectl --context hub apply -f multi-region-documentdb.yaml
kubectl --context hub apply -f documentdb-crp.yaml
```

### Without KubeFleet

If not using KubeFleet, deploy DocumentDB resources to each cluster individually.

#### Step 1: Identify Cluster Names

Determine the name for each Kubernetes cluster. These names are used in the replication configuration:

```bash
# List your clusters
kubectl config get-contexts

# Or for cloud-managed clusters:
az aks list --query "[].name" -o table  # Azure
aws eks list-clusters --query "clusters" --output table  # AWS
gcloud container clusters list --format="table(name)"  # GCP
```

#### Step 2: Create Cluster Identification

On each cluster, create a ConfigMap to identify the cluster name:

```bash
# Run on each cluster
CLUSTER_NAME="cluster-region-name"  # e.g., member-eastus2-cluster

kubectl create configmap cluster-identity \
  --namespace kube-system \
  --from-literal=cluster-name="${CLUSTER_NAME}"
```

#### Step 3: Deploy DocumentDB to Primary Cluster

On the primary cluster:

```yaml title="primary-documentdb.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  nodeCount: 2
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 100Gi
  exposeViaService:
    serviceType: LoadBalancer
  tls:
    gateway:
      mode: SelfSigned
  clusterReplication:
    primary: cluster-primary-name  # This cluster's name
    clusterList:
      - name: cluster-primary-name
      - name: cluster-replica1-name
      - name: cluster-replica2-name
  documentDbCredentialSecret: documentdb-secret
```

Apply to primary:

```bash
kubectl --context primary apply -f primary-documentdb.yaml
```

#### Step 4: Deploy DocumentDB to Replica Clusters

Use the **same YAML** on each replica cluster. The operator detects whether the cluster is primary or replica based on the `clusterReplication.primary` field.

```bash
kubectl --context replica1 apply -f primary-documentdb.yaml
kubectl --context replica2 apply -f primary-documentdb.yaml
```

Each cluster will:

- **Primary:** Run a read-write DocumentDB cluster
- **Replicas:** Run read-only DocumentDB clusters replicating from primary

## Configuration Details

### Replication Configuration

The `clusterReplication` section controls multi-region behavior:

```yaml
spec:
  clusterReplication:
    primary: member-eastus2-cluster  # Write cluster
    clusterList:  # All participating member clusters
      - name: member-westus3-cluster
      - name: member-uksouth-cluster
      - name: member-eastus2-cluster
```

**Key points:**

- **Primary cluster:** The cluster name specified in `primary` accepts read and write operations
- **Replica clusters:** All other clusters in `clusterList` are read-only replicas
- **Include primary in list:** The primary cluster name must appear in the `clusterList`
- **Cluster name uniqueness:** Each cluster must have a unique name across all regions

### Credential Synchronization

The DocumentDB operator expects the same credentials secret on all clusters. With KubeFleet, create the secret on the hub (or in fleet-managed namespace) and it propagates automatically. Without fleet management, create the secret manually on each cluster:

```bash
# Same secret on all clusters
kubectl --context cluster1 create secret generic documentdb-secret \
  --namespace documentdb-preview-ns \
  --from-literal=password='YourSecurePassword123!'

kubectl --context cluster2 create secret generic documentdb-secret \
  --namespace documentdb-preview-ns \
  --from-literal=password='YourSecurePassword123!'

# And so on...
```

!!! warning "Credential Management"
    Synchronize password changes across all clusters manually if not using fleet management. Mismatched credentials will break replication.

### Storage Configuration

Each cluster in a multi-region deployment can use different storage classes. Configure storage at the global level or override per member cluster:

**Global storage configuration:**

```yaml
spec:
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: default-storage-class  # Used by all clusters
```

**Per-cluster storage override:**

```yaml
spec:
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: default-storage-class  # Fallback
  clusterReplication:
    primary: member-eastus2-cluster
    clusterList:
      - name: member-westus3-cluster
        storageClass: managed-csi-premium  # Override for this cluster
      - name: member-uksouth-cluster
        storageClass: azuredisk-standard-ssd  # Override for this cluster
      - name: member-eastus2-cluster
        # Uses global storageClass
```

**Cloud-specific storage classes:**

=== "Azure (AKS)"

    ```yaml
    - name: member-eastus2-cluster
      storageClass: managed-csi  # Azure Disk managed CSI driver
      environment: aks
    ```

=== "AWS (EKS)"

    ```yaml
    - name: member-us-east-1-cluster
      storageClass: gp3  # AWS EBS GP3 volumes
      environment: eks
    ```

=== "GCP (GKE)"

    ```yaml
    - name: member-us-central1-cluster
      storageClass: standard-rwo  # GCP Persistent Disk
      environment: gke
    ```

### Service Exposure

Configure how DocumentDB is exposed in each region:

=== "LoadBalancer"

    **Best for:** Production deployments with external access

    ```yaml
    spec:
      exposeViaService:
        serviceType: LoadBalancer
    ```

    Each cluster gets a public IP for client connections. When using the `environment` configuration at either
    the DocumentDB cluster or Kubernetes cluster level, the tags for the LoadBalancer will change. See the
    cloud-specific setup docs for more details.

=== "ClusterIP"

    **Best for:** In-cluster access only or Ingress-based routing

    ```yaml
    spec:
      exposeViaService:
        serviceType: ClusterIP
    ```

    Clients must access DocumentDB through Ingress or service mesh.

## Verification

### Check Operator Status

Verify the operator is running on all clusters:

```bash
# With KubeFleet
kubectl --context hub get clusterresourceplacement

# Without fleet management (run on each cluster)
kubectl --context cluster1 get pods -n documentdb-operator
kubectl --context cluster2 get pods -n documentdb-operator
```

### Check DocumentDB Status

View DocumentDB status on each cluster:

```bash
kubectl --context primary get documentdb -n documentdb-preview-ns
kubectl --context replica1 get documentdb -n documentdb-preview-ns
kubectl --context replica2 get documentdb -n documentdb-preview-ns
```

Expected output shows `Ready` status and role (primary or replica):

```
NAME                  STATUS   ROLE      AGE
documentdb-preview    Ready    primary   10m
```

## Troubleshooting

### Replication Not Established

If replicas don't receive data from the primary:

1. **Verify network connectivity:**

    ```bash
    # From replica cluster, test connectivity to primary
    kubectl --context replica1 run test-pod --rm -it --image=nicolaka/netshoot -- \
      nc -zv primary-service-endpoint 5432
    ```

2. **Check PostgreSQL replication status on primary:**

    ```bash
    kubectl --context primary exec -it -n documentdb-preview-ns \
      documentdb-preview-1 -- psql -U postgres -c "SELECT * FROM pg_stat_replication;"
    ```

3. **Review operator logs:**

    ```bash
    kubectl --context primary logs -n documentdb-operator \
      -l app.kubernetes.io/name=documentdb-operator --tail=100
    ```

### Cluster Name Mismatch

If a cluster doesn't recognize itself as primary or replica:

1. **Check cluster-identity ConfigMap:**

    ```bash
    kubectl --context cluster1 get configmap cluster-identity \
      -n kube-system -o jsonpath='{.data.cluster-name}'
    ```

2. **Verify name matches DocumentDB spec:**

    The returned name must exactly match one of the cluster names in `spec.clusterReplication.clusterList[*].name`.

3. **Update ConfigMap if incorrect:**

    ```bash
    kubectl --context cluster1 create configmap cluster-identity \
      --namespace kube-system \
      --from-literal=cluster-name="correct-cluster-name" \
      --dry-run=client -o yaml | kubectl apply -f -
    ```

### Storage Issues

If PVCs aren't provisioning:

1. **Verify storage class exists:**

    ```bash
    kubectl --context cluster1 get storageclass
    ```

2. **Check for VolumeSnapshotClass (required for backups):**

    ```bash
    kubectl --context cluster1 get volumesnapshotclass
    ```

3. **Review PVC events:**

    ```bash
    kubectl --context cluster1 get events -n documentdb-preview-ns \
      --field-selector involvedObject.kind=PersistentVolumeClaim
    ```

## Next Steps

- [Failover Procedures](failover-procedures.md) - Learn how to perform planned and unplanned fail overs
- [Backup and Restore](../backup-and-restore.md) - Configure multi-region backup strategies
- [TLS Configuration](../configuration/tls.md) - Secure connections with proper TLS certificates
- [AKS Fleet Deployment Example](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md) - Automated Azure multi-region setup
