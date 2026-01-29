# Backup and Restore

## Prerequisites

### For Kind or Minikube

1. Run the CSI driver deployment script **before** installing the documentdb-operator:

```bash
./operator/src/scripts/test-scripts/deploy-csi-driver.sh
```

2. Validate storage and snapshot components:


```bash
kubectl get storageclass
kubectl get volumesnapshotclasses
```

You should see something like:

StorageClasses:
```
NAME                 PROVISIONER             RECLAIMPOLICY   VOLUMEBINDINGMODE      ALLOWVOLUMEEXPANSION   AGE
csi-hostpath-sc      hostpath.csi.k8s.io     Delete          Immediate              true                   5d20h
standard (default)   rancher.io/local-path   Delete          WaitForFirstConsumer   false                  5d20h
```

VolumeSnapshotClasses:
```
NAME                     DRIVER                DELETIONPOLICY   AGE
csi-hostpath-snapclass   hostpath.csi.k8s.io   Delete           5d19h
```

If `csi-hostpath-snapclass` isn't present, the deploy script didn’t finish correctly. Re-run it.

3. When creating a cluster, ensure you set the appropriate storage class:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  resource:
    storage:
      storageClass: csi-hostpath-sc  # Specify your CSI storage class
  # ... other configuration
```

### AKS

AKS already provides a CSI driver. 

To allow the documentdb-operator to auto-create a default `VolumeSnapshotClass`, set `spec.environment: aks` in your `DocumentDB` spec:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  environment: aks
  # ... other configuration
```

### Other Providers (EKS / GKE / Custom)

Support is emerging; you must manually ensure:
- A CSI driver that supports snapshots
- VolumeSnapshot CRDs installed
- A default `VolumeSnapshotClass`

Example manual snapshot class (adjust DRIVER accordingly):

```yaml
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: generic-snapclass
  annotations:
    snapshot.storage.kubernetes.io/is-default-class: "true"
driver: ebs.csi.aws.com        # or pd.csi.storage.gke.io / other
deletionPolicy: Delete
```

Apply:
```bash
kubectl apply -f volumesnapshotclass.yaml
```

## On-Demand Backup

An on-demand backup creates a single backup of a DocumentDB cluster.

### Creating an On-Demand Backup

Create a `Backup` resource:

```yaml
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: my-backup
  namespace: default # Same namespace as DocumentDB cluster
spec:
  cluster:
    name: my-documentdb-cluster  # Must match the DocumentDB cluster name
  retentionDays: 30  # Optional: backup retention period in days
```

Apply the resource:

```bash
kubectl apply -f backup.yaml
```

### Monitoring Backup Status

Check the backup status:

```bash
kubectl get backups -n default
```

View detailed backup information:

```bash
kubectl describe backup my-backup -n default
```

## Scheduled Backups

Scheduled backups automatically create backups at regular intervals using a cron schedule.

### Creating a Scheduled Backup

Create a `ScheduledBackup` resource on yaml file scheduledbackup.yaml

```yaml
apiVersion: documentdb.io/preview
kind: ScheduledBackup
metadata:
  name: my-scheduled-backup
  namespace: default # Same namespace as DocumentDB
spec:
  cluster:
    name: my-documentdb-cluster  # Must match the DocumentDB cluster name
  schedule: "0 2 * * *"  # Cron expression: daily at 2:00 AM
  retentionDays: 30  # Optional: backup retention period in days
```

Apply the resource:

```bash
kubectl apply -f scheduledbackup.yaml
```

### Cron Schedule Format

The schedule uses standard cron expression format. Common examples:

| Schedule | Meaning |
|----------|---------|
| `0 2 * * *` | Every day at 2:00 AM |
| `0 */6 * * *` | Every 6 hours |
| `0 0 * * 0` | Every Sunday at midnight |
| `*/15 * * * *` | Every 15 minutes |
| `0 2 1 * *` | First day of every month at 2:00 AM |

For more details, see [cron expression format](https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format).

### Monitoring Scheduled Backups

List all ScheduledBackups:

```bash
kubectl get scheduledbackups -n default
```

Check the generated backups:

```bash
kubectl get backups -n default
```

### Important Notes

- If a backup is currently running, the next backup will be queued and start after the current one completes
- The operator will automatically create `Backup` resources according to the schedule
- Failed backups do not prevent subsequent backups from being scheduled
- ScheduledBackups are automatically garbage collected when the source cluster is deleted
- Deleting a ScheduledBackup does NOT delete its created Backup objects; they remain until expiration

## Restore from Backup

You can restore a backup to a **different DocumentDB cluster**.

### List Available Backups

First, identify the backup you want to restore:

```bash
kubectl get backups -n default
```

### Create a New Cluster with Backup Recovery

Create a new `DocumentDB` resource with recovery configuration:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-restored-cluster
  namespace: default
spec:
  bootstrap:
    recovery:
      backup:
        name: my-backup  # Reference the backup to restore from
  #...... other configurations
```

Apply the resource:

```bash
kubectl apply -f restore.yaml
```

## Backup Retention Policy

Backups don't live forever. Each one gets an expiration time. After that time passes, the operator deletes it automatically.

### Where the Retention Value Comes From (priority order)
1. `Backup.spec.retentionDays` (per backup override)
2. `ScheduledBackup.spec.retentionDays` (copied into each created Backup)
3. `DocumentDB.spec.backup.retentionDays` (cluster default)
4. Default (if none set): 30 days

### How it's calculated
- Success: retention starts at `status.stoppedAt`
- Failure: retention starts at `metadata.creationTimestamp`
- Expiration = start + retentionDays * 24h

### Examples
Per-backup override:
```yaml
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: monthly-audit
spec:
  cluster:
    name: prod-cluster
  retentionDays: 90
```

Scheduled backups (14‑day retention):
```yaml
apiVersion: documentdb.io/preview
kind: ScheduledBackup
metadata:
  name: nightly
spec:
  cluster:
    name: prod-cluster
  schedule: "0 2 * * *"
  retentionDays: 14
```

Cluster default (used when Backup doesn't set retention):
```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: prod-cluster
spec:
  backup:
    retentionDays: 30
```

### Important Notes
- Changing retention on a `ScheduledBackup` only affects new backups, not old ones.
- Changing `DocumentDB.spec.backup.retentionDays` doesn’t retroactively update existing backups.
- Failed backups still expire (timer starts at creation).
- Deleting the cluster does NOT delete its Backup objects immediately—they still wait for expiration.
- No "keep forever" mode—export externally if you need permanent archival.

## PersistentVolume Retention and Recovery

The DocumentDB operator supports retaining PersistentVolumes (PVs) after cluster deletion, allowing you to recover data by creating a new cluster from the retained PV.

### Configuring PV Retention

By default, PVs are deleted when the DocumentDB cluster is deleted. To retain the PV, set the `persistentVolumeReclaimPolicy` to `Retain`:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  resource:
    storage:
      pvcSize: 10Gi
      persistentVolumeReclaimPolicy: Retain  # Keep PV after cluster deletion
  # ... other configuration
```

### How PV Retention Works

1. When you set `persistentVolumeReclaimPolicy: Retain`, the operator updates the underlying PersistentVolume's reclaim policy
2. When the DocumentDB cluster is deleted, the PVC is deleted but the PV is retained in a "Released" state
3. The retained PV contains all the database data and can be used to recover the cluster

### Recovering from a Retained PV

To restore a DocumentDB cluster from a retained PV:

**Step 1: Identify the retained PV**

```bash
# List PVs in Released state
kubectl get pv | grep Released
```

**Step 2: Clear the claimRef to make the PV available**

The PV in "Released" state still has a reference to the old deleted PVC. Clear it:

```bash
kubectl patch pv <pv-name> --type=json -p='[{"op": "remove", "path": "/spec/claimRef"}]'
```

Verify the PV is now "Available":

```bash
kubectl get pv <pv-name>
```

**Step 3: Create a new PVC bound to the retained PV**

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: recovered-pvc
  namespace: default
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi  # Must match the PV capacity
  storageClassName: managed-csi  # Must match the PV's storage class
  volumeName: <pv-name>  # Specify the retained PV name
```

Apply and verify the PVC is bound:

```bash
kubectl apply -f recovered-pvc.yaml
kubectl get pvc recovered-pvc
```

**Step 4: Create a new DocumentDB cluster with PVC recovery**

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-recovered-cluster
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 10Gi
      storageClass: managed-csi
  exposeViaService:
    serviceType: ClusterIP
  bootstrap:
    recovery:
      pvc:
        name: recovered-pvc  # Reference the PVC bound to the retained PV
```

### Important Notes for PV Recovery

- The new cluster must be in the same namespace as the PVC
- Storage size and class should match the original configuration
- You cannot specify both `backup` and `pvc` recovery at the same time
- PVC recovery preserves all data including users, roles, and collections
- After successful recovery, consider setting up regular backups for the new cluster
