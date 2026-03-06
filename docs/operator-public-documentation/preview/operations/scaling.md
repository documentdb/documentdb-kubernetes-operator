# Scaling

This guide explains how to scale a DocumentDB cluster by adjusting the number of instances and expanding storage.

## Overview

The DocumentDB operator supports two forms of scaling:

- **Instance scaling** — change `spec.instancesPerNode` to add or remove database replicas (1 to 3).
- **Storage expansion** — increase `spec.resource.storage.pvcSize` to grow persistent volumes.

!!! note
    Horizontal node scaling (adding more nodes via `spec.nodeCount`) is not currently supported. `nodeCount` is fixed at 1.

## Instance Scaling

Each DocumentDB cluster runs on a single node with a configurable number of instances. Increasing `instancesPerNode` adds replicas for high availability and read scalability.

### Instance Configurations

| `instancesPerNode` | Topology | Use Case |
|---------------------|----------|----------|
| 1 | Single primary | Development, testing |
| 2 | Primary + 1 replica | Basic redundancy |
| 3 | Primary + 2 replicas | Production HA (recommended) |

### Scaling Up

To scale from 1 instance to 3:

```bash
kubectl patch documentdb my-cluster -n default --type='json' \
  -p='[{"op": "replace", "path": "/spec/instancesPerNode", "value": 3}]'
```

Or update the manifest:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  instancesPerNode: 3  # Scale to 3 instances
  # ... other configuration
```

```bash
kubectl apply -f documentdb.yaml
```

**What happens**:

1. The operator updates the underlying CNPG Cluster spec.
2. CNPG provisions new replica pods with streaming replication from the primary.
3. Replicas perform a `pg_basebackup` from the primary to initialize.
4. Once caught up, replicas begin receiving WAL (Write-Ahead Log) updates in real time.
5. The cluster status updates when all instances are healthy.

### Scaling Down

To reduce from 3 instances to 1:

```bash
kubectl patch documentdb my-cluster -n default --type='json' \
  -p='[{"op": "replace", "path": "/spec/instancesPerNode", "value": 1}]'
```

**What happens**:

1. The operator updates the CNPG Cluster spec.
2. CNPG terminates replica pods (the primary is never removed).
3. Persistent volumes for removed replicas may be retained depending on the reclaim policy.

!!! warning
    Scaling down removes replicas and reduces availability. In production, maintain at least 3 instances for automatic failover.

### Monitoring Scaling Operations

```bash
# Watch pod creation/termination
kubectl get pods -n default -w

# Check cluster health
kubectl get documentdb my-cluster -n default

# View CNPG cluster status
kubectl get clusters.postgresql.cnpg.io -n default
```

## Storage Expansion

You can increase the storage size for a DocumentDB cluster by updating the PVC size. The underlying storage class must support volume expansion.

### Prerequisites

Verify your storage class allows volume expansion:

```bash
kubectl get storageclass <storage-class> -o jsonpath='{.allowVolumeExpansion}'
```

This should return `true`. If not, you need a storage class that supports expansion.

### Expanding Storage

Update the PVC size:

```bash
kubectl patch documentdb my-cluster -n default --type='json' \
  -p='[{"op": "replace", "path": "/spec/resource/storage/pvcSize", "value": "200Gi"}]'
```

Or update the manifest:

```yaml
spec:
  resource:
    storage:
      pvcSize: 200Gi  # Increased from 100Gi
```

**What happens**:

1. The operator updates the CNPG Cluster storage configuration.
2. CNPG triggers a PVC resize for each instance.
3. The CSI driver expands the underlying volume.
4. Some storage providers may require a pod restart for the filesystem to be resized.

!!! warning
    Storage expansion is a one-way operation. You cannot shrink a PVC after expanding it.

### Monitoring Storage Expansion

```bash
# Check PVC status
kubectl get pvc -n default

# Watch for resize events
kubectl describe pvc <pvc-name> -n default
```

Look for the `FileSystemResizeSuccessful` condition on the PVC.

## Recommended Scaling Practices

### Development Environments

```yaml
spec:
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 10Gi
```

### Production Environments

```yaml
spec:
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: premium-ssd  # Use premium storage
```

### Scaling Checklist

Before scaling, consider:

- [ ] **Backup first** — create an on-demand [backup](backup-and-restore.md) before any scaling operation.
- [ ] **Monitor resources** — ensure your Kubernetes nodes have sufficient CPU and memory for additional instances.
- [ ] **Storage class** — verify volume expansion is supported before attempting storage changes.
- [ ] **Application impact** — scaling operations may cause brief connection disruptions as pods are created or terminated.

## Next Steps

- [Failover](failover.md) — understand automatic failover with multiple instances
- [Upgrades](upgrades.md) — upgrade the operator and DocumentDB versions
- [Maintenance](maintenance.md) — day-to-day operational tasks
