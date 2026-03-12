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

Scaling adjusts the capacity of your DocumentDB cluster to match workload demands.

The DocumentDB operator currently supports:

- **Instance scaling** — change `spec.instancesPerNode` to add or remove database replicas (1 to 3).

!!! note
    Horizontal node scaling (adding more nodes via `spec.nodeCount`) is not currently supported. `nodeCount` is fixed at 1.

!!! note
    PVC resize after creation is not currently supported. Set your initial storage size carefully. See [Storage Configuration](../configuration/storage.md) for guidance on choosing the right `pvcSize`.

## Instance Scaling

Each DocumentDB cluster runs on a single node with a configurable number of instances. Increasing `instancesPerNode` adds replicas for high availability and read scalability.

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

=== "Scaling Down"

    To reduce from 3 instances to 1:

    ```bash
    kubectl patch documentdb my-cluster -n default --type='json' \
      -p='[{"op": "replace", "path": "/spec/instancesPerNode", "value": 1}]'
    ```

    Or update the manifest:

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-cluster
      namespace: default
    spec:
      instancesPerNode: 1  # Scale to 1 instance
      # ... other configuration
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    !!! warning
        Scaling down removes replicas and reduces availability. In production, maintain at least 2 instances for automatic failover.

