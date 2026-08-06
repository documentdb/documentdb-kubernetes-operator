# Backup and Restore Design

## Backup

### VolumeSnapshotClass

A [VolumeSnapshotClass](https://kubernetes.io/docs/concepts/storage/volume-snapshot-classes/) must exist before taking volume snapshots. 
It specifies which CSI driver to use for creating volume snapshots.

**CNPG's Approach:** CNPG requires users to manually create the VolumeSnapshotClass.

**Our Approach:** The DocumentDB operator automatically creates a VolumeSnapshotClass when a Backup resource is created, if one doesn't already exist.

#### Current Support

Currently, we only support **AKS (Azure Kubernetes Service)** with the **`disk.csi.azure.com`** CSI driver.

The operator will automatically create a VolumeSnapshotClass named `azure-disk-snapclass` configured with the Azure disk CSI driver when you create your first Backup resource.

### Backup CRD

We have our own Backup CRD and backup controller in the DocumentDB operator. 
When a Backup resource is created, it triggers a [Kubernetes Volume Snapshot](https://kubernetes.io/blog/2020/12/10/kubernetes-1.20-volume-snapshot-moves-to-ga/#what-is-a-volume-snapshot) on the primary instance of a DocumentDB cluster.

Since DocumentDB uses a [CloudNativePG (CNPG)](https://cloudnative-pg.io/) cluster as the backend, we leverage CNPG's backup functionality. 
When users create a DocumentDB Backup resource, the operator automatically creates a corresponding [CNPG Backup](https://cloudnative-pg.io/documentation/current/backup/) resource.

**Why not use CNPG Backup directly?**

In this phase, our Backup resource acts as a wrapper around CNPG Backup. We maintain our own CRD to support additional capabilities:
- **Multi-region backup support** (implemented) — in a multi-region deployment the operator backs up only the region that currently holds the primary role, skips requests against standby regions, and automatically follows the primary across failovers (site-swap). See the [Backups in multi-region deployments](../operator-public-documentation/preview/operations/backup-and-restore.md#backups-in-multi-region-deployments) user guide.
- **Future:** Multi-node backup capabilities

### Creating On-Demand Backups

Create an on-demand backup by applying the following resource:

```yaml
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: backup-example
  namespace: documentdb-preview-ns
spec:
  cluster:
    name: documentdb-preview
```

## Scheduled Backup

### ScheduledBackup CRD

The ScheduledBackup CRD enables automated, recurring backups using [cron expressions](https://en.wikipedia.org/wiki/Cron).

**Why not use CNPG ScheduledBackup?**

CNPG's [ScheduledBackup](https://cloudnative-pg.io/documentation/current/backup/#scheduled-backups) creates CNPG Backup resources directly. 
Since we have our own Backup CRD with custom logic, we need our own ScheduledBackup implementation.

### Creating Scheduled Backups

Create a scheduled backup using a cron expression:

```yaml
apiVersion: documentdb.io/preview
kind: ScheduledBackup
metadata:
  name: backup-example
  namespace: documentdb-preview-ns
spec:
  schedule: "0 0 0 * * *"  # Daily at midnight
  cluster:
    name: documentdb-preview
```

## Retention Policy

Retention policies control how long backups are preserved before automatic deletion. 
Backups are retained according to their configured retention period, even if the cluster is deleted—allowing users to restore from deleted clusters if needed.

The DocumentDB operator supports retention policies at three levels, with the following precedence:
1. **Backup-level retention** (highest priority) - applies to individual backups
2. **ScheduledBackup-level retention** - applies to backups created by a schedule
3. **Cluster-level retention** (default) - applies to all backups from a cluster

### Backup-Level Retention (On-Demand)

**Field:** `spec.retentionDays`

**Purpose:** Overrides the cluster-level retention for an individual on-demand backup. Useful for long-term retention of critical backups.

**Example:**
```yaml
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: backup-example
spec:
  cluster:
    name: documentdb-preview
  retentionDays: 90  # Override: retain this backup for 90 days
```

### ScheduledBackup-Level Retention

**Field:** `spec.retentionDays`

**Purpose:** Overrides the cluster-level retention for backups created by a scheduled backup job.

**Example:**
```yaml
apiVersion: documentdb.io/preview
kind: ScheduledBackup
metadata:
  name: backup-example
spec:
  schedule: "0 0 0 * * *"  # Daily at midnight
  cluster:
    name: documentdb-preview
  retentionDays: 14  # Override: retain scheduled backups for 14 days
```

### Cluster-Level Retention (Default)

**Field:** `spec.backup.retentionDays`

**Purpose:** Sets the default retention period for all backups created from this cluster.

**Example:**
```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
spec:
  backup:
    retentionDays: 30  # Default: retain all backups for 30 days
  # ...other fields...
```


### Expiration and Cleanup

- **Expiration Time:** Calculated as `backup.status.stoppedAt + retentionDays` (or `backup.creationTimestamp + retentionDays` if backup failed)
- **Automatic Deletion:** When a backup's expiration time is reached, the operator automatically deletes the Backup resource and its associated volume snapshot
- **Status Field:** `backup.status.expiredAt` indicates when the backup will be automatically deleted

Note: CNPG does not yet support retention policies for volume snapshots. This is an ongoing discussion in the CNPG community (see [issue #6009](https://github.com/cloudnative-pg/cloudnative-pg/issues/6009)).


## Deletion Behavior

- **Deleting a Backup resource:** Immediately deletes the associated volume snapshot
- **Deleting a ScheduledBackup resource:** Stops creating new backups but does not delete existing backups created by that schedule
- **Deleting a Cluster:** Backups are retained according to the cluster's `retentionPeriod` setting

## Restore

### Recovery from Backup

The operator supports bootstrapping a new cluster from an existing backup. In-place restoration is not currently supported.

**Recovery Example:**

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview-restore
  namespace: documentdb-preview-ns
spec:
  bootstrap:
    recovery:
      backup:
        name: backup-example
  ......
```
