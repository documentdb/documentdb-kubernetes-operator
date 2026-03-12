---
title: Failover
description: Understand automatic and manual failover for DocumentDB clusters, including local replica promotion and cross-cluster failover for multi-region deployments.
tags:
  - operations
  - failover
  - high-availability
---

# Failover

## Overview

Failover promotes a replica to primary when the current primary becomes unavailable. It protects your application from downtime caused by pod crashes, node failures, or planned maintenance — ensuring writes resume within seconds.

The DocumentDB operator supports two levels of failover:

- **Local failover** — automatic promotion of a replica to primary within a single DocumentDB cluster.
- **Cross-cluster failover** — manual promotion of a standby DocumentDB cluster to primary in a multi-region deployment.

## Local Automatic Failover

When running with multiple instances (`spec.instancesPerNode >= 2`), the operator automatically handles failover if the primary instance becomes unavailable.

## Cross-Cluster Failover (Multi-Region)

For multi-region deployments using cluster replication, you can promote a standby (replica) DocumentDB cluster to become the new primary.

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
- **Implement retry logic** — during failover, connections are briefly interrupted. Applications should retry with exponential backoff.

### Write Behavior During Failover

- Writes to the old primary may fail during the failover window.
- Writes are available on the new primary within seconds of promotion.

### Read Behavior During Failover

- Reads are routed through the primary via the gateway. During failover, there is a brief interruption until the new primary is available.
