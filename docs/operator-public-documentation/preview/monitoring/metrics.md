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
} / 1e5)) * 100
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

### Replication

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_pg_replication_lag` | Gauge | Replication lag in seconds |
| `cnpg_pg_replication_streaming_replicas` | Gauge | Number of streaming replicas |

#### Example Queries

Replication lag per pod:

```promql
cnpg_pg_replication_lag{pod=~".*documentdb.*"}
```

### Connections

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_pg_stat_activity_count` | Gauge | Active backend connections by state |

#### Example Queries

Active connections by state:

```promql
sum by (state) (
  cnpg_pg_stat_activity_count{pod=~".*documentdb.*"}
)
```

### Storage

| Metric | Type | Description |
|--------|------|-------------|
| `cnpg_pg_database_size_bytes` | Gauge | Total database size |
| `cnpg_pg_stat_bgwriter_buffers_checkpoint` | Counter | Buffers written during checkpoints |

#### Example Queries

Database size in GiB:

```promql
cnpg_pg_database_size_bytes{pod=~".*documentdb.*"} / 1024 / 1024 / 1024
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

## Gateway Metrics (Future)

The DocumentDB Gateway does not currently expose application-level metrics. When implemented, expect metrics like:

| Metric | Type | Description |
|--------|------|-------------|
| `documentdb_gateway_requests_total` | Counter | Total API requests (labels: `method`, `status`) |
| `documentdb_gateway_request_duration_seconds` | Histogram | Request latency |
| `documentdb_gateway_active_connections` | Gauge | Current connection count |
| `documentdb_gateway_read_operations_total` | Counter | Read operations (labels: `database`, `collection`) |
| `documentdb_gateway_write_operations_total` | Counter | Write operations (labels: `database`, `collection`) |
| `documentdb_gateway_errors_total` | Counter | Error count (labels: `error_type`, `operation`) |

These will be collected via Prometheus scraping (`/metrics` endpoint) or OTLP push. See the [telemetry design document](https://github.com/microsoft/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/telemetry-design.md) for the planned implementation.

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
