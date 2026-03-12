---
title: Restore a Deleted DocumentDB Cluster
description: Recover a DocumentDB cluster after accidental deletion using backup recovery or retained PersistentVolume recovery.
tags:
  - operations
  - restore
  - disaster-recovery
---

# Restore a Deleted DocumentDB Cluster

## Overview

Restoring a deleted DocumentDB cluster recovers your data after accidental or unplanned DocumentDB cluster removal. Acting quickly matters — retained PersistentVolumes preserve data up to the moment of deletion, while backups restore to the point in time they were taken.

When a DocumentDB cluster is deleted, there are two paths to recovery:

| Method | Requires | Data Freshness |
|--------|----------|----------------|
| **Backup recovery** | A `Backup` resource in `Succeeded` state | Point-in-time (when backup was taken) |
| **PersistentVolume recovery** | PV with `persistentVolumeReclaimPolicy: Retain` | Latest (up to the moment of deletion) |

!!! tip
    PV recovery preserves data up to the moment of deletion, while backup recovery restores to the point in time when the backup was taken. If both are available, PV recovery provides more recent data.

## Method 1: Restore from Backup

If you have a `Backup` resource in `Succeeded` status, follow the restore procedure in [Backup and Restore — Restore from Backup](backup-and-restore.md#restore-from-backup).

## Method 2: Restore from Retained PersistentVolume

Use this method if your deleted DocumentDB cluster had `persistentVolumeReclaimPolicy: Retain` configured (this is the default). This approach recovers data up to the moment of deletion.

### Step 1: Find the Retained PV

```bash
kubectl get pv -l documentdb.io/cluster=<cluster-name>,documentdb.io/namespace=<namespace>
```

Example output:

```
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS     CLAIM
pvc-abc123-def456-789                      10Gi       RWO            Retain           Released   default/data-my-cluster-1
```

The PV should be in `Released` or `Available` status.

### Step 2: Create a New DocumentDB Cluster with PV Recovery

```yaml title="restore-from-pv.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-recovered-cluster
  namespace: <namespace>
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
      storageClass: <original-storage-class>
  exposeViaService:
    serviceType: ClusterIP
  bootstrap:
    recovery:
      persistentVolume:
        name: pvc-abc123-def456-789  # The retained PV name
```

```bash
kubectl apply -f restore-from-pv.yaml
```

### Step 3: Verify the Recovery

```bash
# Wait for the DocumentDB cluster to be ready
kubectl get documentdb my-recovered-cluster -n <namespace> -w
```

Once the status shows `Cluster in healthy state`, connect and verify your data. See [Connect with mongosh](../configuration/networking.md#connect-with-mongosh) for connection instructions.

### Step 4: Clean Up the Source PV

After confirming the recovery is successful, delete the source PV:

```bash
kubectl delete pv pvc-abc123-def456-789
```

## Recovery Constraints

### Backup Recovery

- You **cannot** restore to the original DocumentDB cluster name while the old resources exist. Delete any leftover resources first, or use a new name.
- The backup must be in `Succeeded` status.
- The VolumeSnapshot referenced by the backup must still exist.
- You cannot specify both `backup` and `persistentVolume` in the same recovery spec.

### PV Recovery

- The PV must exist and be in `Available` or `Released` state.
- The PV must have been created with `persistentVolumeReclaimPolicy: Retain` (this is the default).
- PV recovery preserves all data including users, roles, and collections.
- The new DocumentDB cluster can have a different name from the original.
- You cannot specify both `backup` and `persistentVolume` in the same recovery spec.

## Common Pitfalls

### Pitfall 1: PV Already Deleted

If `persistentVolumeReclaimPolicy` was set to `Delete`, the PV is removed along with the DocumentDB cluster. In this case, your only option is backup recovery.

**Prevention**: Keep the default `Retain` policy for production DocumentDB clusters.

### Pitfall 2: No Backups Available

If there are no backups and PVs were deleted:

**Prevention**: Always configure [scheduled backups](backup-and-restore.md#scheduled-backups) for production DocumentDB clusters.

### Pitfall 3: Backup VolumeSnapshot Deleted

If the VolumeSnapshot referenced by a backup was manually deleted, the backup cannot be used for recovery even though the `Backup` resource still exists.

**Prevention**: Do not manually delete VolumeSnapshots associated with active backups.

### Pitfall 4: Storage Class Mismatch

When restoring from a PV, ensure the new DocumentDB cluster uses the same (or compatible) storage class as the original.

### Pitfall 5: Namespace Mismatch

`Backup` resources are namespace-scoped. The new DocumentDB cluster must be created in the same namespace as the backup.

## Post-Recovery Checklist

After restoring a DocumentDB cluster:

- [ ] **Verify data integrity** — connect and confirm your databases, collections, and documents are intact.
- [ ] **Update application connection strings** — if the DocumentDB cluster name changed, update your applications.
- [ ] **Set up scheduled backups** — configure [scheduled backups](backup-and-restore.md#scheduled-backups) for the new DocumentDB cluster.
- [ ] **Configure PV retention** — ensure `persistentVolumeReclaimPolicy: Retain` is set.
- [ ] **Clean up old resources** — delete any leftover PVs, PVCs, or backup resources from the deleted DocumentDB cluster.

## Next Steps

- [Backup and Restore](backup-and-restore.md) — set up regular backups
- [Scaling](scaling.md) — scale the restored DocumentDB cluster
- [Failover](failover.md) — configure high availability
