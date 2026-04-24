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

These metrics are collected by the **OTel Collector sidecar's `kubeletstats` receiver** running in every DocumentDB pod and exported via the sidecar's Prometheus exporter. The receiver scrapes the local kubelet (the one on the pod's own node) and emits container, pod, and node-level resource metrics. Enable by setting `spec.monitoring.kubeletstats: {}` on the DocumentDB CR — this triggers the operator to add the receiver to the sidecar's config and to bind the cluster's ServiceAccount to the chart-installed `documentdb-kubeletstats-reader` ClusterRole.

Metric names use OpenTelemetry semantic conventions; the OTel Prometheus exporter converts dots to underscores at scrape time, which is the form Prometheus stores.

### CPU

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.container.cpu.usage` | `k8s_container_cpu_usage` | Gauge | Container CPU usage (cores) |
| `k8s.container.cpu_limit_utilization` | `k8s_container_cpu_limit_utilization` | Gauge | CPU usage as a fraction of the configured limit |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name`, `k8s_node_name`

#### Example Query

CPU usage per container:

```promql
sum by (k8s_pod_name, k8s_container_name) (
  k8s_container_cpu_usage{
    k8s_namespace_name="documentdb-preview-ns",
    k8s_container_name=~"postgres|documentdb-gateway"
  }
)
```

### Memory

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.container.memory.working_set` | `k8s_container_memory_working_set` | Gauge | Working set memory (bytes) — matches OOM accounting |
| `k8s.container.memory.rss` | `k8s_container_memory_rss` | Gauge | Resident set size (bytes) |
| `k8s.container.memory_limit_utilization` | `k8s_container_memory_limit_utilization` | Gauge | Memory usage as a fraction of the configured limit |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name`

#### Example Query

Memory utilization as a percentage of limit:

```promql
k8s_container_memory_limit_utilization{
  k8s_namespace_name="documentdb-preview-ns"
} * 100
```

### Network

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.pod.network.io` | `k8s_pod_network_io` | Counter | Bytes sent / received per pod (with `direction` attribute: `transmit` / `receive`) |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `direction`, `interface`

#### Example Queries

Network throughput (bytes/sec) per pod:

```promql
sum by (k8s_pod_name) (
  rate(k8s_pod_network_io{
    k8s_namespace_name="documentdb-preview-ns"
  }[5m])
)
```

### Filesystem

| Metric (OTel) | Metric (Prometheus form) | Type | Description |
|---------------|--------------------------|------|-------------|
| `k8s.container.filesystem.usage` | `k8s_container_filesystem_usage` | Gauge | Filesystem usage (bytes) |
| `k8s.container.filesystem.available` | `k8s_container_filesystem_available` | Gauge | Filesystem bytes available |

**Common labels:** `k8s_namespace_name`, `k8s_pod_name`, `k8s_container_name`

> **Falling back to a direct cAdvisor scrape.** If you can't enable `monitoring.kubeletstats` (e.g., your cluster's kubelet exposes `/stats/summary` only via a non-standard auth path), you can scrape `/metrics/cadvisor` from the kubelet directly. The metric names are different (`container_cpu_usage_seconds_total`, `container_memory_working_set_bytes`, …); see the [cAdvisor docs](https://github.com/google/cadvisor/blob/master/docs/storage/prometheus.md) for the full list.

## Gateway Metrics

The DocumentDB Gateway is being instrumented to emit application-level metrics over OTLP, and a future operator release will document these once the gateway image with that instrumentation ships in a public release.

## CNPG / PostgreSQL Metrics

PostgreSQL-level metrics from the CloudNative-PG instance manager are out of scope for this preview. A future revision of the operator will surface a curated set of these metrics through the OTel sidecar; until then, see the [CloudNative-PG monitoring docs](https://cloudnative-pg.io/documentation/current/monitoring/) if you need them today.
