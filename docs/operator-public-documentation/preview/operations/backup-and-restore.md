# Backup and Restore

This guide covers the concepts, procedures, and troubleshooting for backing up and restoring DocumentDB clusters using the Kubernetes operator.

## Overview

The DocumentDB operator provides a snapshot-based backup system built on Kubernetes [VolumeSnapshots](https://kubernetes.io/docs/concepts/storage/volume-snapshots/). Each backup captures a point-in-time copy of the primary instance's persistent volume, which can later be used to bootstrap a new cluster.

Key characteristics:

- **VolumeSnapshot-based** — backups use the CSI driver's snapshot capability, so they are fast and storage-efficient.
- **Primary-only** — the operator always targets the primary instance for backups.
- **Namespace-scoped** — `Backup` and `ScheduledBackup` resources must reside in the same namespace as the `DocumentDB` cluster.
- **Retention-managed** — expired backups are automatically deleted by the operator.

## Prerequisites

Before creating backups, ensure your cluster has the required snapshot infrastructure.

### Kind or Minikube

Run the CSI driver deployment script **before** installing the operator:

```bash
./operator/src/scripts/test-scripts/deploy-csi-driver.sh
```

Validate storage and snapshot components:

```bash
kubectl get storageclass
kubectl get volumesnapshotclasses
```

You should see a `VolumeSnapshotClass` such as `csi-hostpath-snapclass`. If it's missing, re-run the deploy script.

When creating a cluster, specify the CSI storage class:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  resource:
    storage:
      storageClass: csi-hostpath-sc
```

### AKS

AKS provides a CSI driver out of the box. Set `spec.environment: aks` so the operator can auto-create a default `VolumeSnapshotClass`:

```yaml
spec:
  environment: aks
```

### EKS / GKE / Other Providers

Ensure the following are in place:

- A CSI driver that supports snapshots
- VolumeSnapshot CRDs installed
- A default `VolumeSnapshotClass`

Example for EKS:

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: ebs-snapclass
  annotations:
    snapshot.storage.kubernetes.io/is-default-class: "true"
driver: ebs.csi.aws.com
deletionPolicy: Delete
```

## On-Demand Backup

An on-demand backup creates a single backup of a DocumentDB cluster.

### Creating a Backup

```yaml
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: my-backup
  namespace: default
spec:
  cluster:
    name: my-documentdb-cluster
  retentionDays: 30  # Optional: defaults to cluster setting or 30 days
```

```bash
kubectl apply -f backup.yaml
```

### Monitoring Backup Progress

```bash
# List all backups
kubectl get backups -n default

# Watch backup status
kubectl get backups -n default -w

# Detailed backup information
kubectl describe backup my-backup -n default
```

A backup transitions through the following phases:

| Phase | Description |
|-------|-------------|
| `Pending` | Backup created, waiting for VolumeSnapshot |
| `Running` | VolumeSnapshot in progress |
| `Succeeded` | Backup completed successfully |
| `Failed` | Backup failed (check events for details) |
| `Skipped` | Backup skipped (for example, on a standby cluster) |

### Verifying a Successful Backup

After the backup shows `Succeeded`:

```bash
# Confirm the VolumeSnapshot was created
kubectl get volumesnapshots -n default

# Check the backup status fields
kubectl get backup my-backup -n default -o jsonpath='{.status}' | jq
```

The status should show `startedAt`, `stoppedAt`, and `expiredAt` timestamps.

## Scheduled Backups

Scheduled backups automatically create `Backup` resources at regular intervals using a cron schedule.

### Creating a Scheduled Backup

```yaml
apiVersion: documentdb.io/preview
kind: ScheduledBackup
metadata:
  name: nightly-backup
  namespace: default
spec:
  cluster:
    name: my-documentdb-cluster
  schedule: "0 2 * * *"    # Daily at 2:00 AM
  retentionDays: 14         # Optional
```

```bash
kubectl apply -f scheduledbackup.yaml
```

### Cron Schedule Examples

| Schedule | Meaning |
|----------|---------|
| `0 2 * * *` | Every day at 2:00 AM |
| `0 */6 * * *` | Every 6 hours |
| `0 0 * * 0` | Every Sunday at midnight |
| `*/15 * * * *` | Every 15 minutes |
| `0 2 1 * *` | First day of every month at 2:00 AM |

For more details, see [cron expression format](https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format).

### Monitoring Scheduled Backups

```bash
# Check schedule status (last and next scheduled time)
kubectl get scheduledbackups -n default

# List generated backups
kubectl get backups -n default --sort-by=.metadata.creationTimestamp
```

### Behavior Notes

- If a backup is still running when the next schedule triggers, the new backup is queued until the current one completes.
- Failed backups do not block future scheduled backups.
- `ScheduledBackup` resources are garbage-collected when the source cluster is deleted.
- Deleting a `ScheduledBackup` does **not** delete its previously created `Backup` objects.

## Restore from Backup

You can restore a backup by creating a **new** DocumentDB cluster that references the backup.

!!! warning
    In-place restore is not supported. You must create a new cluster to restore from a backup.

### Step 1: Identify the Backup

```bash
kubectl get backups -n default
```

Choose a backup in `Succeeded` status.

### Step 2: Create a New Cluster

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-restored-cluster
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 10Gi
  bootstrap:
    recovery:
      backup:
        name: my-backup  # Name of the backup to restore from
```

```bash
kubectl apply -f restore.yaml
```

### Step 3: Verify the Restore

```bash
# Wait for the cluster to become healthy
kubectl get documentdb my-restored-cluster -n default -w

# Connect and verify data
kubectl port-forward pod/my-restored-cluster-1 10260:10260 -n default
mongosh 127.0.0.1:10260 -u <username> -p <password> \
  --authenticationMechanism SCRAM-SHA-256 \
  --tls --tlsAllowInvalidCertificates
```

## Backup Retention Policy

Each backup receives an expiration time. After expiration, the operator deletes it automatically.

### Retention Priority (highest to lowest)

1. `Backup.spec.retentionDays` — per-backup override
2. `ScheduledBackup.spec.retentionDays` — applied to all backups it creates
3. `DocumentDB.spec.backup.retentionDays` — cluster-wide default
4. **Default**: 30 days (if nothing is set)

### How Expiration Is Calculated

- **Successful backups**: retention starts at `status.stoppedAt`
- **Failed backups**: retention starts at `metadata.creationTimestamp`
- Expiration = start time + (`retentionDays` × 24 hours)

### Important Retention Notes

- Changing `retentionDays` on a `ScheduledBackup` only affects **new** backups.
- Changing `DocumentDB.spec.backup.retentionDays` does not retroactively update existing backups.
- Failed backups still expire (timer starts at creation).
- Deleting the cluster does **not** immediately delete its `Backup` objects — they wait for expiration.
- There is no "keep forever" option. Export backups externally for permanent archival.

## Troubleshooting

### Backup Stays in Pending State

**Symptoms**: Backup remains in `Pending` phase indefinitely.

**Possible causes**:

- No `VolumeSnapshotClass` available. Verify:
  ```bash
  kubectl get volumesnapshotclasses
  ```
- CSI driver does not support snapshots. Check CSI driver capabilities.
- The `DocumentDB` cluster primary is not ready.

### Backup Fails

**Symptoms**: Backup transitions to `Failed`.

**Actions**:

```bash
# Check backup events
kubectl describe backup <backup-name> -n <namespace>

# Check operator logs
kubectl logs -n documentdb-operator deployment/documentdb-operator --tail=100

# Check CNPG backup status
kubectl get backups.postgresql.cnpg.io -n <namespace>
```

### Restore Cluster Not Starting

**Symptoms**: Restored cluster pods stay in `Pending` or `CrashLoopBackOff`.

**Actions**:

- Verify the backup exists and is in `Succeeded` status.
- Ensure the VolumeSnapshot referenced by the backup still exists.
- Check that the storage class supports volume cloning.
- Review pod events:
  ```bash
  kubectl describe pod <restored-cluster-pod> -n <namespace>
  ```

### Backups Skipped on Standby Clusters

This is expected behavior. In multi-region setups, the operator skips backups on standby (replica) clusters to avoid duplicate backups. Only the primary cluster creates backups.

## Next Steps

- [Restore a Deleted Cluster](restore-deleted-cluster.md) — recover from accidental deletion
- [Scaling](scaling.md) — adjust instance count and storage
- [Failover](failover.md) — understand automatic and manual failover
