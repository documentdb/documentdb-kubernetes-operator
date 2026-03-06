# Failover

This guide explains how automatic and manual failover works in DocumentDB clusters, how to test it, and what to consider for your applications.

## Overview

The DocumentDB operator supports two levels of failover:

- **Local failover** — automatic promotion of a replica to primary within a single cluster (managed by CNPG).
- **Cross-cluster failover** — manual promotion of a standby cluster to primary in a multi-region deployment.

Automatic failover requires at least 2 instances (`spec.instancesPerNode >= 2`), with 3 recommended for production.

## Local Automatic Failover

When running with multiple instances, the operator (via CNPG) automatically handles failover if the primary instance becomes unavailable.

### How It Works

1. CNPG continuously monitors the health of all instances.
2. If the primary instance fails (pod crash, node failure, or unresponsive), CNPG detects the failure.
3. CNPG promotes the most up-to-date replica as the new primary.
4. Remaining replicas reconfigure to replicate from the new primary.
5. The Kubernetes Service automatically routes traffic to the new primary.
6. When the failed instance recovers, it rejoins as a replica.

### Failover Timeline

| Event | Approximate Time |
|-------|-----------------|
| Primary failure detected | Seconds |
| Replica promotion begins | Seconds |
| New primary accepting writes | ~10–30 seconds |
| Service endpoint updated | Seconds (after promotion) |
| Old primary rejoins as replica | Minutes (after recovery) |

!!! note
    Actual failover times depend on cluster load, network conditions, and the volume of uncommitted WAL data.

### High Availability Configuration

For production workloads, deploy with 3 instances:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  instancesPerNode: 3
  # ... other configuration
```

With 3 instances, the cluster can tolerate the loss of any single instance (including the primary) without data loss or service interruption.

## Cross-Cluster Failover (Multi-Region)

For multi-region deployments using cluster replication, you can promote a standby (replica) cluster to become the new primary.

### Architecture

In a multi-region setup:

- One cluster is designated as the **primary** and handles all writes.
- Other clusters are **standbys** that replicate from the primary via streaming replication.
- Cross-cluster networking is managed through Azure Fleet, Istio, or direct configuration.

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

### Promoting a Standby Cluster

To promote a standby cluster to primary, update the `primary` field in all cluster configurations:

```bash
# On the new primary cluster
kubectl patch documentdb my-cluster -n default --type='json' \
  -p='[{"op": "replace", "path": "/spec/clusterReplication/primary", "value": "standby-cluster-1"}]'
```

**What happens**:

1. The operator reads the promotion token from the current primary.
2. The promotion token is applied to the CNPG ReplicaCluster configuration on the new primary.
3. CNPG promotes the standby to an independent primary.
4. The old primary detects the role change and reconfigures as a replica.
5. Quorum writes and replication slots are updated across all clusters.

!!! warning
    Cross-cluster failover is a significant operation. Ensure all standby clusters are fully caught up with replication before promoting.

### High Availability Settings for Multi-Region

When using cluster replication with high availability enabled:

```yaml
spec:
  clusterReplication:
    highAvailability:
      enabled: true
```

This configures:

- **Primary cluster**: 3 instances (primary + 1 local standby + WAL replica slot), with quorum (synchronous) writes.
- **Standby clusters**: 1 instance (receives streaming replication from the primary).

## Testing Failover

### Test 1: Pod Deletion (Simulated Failure)

Delete the primary pod to simulate a crash:

```bash
# Identify the primary pod
kubectl get pods -n default -l role=primary

# Delete the primary pod
kubectl delete pod <primary-pod-name> -n default
```

**Expected behavior**:

1. A replica is promoted to primary within seconds.
2. The deleted pod is recreated and rejoins as a replica.
3. The cluster returns to a healthy state.

### Test 2: Monitoring During Failover

Watch the failover in real time:

```bash
# In terminal 1: watch pod status
kubectl get pods -n default -w

# In terminal 2: watch cluster status
kubectl get documentdb my-cluster -n default -w

# In terminal 3: continuously test connectivity
while true; do
  mongosh 127.0.0.1:10260 -u <user> -p <pass> \
    --authenticationMechanism SCRAM-SHA-256 \
    --tls --tlsAllowInvalidCertificates \
    --eval "db.runCommand({ping: 1})" 2>&1 | head -1
  sleep 1
done
```

### Test 3: Verify Data Integrity

After failover completes:

```bash
# Connect to the new primary
mongosh 127.0.0.1:10260 -u <user> -p <pass> \
  --authenticationMechanism SCRAM-SHA-256 \
  --tls --tlsAllowInvalidCertificates

# Verify data is intact
use testdb
db.test_collection.countDocuments()
```

## Application Considerations

### Connection Handling

- **Use the Kubernetes Service** — always connect through the DocumentDB Service (not directly to pod IPs). The Service automatically routes to the current primary.
- **Implement retry logic** — during failover, connections are briefly interrupted. Applications should retry with exponential backoff.
- **Use connection pooling** — connection pools help absorb the brief disruption during failover by transparently re-establishing connections.

### Write Behavior During Failover

- Writes to the old primary may fail during the failover window.
- Writes are available on the new primary within seconds of promotion.
- With quorum writes enabled (multi-region HA), acknowledged writes are guaranteed durable on at least one replica.

### Read Behavior During Failover

- Reads from replicas continue to work during primary failover.
- If using the primary Service endpoint for reads, there is a brief interruption.

### Connection String Recommendations

Use the DocumentDB Service connection string with appropriate timeout and retry settings:

```bash
# Get the connection string
kubectl get documentdb my-cluster -n default -o jsonpath='{.status.connectionString}'
```

In your application, configure:

- **Connection timeout**: 5–10 seconds
- **Server selection timeout**: 30 seconds
- **Retry writes**: enabled
- **Retry reads**: enabled

## Troubleshooting

### Failover Not Happening

**Possible causes**:

- Only 1 instance configured (`instancesPerNode: 1`). Failover requires at least 2 instances.
- CNPG operator is not running. Check:
  ```bash
  kubectl get pods -n cnpg-system
  ```

### Failover Takes Too Long

**Possible causes**:

- Large amount of uncommitted WAL data. Check replication lag:
  ```bash
  kubectl exec -it <replica-pod> -n default -c postgres -- \
    psql -c "SELECT pg_last_wal_receive_lsn() - pg_last_wal_replay_lsn() AS lag_bytes;"
  ```
- Node resource constraints. Check node capacity:
  ```bash
  kubectl top nodes
  ```

### Application Cannot Connect After Failover

**Actions**:

1. Verify the Service endpoints:
   ```bash
   kubectl get endpoints -n default
   ```
2. Check that the new primary pod is in `Ready` state.
3. Ensure your application is connecting through the Service (not a hardcoded pod IP).

## Next Steps

- [Scaling](scaling.md) — adjust instance count for better availability
- [Backup and Restore](backup-and-restore.md) — protect against data loss
- [Maintenance](maintenance.md) — routine operational tasks
