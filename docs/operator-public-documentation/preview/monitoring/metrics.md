# Metrics Reference

This page documents the key metrics available when monitoring a DocumentDB cluster, organized by source. Each section includes the metric name, description, labels, and example PromQL queries.

## Container Resource Metrics

These metrics are collected via the kubelet/cAdvisor interface (or the OpenTelemetry `kubeletstats` receiver). They cover CPU, memory, network, and filesystem for the **postgres** and **documentdb-gateway** containers in each DocumentDB pod.

### CPU

| Metric | Type | Description |
|--------|------|-------------|
| `container_cpu_usage_seconds_total` | Counter | Cumulative CPU time consumed in seconds |
| `container_spec_cpu_quota` | Gauge | CPU quota (microseconds per `cpu_period`) |
| `container_spec_cpu_period` | Gauge | CPU CFS scheduling period (microseconds) |

**Common labels:** `namespace`, `pod`, `container`, `node`

#### Example Queries

CPU usage rate per container over 5 minutes:

```promql
rate(container_cpu_usage_seconds_total{
  container=~"postgres|documentdb-gateway",
  pod=~".*documentdb.*"
}[5m])
```

CPU utilization as a percentage of limit:

```promql
(rate(container_cpu_usage_seconds_total{
  container="postgres",
  pod=~".*documentdb.*"
}[5m])
/ on(pod, container)
(container_spec_cpu_quota{
  container="postgres",
  pod=~".*documentdb.*"
}
/ container_spec_cpu_period{
  container="postgres",
  pod=~".*documentdb.*"
})) * 100
```

Compare gateway vs. postgres CPU across all pods:

```promql
sum by (container) (
  rate(container_cpu_usage_seconds_total{
    container=~"postgres|documentdb-gateway",
    pod=~".*documentdb.*"
  }[5m])
)
```

### Memory

| Metric | Type | Description |
|--------|------|-------------|
| `container_memory_working_set_bytes` | Gauge | Current working set memory (bytes) |
| `container_memory_rss` | Gauge | Resident set size (bytes) |
| `container_memory_cache` | Gauge | Page cache memory (bytes) |
| `container_spec_memory_limit_bytes` | Gauge | Memory limit (bytes) |

**Common labels:** `namespace`, `pod`, `container`, `node`

#### Example Queries

Memory usage in MiB per container:

```promql
container_memory_working_set_bytes{
  container=~"postgres|documentdb-gateway",
  pod=~".*documentdb.*"
} / 1024 / 1024
```

Memory utilization as a percentage of limit:

```promql
(container_memory_working_set_bytes{
  container=~"postgres|documentdb-gateway",
  pod=~".*documentdb.*"
}
/ container_spec_memory_limit_bytes{
  container=~"postgres|documentdb-gateway",
  pod=~".*documentdb.*"
}) * 100
```

Top 5 pods by memory usage:

```promql
topk(5,
  sum by (pod) (
    container_memory_working_set_bytes{
      container=~"postgres|documentdb-gateway",
      pod=~".*documentdb.*"
    }
  )
)
```

### Network

| Metric | Type | Description |
|--------|------|-------------|
| `container_network_receive_bytes_total` | Counter | Bytes received |
| `container_network_transmit_bytes_total` | Counter | Bytes transmitted |

**Common labels:** `namespace`, `pod`, `interface`

#### Example Queries

Network throughput (bytes/sec) per pod:

```promql
sum by (pod) (
  rate(container_network_receive_bytes_total{
    pod=~".*documentdb.*"
  }[5m])
  + rate(container_network_transmit_bytes_total{
    pod=~".*documentdb.*"
  }[5m])
)
```

### Filesystem

| Metric | Type | Description |
|--------|------|-------------|
| `container_fs_usage_bytes` | Gauge | Filesystem usage (bytes) |
| `container_fs_reads_bytes_total` | Counter | Filesystem read bytes |
| `container_fs_writes_bytes_total` | Counter | Filesystem write bytes |

**Common labels:** `namespace`, `pod`, `container`, `device`

#### Example Queries

Disk I/O rate for the postgres container:

```promql
rate(container_fs_writes_bytes_total{
  container="postgres",
  pod=~".*documentdb.*"
}[5m])
```

## Gateway Metrics

The DocumentDB Gateway exports application-level metrics via OTLP (OpenTelemetry Protocol) push. The gateway sidecar injector automatically sets `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_RESOURCE_ATTRIBUTES` (with `service.instance.id` set to the pod name) on each gateway container, so metrics are exported without manual configuration.

Metrics are exported to an OpenTelemetry Collector, which converts them to Prometheus format via the `prometheus` exporter.

### Operations

| Metric | Type | Description |
|--------|------|-------------|
| `db_client_operations_total` | Counter | Total MongoDB operations processed |
| `db_client_operation_duration_seconds_total` | Counter | Cumulative operation duration |

**Common labels:** `db_operation_name` (e.g., `Find`, `Insert`, `Update`, `Aggregate`, `Delete`), `db_namespace`, `db_system_name`, `service_instance_id` (pod name), `error_type` (set on failed operations)

#### Example Queries

Operations per second by command type:

```promql
sum by (db_operation_name) (
  rate(db_client_operations_total[1m])
)
```

Average latency per operation (milliseconds):

```promql
sum by (db_operation_name) (
  rate(db_client_operation_duration_seconds_total{db_operation_phase=""}[1m])
) / sum by (db_operation_name) (
  rate(db_client_operations_total[1m])
) * 1000
```

Error rate as a percentage:

```promql
sum(rate(db_client_operations_total{error_type!=""}[1m]))
/ sum(rate(db_client_operations_total[1m])) * 100
```

Operations per second for a specific instance:

```promql
sum by (db_operation_name) (
  rate(db_client_operations_total{
    service_instance_id="documentdb-preview-1"
  }[1m])
)
```

### Client Connections

| Metric | Type | Description |
|--------|------|-------------|
| `gateway_client_connections_active` | Gauge | Current active client connections |
| `gateway_client_connections_total` | Counter | Cumulative client connections accepted |

**Common labels:** `service_instance_id` (pod name)

#### Example Queries

Active connections per instance:

```promql
gateway_client_connections_active
```

Connection rate:

```promql
rate(gateway_client_connections_total[1m])
```

### Connection Pool

| Metric | Type | Description |
|--------|------|-------------|
| `db_client_connection_active` | Gauge | Active backend (PG) pool connections |
| `db_client_connection_idle` | Gauge | Idle backend pool connections |
| `db_client_connection_max` | Gauge | Maximum pool size |
| `db_client_connection_waiting` | Gauge | Requests waiting for a pool connection |

**Common labels:** `service_instance_id` (pod name)

#### Example Queries

Pool utilization:

```promql
db_client_connection_active
/ db_client_connection_max * 100
```

### Request/Response Size

| Metric | Type | Description |
|--------|------|-------------|
| `db_client_request_size_bytes_total` | Counter | Cumulative request payload size |
| `db_client_response_size_bytes_total` | Counter | Cumulative response payload size |

**Common labels:** `service_instance_id` (pod name)

#### Example Queries

Average request throughput (bytes/sec):

```promql
sum(rate(db_client_request_size_bytes_total[1m]))
```

### Operation Phases

| Metric | Type | Description |
|--------|------|-------------|
| `db_client_operation_duration_seconds_total` | Counter | Duration broken down by phase |

**Key `db_operation_phase` values:** `pg_query`, `cursor_iteration`, `bson_serialization`, `command_parsing`

#### Example Queries

Time spent in each phase per second:

```promql
sum by (db_operation_phase) (
  rate(db_client_operation_duration_seconds_total{
    db_operation_phase!=""
  }[1m])
)
```

## Operator Metrics (controller-runtime)

The DocumentDB operator binary exposes standard controller-runtime metrics on its metrics endpoint. These track reconciliation performance and work queue health.

### Reconciliation

| Metric | Type | Description |
|--------|------|-------------|
| `controller_runtime_reconcile_total` | Counter | Total reconciliations |
| `controller_runtime_reconcile_errors_total` | Counter | Total reconciliation errors |
| `controller_runtime_reconcile_time_seconds` | Histogram | Time spent in reconciliation |

**Common labels:** `controller` (e.g., `documentdb-controller`, `backup`, `scheduledbackup`, `certificate-controller`, `persistentvolume`), `result` (`success`, `error`, `requeue`, `requeue_after`)

#### Example Queries

Reconciliation error rate by controller:

```promql
sum by (controller) (
  rate(controller_runtime_reconcile_errors_total[5m])
)
```

P95 reconciliation latency for the DocumentDB controller:

```promql
histogram_quantile(0.95,
  sum by (le) (
    rate(controller_runtime_reconcile_time_seconds_bucket{
      controller="documentdb-controller"
    }[5m])
  )
)
```

Reconciliation throughput (reconciles/sec):

```promql
sum by (controller) (
  rate(controller_runtime_reconcile_total[5m])
)
```

### Work Queue

| Metric | Type | Description |
|--------|------|-------------|
| `workqueue_depth` | Gauge | Current number of items in the queue |
| `workqueue_adds_total` | Counter | Total items added |
| `workqueue_queue_duration_seconds` | Histogram | Time items spend in queue before processing |
| `workqueue_work_duration_seconds` | Histogram | Time spent processing items |
| `workqueue_retries_total` | Counter | Total retries |

**Common labels:** `name` (queue name, maps to controller name)

#### Example Queries

Work queue depth by controller:

```promql
workqueue_depth{name=~"documentdb-controller|backup|scheduledbackup|certificate-controller"}
```

Average time items spend waiting in queue:

```promql
rate(workqueue_queue_duration_seconds_sum{name="documentdb-controller"}[5m])
/ rate(workqueue_queue_duration_seconds_count{name="documentdb-controller"}[5m])
```

## CNPG / PostgreSQL Metrics

CloudNative-PG exposes PostgreSQL-level metrics from each managed pod. These are available when CNPG monitoring is enabled. For the full list, see the [CloudNative-PG monitoring docs](https://cloudnative-pg.io/documentation/current/monitoring/).

Additionally, the OpenTelemetry Collector's `postgresql` receiver collects metrics directly from PostgreSQL via SQL queries.

### Replication

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_pg_replication_lag` | Gauge | Replication lag in seconds (CNPG) |
| `postgresql_replication_data_delay` | Gauge | Replication data delay (OTel PG receiver) |

#### Example Queries

Replication lag per pod:

```promql
cnpg_pg_replication_lag{pod=~".*documentdb.*"}
```

### Connections

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_pg_stat_activity_count` | Gauge | Active backend connections by state (CNPG) |
| `postgresql_backends` | Gauge | Number of backends (OTel PG receiver) |
| `postgresql_connection_max` | Gauge | Maximum connections (OTel PG receiver) |

#### Example Queries

Active connections by state:

```promql
sum by (state) (
  cnpg_pg_stat_activity_count{pod=~".*documentdb.*"}
)
```

Backend utilization:

```promql
postgresql_backends / postgresql_connection_max * 100
```

### Storage

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_pg_database_size_bytes` | Gauge | Total database size (CNPG) |
| `postgresql_db_size_bytes` | Gauge | Database size (OTel PG receiver) |
| `postgresql_wal_age_seconds` | Gauge | WAL age (OTel PG receiver) |

#### Example Queries

Database size in GiB:

```promql
postgresql_db_size_bytes / 1024 / 1024 / 1024
```

### Operations

| Metric | Type | Description |
|--------|------|-------------|
| `postgresql_commits_total` | Counter | Total committed transactions |
| `postgresql_rollbacks_total` | Counter | Total rolled-back transactions |
| `postgresql_operations_total` | Counter | Row operations (labels: `operation`) |

#### Example Queries

Transaction rate:

```promql
rate(postgresql_commits_total[1m])
```

Row operations per second by type:

```promql
sum by (operation) (rate(postgresql_operations_total[1m]))
```

### Cluster Health

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_collector_up` | Gauge | 1 if the CNPG metrics collector is running |
| `cnpg_pg_postmaster_start_time` | Gauge | PostgreSQL start timestamp |

#### Example Queries

Detect pods where the metrics collector is down:

```promql
cnpg_collector_up{pod=~".*documentdb.*"} == 0
```

## OpenTelemetry Metric Names

When using the OpenTelemetry `kubeletstats` receiver, metric names use the OpenTelemetry naming convention instead of Prometheus-style names:

| OpenTelemetry Name | Prometheus Equivalent |
|---|---|
| `k8s.container.cpu.time` | `container_cpu_usage_seconds_total` |
| `k8s.container.memory.usage` | `container_memory_working_set_bytes` |
| `k8s.container.cpu.limit` | `container_spec_cpu_quota` |
| `k8s.container.memory.limit` | `container_spec_memory_limit_bytes` |
| `k8s.pod.network.io` | `container_network_*_bytes_total` |

When writing queries, use the naming convention matching your collection method. The telemetry playground uses the OpenTelemetry names; a direct Prometheus scrape of cAdvisor uses Prometheus names.
