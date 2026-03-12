---
title: Maintenance
description: Day-to-day operational tasks for DocumentDB clusters including monitoring, log management, resource tuning, node maintenance, and troubleshooting.
tags:
  - operations
  - maintenance
  - monitoring
---

# Maintenance

## Overview

Maintenance covers the day-to-day tasks that keep your DocumentDB cluster healthy and performant. Regular monitoring, log review, and proactive resource management prevent outages and help you catch issues before they affect applications.

## Monitoring DocumentDB Cluster Health

### DocumentDB Cluster Status

Check the overall health of your DocumentDB clusters:

```bash
# List all DocumentDB clusters and their status
kubectl get documentdb -n <namespace>

# Detailed cluster information
kubectl describe documentdb <cluster-name> -n <namespace>
```

| What to check | Normal | Investigate if |
|---------------|--------|----------------|
| `STATUS` column | `Cluster in healthy state` | Any other status (e.g., `Setting up primary`, `Creating replica`) persists longer than a few minutes |
| `INSTANCES` column | Matches `spec.instances` (e.g., `3` for a 3-instance DocumentDB cluster) | Instance count is lower than expected |
| `AGE` column | Consistent with deployment time | Unexpectedly recent — may indicate an unplanned restart |

### Pod Health

```bash
# Check pod status (each pod runs PostgreSQL + gateway sidecar)
kubectl get pods -n <namespace> -l documentdb.io/cluster=<cluster-name>

# View pod resource usage
kubectl top pods -n <namespace>
```

| What to check | Normal | Investigate if |
|---------------|--------|----------------|
| `READY` column | `2/2` (PostgreSQL container + gateway sidecar) | Less than `2/2` — one or both containers are not ready |
| `STATUS` column | `Running` | `CrashLoopBackOff`, `Error`, `Pending`, or `Init` persisting beyond startup |
| `RESTARTS` column | `0` (or very low over the cluster lifetime) | High or rapidly increasing — indicates repeated container crashes |
| Resource usage (`kubectl top`) | CPU and memory well within `spec.resources` limits | CPU consistently near limit (throttling) or memory approaching limit (OOMKill risk) |

### Advanced Diagnostics

For deeper diagnostics, inspect the underlying database cluster resource:

```bash
kubectl get clusters.postgresql.cnpg.io -n <namespace>

kubectl describe clusters.postgresql.cnpg.io <cluster-name> -n <namespace>
```

| What to check | Normal | Investigate if |
|---------------|--------|----------------|
| Cluster phase | `Cluster in healthy state` | Any other phase persists (e.g., `Setting up primary`, `Upgrading cluster`) |
| Replication status (in `describe` output) | All replicas show `streaming` state | Any replica shows `not streaming` or has high replication lag |
| Conditions (in `describe` output) | All conditions show `True` | Any condition is `False` — read the condition message for details |

## Log Management

=== "DocumentDB Operator Logs"

    ```bash
    # Recent operator logs
    kubectl logs -n documentdb-operator deployment/documentdb-operator --tail=100

    # Follow operator logs in real time
    kubectl logs -n documentdb-operator deployment/documentdb-operator -f
    ```

    **What's normal:** Periodic reconciliation messages, successful backup notifications.

    **Investigate if:** Repeated `ERROR` or `WARNING` lines, reconciliation failures, or stack traces appear.

=== "PostgreSQL Logs"

    Access PostgreSQL logs inside a specific pod:

    ```bash
    kubectl exec -it <pod-name> -n <namespace> -c postgres -- \
      cat /controller/log/postgresql.log
    ```

    **What's normal:** Startup messages, checkpoint completions, autovacuum activity.

    **Investigate if:** `FATAL`, `PANIC`, or repeated `ERROR` entries appear. Watch for `out of memory`, `no space left on device`, or `too many connections` messages.

=== "Gateway Logs"

    Access gateway (sidecar) logs:

    ```bash
    kubectl logs <pod-name> -n <namespace> -c gateway
    ```

    **What's normal:** Successful connection handling, startup messages.

    **Investigate if:** Repeated connection refused errors, authentication failures, or TLS handshake errors appear.

### Configuring Log Level

The `spec.logLevel` field controls the PostgreSQL instance log verbosity. It does not affect the DocumentDB operator or gateway logs.

```yaml
spec:
  logLevel: "info"  # Options: debug, info, warning, error
```

Apply the change:

```bash
kubectl apply -f documentdb.yaml
```

## Resource Monitoring

```bash
# Pod resource consumption
kubectl top pods -n <namespace>

# Node resource consumption
kubectl top nodes
```

| What to check | Normal | Investigate if |
|---------------|--------|----------------|
| Pod CPU usage | Varies with workload; stays below `spec.resources.limits.cpu` | Consistently near the CPU limit — queries may be throttled. Consider [scaling up](scaling.md). |
| Pod memory usage | Stable under `spec.resources.limits.memory` | Approaching or hitting the memory limit — pods may be OOMKilled. Check for memory-heavy queries or increase limits. |
| Node resource usage | Enough headroom for pod scheduling and bursts | Nodes above 80% utilization — new pods may fail to schedule or existing pods may be evicted. |

### Storage Monitoring

Monitor persistent volume usage:

```bash
# Check PVC status and capacity
kubectl get pvc -n <namespace>

# Check actual disk usage inside a pod
kubectl exec -it <pod-name> -n <namespace> -c postgres -- df -h /var/lib/postgresql/data
```

| What to check | Normal | Investigate if |
|---------------|--------|----------------|
| PVC `STATUS` | `Bound` | `Pending` — the storage class may not be able to provision a volume |
| Disk usage (`df -h`) | Below 70% of capacity | Above 80% — risk of the database halting when storage is full. Plan a migration to a larger volume. |
| Growth rate | Gradual and predictable | Sudden spikes — may indicate a bulk data load, excessive logging, or WAL accumulation |

!!! note
    PVC resize is not currently supported but is planned for a future release. If storage usage approaches capacity, provision a new DocumentDB cluster with larger `pvcSize` and restore from a backup. See [Storage Configuration](../configuration/storage.md) for details.


## Routine Checks

=== "Daily"

    - [ ] Verify DocumentDB cluster health: `kubectl get documentdb -n <namespace>`
    - [ ] Check backup status: `kubectl get backups -n <namespace>`
    - [ ] Monitor pod status: `kubectl get pods -n <namespace>`

=== "Weekly"

    - [ ] Review operator logs for warnings or errors
    - [ ] Check storage utilization across all PVCs
    - [ ] Verify scheduled backups are running on schedule
    - [ ] Review pod resource usage trends

=== "Before Maintenance"

    - [ ] Create an on-demand [backup](backup-and-restore.md)
    - [ ] Verify the backup succeeds
    - [ ] Confirm the DocumentDB cluster has multiple instances for failover
    - [ ] Document the current DocumentDB cluster state (version, instance count, storage)

## Events and Alerts

The operator emits Kubernetes events for significant state changes:

```bash
# View events for a DocumentDB cluster
kubectl get events -n <namespace> --field-selector involvedObject.name=<cluster-name>

# View all DocumentDB-related events
kubectl get events -n <namespace> --sort-by=.lastTimestamp
```

Key events to watch for:

| Event | Meaning | Action |
|-------|---------|--------|
| `BackupSucceeded` | A backup completed successfully | No action needed — verify periodically that backups are running on schedule |
| `BackupFailed` | A backup failed | **Investigate immediately.** Check operator logs and storage configuration. Ensure your backup target is reachable. |
| `FailoverCompleted` | A failover occurred | Check why the previous primary became unavailable (node failure, resource exhaustion, or network issue). See [Failover](failover.md). |
| `PVRetained` | PVs were retained after DocumentDB cluster deletion | Expected if `reclaimPolicy: Retain`. Clean up PVs manually if no longer needed. |

## Troubleshooting Common Issues

=== "Unhealthy State"

    ```bash
    # Check DocumentDB status
    kubectl describe documentdb <cluster-name> -n <namespace>

    # Check underlying database cluster status
    kubectl describe clusters.postgresql.cnpg.io <cluster-name> -n <namespace>

    # Check pod logs
    kubectl logs <pod-name> -n <namespace> -c postgres --tail=100
    ```

    **What to look for:** The `Status.Conditions` section in the `describe` output tells you which condition is failing. Pod logs often reveal the root cause (storage full, connection limits, extension errors).

    **Next steps:** If the DocumentDB cluster does not recover within a few minutes, check for node-level issues (`kubectl describe node`) and review recent changes to the DocumentDB manifest.

=== "CrashLoopBackOff"

    ```bash
    # Check pod events
    kubectl describe pod <pod-name> -n <namespace>

    # Check previous container logs
    kubectl logs <pod-name> -n <namespace> -c postgres --previous
    ```

    **What to look for:** The `Events` section shows why the container exited. The `--previous` logs show what happened in the last run before the crash.

    Common causes and fixes:

    | Cause | Symptom | Fix |
    |-------|---------|-----|
    | Insufficient memory | `OOMKilled` in pod events | Increase `spec.resources.limits.memory` |
    | Storage full | `No space left on device` in logs | Provision a new DocumentDB cluster with larger `pvcSize` and [restore from backup](backup-and-restore.md) |
    | Extension version mismatch | Extension load errors in logs | Verify `spec.documentDBVersion` is correct. See [Upgrades](upgrades.md). |

=== "Gateway Sidecar Not Ready"

    ```bash
    # Check gateway container logs
    kubectl logs <pod-name> -n <namespace> -c gateway

    # Check if credentials secret exists
    kubectl get secret documentdb-credentials -n <namespace>
    ```

    **What to look for:** Connection refused or TLS errors in gateway logs. Missing secrets cause the gateway to fail authentication.

    **Next steps:** Verify the `documentdb-credentials` secret exists and contains valid credentials. If using TLS, confirm certificates are valid and not expired (see [TLS Configuration](../configuration/tls.md)).

## Next Steps

- [Backup and Restore](backup-and-restore.md) — set up backup policies
- [Scaling](scaling.md) — adjust DocumentDB cluster capacity
- [Upgrades](upgrades.md) — keep your DocumentDB cluster up to date
- [Failover](failover.md) — understand failover behavior
