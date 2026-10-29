---
title: Metrics Reference
description: Reference for DocumentDB-owned metrics emitted by the operator-managed OpenTelemetry sidecar.
tags:
  - monitoring
  - metrics
  - opentelemetry
---

# Metrics Reference

This page documents metrics that are part of the DocumentDB operator monitoring contract. OpenTelemetry metric names are canonical; each backend may render names and labels differently.

## Available metrics

### PostgreSQL health

The sidecar runs a lightweight SQL health query against the local PostgreSQL container in each DocumentDB pod.

| OpenTelemetry metric | Type | Description |
|----------------------|------|-------------|
| `documentdb.postgres.up` | Gauge | `1` when the local PostgreSQL container responds to the sidecar health query. |

Common resource attributes include:

| Attribute | Description |
|-----------|-------------|
| `documentdb.cluster` | DocumentDB cluster name |
| `k8s.namespace.name` | Kubernetes namespace |
| `k8s.pod.name` | Kubernetes pod name |

When exported through the Prometheus exporter, this metric is commonly queried as:

```promql
documentdb_postgres_up{documentdb_cluster="my-cluster"}
```

## Planned DocumentDB metric groups

The preview monitoring API is intentionally small while instrumentation lands. These areas are planned or out of scope for the current preview docs:

| Area | Status |
|------|--------|
| Gateway application metrics | Planned. The sidecar can receive local OTLP from the gateway, but user-facing gateway metrics will be documented after a public gateway image emits them. |
| CNPG/PostgreSQL internals | Out of preview scope. A future revision may expose a curated subset such as replication freshness, PostgreSQL availability, WAL health, and database size. |
| Operator controller metrics | Not yet exposed end-to-end through the operator Helm chart. |

## Pod and container resource metrics

Pod CPU, container memory, network I/O, filesystem usage, and node metrics are Kubernetes platform metrics. The DocumentDB operator does not install a node-level collector or grant kubelet permissions to DocumentDB pods.

Use your existing platform collector, managed cloud agent, kube-prometheus-stack, or OTel Collector DaemonSet for these metrics. Filter that data to DocumentDB workloads using pod or container attributes such as:

| Attribute | Typical values |
|-----------|----------------|
| `k8s.namespace.name` | Namespace that contains the DocumentDB cluster |
| `k8s.pod.name` | DocumentDB pod name |
| `k8s.container.name` | `postgres`, `documentdb-gateway`, `otel-collector` |

If your cluster does not already collect kubelet-backed resource metrics, the playground includes a reference DaemonSet at [`documentdb-playground/telemetry/container-metrics/`](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/telemetry/container-metrics). Treat it as a starting point for demo or platform-owned deployments, not as part of the DocumentDB operator monitoring contract.
