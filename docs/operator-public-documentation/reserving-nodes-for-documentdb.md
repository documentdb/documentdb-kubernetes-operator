# Reserving Nodes for DocumentDB Workloads

This guide explains how to dedicate Kubernetes worker nodes exclusively to DocumentDB (PostgreSQL) workloads for optimal performance and isolation in production environments.

## Overview

By reserving specific nodes for DocumentDB, you ensure:

- **Resource isolation**: Database workloads don't compete with other applications
- **Predictable performance**: Dedicated CPU, memory, and I/O resources
- **Better fault tolerance**: Database instances spread across dedicated nodes

> **Best Practice**: Deploy dedicated nodes in multiples of three—ideally one per availability zone. This ensures a 3-instance DocumentDB cluster (1 primary + 2 replicas) is distributed across different nodes.

## Step 1: Label Your Nodes

Apply the `postgres` role label to nodes designated for DocumentDB. This reserved label can only be applied after the node is created:

```bash
kubectl label node <NODE-NAME> node-role.kubernetes.io/postgres=
```

Verify the label:

```bash
kubectl get nodes -l node-role.kubernetes.io/postgres
```

## Step 2: Taint Your Nodes

Prevent non-database workloads from being scheduled on these nodes. Use a custom taint key (cloud providers may restrict `kubernetes.io` namespace taints):

```bash
kubectl taint node <NODE-NAME> workload=postgres:NoSchedule
```

This ensures only pods that explicitly tolerate this taint can run on these nodes.

## Step 3: Configure DocumentDB Scheduling

> **Note**: The DocumentDB operator currently does not expose `nodeSelector` or `tolerations` directly in the `DocumentDBSpec`. You can configure scheduling by patching the underlying CNPG Cluster resource after creation, or request this feature be added to the operator.

### Patching the CNPG Cluster

After deploying DocumentDB, patch the underlying CNPG Cluster:

```bash
kubectl patch cluster <CLUSTER-NAME> -n <NAMESPACE> --type=merge -p '
{
  "spec": {
    "affinity": {
      "nodeSelector": {
        "node-role.kubernetes.io/postgres": ""
      },
      "tolerations": [
        {
          "key": "workload",
          "operator": "Equal",
          "value": "postgres",
          "effect": "NoSchedule"
        }
      ]
    }
  }
}'
```

### Example: Full Affinity Configuration

For production deployments with anti-affinity (instances on different nodes/zones):

```yaml
spec:
  affinity:
    nodeSelector:
      node-role.kubernetes.io/postgres: ""
    tolerations:
      - key: workload
        operator: Equal
        value: postgres
        effect: NoSchedule
    enablePodAntiAffinity: true
    topologyKey: topology.kubernetes.io/zone  # Spread across AZs
```

## Cloud Provider Node Pools

### Azure AKS

Create a dedicated node pool. AKS restricts `kubernetes.io` namespace labels during creation, so use a custom label and apply the reserved label after:

```bash
# Create node pool with custom label and taint
az aks nodepool add \
  --resource-group <RG> \
  --cluster-name <CLUSTER> \
  --name postgrespool \
  --node-count 3 \
  --node-vm-size Standard_D8s_v3 \
  --labels workload=postgres \
  --node-taints workload=postgres:NoSchedule \
  --zones 1 2 3

# Apply the reserved postgres label after node creation
for node in $(kubectl get nodes -l workload=postgres -o name); do
  kubectl label $node node-role.kubernetes.io/postgres=
done
```

### AWS EKS

```bash
eksctl create nodegroup \
  --cluster <CLUSTER> \
  --name postgres-nodes \
  --node-type m5.2xlarge \
  --nodes 3 \
  --node-labels "workload=postgres" \
  --node-taints "workload=postgres:NoSchedule"

# Apply the reserved postgres label after node creation
for node in $(kubectl get nodes -l workload=postgres -o name); do
  kubectl label $node node-role.kubernetes.io/postgres=
done
```

### GCP GKE

```bash
gcloud container node-pools create postgres-pool \
  --cluster <CLUSTER> \
  --num-nodes 3 \
  --machine-type n2-standard-8 \
  --node-labels workload=postgres \
  --node-taints workload=postgres:NoSchedule

# Apply the reserved postgres label after node creation
for node in $(kubectl get nodes -l workload=postgres -o name); do
  kubectl label $node node-role.kubernetes.io/postgres=
done
```

## Recommended Node Sizing

| Workload | vCPU | Memory | Storage |
|----------|------|--------|---------|
| Development | 2 | 8 GB | 50 GB SSD |
| Production (small) | 4 | 16 GB | 200 GB SSD |
| Production (medium) | 8 | 32 GB | 500 GB SSD |
| Production (large) | 16+ | 64+ GB | 1+ TB NVMe |

## References

- [CloudNativePG Architecture - Reserving Nodes](https://cloudnative-pg.io/docs/1.27/architecture/#reserving-nodes-for-postgresql-workloads)
- [Kubernetes Node Affinity](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/)
- [Kubernetes Taints and Tolerations](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/)
