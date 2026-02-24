# Monitoring Overview

This guide describes how to monitor DocumentDB clusters running on Kubernetes using OpenTelemetry, Prometheus, and Grafana.

## Prerequisites

- A running Kubernetes cluster with the DocumentDB operator installed
- [Helm 3](https://helm.sh/docs/intro/install/) for deploying Prometheus and Grafana
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured for your cluster
- [`jq`](https://jqlang.github.io/jq/) for processing JSON in verification commands
- (Optional) [OpenTelemetry Operator](https://opentelemetry.io/docs/kubernetes/operator/) for managed collector deployments

## Architecture

A DocumentDB pod contains two containers:

- **PostgreSQL container** — the DocumentDB engine (PostgreSQL with DocumentDB extensions)
- **Gateway container** — MongoDB-compatible API sidecar

The recommended monitoring stack collects infrastructure metrics from these containers and stores them for visualization and alerting.

```
┌──────────────────────────────────────────────────────┐
│                   Grafana                             │
│              (dashboards & alerts)                    │
└──────────────────┬───────────────────────────────────┘
                   │
┌──────────────────┴───────────────────────────────────┐
│                 Prometheus                            │
│            (metrics storage)                         │
└──────────────────┬───────────────────────────────────┘
                   │ remote write
┌──────────────────┴───────────────────────────────────┐
│        OpenTelemetry Collector                        │
│  Receivers: kubeletstats, k8s_cluster, prometheus     │
│  Processors: resource detection, attribute enrichment │
│  Exporters: prometheusremotewrite                     │
└──────────────────┬───────────────────────────────────┘
                   │ scrape
┌──────────────────┴───────────────────────────────────┐
│              DocumentDB Pods                          │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │  PostgreSQL   │  │   Gateway    │                  │
│  │  container    │  │  container   │                  │
│  └──────────────┘  └──────────────┘                  │
└──────────────────────────────────────────────────────┘
```

### Collector deployment modes

The [telemetry design document](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/telemetry-design.md) recommends the OpenTelemetry Collector as a **DaemonSet** (one collector per node) for single-tenant clusters. This provides:

- Lower resource overhead — one collector per node instead of one per pod
- Node-level metrics visibility (CPU, memory, filesystem)
- Simpler configuration and management

The [telemetry playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/telemetry) implements a **Deployment** (one collector per namespace) instead, which is better suited for multi-tenant setups requiring per-namespace metric isolation. Choose the mode that fits your isolation requirements.

## Prometheus Integration

### Operator Metrics

The DocumentDB operator exposes a metrics endpoint via controller-runtime. By default:

- **Bind address**: controlled by `--metrics-bind-address` (default `0`, disabled)
- **Secure mode**: `--metrics-secure=true` serves via HTTPS with authn/authz
- **Certificates**: supply `--metrics-cert-path` for custom TLS, otherwise self-signed certs are generated

To enable metrics scraping, set the bind address in the operator deployment (for example, `:8443` for HTTPS or `:8080` for HTTP).

### CNPG Cluster Metrics

The underlying CloudNative-PG cluster exposes PostgreSQL metrics on each pod. These are collected by the OpenTelemetry Collector's Prometheus receiver via Kubernetes service discovery. Key metric sources:

| Source | Method | Metrics |
|--------|--------|---------|
| kubelet/cAdvisor | `kubeletstats` receiver | Container CPU, memory, network, filesystem |
| Kubernetes API | `k8s_cluster` receiver | Pod status, restart counts, resource requests/limits |
| Application endpoints | `prometheus` receiver | Custom application metrics (when available) |

### ServiceMonitor / PodMonitor

The operator does not ship a metrics `Service` or `ServiceMonitor` by default. If you use the Prometheus Operator and want to scrape controller-runtime metrics, create a `Service` and `ServiceMonitor` matching your deployment. For example, with a Helm release named `documentdb`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: documentdb-operator-metrics
  namespace: documentdb-operator
  labels:
    app: documentdb
spec:
  selector:
    app: documentdb            # must match your Helm release name
  ports:
    - name: metrics
      port: 8443
      targetPort: 8443
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: documentdb-operator
  namespace: documentdb-operator
spec:
  selector:
    matchLabels:
      app: documentdb          # must match the Service labels above
  endpoints:
    - port: metrics
      scheme: https
      tlsConfig:
        insecureSkipVerify: true   # use a proper CA bundle in production
```

!!! note
    Adjust the `app` label to match your Helm release name. The operator must be started with `--metrics-bind-address=:8443` for the endpoint to be available.

## Key Metrics

### Container Resource Metrics

| Metric | Description | Container |
|--------|-------------|-----------|
| `container_cpu_usage_seconds_total` | Cumulative CPU time consumed | postgres, documentdb-gateway |
| `container_memory_working_set_bytes` | Current memory usage | postgres, documentdb-gateway |
| `container_spec_memory_limit_bytes` | Memory limit | postgres, documentdb-gateway |
| `container_network_receive_bytes_total` | Network bytes received | pod-level |
| `container_fs_reads_bytes_total` | Filesystem read bytes | postgres |

### Controller-Runtime Metrics

| Metric | Description |
|--------|-------------|
| `controller_runtime_reconcile_total` | Total reconciliations by controller and result |
| `controller_runtime_reconcile_errors_total` | Total reconciliation errors |
| `controller_runtime_reconcile_time_seconds` | Reconciliation duration histogram |
| `workqueue_depth` | Current depth of the work queue |
| `workqueue_adds_total` | Total items added to the work queue |

### CNPG / PostgreSQL Metrics

When the CNPG monitoring is enabled, additional PostgreSQL-level metrics are available:

| Metric | Description |
|--------|-------------|
| `cnpg_collector_up` | Whether the CNPG metrics collector is running |
| `cnpg_pg_replication_lag` | Replication lag in seconds |
| `cnpg_pg_stat_activity_count` | Number of active connections |
| `cnpg_pg_database_size_bytes` | Database size |

For the full CNPG metrics reference, see the [CloudNative-PG monitoring documentation](https://cloudnative-pg.io/documentation/current/monitoring/).

## Telemetry Playground

The [`documentdb-playground/telemetry/`](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/telemetry) directory contains a complete reference implementation with:

- Multi-tenant namespace isolation (separate Prometheus + Grafana per team)
- OpenTelemetry Collector configurations for cAdvisor metric scraping
- Automated Grafana dashboard provisioning scripts
- AKS cluster setup with the OpenTelemetry Operator

Run the quickstart:

```bash
cd documentdb-playground/telemetry/scripts/

# One-time infrastructure setup
./create-cluster.sh --install-all

# Deploy multi-tenant DocumentDB + monitoring
./deploy-multi-tenant-telemetry.sh

# Create Grafana dashboards
./setup-grafana-dashboards.sh sales-namespace

# Access Grafana
kubectl port-forward -n sales-namespace svc/grafana-sales 3001:3000 &
# Open http://localhost:3001 (playground default: admin / admin123 — change in production)
```

See the [telemetry design document](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/telemetry-design.md) for the full architecture rationale including DaemonSet vs. sidecar trade-offs, OTLP receiver plans, and future application-level metrics.

## Verification

After deploying the monitoring stack, confirm that metrics are flowing:

```bash
# Check that the OpenTelemetry Collector pods are running
kubectl get pods -l app.kubernetes.io/name=opentelemetry-collector

# Verify Prometheus is receiving metrics (port-forward first)
kubectl port-forward svc/prometheus-server 9090:80 &
curl -s 'http://localhost:9090/api/v1/query?query=up' | jq '.data.result | length'

# Confirm DocumentDB container metrics are present
curl -s 'http://localhost:9090/api/v1/query?query=container_cpu_usage_seconds_total{pod=~".*documentdb.*"}' \
  | jq '.data.result | length'
```

If no metrics appear, check:

- The collector's service account has RBAC access to the kubelet metrics API
- Namespace label filters in the collector config match your DocumentDB namespace
- The Prometheus remote-write endpoint is reachable from the collector

## Next Steps

- [Metrics Reference](metrics.md) — detailed metric descriptions and PromQL query examples
- [CloudNative-PG Monitoring](https://cloudnative-pg.io/documentation/current/monitoring/) — upstream PostgreSQL metrics
