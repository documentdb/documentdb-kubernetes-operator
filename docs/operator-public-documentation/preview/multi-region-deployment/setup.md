---
title: Multi-region setup guide
description: Step-by-step instructions for deploying DocumentDB across multiple Kubernetes clusters with replication, prerequisites, and configuration examples.
tags:
  - multi-region
  - setup
  - deployment
  - replication
---

## Prerequisites

### Infrastructure requirements

Before deploying DocumentDB in multi-region mode, ensure you have:

- **Multiple Kubernetes clusters:** 2 or more Kubernetes clusters in different regions
- **Network connectivity:** Kubernetes clusters can communicate over private networking or the internet
- **Storage:** CSI-compatible storage class in each Kubernetes cluster with snapshot support
- **Load balancing:** LoadBalancer or Ingress capability for external access (optional)

### Required components

Install these components on **all** Kubernetes clusters:

#### 1. cert-manager

Required for TLS certificate management between Kubernetes clusters.

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

#### 2. DocumentDB operator

Install the operator on each Kubernetes cluster.

```bash
helm repo add documentdb https://documentdb.github.io/documentdb-kubernetes-operator
helm repo update
helm install documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator \
  --create-namespace
```

Verify installation:

```bash
kubectl get pods -n documentdb-operator
```

#### 3. Kubernetes cluster identity ConfigMap

Each Kubernetes cluster in a multi-region deployment must identify itself with
a unique Kubernetes cluster name. Create a ConfigMap on each Kubernetes cluster:

```bash
# Run on each Kubernetes cluster and replace with your actual cluster name.
CLUSTER_NAME="member-eastus2-cluster"  # for example: member-eastus2-cluster, member-westus3-cluster

kubectl create configmap cluster-identity \
  --namespace kube-system \
  --from-literal=cluster-name="${CLUSTER_NAME}"
```

!!! note
    The Kubernetes cluster name in this ConfigMap must exactly match one
    of the member Kubernetes cluster names in `spec.clusterReplication.clusterList[].name`.

This is required because the DocumentDB CRD is the same across primaries and
replicas, and each Kubernetes cluster must identify its own role in the topology.

### Network configuration

#### VNet/VPC peering (single cloud provider)

For Kubernetes clusters in the same cloud provider, configure VNet or VPC peering:

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

    Repeat for all Kubernetes cluster pairs in a full mesh topology.

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

## Deployment options

Choose a deployment approach based on your infrastructure and operational preferences.

### With KubeFleet (recommended)

KubeFleet systems simplify multi-region operations by:

- **Centralized control:** Define resources once, deploy everywhere
- **Automatic propagation:** Resources sync to member Kubernetes clusters automatically
- **Coordinated updates:** Roll out changes across regions consistently

#### Step 1: Deploy fleet infrastructure

Install KubeFleet or another fleet management system:

Configure member Kubernetes clusters to join the fleet. See
[deploy-fleet-bicep.sh](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/deploy-fleet-bicep.sh)
"KUBEFLEET SETUP" for a complete automated setup example.

#### Step 2: Install cert-manager and DocumentDB operator

Install the cert manager and DocumentDB operator to the hub per the
[Required Components](#required-components) section, then create `ClusterResourcePlacements`
to deploy them both to all member Kubernetes clusters.

- [cert-manager CRP](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/cert-manager-crp.yaml)
- [documentdb-operator CRP](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/documentdb-operator-crp.yaml)

#### Step 3: Deploy multi-region DocumentDB

Create a DocumentDB resource with replication configuration. The example uses substitutions
with a script, so you will need to replace all the {{PLACEHOLDERS}}.

- [DocumentDB CRD, secret, and CRP](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/documentdb-resource-crp.yaml)

Within the CRD The `clusterReplication` section enables multi-region deployment,
`primary` specifies which Kubernetes cluster accepts write operations, and `clusterList`
lists all member Kubernetes clusters that host DocumentDB instances (including the
primary) and accepts a more granular `environment` and `storageClass` variable.

### Without KubeFleet

If you are not using KubeFleet, deploy DocumentDB resources to each Kubernetes cluster individually.

#### Step 1: Identify Kubernetes cluster names

Determine the name for each Kubernetes cluster. These names are used in the replication configuration:

```bash
# List your clusters
kubectl config get-contexts

# Or for cloud-managed clusters:
az aks list --query "[].name" -o table  # Azure
aws eks list-clusters --query "clusters" --output table  # AWS
gcloud container clusters list --format="table(name)"  # GCP
```

#### Step 2: Create Kubernetes cluster identification

On each Kubernetes cluster, create a ConfigMap to identify the Kubernetes cluster name:

```bash
# Run on each Kubernetes cluster
CLUSTER_NAME="cluster-region-name"  # for example: member-eastus2-cluster

kubectl create configmap cluster-identity \
  --namespace kube-system \
  --from-literal=cluster-name="${CLUSTER_NAME}"
```

#### Step 3: Deploy cert-manager and DocumentDB operator to each cluster

Install the cert manager and DocumentDB operator to the hub per the
[Required Components](#required-components) section on each Kubernetes cluster.
When making changes to any resource, you must make that same change across
each Kubernetes cluster so they are all in sync, as the operator works under
the assumption that all members have the same resources.

### Storage configuration

Each Kubernetes cluster in a multi-region deployment can use different storage classes.
Configure storage at the global level or override per member Kubernetes cluster:

**Global storage configuration:**

```yaml
spec:
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: default-storage-class  # Used by all Kubernetes clusters
```

**Per-Kubernetes-cluster storage override:**

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
        storageClass: managed-csi-premium  # Override for this Kubernetes cluster
      - name: member-uksouth-cluster
        storageClass: azuredisk-standard-ssd  # Override for this Kubernetes cluster
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

### Service exposure

Configure how DocumentDB is exposed in each region:

=== "LoadBalancer"

    **Best for:** Production deployments with external access

    ```yaml
    spec:
      exposeViaService:
        serviceType: LoadBalancer
    ```

    Each Kubernetes cluster gets a public IP for client connections. When you use the `environment`
    configuration at either the DocumentDB cluster or Kubernetes cluster level, the tags for the
    LoadBalancer change. See the
    cloud-specific setup docs for more details.

=== "ClusterIP"

    **Best for:** In-cluster access only or Ingress-based routing

    ```yaml
    spec:
      exposeViaService:
        serviceType: ClusterIP
    ```

    Clients must access DocumentDB through Ingress or service mesh.

## Troubleshooting

### Replication not established

If replicas don't receive data from the primary:

1. **Verify network connectivity:**

    ```bash
    # From a replica Kubernetes cluster, test connectivity to primary
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

### Kubernetes cluster name mismatch

If a Kubernetes cluster doesn't recognize itself as primary or replica:

1. **Check cluster-identity ConfigMap:**

    ```bash
    kubectl --context cluster1 get configmap cluster-identity \
      -n kube-system -o jsonpath='{.data.cluster-name}'
    ```

2. **Verify the name matches the DocumentDB spec:**

    The returned name must exactly match one of the Kubernetes cluster names in `spec.clusterReplication.clusterList[*].name`.

3. **Update ConfigMap if incorrect:**

    ```bash
    kubectl --context cluster1 create configmap cluster-identity \
      --namespace kube-system \
      --from-literal=cluster-name="correct-cluster-name" \
      --dry-run=client -o yaml | kubectl apply -f -
    ```

### Storage issues

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

## Next steps

- [Failover procedures](failover-procedures.md) - Learn how to perform planned and unplanned failovers
- [Backup and restore](../backup-and-restore.md) - Configure multi-region backup strategies
- [TLS configuration](../configuration/tls.md) - Secure connections with proper TLS certificates
- [AKS Fleet deployment example](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md) - Automated Azure multi-region setup
