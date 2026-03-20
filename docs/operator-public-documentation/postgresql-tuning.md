# PostgreSQL Parameter Tuning

DocumentDB Kubernetes Operator provides intelligent PostgreSQL parameter tuning with memory-aware defaults, static best-practice values, and full user customization.

## How It Works

The operator manages PostgreSQL parameters through a layered merge system with clear priority:

| Priority | Source | Description |
|----------|--------|-------------|
| 1 (highest) | **Protected parameters** | Operator-managed values that cannot be overridden |
| 2 | **User overrides** | Values from `spec.postgresParameters` |
| 3 | **Memory-aware defaults** | Auto-computed from pod memory limit |
| 4 (lowest) | **Static defaults** | Best-practice values for all deployments |

## Resource Configuration

Configure CPU and memory for your DocumentDB pods using the `spec.resource` section:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
spec:
  resource:
    storage:
      pvcSize: "50Gi"
    memory: "8Gi"    # Pod memory limit (Guaranteed QoS)
    cpu: "4"         # Pod CPU limit (Guaranteed QoS)
```

When `memory` is set, the operator uses **Guaranteed QoS** (requests = limits), as recommended by CloudNative-PG for database workloads. This ensures predictable performance and stable memory for PostgreSQL buffer management.

If `memory` is not specified (or set to `"0"`), no resource limits are applied and static fallback values are used for memory-sensitive parameters.

## Memory-Aware Defaults

When a memory limit is configured, these parameters are automatically computed:

| Parameter | Formula | Example (8Gi) |
|-----------|---------|---------------|
| `shared_buffers` | 25% of memory | 2GB |
| `effective_cache_size` | 75% of memory | 6GB |
| `work_mem` | memory / (max_connections × 4) | 6MB |
| `maintenance_work_mem` | min(2GB, 10% of memory) | 819MB |

### Sizing Reference

| Pod Memory | shared_buffers | effective_cache_size | work_mem | maintenance_work_mem |
|-----------|----------------|---------------------|----------|---------------------|
| (not set) | 256MB | 512MB | 16MB | 128MB |
| 2Gi | 512MB | 1536MB | 4MB | 204MB |
| 4Gi | 1GB | 3GB | 4MB | 409MB |
| 8Gi | 2GB | 6GB | 6MB | 819MB |
| 16Gi | 4GB | 12GB | 13MB | 1638MB |
| 32Gi | 8GB | 24GB | 27MB | 2GB |

## Static Defaults

These best-practice values are applied to all clusters regardless of memory:

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `max_connections` | 300 | DocumentDB gateway is connection-heavy |
| `random_page_cost` | 1.1 | Optimized for SSD storage (typical in cloud) |
| `effective_io_concurrency` | 200 | Modern SSD parallelism |
| `checkpoint_completion_target` | 0.9 | Spread checkpoint I/O |
| `wal_buffers` | 16MB | Adequate for most workloads |
| `min_wal_size` | 256MB | Prevent excessive WAL recycling |
| `max_wal_size` | 2GB | Limit checkpoint distance |
| `autovacuum_vacuum_scale_factor` | 0.1 | More aggressive vacuum triggers |
| `autovacuum_analyze_scale_factor` | 0.05 | More frequent statistics updates |
| `autovacuum_vacuum_cost_delay` | 2ms | Reduce vacuum I/O throttling |
| `autovacuum_max_workers` | 4 | Parallel autovacuum |

## User Overrides

Override any non-protected parameter via `spec.postgresParameters`:

```yaml
spec:
  postgresParameters:
    max_connections: "500"
    work_mem: "64MB"
    shared_buffers: "4GB"
    log_min_duration_statement: "1000"
```

User overrides take precedence over both memory-aware and static defaults.

## Protected Parameters

These parameters are managed by the operator and **cannot be overridden**:

| Parameter | Value | Reason |
|-----------|-------|--------|
| `cron.database_name` | postgres | Required by pg_cron extension |
| `max_replication_slots` | 10 | Required for CNPG replication |
| `max_wal_senders` | 10 | Required for CNPG replication |
| `max_prepared_transactions` | 100 | Required by Citus distributed transactions |
| `wal_level` | logical | Only when ChangeStreams feature gate is enabled |

!!! warning
    Setting any of these in `postgresParameters` will be silently overridden by the operator.

## Complete Example

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: production-cluster
spec:
  nodeCount: 1
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: "100Gi"
      storageClass: "premium-ssd"
    memory: "16Gi"
    cpu: "8"
  postgresParameters:
    max_connections: "500"
    log_min_duration_statement: "500"
    idle_in_transaction_session_timeout: "300000"
  featureGates:
    ChangeStreams: true
```

This configuration will produce the following effective parameters (among others):

- `shared_buffers`: 4GB (auto-computed from 16Gi)
- `effective_cache_size`: 12GB (auto-computed)
- `max_connections`: 500 (user override)
- `wal_level`: logical (protected, from ChangeStreams gate)
- `cron.database_name`: postgres (protected)

## Troubleshooting

### Parameters not taking effect

Some parameters (like `shared_buffers`) require a PostgreSQL restart. The operator triggers a rolling restart when these parameters change. Check the CNPG cluster status:

```bash
kubectl get cluster -n <namespace>
```

### Memory-aware defaults showing static fallbacks

If memory-aware defaults show fallback values (e.g., shared_buffers=256MB), verify that `spec.resource.memory` is set in your DocumentDB CR:

```bash
kubectl get documentdb <name> -o jsonpath='{.spec.resource.memory}'
```
