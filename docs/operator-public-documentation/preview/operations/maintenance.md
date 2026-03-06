# Maintenance

This guide covers day-to-day operational tasks for managing DocumentDB clusters, including monitoring, log management, resource tuning, and node maintenance.

## Monitoring Cluster Health

### Cluster Status

Check the overall health of your DocumentDB clusters:

```bash
# List all DocumentDB clusters and their status
kubectl get documentdb -n <namespace>

# Detailed cluster information
kubectl describe documentdb <cluster-name> -n <namespace>
```

A healthy cluster shows `Cluster in healthy state` in the STATUS column.

### Pod Health

```bash
# Check pod status (each pod runs PostgreSQL + gateway sidecar)
kubectl get pods -n <namespace> -l documentdb.io/cluster=<cluster-name>

# View pod resource usage
kubectl top pods -n <namespace>
```

Each pod should show `2/2` in the READY column (PostgreSQL container + gateway sidecar).

### CNPG Cluster Status

For deeper diagnostics, inspect the underlying CNPG cluster:

```bash
# CNPG cluster status
kubectl get clusters.postgresql.cnpg.io -n <namespace>

# Detailed CNPG status
kubectl describe clusters.postgresql.cnpg.io <cluster-name> -n <namespace>
```

## Log Management

### Operator Logs

View the DocumentDB operator logs:

```bash
# Recent operator logs
kubectl logs -n documentdb-operator deployment/documentdb-operator --tail=100

# Follow operator logs in real time
kubectl logs -n documentdb-operator deployment/documentdb-operator -f
```

### PostgreSQL Logs

Access PostgreSQL logs inside a specific pod:

```bash
kubectl exec -it <pod-name> -n <namespace> -c postgres -- \
  cat /controller/log/postgresql.log
```

### Gateway Logs

Access gateway (sidecar) logs:

```bash
kubectl logs <pod-name> -n <namespace> -c gateway
```

### Configuring Log Level

Adjust the DocumentDB log level in the cluster spec:

```yaml
spec:
  logLevel: "info"  # Options: debug, info, warning, error
```

Apply the change:

```bash
kubectl apply -f documentdb.yaml
```

## Resource Management

### Viewing Current Resource Usage

```bash
# Pod resource consumption
kubectl top pods -n <namespace>

# Node resource consumption
kubectl top nodes
```

### Recommended Resource Allocations

| Workload | CPU | Memory | Storage |
|----------|-----|--------|---------|
| Development | 1 core | 2 GiB | 10 GiB |
| Production | 2–4 cores | 4–8 GiB | 100 GiB+ |
| High-load | 4–8 cores | 8–16 GiB | 500 GiB+ |

### Storage Monitoring

Monitor persistent volume usage:

```bash
# Check PVC status and capacity
kubectl get pvc -n <namespace>

# Check actual disk usage inside a pod
kubectl exec -it <pod-name> -n <namespace> -c postgres -- df -h /var/lib/postgresql/data
```

!!! warning
    If storage usage approaches capacity, expand storage before the volume fills up. See the [Scaling guide](scaling.md#storage-expansion) for instructions.

## Node Maintenance

When performing maintenance on Kubernetes nodes (OS updates, hardware changes), follow these steps to minimize impact on your DocumentDB cluster.

### Step 1: Identify Affected Pods

```bash
# Find which node each pod runs on
kubectl get pods -n <namespace> -o wide
```

### Step 2: Cordon the Node

Prevent new pods from being scheduled on the node:

```bash
kubectl cordon <node-name>
```

### Step 3: Drain the Node

Evict pods from the node (with appropriate timeouts):

```bash
kubectl drain <node-name> \
  --ignore-daemonsets \
  --delete-emptydir-data \
  --grace-period=300
```

**What happens**:

- If the primary pod is on this node, CNPG triggers an automatic failover to a replica before evicting.
- Replica pods are rescheduled to other available nodes.
- With 3 instances, the cluster remains available throughout.

!!! warning
    With a single-instance cluster (`instancesPerNode: 1`), draining the node causes downtime. Scale to at least 2 instances before performing node maintenance.

### Step 4: Perform Maintenance

Complete your node maintenance (OS updates, patches, etc.).

### Step 5: Uncordon the Node

Allow pods to be scheduled on the node again:

```bash
kubectl uncordon <node-name>
```

### Step 6: Verify Cluster Health

```bash
kubectl get documentdb <cluster-name> -n <namespace>
kubectl get pods -n <namespace>
```

## Rolling Restarts

To restart all DocumentDB pods without downtime (for example, to pick up ConfigMap changes):

```bash
# CNPG handles rolling restarts when the cluster spec changes
# You can trigger a restart by updating an annotation
kubectl annotate clusters.postgresql.cnpg.io <cluster-name> -n <namespace> \
  kubectl.kubernetes.io/restartedAt="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
```

CNPG restarts replicas first, then the primary, ensuring continuous availability (with 2+ instances).

## Routine Checks

### Daily

- [ ] Verify cluster health: `kubectl get documentdb -n <namespace>`
- [ ] Check backup status: `kubectl get backups -n <namespace>`
- [ ] Monitor pod status: `kubectl get pods -n <namespace>`

### Weekly

- [ ] Review operator logs for warnings or errors
- [ ] Check storage utilization across all PVCs
- [ ] Verify scheduled backups are running on schedule
- [ ] Review pod resource usage trends

### Before Maintenance Windows

- [ ] Create an on-demand [backup](backup-and-restore.md)
- [ ] Verify the backup succeeds
- [ ] Confirm the cluster has multiple instances for failover
- [ ] Document the current cluster state (version, instance count, storage)

## Events and Alerts

The operator emits Kubernetes events for significant state changes:

```bash
# View events for a DocumentDB cluster
kubectl get events -n <namespace> --field-selector involvedObject.name=<cluster-name>

# View all DocumentDB-related events
kubectl get events -n <namespace> --sort-by=.lastTimestamp
```

Key events to watch for:

| Event | Meaning |
|-------|---------|
| `BackupSucceeded` | A backup completed successfully |
| `BackupFailed` | A backup failed (investigate immediately) |
| `FailoverCompleted` | A failover occurred (check for underlying issues) |
| `PVRetained` | PVs were retained after cluster deletion |

## Troubleshooting Common Issues

### Cluster Stuck in Unhealthy State

```bash
# Check DocumentDB status
kubectl describe documentdb <cluster-name> -n <namespace>

# Check CNPG cluster status
kubectl describe clusters.postgresql.cnpg.io <cluster-name> -n <namespace>

# Check pod logs
kubectl logs <pod-name> -n <namespace> -c postgres --tail=100
```

### Pod in CrashLoopBackOff

```bash
# Check pod events
kubectl describe pod <pod-name> -n <namespace>

# Check previous container logs
kubectl logs <pod-name> -n <namespace> -c postgres --previous
```

Common causes:

- Insufficient memory (OOMKilled)
- Storage full
- Extension version mismatch

### Gateway Sidecar Not Ready

```bash
# Check gateway container logs
kubectl logs <pod-name> -n <namespace> -c gateway

# Check if credentials secret exists
kubectl get secret documentdb-credentials -n <namespace>
```

## Next Steps

- [Backup and Restore](backup-and-restore.md) — set up backup policies
- [Scaling](scaling.md) — adjust cluster capacity
- [Upgrades](upgrades.md) — keep your cluster up to date
- [Failover](failover.md) — understand failover behavior
