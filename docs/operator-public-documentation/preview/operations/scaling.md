---
title: Scaling
description: Scale DocumentDB clusters by adjusting instance count for high availability and read throughput.
tags:
  - operations
  - scaling
  - storage
---

# Scaling

## Overview

Scaling adjusts the capacity of your DocumentDB cluster to match workload demands. Scale instances up for high availability and read throughput.

The DocumentDB operator currently supports:

- **Instance scaling** — change `spec.instancesPerNode` to add or remove database replicas (1 to 3).

!!! note
    Horizontal node scaling (adding more nodes via `spec.nodeCount`) is not currently supported. `nodeCount` is fixed at 1.

!!! note
    PVC resize after creation is not currently supported. Set your initial storage size carefully. See [Storage Configuration](../configuration/storage.md) for guidance on choosing the right `pvcSize`.

## Instance Scaling

Each DocumentDB cluster runs on a single node with a configurable number of instances. Increasing `instancesPerNode` adds replicas for high availability and read scalability.

### Instance Configurations

| `instancesPerNode` | Topology | Use Case |
|---------------------|----------|----------|
| 1 | Single primary | Development, testing |
| 2 | Primary + 1 replica | Basic redundancy |
| 3 | Primary + 2 replicas | Production HA (recommended) |

=== "Scaling Up"

    To scale from 1 instance to 3:

    ```bash
    kubectl patch documentdb my-cluster -n default --type='json' \
      -p='[{"op": "replace", "path": "/spec/instancesPerNode", "value": 3}]'
    ```

    Or update the manifest:

    ```yaml title="documentdb.yaml"
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

    1. The operator provisions new replica pods with streaming replication from the primary.
    2. Replicas perform a `pg_basebackup` from the primary to initialize.
    3. Once caught up, replicas begin receiving WAL (Write-Ahead Log) updates in real time.
    4. The DocumentDB cluster status updates when all instances are healthy.

=== "Scaling Down"

    To reduce from 3 instances to 1:

    ```bash
    kubectl patch documentdb my-cluster -n default --type='json' \
      -p='[{"op": "replace", "path": "/spec/instancesPerNode", "value": 1}]'
    ```

    **What happens**:

    1. The operator terminates replica pods (the primary is never removed).
    2. Persistent volumes for removed replicas may be retained depending on the reclaim policy.

    !!! warning
        Scaling down removes replicas and reduces availability. In production, maintain at least 3 instances for automatic failover.

### Monitoring Scaling Operations

```bash
# Watch pod creation/termination
kubectl get pods -n default -w

# Check cluster health
kubectl get documentdb my-cluster -n default

# View database cluster status
kubectl get clusters.postgresql.cnpg.io -n default
```

## Storage

Storage size is set when creating a DocumentDB cluster via `spec.resource.storage.pvcSize`. PVC resize after creation is not currently supported by the operator. For storage configuration details including storage classes, reclaim policies, and disk encryption, see [Storage Configuration](../configuration/storage.md).

## Recommended Scaling Practices

=== "Development"

    ```yaml
    spec:
      instancesPerNode: 1
      resource:
        storage:
          pvcSize: 10Gi
    ```

=== "Production"

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
