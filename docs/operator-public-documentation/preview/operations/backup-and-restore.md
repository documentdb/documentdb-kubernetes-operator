---
title: Backup and Restore
description: Create on-demand and scheduled VolumeSnapshot backups of DocumentDB clusters, restore data from a backup, and configure retention policies.
tags:
  - operations
  - backup
  - restore
---

# Backup and Restore

## Overview

Backups protect your DocumentDB cluster against data loss from accidental deletion, corruption, or failed upgrades. A reliable backup strategy is the foundation of any production deployment — without it, recovery may be impossible.

The DocumentDB operator provides a snapshot-based backup system built on Kubernetes [VolumeSnapshots](https://kubernetes.io/docs/concepts/storage/volume-snapshots/). Each backup captures a snapshot of the primary instance's persistent volume, which can later be used to bootstrap a new DocumentDB cluster. Any writes that occurred after the snapshot and before a failure are not captured — these backups do not provide point-in-time recovery (PITR).

Key characteristics:

- **VolumeSnapshot-based** — backups use the [CSI (Container Storage Interface)](https://kubernetes.io/docs/concepts/storage/volumes/#csi) driver's snapshot capability, so they are fast and storage-efficient.
- **Primary-only** — the operator always targets the primary instance for backups.
- **Namespace-scoped** — `Backup` and `ScheduledBackup` resources must reside in the same namespace as the `DocumentDB` cluster.
- **Retention-managed** — expired backups are automatically deleted by the operator.

## Prerequisites

Before creating backups, ensure your Kubernetes cluster has the required snapshot support.

=== "Kind / Minikube"

    Run the [CSI driver deployment script](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/operator/src/scripts/test-scripts/deploy-csi-driver.sh) before creating a backup:

    ```bash
    ./operator/src/scripts/test-scripts/deploy-csi-driver.sh
    ```

    Validate storage and snapshot components:

    ```bash
    kubectl get storageclass
    kubectl get volumesnapshotclasses
    ```

    You should see a `VolumeSnapshotClass` such as `csi-hostpath-snapclass`. If it's missing, re-run the deploy script.

    When creating a DocumentDB cluster, specify the CSI storage class:

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

=== "AKS"

    AKS provides a CSI driver out of the box. Set `spec.environment: aks` so the operator can auto-create a default `VolumeSnapshotClass`:

    ```yaml
    spec:
      environment: aks
    ```

=== "EKS / GKE / Other"

    Ensure the following are in place:

    - A CSI driver that supports snapshots
    - VolumeSnapshot CRDs installed
    - A default `VolumeSnapshotClass`

    Example for EKS:

    ```yaml title="volume-snapshot-class.yaml"
    apiVersion: snapshot.storage.k8s.io/v1
    kind: VolumeSnapshotClass
    metadata:
      name: ebs-snapclass
      annotations:
        snapshot.storage.kubernetes.io/is-default-class: "true"
    driver: ebs.csi.aws.com
    deletionPolicy: Delete
    ```

## Backup

=== "On-Demand Backup"

    An on-demand backup creates a single point-in-time backup of a DocumentDB cluster.

    ```yaml title="backup.yaml"
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

    For the full list of fields, see the [Backup API Reference](../api-reference.md#backup).

=== "Scheduled Backup"

    Scheduled backups automatically create `Backup` resources at regular intervals using a cron schedule.

    ```yaml title="scheduledbackup.yaml"
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

    For the full list of fields, see the [ScheduledBackup API Reference](../api-reference.md#scheduledbackup).

    **Cron schedule examples:**

    | Schedule | Meaning |
    |----------|---------|
    | `0 2 * * *` | Every day at 2:00 AM |
    | `0 */6 * * *` | Every 6 hours |
    | `0 0 * * 0` | Every Sunday at midnight |
    | `0 2 1 * *` | First day of every month at 2:00 AM |

    For more details, see [cron expression format](https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format).

    **Behavior notes:**

    - If a backup is still running when the next schedule triggers, the new backup is queued until the current one completes.
    - Failed backups do not block future scheduled backups.
    - Deleting a `ScheduledBackup` does **not** delete its previously created `Backup` objects.

## Restore from Backup

You can restore a backup by creating a **new** DocumentDB cluster that references the backup.

!!! warning
    In-place restore is not supported. You must create a new DocumentDB cluster to restore from a backup.

### Step 1: Identify the Backup

List backups for your DocumentDB cluster and choose one in `completed` status:

```bash
kubectl get backups -n <namespace>
```

### Step 2: Create a New DocumentDB Cluster

```yaml title="restore.yaml"
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
  exposeViaService:
    serviceType: ClusterIP
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
# Wait for the DocumentDB cluster to become healthy
kubectl get documentdb my-restored-cluster -n default -w
```

Once the status shows `Cluster in healthy state`, connect and verify your data. See [Connect with mongosh](../configuration/networking.md#connect-with-mongosh) for connection instructions.

### Restore Constraints

- You **cannot** restore to the original DocumentDB cluster name while the old resources exist. Delete any leftover resources first, or use a new name.
- The backup must be in `completed` status.
- The VolumeSnapshot referenced by the backup must still exist — if it was manually deleted, the backup cannot be used for recovery.
- You cannot specify both `backup` and `persistentVolume` in the same recovery spec.

For additional recovery options (including PV-based recovery), see [Restore a Deleted DocumentDB Cluster](restore-deleted-cluster.md).

## Backup Retention Policy

Each backup receives an expiration time. After expiration, the operator deletes it automatically. You can define the retention period at multiple levels:

| Level | Field | Scope |
|-------|-------|-------|
| Per-backup | `Backup.spec.retentionDays` | Overrides all other settings for a single backup |
| Per-schedule | `ScheduledBackup.spec.retentionDays` | Applied to all backups created by this schedule |
| Per-cluster | `DocumentDB.spec.backup.retentionDays` | Cluster-wide default for all backups |
| Default | — | 30 days (if nothing is set) |

The operator resolves retention in priority order: per-backup > per-schedule > per-cluster > default.

### How Expiration Is Calculated

- **Successful backups**: retention starts at `status.stoppedAt`
- **Failed backups**: retention starts at `metadata.creationTimestamp`
- Expiration = start time + (`retentionDays` × 24 hours)

### Important Retention Notes

- Changing `retentionDays` on a `ScheduledBackup` only affects **new** backups.
- Changing `DocumentDB.spec.backup.retentionDays` does not retroactively update existing backups.
- Failed backups still expire (timer starts at creation).
- Deleting the DocumentDB cluster does **not** immediately delete its `Backup` objects — they wait for expiration.
- There is no "keep forever" option. Export backups externally for permanent archival.

