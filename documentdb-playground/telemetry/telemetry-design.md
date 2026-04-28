# DocumentDB Telemetry Architecture Design

## Overview

This document outlines the telemetry architecture for collecting CPU and memory metrics from DocumentDB instances running on Kubernetes and visualizing them through Grafana dashboards.

## Current Implementation Status

As of the Path A rescope, the local telemetry playground uses a **split collector model**, not the earlier unified-collector design captured later in this document:

1. **Per-pod OTel sidecar** (`spec.monitoring.enabled=true`) collects pod-local signals:
   - `sqlquery` for PostgreSQL / DocumentDB health
   - `otlp` on `127.0.0.1:4317` for gateway-pushed metrics
   - Prometheus export on port `9188`
2. **Chart-managed `containerMetrics` DaemonSet** (`--set containerMetrics.enabled=true`) collects kubelet-backed resource metrics:
   - `kubeletstats` against the local kubelet `/stats/summary`
   - container / pod / node CPU, memory, network, and filesystem metrics
   - Prometheus export on port `8889`

This split keeps pod-local metrics inside the workload while moving the kubelet `nodes/stats` privilege to a single chart-managed ServiceAccount. It also matches OpenTelemetry guidance for the kubeletstats receiver: **DaemonSet preferred, sidecar not supported**.

```mermaid
flowchart LR
  subgraph pod["DocumentDB pod"]
    pg["PostgreSQL / DocumentDB"]
    gw["Gateway"]
    sidecar["OTel sidecar :9188"]
    pg --> sidecar
    gw -->|OTLP 127.0.0.1:4317| sidecar
  end

  kubelet["kubelet /stats/summary"] --> ds["containerMetrics DaemonSet :8889"]
  sidecar -->|scrape| prom["Prometheus"]
  ds -->|scrape| prom
  prom --> grafana["Grafana"]
```

Prometheus in the local playground scrapes both jobs directly. Any references below to a unified collector, `k8s_cluster`, `filelog`, `prometheusremotewrite`, or `K8S_NODE_NAME` describe the earlier design exploration and future ideas, not the current manifests.

## Current DocumentDB Architecture

### Pod Structure
Each DocumentDB instance consists of:
- **1 Pod per instancePerNode** (currently limited to 1)
- **2 Containers per Pod**:
  1. **PostgreSQL Container**: The main DocumentDB engine (based on PostgreSQL with DocumentDB extensions)
  2. **Gateway Container**: DocumentDB gateway sidecar for MongoDB API compatibility

### Deployment Flow
1. **Cluster Preparation**: Install dependencies (CloudNative-PG operator, storage classes, etc.)
2. **Operator Installation**: Deploy DocumentDB operator
3. **Instance Deployment**: Create DocumentDB custom resources

## Historical / Future Telemetry Architecture

### Architecture Decision: DaemonSet vs Sidecar

**RECOMMENDED: DaemonSet Approach (One Collector Per Node)**

For DocumentDB monitoring, we recommend **one OpenTelemetry Collector per node** (DaemonSet) rather than sidecar injection:

#### **Why DaemonSet is Better for DocumentDB:**

| Factor | DaemonSet (✅ Recommended) | Sidecar |
|--------|---------------------------|---------|
| **Resource Usage** | 50MB RAM per node | 50MB RAM per DocumentDB pod |
| **Node Metrics** | ✅ Full node visibility | ❌ No node-level metrics |
| **Scalability** | Linear with nodes | Linear with pods |
| **Management** | Simple (3-5 collectors) | Complex (10+ collectors) |
| **DocumentDB Context** | Perfect for current 1-pod-per-node | Overkill for current setup |

#### **Resource Comparison Example:**
```yaml
# Scenario: 9 DocumentDB pods across 3 nodes (3 pods per node)
# instancesPerNode: 3 (maximum supported)

# DaemonSet: 3 collectors total (1 per node)
Total Resources: 150MB RAM, 150m CPU

# Sidecar: 9 collectors (1 per DocumentDB pod)  
Total Resources: 450MB RAM, 450m CPU

# DaemonSet saves: 67% resources
```

#### **When to Consider Sidecar:**
- High-cardinality custom application metrics
- Pod-specific configuration requirements  
- Multi-tenant isolation needs
- Different metric collection intervals per pod

#### **For DocumentDB Use Case:**
- ✅ **Infrastructure monitoring focus** (CPU, memory, I/O)
- ✅ **Node-level context important** (node resources affect DocumentDB performance)
- ✅ **Current architecture**: 1 pod per node, future support for up to 3 pods per node
- ✅ **Resource efficiency** critical for production deployments

### Architecture Components

```
┌─────────────────────────────────────────────────────────────────┐
│                        Grafana Dashboard                        │
│                     (Visualization Layer)                      │
└─────────────────────────┬───────────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────────┐
│                      Prometheus                                 │
│                   (Metrics Storage)                             │
└─────────────────────────┬───────────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────────┐
│              OpenTelemetry Collector (DaemonSet)                │
│                   (Unified Metrics Collection)                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ Receivers:                                                  ││
│  │ • kubeletstats (cAdvisor + Node metrics)                   ││
│  │ • k8s_cluster (Kube State Metrics)                         ││
│  │ • prometheus (scraping endpoints)                          ││
│  │ • filelog (container logs)                                 ││
│  └─────────────────────────────────────────────────────────────┘│
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ Processors:                                                 ││
│  │ • resource detection                                        ││
│  │ • attribute enhancement                                     ││
│  │ • metric filtering                                          ││
│  └─────────────────────────────────────────────────────────────┘│
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ Exporters:                                                  ││
│  │ • prometheusremotewrite                                     ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────┬───────────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────────┐
│                 Kubernetes Cluster                              │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                DocumentDB Pods                              ││
│  │  ┌─────────────────┐  ┌─────────────────┐                  ││
│  │  │ PostgreSQL      │  │ Gateway         │                  ││
│  │  │ Container       │  │ Container       │                  ││
│  │  │ (DocumentDB)    │  │ (MongoDB API)   │                  ││
│  │  └─────────────────┘  └─────────────────┘                  ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

### 1. Metrics Collection Layer (OpenTelemetry Collector)

The OpenTelemetry Collector runs as a DaemonSet on each node and provides unified collection of all metrics through various receivers:

#### A. Kubelet Stats Receiver (Replaces cAdvisor + Node Exporter)
- **Source**: Kubelet's built-in metrics API
- **Container Metrics Collected**:
  - CPU usage (cores, percentage)
  - Memory usage (RSS, cache, swap)
  - Memory limits and requests  
  - CPU limits and requests
  - Network I/O
  - Filesystem I/O
- **Node Metrics Collected**:
  - Node CPU utilization
  - Node memory utilization
  - Node filesystem usage
  - Node network statistics

#### B. Kubernetes Cluster Receiver (Replaces Kube State Metrics)
- **Source**: Kubernetes API server
- **Metrics Collected**:
  - Pod status and phases
  - Container restart counts
  - Resource requests and limits
  - DocumentDB custom resource status
  - Node status and conditions

#### C. Prometheus Receiver (For Application Metrics)
- **Source**: Application metrics endpoints from DocumentDB containers
- **Use Case**: Custom DocumentDB application metrics
- **Future Enhancement**: Gateway container request metrics (Read/Write operations)

#### D. OTLP Receiver (Optional Future Enhancement)
- **Source**: Direct OpenTelemetry instrumentation from applications
- **Use Case**: High-performance metrics collection from DocumentDB Gateway
- **Protocol**: Native OpenTelemetry Protocol (OTLP)

#### OpenTelemetry Collector Configuration
```yaml
receivers:
  kubeletstats:
    collection_interval: 20s
    auth_type: "serviceAccount"
    endpoint: "https://${env:K8S_NODE_NAME}:10250"
    insecure_skip_verify: true
    metric_groups:
      - container
      - pod
      - node
      - volume
    metrics:
      k8s.container.cpu_limit:
        enabled: true
      k8s.container.cpu_request:
        enabled: true
      k8s.container.memory_limit:
        enabled: true
      k8s.container.memory_request:
        enabled: true

  k8s_cluster:
    auth_type: serviceAccount
    node: ${env:K8S_NODE_NAME}
    metadata_exporters: [prometheus]

  # Application metrics from DocumentDB Gateway containers
  prometheus/gateway:
    config:
      scrape_configs:
        - job_name: 'documentdb-gateway'
          kubernetes_sd_configs:
            - role: pod
          relabel_configs:
            - source_labels: [__meta_kubernetes_pod_label_app]
              regex: 'documentdb.*'
              action: keep
            - source_labels: [__meta_kubernetes_pod_container_name]
              regex: 'gateway'
              action: keep
            - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
              action: keep
              regex: true
            - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
              action: replace
              target_label: __metrics_path__
              regex: (.+)
            - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
              action: replace
              regex: ([^:]+)(?::\d+)?;(\d+)
              replacement: $1:$2
              target_label: __address__

  # Future: Native OTLP for high-performance metrics
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  resourcedetection:
    detectors: [env, k8snode, kubernetes]
    timeout: 2s
    override: false

  attributes/documentdb:
    actions:
      - key: documentdb.instance
        from_attribute: k8s.pod.label.app
        action: insert
      - key: documentdb.component
        from_attribute: k8s.container.name
        action: insert
      - key: documentdb.operation_type
        from_attribute: operation
        action: insert

  filter/documentdb:
    metrics:
      include:
        match_type: regexp
        resource_attributes:
          - key: k8s.pod.label.app
            value: "documentdb.*"

exporters:
  prometheusremotewrite:
    endpoint: "http://prometheus:9090/api/v1/write"
    tls:
      insecure: true

service:
  pipelines:
    metrics:
      receivers: [kubeletstats, k8s_cluster, prometheus/gateway, otlp]
      processors: [resourcedetection, attributes/documentdb, filter/documentdb]
      exporters: [prometheusremotewrite]
```

### 2. Metrics Storage Layer

#### Prometheus Configuration (Simplified)
Since OpenTelemetry Collector handles all metric collection and forwarding, Prometheus configuration is simplified:

```yaml
# Prometheus receives metrics via remote write from OpenTelemetry Collector
global:
  scrape_interval: 15s
  evaluation_interval: 15s

# OpenTelemetry Collector pushes metrics here
remote_write_configs: []  # Not needed as OTel pushes via API

# Optional: Direct scraping of Prometheus metrics from OTel Collector itself
scrape_configs:
  - job_name: 'otel-collector'
    static_configs:
      - targets: ['otel-collector:8888']  # OTel Collector's own metrics
```

### 3. Visualization Layer

#### Grafana Dashboard Structure

##### Panel 1: DocumentDB Instance Overview
- **Metrics**:
  - Total number of DocumentDB instances
  - Instance health status
  - Pod restarts in last 24h

##### Panel 2: CPU Metrics
- **PostgreSQL Container CPU**:
  - `rate(k8s_container_cpu_time{k8s_container_name="postgres",k8s_pod_label_app=~"documentdb.*"}[5m])`
- **Gateway Container CPU**:
  - `rate(k8s_container_cpu_time{k8s_container_name="gateway",k8s_pod_label_app=~"documentdb.*"}[5m])`
- **CPU Utilization vs Limits**:
  - `(rate(k8s_container_cpu_time[5m]) / k8s_container_cpu_limit) * 100`

##### Panel 3: Memory Metrics
- **PostgreSQL Container Memory**:
  - `k8s_container_memory_usage{k8s_container_name="postgres",k8s_pod_label_app=~"documentdb.*"}`
- **Gateway Container Memory**:
  - `k8s_container_memory_usage{k8s_container_name="gateway",k8s_pod_label_app=~"documentdb.*"}`
- **Memory Utilization vs Limits**:
  - `(k8s_container_memory_usage / k8s_container_memory_limit) * 100`

##### Panel 4: Gateway Application Metrics (Future Enhancement)
- **Read Operations per Second**:
  - `rate(documentdb_gateway_read_operations_total[5m])`
- **Write Operations per Second**:
  - `rate(documentdb_gateway_write_operations_total[5m])`
- **Operation Latency**:
  - `histogram_quantile(0.95, rate(documentdb_gateway_operation_duration_seconds_bucket[5m]))`
- **Error Rate**:
  - `rate(documentdb_gateway_errors_total[5m]) / rate(documentdb_gateway_operations_total[5m]) * 100`

##### Panel 5: Resource Efficiency
- **CPU Requests vs Usage**
- **Memory Requests vs Usage**
- **Resource waste indicators**

## Application Metrics Integration (Future Enhancement)

### Gateway Container Metrics

When the DocumentDB Gateway container starts emitting application metrics, the DaemonSet architecture seamlessly supports this through multiple collection methods:

#### Method 1: Prometheus Metrics Endpoint (Recommended)
```yaml
# Gateway container exposes metrics on /metrics endpoint
apiVersion: v1
kind: Pod
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
    prometheus.io/path: "/metrics"
spec:
  containers:
  - name: gateway
    image: ghcr.io/microsoft/documentdb/documentdb-local:16
    ports:
    - containerPort: 8080
      name: metrics
```

#### Method 2: OTLP Direct Push (High Performance)
```yaml
# Gateway pushes metrics directly to OTel Collector
# No scraping needed, lower latency, higher throughput
environment:
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://localhost:4317"  # OTel Collector on same node
  - name: OTEL_SERVICE_NAME
    value: "documentdb-gateway"
```

### Expected Gateway Metrics

#### Request Metrics
- `documentdb_gateway_requests_total{method, status}` - Total API requests
- `documentdb_gateway_request_duration_seconds` - Request latency histogram
- `documentdb_gateway_active_connections` - Current active connections

#### Operation Metrics  
- `documentdb_gateway_read_operations_total{database, collection}` - Read operations
- `documentdb_gateway_write_operations_total{database, collection}` - Write operations
- `documentdb_gateway_delete_operations_total{database, collection}` - Delete operations
- `documentdb_gateway_query_operations_total{database, collection}` - Query operations

#### Performance Metrics
- `documentdb_gateway_operation_duration_seconds{operation_type}` - Operation latency
- `documentdb_gateway_cache_hits_total` - Cache hit rate
- `documentdb_gateway_cache_misses_total` - Cache miss rate
- `documentdb_gateway_connection_pool_size` - Connection pool metrics

#### Error Metrics
- `documentdb_gateway_errors_total{error_type, operation}` - Error counts
- `documentdb_gateway_timeouts_total{operation}` - Timeout counts
- `documentdb_gateway_retries_total{operation}` - Retry attempts

### DaemonSet Advantages for Application Metrics

#### ✅ **Perfect Compatibility**
- **Prometheus scraping**: OTel Collector autodiscovers Gateway pods
- **OTLP push**: Gateway can push directly to collector on same node
- **Service discovery**: Automatic discovery of new DocumentDB instances
- **Label propagation**: Kubernetes labels automatically added to metrics

#### ✅ **Network Efficiency**
- **Local collection**: Metrics collected on same node (low latency)
- **Reduced hops**: No cross-node network traffic for metrics
- **Batch processing**: Efficient batching before sending to Prometheus

#### ✅ **Operational Benefits**
- **Single configuration**: Same collector handles infra + app metrics
- **Unified pipeline**: Infrastructure and application metrics in same flow
- **Consistent labeling**: Same resource detection and attribute processing
- **Simplified debugging**: One place to troubleshoot metrics collection

### Updated Architecture with Application Metrics

```
┌─────────────────────────────────────────────────────────────────┐
│                        Grafana Dashboard                        │
│           Infrastructure + Application Metrics                  │
└─────────────────────────┬───────────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────────┐
│                      Prometheus                                 │
│                   (Unified Storage)                             │
└─────────────────────────┬───────────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────────┐
│              OpenTelemetry Collector (DaemonSet)                │
│                   (Unified Collection Agent)                    │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ Receivers:                                                  ││
│  │ • kubeletstats (Infrastructure metrics)                    ││
│  │ • k8s_cluster (Kubernetes metrics)                         ││
│  │ • prometheus (Gateway /metrics scraping)                   ││
│  │ • otlp (Gateway direct push) ← NEW                         ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────┬───────────────────────────────────────┘
                          │
┌─────────────────────────┴───────────────────────────────────────┐
│                DocumentDB Pods                                  │
│  ┌─────────────────┐  ┌─────────────────┐                      │
│  │ PostgreSQL      │  │ Gateway         │                      │
│  │ Container       │  │ Container       │                      │
│  │                 │  │ • /metrics ← NEW│                      │
│  │                 │  │ • OTLP push ← NEW│                     │
│  └─────────────────┘  └─────────────────┘                      │
└─────────────────────────────────────────────────────────────────┘
```

## Implementation Plan

### Phase 1: OpenTelemetry Collector Setup
1. **Deploy OpenTelemetry Operator**
   ```bash
   # Install OpenTelemetry Operator
   kubectl apply -f https://github.com/open-telemetry/opentelemetry-operator/releases/latest/download/opentelemetry-operator.yaml
   ```

2. **Deploy OpenTelemetry Collector as DaemonSet**
   ```yaml
   apiVersion: opentelemetry.io/v1alpha1
   kind: OpenTelemetryCollector
   metadata:
     name: documentdb-metrics-collector
     namespace: documentdb-telemetry
   spec:
     mode: daemonset
     serviceAccount: otel-collector
     config: |
       # [OpenTelemetry configuration from above]
   ```

3. **Deploy Prometheus (Simplified)**
   ```bash
   # Deploy Prometheus without Node Exporter or Kube State Metrics
   helm install prometheus prometheus-community/prometheus \
     --namespace monitoring \
     --create-namespace \
     --set nodeExporter.enabled=false \
     --set kubeStateMetrics.enabled=false \
     --set server.persistentVolume.enabled=true
   ```

### Phase 2: DocumentDB Application Metrics Integration
1. **Gateway Container Enhancement**
   - Add metrics endpoint (`/metrics` on port 8080)
   - Implement OpenTelemetry instrumentation
   - Add prometheus annotations to pods

2. **Collector Configuration Update**
   ```yaml
   # Add to existing OTel Collector config
   receivers:
     prometheus/gateway:
       config:
         scrape_configs:
           - job_name: 'documentdb-gateway'
             kubernetes_sd_configs:
               - role: pod
   ```

3. **Enhanced Dashboards**
   - Add application metrics panels
   - Create alerts for operation errors
   - Add capacity planning metrics

### Phase 3: Advanced Application Monitoring
1. **Create DocumentDB-specific Grafana dashboard**
2. **Implement custom metrics for DocumentDB operations**
3. **Add capacity planning metrics**

## Configuration Examples

### DocumentDB Pod Labels for Monitoring
The DocumentDB operator should add these labels to pods for proper metric collection:

```yaml
metadata:
  labels:
    app.kubernetes.io/name: documentdb
    app.kubernetes.io/instance: "{{ .Values.documentdb.name }}"
    app.kubernetes.io/component: database
    documentdb.documentdb.io/instance: "{{ .Values.documentdb.name }}"
```

### Prometheus Recording Rules (Updated for OpenTelemetry metrics)
```yaml
groups:
  - name: documentdb.rules
    rules:
    - record: documentdb:cpu_usage_rate
      expr: rate(k8s_container_cpu_time{k8s_container_name=~"postgres|gateway",k8s_pod_label_app=~"documentdb.*"}[5m])
    
    - record: documentdb:memory_usage_bytes
      expr: k8s_container_memory_usage{k8s_container_name=~"postgres|gateway",k8s_pod_label_app=~"documentdb.*"}
    
    - record: documentdb:cpu_utilization_percent
      expr: (documentdb:cpu_usage_rate / k8s_container_cpu_limit) * 100
```

### Alert Rules (Updated for OpenTelemetry metrics)
```yaml
groups:
  - name: documentdb.alerts
    rules:
    - alert: DocumentDBHighCPUUsage
      expr: documentdb:cpu_utilization_percent > 80
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "DocumentDB instance {{ $labels.k8s_pod_name }} has high CPU usage"
        description: "CPU usage is above 80% for 5 minutes"
    
    - alert: DocumentDBHighMemoryUsage
      expr: (documentdb:memory_usage_bytes / k8s_container_memory_limit) * 100 > 85
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "DocumentDB instance {{ $labels.k8s_pod_name }} has high memory usage"
        description: "Memory usage is above 85% for 5 minutes"
```

## Deployment Instructions

### 1. Deploy OpenTelemetry Monitoring Stack
```bash
# Create telemetry namespace
kubectl create namespace documentdb-telemetry

# Deploy OpenTelemetry Operator
kubectl apply -f https://github.com/open-telemetry/opentelemetry-operator/releases/latest/download/opentelemetry-operator.yaml

# Deploy OpenTelemetry Collector
kubectl apply -f documentdb-playground/telemetry/otel-collector.yaml

# Deploy Prometheus (simplified without Node Exporter)
helm install prometheus prometheus-community/prometheus \
  --namespace documentdb-telemetry \
  --set nodeExporter.enabled=false \
  --set kubeStateMetrics.enabled=false

# Deploy Grafana
helm install grafana grafana/grafana \
  --namespace documentdb-telemetry
```

### 2. Configure DocumentDB for Monitoring
Update the DocumentDB operator to include monitoring labels and annotations in the CNPG cluster specification.

### 3. Import Grafana Dashboard
Import the pre-built DocumentDB dashboard JSON into Grafana for immediate visualization.

## Security Considerations

1. **RBAC**: Ensure OpenTelemetry Collector has minimal required permissions for Kubelet API access
2. **Network Policies**: Restrict access to metrics endpoints and collector APIs
3. **Data Retention**: Configure appropriate retention policies for metrics in Prometheus
4. **Authentication**: Secure Grafana with proper authentication
5. **Service Account**: Use dedicated service account for OpenTelemetry Collector with appropriate cluster roles

## OpenTelemetry RBAC Configuration
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: otel-collector
  namespace: documentdb-telemetry
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: otel-collector
rules:
- apiGroups: [""]
  resources: ["nodes", "nodes/proxy", "nodes/metrics", "services", "endpoints", "pods"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps"]
  resources: ["daemonsets", "deployments", "replicasets"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["documentdb.io"]
  resources: ["dbs"]
  verbs: ["get", "list", "watch"]
- nonResourceURLs: ["/metrics", "/metrics/cadvisor"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: otel-collector
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: otel-collector
subjects:
- kind: ServiceAccount
  name: otel-collector
  namespace: documentdb-telemetry
```

## Monitoring Best Practices

1. **Label Consistency**: Use consistent labeling across all DocumentDB resources
2. **Metric Cardinality**: Avoid high-cardinality labels that could impact Prometheus performance
3. **Alert Thresholds**: Set realistic thresholds based on workload patterns
4. **Dashboard Organization**: Group related metrics and use consistent color schemes
5. **Performance Impact**: Monitor the monitoring stack's own resource usage

## Future Enhancements

1. **Custom DocumentDB Metrics**: Implement DocumentDB-specific application metrics
2. **Distributed Tracing**: Add OpenTelemetry for request tracing
3. **Log Aggregation**: Integrate with ELK stack for log analysis
4. **Capacity Planning**: Implement predictive analytics for resource planning
5. **Multi-Cloud Support**: Extend monitoring to work across different cloud providers
