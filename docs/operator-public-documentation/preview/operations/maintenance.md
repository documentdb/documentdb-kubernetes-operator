---
title: Maintenance
description: Monitor DocumentDB cluster health, review PostgreSQL and gateway logs, track resource usage, and configure Kubernetes events and alerts.
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
| `AGE` column | Consistent with deployment time | Unexpectedly recent — may indicate an unplanned restart |

### Pod Health

```bash
# Check pod status (each pod runs PostgreSQL + gateway sidecar)
kubectl get pods -n <namespace> -l app=<cluster-name>

# View pod resource usage
kubectl top pods -n <namespace>
```

| What to check | Normal | Investigate if |
|---------------|--------|----------------|
| `READY` column | `2/2` (PostgreSQL container + gateway sidecar) | Less than `2/2` — one or both containers are not ready |
| `STATUS` column | `Running` | `CrashLoopBackOff`, `Error`, `Pending`, or `Init` persisting beyond startup |
| `RESTARTS` column | `0` (or very low over the cluster lifetime) | High or rapidly increasing — indicates repeated container crashes |
| Resource usage (`kubectl top`) | CPU and memory stable under normal workload | CPU consistently maxed out (throttling) or memory climbing steadily (OOMKill risk) |

## Log Management

!!! tip
    We recommend setting up centralized log collection as part of your observability strategy. See the [telemetry playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/telemetry) for OpenTelemetry, Prometheus, and Grafana integration examples.

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
      cat /controller/log/postgres
    ```

    **What's normal:** Startup messages, checkpoint completions, autovacuum activity.

    **Investigate if:** `FATAL`, `PANIC`, or repeated `ERROR` entries appear. Watch for `out of memory`, `no space left on device`, or `too many connections` messages.

=== "Gateway Logs"

    Access gateway (sidecar) logs:

    ```bash
    kubectl logs <pod-name> -n <namespace> -c documentdb-gateway
    ```

    **What's normal:** Successful connection handling, startup messages.

    **Investigate if:** Repeated connection refused errors, authentication failures, or TLS handshake errors appear.

### Configuring Log Level

The `spec.logLevel` field controls the PostgreSQL instance log verbosity. It does not affect the DocumentDB operator or gateway logs.

```yaml
spec:
  logLevel: "warning"  # Options: debug, info, warning, error
```

!!! tip
    For production deployments, use `warning` or `error` to reduce log volume. Reserve `info` or `debug` for troubleshooting.

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
| Pod CPU usage | Varies with workload; no sustained spikes | Consistently maxed out — queries may be throttled |
| Pod memory usage | Stable and predictable | Climbing steadily or hitting node limits — pods may be OOMKilled. Check for memory-heavy queries. |
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
| `BackupSchedule` | A scheduled backup created a Backup resource | No action needed — verify periodically that backups are running on schedule |
| `BackupFailed` | A backup failed | **Investigate immediately.** Check operator logs and storage configuration. Ensure your backup target is reachable. |
| `InvalidSchedule` | A ScheduledBackup has an invalid cron expression | Fix the `spec.schedule` field in your ScheduledBackup resource. |
| `PVsRetained` | PVs were retained after DocumentDB cluster deletion | Expected if `reclaimPolicy: Retain`. Clean up PVs manually if no longer needed. |
