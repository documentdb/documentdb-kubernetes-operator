# Restore a Deleted Cluster

This guide explains how to recover a DocumentDB cluster after accidental deletion using either a backup or a retained PersistentVolume.

## Overview

When a DocumentDB cluster is deleted, there are two paths to recovery:

| Method | Requires | Data Freshness |
|--------|----------|----------------|
| **Backup recovery** | A `Backup` resource in `Succeeded` state | Point-in-time (when backup was taken) |
| **PersistentVolume recovery** | PV with `persistentVolumeReclaimPolicy: Retain` | Latest (up to the moment of deletion) |

!!! tip
    PV recovery preserves data up to the moment of deletion, while backup recovery restores to the point in time when the backup was taken. If both are available, PV recovery provides more recent data.

## Method 1: Restore from Backup

Use this method if you have a `Backup` resource in `Succeeded` status.

### Step 1: Find Available Backups

```bash
kubectl get backups -n <namespace>
```

Example output:

```
NAME                STATUS      AGE
nightly-20260305    Succeeded   1d
nightly-20260304    Succeeded   2d
pre-upgrade-backup  Succeeded   5d
```

Choose the most recent successful backup.

### Step 2: Create a New Cluster from Backup

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-restored-cluster
  namespace: <namespace>
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
  bootstrap:
    recovery:
      backup:
        name: nightly-20260305  # The backup to restore from
```

```bash
kubectl apply -f restore-from-backup.yaml
```

### Step 3: Wait for the Cluster to Be Ready

```bash
kubectl get documentdb my-restored-cluster -n <namespace> -w
```

Wait until the status shows `Cluster in healthy state`.

### Step 4: Verify Data

```bash
# Port-forward to the new cluster
kubectl port-forward pod/my-restored-cluster-1 10260:10260 -n <namespace>

# Connect and verify
mongosh 127.0.0.1:10260 -u <username> -p <password> \
  --authenticationMechanism SCRAM-SHA-256 \
  --tls --tlsAllowInvalidCertificates

# Check your databases and collections
show dbs
use <your-database>
db.<your-collection>.countDocuments()
```

## Method 2: Restore from Retained PersistentVolume

Use this method if your deleted cluster had `persistentVolumeReclaimPolicy: Retain` configured (this is the default). This approach recovers data up to the moment of deletion.

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

### Step 2: Create a New Cluster with PV Recovery

```yaml
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

### What the Operator Does Automatically

1. Validates that the PV exists and is in `Available` or `Released` state.
2. If the PV is `Released`, clears the `claimRef` to make it available for binding.
3. Creates a temporary PVC bound to the retained PV.
4. Uses the temporary PVC as a data source for CNPG to clone the data.
5. After the cluster becomes healthy, deletes the temporary PVC.
6. The source PV is released back for manual cleanup or reuse.

### Step 3: Verify the Recovery

```bash
# Wait for the cluster to be ready
kubectl get documentdb my-recovered-cluster -n <namespace> -w

# Connect and verify data
kubectl port-forward pod/my-recovered-cluster-1 10260:10260 -n <namespace>
mongosh 127.0.0.1:10260 -u <username> -p <password> \
  --authenticationMechanism SCRAM-SHA-256 \
  --tls --tlsAllowInvalidCertificates

# Verify databases and collections
show dbs
use <your-database>
db.<your-collection>.countDocuments()
```

### Step 4: Clean Up the Source PV

After confirming the recovery is successful, delete the source PV:

```bash
kubectl delete pv pvc-abc123-def456-789
```

## Recovery Constraints

### Backup Recovery

- You **cannot** restore to the original cluster name while the old resources exist. Delete any leftover resources first, or use a new name.
- The backup must be in `Succeeded` status.
- The VolumeSnapshot referenced by the backup must still exist.
- You cannot specify both `backup` and `persistentVolume` in the same recovery spec.

### PV Recovery

- The PV must exist and be in `Available` or `Released` state.
- The PV must have been created with `persistentVolumeReclaimPolicy: Retain` (this is the default).
- PV recovery preserves all data including users, roles, and collections.
- The new cluster can have a different name from the original.
- You cannot specify both `backup` and `persistentVolume` in the same recovery spec.

## Common Pitfalls

### Pitfall 1: PV Already Deleted

If `persistentVolumeReclaimPolicy` was set to `Delete`, the PV is removed along with the cluster. In this case, your only option is backup recovery.

**Prevention**: Keep the default `Retain` policy for production clusters.

### Pitfall 2: No Backups Available

If there are no backups and PVs were deleted:

**Prevention**: Always configure [scheduled backups](backup-and-restore.md#scheduled-backups) for production clusters.

### Pitfall 3: Backup VolumeSnapshot Deleted

If the VolumeSnapshot referenced by a backup was manually deleted, the backup cannot be used for recovery even though the `Backup` resource still exists.

**Prevention**: Do not manually delete VolumeSnapshots associated with active backups.

### Pitfall 4: Storage Class Mismatch

When restoring from a PV, ensure the new cluster uses the same (or compatible) storage class as the original.

### Pitfall 5: Namespace Mismatch

`Backup` resources are namespace-scoped. The new cluster must be created in the same namespace as the backup.

## Post-Recovery Checklist

After restoring a cluster:

- [ ] **Verify data integrity** — connect and confirm your databases, collections, and documents are intact.
- [ ] **Update application connection strings** — if the cluster name changed, update your applications.
- [ ] **Set up scheduled backups** — configure [scheduled backups](backup-and-restore.md#scheduled-backups) for the new cluster.
- [ ] **Configure PV retention** — ensure `persistentVolumeReclaimPolicy: Retain` is set.
- [ ] **Clean up old resources** — delete any leftover PVs, PVCs, or backup resources from the deleted cluster.

## Next Steps

- [Backup and Restore](backup-and-restore.md) — set up regular backups
- [Scaling](scaling.md) — scale the restored cluster
- [Failover](failover.md) — configure high availability
