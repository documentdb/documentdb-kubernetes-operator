# Container Metrics Collector Reference

This directory contains a reference OpenTelemetry Collector DaemonSet for clusters that do not already collect kubelet-backed container metrics.

Most production clusters already run a node-level metrics collector such as kube-prometheus-stack, an OTel Collector DaemonSet, AKS Container Insights, GKE Cloud Monitoring, CloudWatch Agent, Datadog, or similar. If your platform already collects kubelet metrics, use that existing collector instead of deploying this example.

Apply the reference collector after installing the DocumentDB operator:

```bash
kubectl apply -f documentdb-playground/telemetry/container-metrics/
```

The manifest deploys one OTel Collector per node in the `documentdb-operator` namespace. Its ServiceAccount has `get` access to `nodes/stats` and `nodes/proxy`; tenant DocumentDB pods do not receive kubelet privileges.
