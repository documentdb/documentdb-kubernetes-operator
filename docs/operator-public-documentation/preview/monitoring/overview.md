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
- **Gateway container** — MongoDB-compatible API sidecar that exports telemetry via OTLP

The gateway sidecar injector automatically configures each gateway container with:

- `OTEL_EXPORTER_OTLP_ENDPOINT` — points to an OpenTelemetry Collector service
- `OTEL_RESOURCE_ATTRIBUTES` — sets `service.instance.id` to the pod name for per-instance metric attribution

The recommended monitoring stack collects three signals — **metrics**, **traces**, and **logs** — from these containers and stores them for visualization and alerting.

```
┌──────────────────────────────────────────────────────┐
│                   Grafana                             │
│        (dashboards, alerts, trace viewer)             │
└──────────┬──────────────┬──────────────┬─────────────┘
           │              │              │
     ┌─────┴─────┐  ┌────┴────┐   ┌────┴────┐
     │ Prometheus │  │  Tempo  │   │  Loki   │
     │ (metrics)  │  │(traces) │   │ (logs)  │
     └─────┬─────┘  └────┬────┘   └────┬────┘
           │              │              │
┌──────────┴──────────────┴──────────────┴─────────────┐
│              OpenTelemetry Collector                   │
│  Receivers: otlp, postgresql, kubeletstats            │
│  Processors: batch, resource                          │
│  Exporters: prometheus, otlp/tempo, otlphttp/loki     │
└──────────┬──────────────┬────────────────────────────┘
           │              │
    ┌──────┴──────┐  ┌───┴──────────────┐
    │  OTLP push  │  │  SQL scrape      │
    │  (gateway)  │  │  (PG receiver)   │
    └──────┬──────┘  └───┬──────────────┘
           │              │
┌──────────┴──────────────┴────────────────────────────┐
│              DocumentDB Pods                          │
│  ┌──────────────┐  ┌──────────────┐                  │
│  │  PostgreSQL   │  │   Gateway    │──── OTLP push    │
│  │  container    │  │  container   │  (metrics,       │
│  │              ◄──SQL scrape      │   traces, logs)  │
│  └──────────────┘  └──────────────┘                  │
└──────────────────────────────────────────────────────┘
```

### How gateway telemetry reaches the collector

The gateway sidecar injector (a CNPG plugin) injects an `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable into every gateway container. The endpoint follows the pattern:

```
http://<cluster-name>-collector.<namespace>.svc.cluster.local:4317
```

The collector must be reachable at this address. In the local telemetry playground, an `ExternalName` service bridges the namespace gap between the DocumentDB namespace and the observability namespace.

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

The underlying CloudNative-PG cluster exposes PostgreSQL metrics on each pod. These are collected by the OpenTelemetry Collector's `postgresql` receiver via direct SQL queries, or by the `prometheus` receiver via Kubernetes service discovery. Key metric sources:

| Source | Method | Metrics |
|--------|--------|---------|
| kubelet/cAdvisor | `kubeletstats` receiver | Container CPU, memory, network, filesystem |
| PostgreSQL | `postgresql` receiver (SQL) | Backends, commits, rollbacks, replication lag, DB size |
| Gateway | OTLP push | Operations, latency, connections, request/response size |
| Kubernetes API | `k8s_cluster` receiver | Pod status, restart counts, resource requests/limits |

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

### Gateway Application Metrics

These metrics are pushed via OTLP from the gateway sidecar to the OpenTelemetry Collector:

| Metric | Description |
|--------|-------------|
| `db_client_operations_total` | Total MongoDB operations by command type |
| `db_client_operation_duration_seconds_total` | Cumulative operation latency |
| `gateway_client_connections_active` | Current active client connections |
| `gateway_client_connections_total` | Cumulative connections accepted |
| `db_client_connection_active` | Active backend pool connections |
| `db_client_connection_idle` | Idle backend pool connections |
| `db_client_connection_max` | Maximum backend pool size |
| `db_client_request_size_bytes_total` | Cumulative request payload size |
| `db_client_response_size_bytes_total` | Cumulative response payload size |

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

### PostgreSQL Metrics

When using the OTel `postgresql` receiver or CNPG monitoring, additional PostgreSQL-level metrics are available:

| Metric | Description |
|--------|-------------|
| `postgresql_backends` | Number of active backends |
| `postgresql_commits_total` | Total committed transactions |
| `postgresql_rollbacks_total` | Total rolled-back transactions |
| `postgresql_replication_data_delay` | Replication data delay (seconds) |
| `postgresql_db_size_bytes` | Database size |
| `cnpg_pg_replication_lag` | Replication lag in seconds (CNPG) |
| `cnpg_pg_stat_activity_count` | Number of active connections (CNPG) |

For the full CNPG metrics reference, see the [CloudNative-PG monitoring documentation](https://cloudnative-pg.io/documentation/current/monitoring/).

## Telemetry Playground

The [`documentdb-playground/telemetry/`](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/telemetry) directory contains reference implementations:

### Local (Kind)

The `local/` subdirectory provides a self-contained local demo on a Kind cluster with:

- 3-node DocumentDB HA cluster (1 primary + 2 streaming replicas)
- Full observability stack: OTel Collector, Prometheus, Tempo, Loki, Grafana
- Gateway metrics, traces, and logs via OTLP push
- PostgreSQL metrics via the OTel `postgresql` receiver
- System resource metrics via the `kubeletstats` receiver
- Pre-built Grafana dashboard with Gateway, PostgreSQL, and System Resources sections
- Traffic generator for demo workload

```bash
cd documentdb-playground/telemetry/local/scripts/

# Create Kind cluster with local registry
./setup-kind.sh

# Deploy operator, DocumentDB HA, and observability stack
# (see documentdb-playground/telemetry/local/README.md for full steps)
```

### Cloud (Multi-tenant)

The cloud setup supports multi-tenant namespace isolation with:

- Separate Prometheus + Grafana per team
- OpenTelemetry Collector configurations for cAdvisor metric scraping
- Automated Grafana dashboard provisioning scripts
- AKS cluster setup with the OpenTelemetry Operator

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
```

See the [telemetry design document](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/telemetry-design.md) for the full architecture rationale including DaemonSet vs. sidecar trade-offs, OTLP receiver plans, and future application-level metrics.

## Verification

After deploying the monitoring stack, confirm that metrics are flowing:

```bash
# Check that the OpenTelemetry Collector pods are running
kubectl get pods -l app=otel-collector -n observability

# Verify Prometheus is receiving metrics (port-forward first)
kubectl port-forward svc/prometheus 9090:9090 -n observability &
curl -s 'http://localhost:9090/api/v1/query?query=up' | jq '.data.result | length'

# Confirm gateway metrics are present
curl -s 'http://localhost:9090/api/v1/query?query=db_client_operations_total' \
  | jq '.data.result | length'

# Confirm PostgreSQL metrics are present
curl -s 'http://localhost:9090/api/v1/query?query=postgresql_backends' \
  | jq '.data.result | length'

# Confirm kubeletstats metrics are present
curl -s 'http://localhost:9090/api/v1/query?query=k8s_pod_cpu_usage' \
  | jq '.data.result | length'
```

If no metrics appear, check:

- The collector's service account has RBAC access to the kubelet metrics API (`nodes/stats` resource)
- The `ExternalName` service bridges the DocumentDB namespace to the collector namespace
- The sidecar injector is running and injecting `OTEL_EXPORTER_OTLP_ENDPOINT` into gateway containers
- Namespace label filters in the collector config match your DocumentDB namespace

## Next Steps

- [Metrics Reference](metrics.md) — detailed metric descriptions and PromQL query examples
- [CloudNative-PG Monitoring](https://cloudnative-pg.io/documentation/current/monitoring/) — upstream PostgreSQL metrics
