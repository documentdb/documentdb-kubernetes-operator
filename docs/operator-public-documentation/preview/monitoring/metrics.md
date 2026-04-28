---
title: Metrics Reference
description: Detailed reference of all metrics available when monitoring DocumentDB clusters, with PromQL examples.
tags:
  - monitoring
  - metrics
  - prometheus
  - opentelemetry
---

# Metrics Reference

This page documents the key metrics available when monitoring a DocumentDB cluster, organized by source. Each section includes the metric name, description, labels, and example PromQL queries.

## Container Resource Metrics

These metrics are collected by the **chart-managed `containerMetrics` DaemonSet** — one OTel Collector per node, each scraping its local kubelet's `/stats/summary` and exporting via Prometheus. Enable at chart install time with `--set containerMetrics.enabled=true`. The DaemonSet uses a single chart-managed ServiceAccount with `nodes/stats` GET; tenant DocumentDB pods receive no kubelet privileges. This matches OpenTelemetry's [recommended deployment for the kubeletstats receiver](https://opentelemetry.io/docs/platforms/kubernetes/collector/components/#kubeletstats-receiver).

Metric names use OpenTelemetry semantic conventions; the OTel Prometheus exporter converts dots to underscores at scrape time, which is the form Prometheus stores.

### CPU

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `container.cpu.time` | `container_cpu_time_seconds_total` | Counter | Cumulative container CPU time (seconds) |
| `container.cpu.utilization` | `container_cpu_utilization_ratio` | Gauge | Container CPU utilization (fraction of one core) |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name`, `k8s_node_name`

> **CPU/memory limit utilization.** The kubeletstats receiver can also emit `container.cpu_limit_utilization` and `container.memory_limit_utilization`, but only when the container has resource limits configured. The default chart-managed `containerMetrics` config does not enable these metrics.

#### Example Query

CPU rate per container:

```promql
sum by (k8s_pod_name, k8s_container_name) (
  rate(container_cpu_time_seconds_total{
    k8s_namespace_name="documentdb-preview-ns",
    k8s_container_name=~"postgres|documentdb-gateway"
  }[5m])
)
```

### Memory

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.container.memory.working_set` | `container_memory_working_set_bytes` | Gauge | Working set memory (bytes) — matches OOM accounting |
| `k8s.container.memory.rss` | `container_memory_rss_bytes` | Gauge | Resident set size (bytes) |
| `k8s.container.memory.usage` | `container_memory_usage_bytes` | Gauge | Total memory usage (bytes) |
| `k8s.container.memory.available` | `container_memory_available_bytes` | Gauge | Memory available (bytes) — present only when limit is set |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name`

#### Example Query

Working-set memory per container:

```promql
sum by (k8s_pod_name, k8s_container_name) (
  container_memory_working_set_bytes{
    k8s_namespace_name="documentdb-preview-ns"
  }
)
```

### Network

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.pod.network.io` | `k8s_pod_network_io_bytes_total` | Counter | Bytes sent / received per pod (with `direction` attribute: `transmit` / `receive`) |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `direction`, `interface`

#### Example Queries

Network throughput (bytes/sec) per pod:

```promql
sum by (k8s_pod_name) (
  rate(k8s_pod_network_io_bytes_total{
    k8s_namespace_name="documentdb-preview-ns"
  }[5m])
)
```

### Filesystem

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.container.filesystem.usage` | `container_filesystem_usage_bytes` | Gauge | Filesystem usage (bytes) |
| `k8s.container.filesystem.available` | `container_filesystem_available_bytes` | Gauge | Filesystem bytes available |
| `k8s.container.filesystem.capacity` | `container_filesystem_capacity_bytes` | Gauge | Filesystem capacity (bytes) |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name`

> **Naming convention.** The OTel→Prometheus translator drops the `k8s.container.` prefix (so `k8s.container.cpu.usage` becomes `container_cpu_usage`) but keeps `k8s.pod.*` and `k8s.node.*` (so `k8s.pod.network.io` becomes `k8s_pod_network_io_bytes_total`).

## Gateway Metrics

The DocumentDB Gateway is being instrumented to emit application-level metrics over OTLP, and a future operator release will document these once the gateway image with that instrumentation ships in a public release.

## CNPG / PostgreSQL Metrics

PostgreSQL-level metrics from the CloudNative-PG instance manager are out of scope for this preview. A future revision of the operator will surface a curated set of these metrics through the OTel sidecar; until then, see the [CloudNative-PG monitoring docs](https://cloudnative-pg.io/documentation/current/monitoring/) if you need them today.
