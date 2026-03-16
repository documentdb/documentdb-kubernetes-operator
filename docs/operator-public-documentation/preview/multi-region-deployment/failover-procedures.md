---
title: Multi-Region Failover Procedures
description: Step-by-step runbooks for planned and unplanned DocumentDB failovers across regions, including verification and rollback procedures.
tags:
  - multi-region
  - failover
  - disaster-recovery
  - operations
---

## Overview

A **failover** promotes a replica cluster to become the new primary, making it
accept write operations. The previous primary (if still available) becomes a replica
replicating from the new primary.

**When to perform failover:**

- **Planned maintenance:** Region maintenance, infrastructure upgrades, cost optimization
- **Disaster recovery:** Primary region outage, network partition, catastrophic failure
- **Performance optimization:** Move primary closer to write-heavy workload
- **Testing:** Validate disaster recovery procedures

## Failover Types

### Planned Failover

This is a failover where the primary is safely demoted, writes are all flushed,
and then the new primary is promoted. This kind of failover has no data loss, a
set window where writes aren't accepted, and the same number of replicas before
and after.

### Unplanned Failover (Disaster Recovery)

This is a failover where the primary becomes unavailable and has to be forced out
of the DocumentDB cluster entirely. Downtime depends on how quickly primary degradation
is detected and, if HA is enabled, how long it takes to scale up the new primary.
Some writes to the failed primary might be lost, but with high availability enabled,
clients can determine when writes were not committed to replicas. After an unplanned
failover, the DocumentDB cluster has one fewer region, and you will need to add
the region back when the Kubernetes cluster is back online, or add a replacement
region. See the [add region playground guide](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/fleet-add-region/README.md)
for an example.

## Prerequisites

Before performing any failover:

- **Replica health:** Target replica cluster is running and replication is current
- **Network access:** You have kubectl access to all clusters involved
- **Backup available:** Recent backup exists for rollback if needed
- **Monitoring:** Metrics and logs are accessible for verification
- **Communication:** Stakeholders are notified (for planned failover)
- **Application readiness:** Applications can handle brief connection interruption
- **kubectl-documentdb plugin:** Install the plugin for streamlined failover operations [kubectl-plugin](../kubectl-plugin.md)

### Check Current Replication Status

Identify the current primary and verify replica health:

```bash
# View current primary setting
kubectl --context hub get documentdb documentdb-preview \
  -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.primary}'

# Check replication status on primary
kubectl --context primary exec -it -n documentdb-preview-ns \
  documentdb-preview-1 -- psql -U postgres -c "SELECT * FROM pg_stat_replication;"
```

Expected output shows active replication to all replicas:

```text
 pid | usename  | application_name | client_addr | state     | sent_lsn   | write_lsn  | flush_lsn  | replay_lsn | sync_state
-----+----------+------------------+-------------+-----------+------------+------------+------------+------------+------------
 123 | postgres | replica1         | 10.2.1.5    | streaming | 0/30000A8  | 0/30000A8  | 0/30000A8  | 0/30000A8  | async
 124 | postgres | replica2         | 10.3.1.5    | streaming | 0/30000A8  | 0/30000A8  | 0/30000A8  | 0/30000A8  | async
```

**Key indicators:**

- **state:** Should be `streaming`
- **LSN values:** `replay_lsn` should be close to `sent_lsn` (low replication lag)

## Planned Failover Procedure

Use this procedure when the primary cluster is healthy and you want to switch primary regions in a controlled manner.

### Step 1: Pre-Failover Verification

Verify system health before starting:

```bash
# 1. Check all DocumentDB clusters are ready
kubectl --context hub get documentdb -A

# 2. Verify replication lag is low (< 1 second)
kubectl --context current-primary exec -it -n documentdb-preview-ns \
  documentdb-preview-1 -- psql -U postgres -c \
  "SELECT client_addr, state, replay_lag FROM pg_stat_replication;"

# 3. Check target replica is healthy
kubectl --context new-primary get pods -n documentdb-preview-ns
```

All checks should show healthy status before proceeding.

### Step 2: Perform Failover

Promote the replica to become the new primary cluster.

=== "Using kubectl-documentdb Plugin (KubeFleet deployment required)"

    The plugin handles the CRD change and automatically waits for convergence:

    ```bash
    kubectl documentdb promote \
      --documentdb documentdb-preview \
      --namespace documentdb-preview-ns \
      --target-cluster new-primary-cluster-name \
      --hub-context hub
    ```

=== "Using kubectl patch with Fleet"

    Update the DocumentDB resource on the hub cluster:

    ```bash
    kubectl --context hub patch documentdb documentdb-preview \
      -n documentdb-preview-ns \
      --type='merge' \
      -p '{"spec":{"clusterReplication":{"primary":"new-primary-cluster-name"}}}'
    ```

    The fleet controller propagates the change to all member clusters automatically.

=== "Using kubectl patch without KubeFleet"

    Update the DocumentDB resource on **all** clusters:

    ```bash
    # Update on all clusters (use loop or run individually)
    for context in cluster1 cluster2 cluster3; do
      kubectl --context $context patch documentdb documentdb-preview \
        -n documentdb-preview-ns \
        --type='merge' \
        -p '{"spec":{"clusterReplication":{"primary":"new-primary-cluster-name"}}}'
    done
    ```

**What happens:**

1. The operator detects the primary change
2. The old primary becomes a replica after flushing writes
3. The new primary cluster scales up (if HA) and starts to accept writes
4. Replication direction reverses (new primary → replicas including old primary)

### Step 3: Monitor Failover Progress

Watch operator logs and DocumentDB status:

```bash
# Watch DocumentDB status on new primary
watch kubectl --context new-primary get documentdb -n documentdb-preview-ns

# Monitor operator logs on new primary
kubectl --context new-primary logs -n documentdb-operator \
  -l app.kubernetes.io/name=documentdb-operator -f

# Check pod status
kubectl --context new-primary get pods -n documentdb-preview-ns -w
```

### Step 4: Verify New Primary

Confirm the new primary accepts writes:

```bash
# Port forward to new primary
kubectl --context new-primary port-forward \
  -n documentdb-preview-ns svc/documentdb-preview 10260:10260 &

# Connect with mongosh
mongosh "mongodb://admin:password@localhost:10260/?tls=true&tlsAllowInvalidCertificates=true"

# Test write operation
db.testCollection.insertOne({
  message: "Write test after failover",
  timestamp: new Date()
})

# Should succeed without errors
```

### Step 5: Verify Old Primary as Replica

Check that the old primary is now replicating from the new primary:

```bash
# Verify replication status ON NEW PRIMARY
kubectl --context new-primary exec -it -n documentdb-preview-ns \
  documentdb-preview-1 -- psql -U postgres -c "SELECT * FROM pg_stat_replication;"
```

You should see the old primary listed as a replica receiving replication stream.

### Step 6: Post-Failover Validation

Run comprehensive checks:

```bash
# 1. Verify all clusters are in sync
for context in cluster1 cluster2 cluster3; do
  echo "=== $context ==="
  kubectl --context $context get documentdb -n documentdb-preview-ns
done

# 2. Check application health
kubectl --context new-primary get pods -n app-namespace

# 3. Review metrics and logs for errors
# (use your monitoring system: Prometheus, Grafana, CloudWatch, etc.)

# 4. Verify data consistency (read from all replicas)
```

## Unplanned Failover Procedure (Disaster Recovery)

Use this procedure when the primary cluster is unavailable and you need to immediately promote a replica.

!!! danger "Data Loss Risk"
    Unplanned failover may result in data loss if the primary cluster failed before replicating recent writes. Assess replication lag before deciding which replica to promote.

### Step 1: Assess the Situation

Determine the scope of the outage:

```bash
# 1. Check primary cluster accessibility
kubectl --context primary get nodes
# If this fails, primary cluster is unreachable

# 2. Check replica cluster health
kubectl --context replica1 get documentdb -n documentdb-preview-ns
kubectl --context replica2 get documentdb -n documentdb-preview-ns

# 3. Check cloud provider status pages for regional outages
```

### Step 2: Select Target Replica

Choose which replica to promote based on:

- **Replication lag:** Prefer the replica with the lowest lag (most recent data)
- **Geographic location:** Consider application proximity
- **Cluster health:** Ensure target cluster is fully operational

If you cannot query the primary, check the last known replication status from monitoring dashboards or logs.

### Step 3: Promote Replica to Primary

Immediately promote the selected replica to become the new primary.

=== "Using kubectl-documentdb Plugin (Recommended)"

    ```bash
    kubectl documentdb promote \
      --documentdb documentdb-preview \
      --namespace documentdb-preview-ns \
      --target-cluster replica-cluster-name \
      --hub-context hub \
      --failover \
      --wait-timeout 15m
    ```

    The plugin handles the change to clusterList and primary and monitors for successful convergence. Use `--skip-wait` if you need to return immediately and verify manually.

=== "Using kubectl patch with KubeFleet"

    ```bash
    # Remove failed primary from cluster list and set new primary in one command
    kubectl --context hub patch documentdb documentdb-preview \
      -n documentdb-preview-ns \
      --type='merge' \
      -p '{"spec":{"clusterReplication":{"primary":"replica-cluster-name","clusterList":[{"name":"replica-cluster-name"},{"name":"other-replica-cluster-name"}]}}}'
    ```

    Replace the `clusterList` entries with your actual list of healthy clusters, excluding the failed primary.

=== "Using kubectl without KubeFleet"

    ```bash
    # Update on all accessible clusters
    # Remove failed primary from cluster list and set new primary in one command
    for context in replica1 replica2; do
      kubectl --context $context patch documentdb documentdb-preview \
        -n documentdb-preview-ns \
        --type='merge' \
        -p '{"spec":{"clusterReplication":{"primary":"replica-cluster-name","clusterList":[{"name":"replica-cluster-name"},{"name":"other-replica-cluster-name"}]}}}'
    done
    ```

    Replace the `clusterList` entries with your actual list of healthy clusters, excluding the failed primary.

**What happens:**

1. The operator detects the primary and cluster list changes
2. The new primary cluster scales up (if HA) and starts to accept writes
3. The old primary is removed from replication

### Step 4: Verify New Primary

Confirm the promoted replica is accepting writes:

```bash
# Check status
kubectl --context new-primary get documentdb documentdb-preview \
  -n documentdb-preview-ns

# Test write access
kubectl --context new-primary port-forward \
  -n documentdb-preview-ns svc/documentdb-preview 10260:10260 &

mongosh "mongodb://admin:password@localhost:10260/?tls=true&tlsAllowInvalidCertificates=true"
db.testCollection.insertOne({message: "DR failover test"})
```

### Step 5: Monitor Recovery

```bash
# Application pod logs
kubectl --context app-cluster logs -l app=your-app --tail=100 -f

# DocumentDB operator logs
kubectl --context new-primary logs -n documentdb-operator \
  -l app.kubernetes.io/name=documentdb-operator -f
```

### Step 6: Handle Failed Primary Recovery

When the failed primary Kubernetes cluster recovers, you need to re-add it to the DocumentDB cluster
as a replica. For detailed guidance on adding a region back to your DocumentDB cluster,
see the [add region playground guide](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/fleet-add-region/README.md).
