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
| **Backup recovery** | A `Backup` resource in `completed` state | Point-in-time (when backup was taken) |
| **PersistentVolume recovery** | PV with `persistentVolumeReclaimPolicy: Retain` | Latest (up to the moment of deletion) |

!!! tip
    PV recovery preserves data up to the moment of deletion, while backup recovery restores to the point in time when the backup was taken. If both are available, PV recovery provides more recent data.

## Method 1: Restore from Backup

If you have a `Backup` resource in `completed` status, follow the restore procedure in [Backup and Restore — Restore from Backup](backup-and-restore.md#restore-from-backup).

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

