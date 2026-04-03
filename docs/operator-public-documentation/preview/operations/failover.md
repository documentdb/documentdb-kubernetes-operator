---
title: Failover
description: How DocumentDB handles automatic failover through local replica promotion, cross-cluster failover for multi-region deployments, and application connection considerations.
tags:
  - operations
  - failover
  - high-availability
---

# Failover

## Overview

Failover promotes a replica to primary when the current primary becomes unavailable. Because all client traffic — both reads and writes — routes through the primary, failover causes a brief period of downtime until the new primary is ready. This typically completes within seconds.

The DocumentDB operator supports two levels of failover:

- **Local failover** — automatic promotion of a replica to primary within a single DocumentDB cluster.
- **Cross-cluster failover** — manual promotion of a standby DocumentDB cluster to primary in a multi-region deployment.

## Local Automatic Failover

Local automatic failover requires at least two instances (`spec.instancesPerNode >= 2`). With a single instance, there is only the primary and no replica available to promote — so failover is not possible. When multiple instances are running, the operator automatically promotes a replica to primary if the current primary becomes unavailable.

!!! tip
    Match `spec.instancesPerNode` to the number of availability zones in your Kubernetes cluster. This distributes replicas across zones and ensures the DocumentDB cluster can survive a zone failure.

## Cross-Cluster Failover (Multi-Region)

For multi-region deployments using cluster replication, you can promote a standby (replica) DocumentDB cluster to become the new primary. The primary itself can be a multi-instance HA cluster (with `spec.instancesPerNode >= 2`), providing local failover within the region. Cross-cluster failover to another region is only needed if the entire primary region becomes unavailable.

For end-to-end setup examples, see the [AKS Fleet multi-region deployment](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/aks-fleet-deployment) and [multi-cloud deployment](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/multi-cloud-deployment) playgrounds.

### Architecture

In a multi-region setup:

- One DocumentDB cluster is designated as the **primary** and handles all writes.
- Other DocumentDB clusters are **standbys** that replicate from the primary via streaming replication.

```yaml
spec:
  clusterReplication:
    crossCloudNetworkingStrategy: AzureFleet  # or Istio, None
    primary: primary-cluster
    clusterList:
      - name: primary-cluster
      - name: standby-cluster-1
      - name: standby-cluster-2
```

### Promoting a Standby DocumentDB Cluster

To promote a standby DocumentDB cluster to primary, update the `primary` field in all DocumentDB cluster configurations:

```bash
# On the new primary cluster
kubectl patch documentdb my-cluster -n default --type='json' \
  -p='[{"op": "replace", "path": "/spec/clusterReplication/primary", "value": "standby-cluster-1"}]'
```

## Application Considerations

### Connection Handling

- **Use the Kubernetes Service** — always connect through the [Kubernetes Service](../configuration/networking.md#service-types) (not directly to pod IPs). The Service automatically routes to the current primary.
- **Implement retry logic** — during failover, all connections are briefly interrupted. Applications should retry with exponential backoff.

### Behavior During Failover

During failover:
- Existing connections to the old primary are dropped.
- There is a brief period of downtime until the new primary is promoted and ready to accept connections.
- Once the new primary is available, the Kubernetes Service routes traffic to it automatically.
