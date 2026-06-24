# PostgreSQL Parameter Tuning

DocumentDB Kubernetes Operator provides intelligent PostgreSQL parameter tuning with memory-aware defaults, static best-practice values, and full user customization.

## How It Works

The operator manages PostgreSQL parameters through a layered merge system with clear priority:

| Priority | Source | Description |
|----------|--------|-------------|
| 1 (highest) | **Protected parameters** | Operator-managed values that cannot be overridden |
| 2 | **User overrides** | Values from `spec.postgres.parameters` |
| 3 | **Memory-aware defaults** | Auto-computed from pod memory limit |
| 4 (lowest) | **Static defaults** | Best-practice values for all deployments |

## Resource Configuration

Configure CPU and memory for your DocumentDB pods using the `spec.resource` section. The `memory` and `cpu` values are total pod envelopes, not only PostgreSQL resources:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
spec:
  resource:
    storage:
      pvcSize: "50Gi"
    memory: "8Gi"    # Total pod memory envelope
    cpu: "4"         # Total pod CPU envelope (carved like memory)
```

When top-level `memory` or `cpu` is set, the operator allocates that envelope across the PostgreSQL container, the documentdb-gateway sidecar, and, when monitoring is enabled, the OTel Collector sidecar. Each component gets its own container resource settings so sidecars reserve their memory/CPU and a sidecar memory leak is OOM-killed in that sidecar instead of crowding out PostgreSQL.

If neither an envelope nor any per-container value is specified for a dimension, no limits are applied for that dimension and static fallback values are used for memory-sensitive parameters when memory is unmanaged. See [The envelope is optional](#the-envelope-is-optional) below for omitting the envelope while still sizing containers.

!!! note
    Changing `memory` (or `cpu`) triggers a rolling restart of the DocumentDB pods,
    causing brief downtime. The pod is recreated with the new resource limits, and the
    memory-aware PostgreSQL parameters (`shared_buffers`, `effective_cache_size`,
    `work_mem`, `maintenance_work_mem`) are recomputed and applied at the same time.

## Sidecar Memory Isolation

The operator treats `spec.resource.memory` and `spec.resource.cpu` as total pod envelopes and carves out sidecar reservations before computing PostgreSQL settings:

- **documentdb-gateway**: by default, reserves 18.75% of the total memory envelope, capped at 32Gi; its configured CPU reservation is carved from the CPU envelope.
- **OTel Collector**: when `spec.monitoring.enabled` is true, defaults to a 48Mi memory request, a 128Mi memory limit, a 50m CPU request, and a 200m CPU limit (Burstable — the requests are reserved and the limits cap a telemetry burst).
- **PostgreSQL**: receives the remaining memory and CPU, and memory-aware parameters such as `shared_buffers` are recomputed from that database allocation.

Override individual containers with `spec.resource.gateway`, `spec.resource.database`, and `spec.resource.otel` when a cluster needs explicit sizing:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: sized-cluster
spec:
  monitoring:
    enabled: true
  resource:
    memory: "8Gi"        # Total pod memory envelope
    cpu: "4"             # Total pod CPU envelope
    gateway:
      memory: "1Gi"
      cpu: "500m"
    database:
      memory: "6Gi"
      cpu: "3"
    otel:
      memory: "128Mi"
      cpu: "50m"
```

Each per-component value is a Kubernetes quantity string and, when set, overrides the automatic split for that container.

### The envelope is optional

`spec.resource.memory` and `spec.resource.cpu` (the pod envelope) are optional. For each dimension independently:

- **Set the envelope** and let the operator divide it (gateway and OTel reserved, PostgreSQL gets the remainder).
- **Omit the envelope** and instead set that dimension on **both** `spec.resource.gateway` and `spec.resource.database` — the effective envelope is the sum of the containers (the OTel collector uses its default if you do not set it). For example:

```yaml
spec:
  resource:
    storage:
      pvcSize: "50Gi"
    # no top-level memory/cpu — derived from the containers below
    gateway:
      memory: "1Gi"
      cpu: "500m"
    database:
      memory: "6Gi"
      cpu: "3"
```

- **Omit the envelope and all container values** for a dimension to leave it unmanaged (no limits).

If you omit the envelope but only partially specify the containers (for example, you set `gateway.memory` but not `database.memory`), the resource is **rejected** by the validating webhook, because the sidecar reservation and PostgreSQL remainder for that dimension cannot be derived without the envelope. Likewise, an explicit envelope that the sidecar reservations exhaust — or that explicit per-container values exceed — is rejected.

Cluster-wide defaults are configured with the operator Helm chart:

```yaml
operator:
  sidecarResources:
    gatewayMemoryFraction: "0.1875"
    gatewayMemoryCap: "32Gi"
    gatewayCpuLimit: ""        # optional; bounds gateway async worker threads
    otelMemoryRequest: "48Mi"
    otelMemoryLimit: "128Mi"
    otelCpuRequest: "50m"
    otelCpuLimit: "200m"       # ceiling on the collector's CPU burst
```

Use per-cluster `spec.resource` overrides for individual workload needs; use Helm values to change fleet-wide defaults for clusters managed by the operator.

## Memory-Aware Defaults

When PostgreSQL has an effective database memory allocation, these parameters are automatically computed from that allocation:

| Parameter | Formula | Example (8Gi database allocation) |
|-----------|---------|---------------|
| `shared_buffers` | 25% of memory | 2GB |
| `effective_cache_size` | 75% of memory | 6GB |
| `work_mem` | memory / (max_connections × 4) | 6MB |
| `maintenance_work_mem` | min(2GB, 10% of memory) | 819MB |

### Sizing Reference

| Database Memory | shared_buffers | effective_cache_size | work_mem | maintenance_work_mem |
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

Override any non-protected parameter via `spec.postgres.parameters`:

```yaml
spec:
  postgres:
    parameters:
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
| `max_prepared_transactions` | 100 | Enables two-phase commit (PREPARE TRANSACTION) for multi-document transactions |
| `wal_level` | logical | Only when ChangeStreams feature gate is enabled |

!!! warning
    Setting any of these in `spec.postgres.parameters` will be silently overridden by the operator.

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
  postgres:
    parameters:
      max_connections: "500"
      log_min_duration_statement: "500"
      idle_in_transaction_session_timeout: "300000"
  featureGates:
    ChangeStreams: true
```

This configuration will produce the following effective parameters (among others):

- `shared_buffers`: auto-computed from the PostgreSQL memory remaining after sidecar reservations
- `effective_cache_size`: auto-computed from the same effective database allocation
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
