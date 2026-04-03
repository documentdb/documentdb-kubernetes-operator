---
title: Frequently Asked Questions
description: Common questions about the DocumentDB Kubernetes Operator, installation, configuration, and operations.
tags:
  - faq
  - troubleshooting
  - getting-started
---

# Frequently Asked Questions (FAQ)

## General

### Can I run stateful workloads like databases on Kubernetes?

Yes. An [independent research survey](https://dok.community/data-on-kubernetes-2021/) commissioned by the Data on Kubernetes Community found that 90% of respondents believe Kubernetes is ready for stateful workloads, and 70% run databases in production. The DocumentDB Kubernetes Operator builds on this foundation by providing a purpose-built operator that handles the complexity of deploying and managing DocumentDB clusters.

### What is the relationship between DocumentDB and PostgreSQL?

DocumentDB uses PostgreSQL as its underlying storage engine. The DocumentDB Gateway provides MongoDB-compatible APIs on top of PostgreSQL, so you connect to your cluster using standard MongoDB drivers and tools (such as `mongosh`) while the data is stored in PostgreSQL.

### Which Kubernetes distributions are supported?

The operator works on any conformant Kubernetes distribution (version 1.35 or later). It has been tested on:

- **Azure Kubernetes Service (AKS)**
- **Amazon Elastic Kubernetes Service (EKS)**
- **Google Kubernetes Engine (GKE)**
- **kind** and **minikube** for local development

### Is this project production-ready?

The operator is under active development and currently in **preview**. We don't yet recommend it for production workloads. We welcome feedback and contributions as we work toward general availability.

### Where can I find the full CRD field reference?

See the [API Reference](api-reference.md) for auto-generated documentation of all DocumentDB, Backup, and ScheduledBackup CRD fields with types, defaults, and validation rules.

## Installation

### What are the minimum Kubernetes version requirements?

Kubernetes **1.35 or later** is required. The operator uses the [ImageVolume](https://kubernetes.io/docs/concepts/storage/volumes/#image) feature, which became generally available in Kubernetes 1.35.

### Do I need to install CloudNativePG separately?

No. The DocumentDB operator Helm chart includes CloudNativePG as a dependency. It is installed automatically when you install the operator.

### What is cert-manager used for?

[cert-manager](https://cert-manager.io) manages TLS certificates for the DocumentDB gateway. It must be installed before the operator. See the [quickstart guide](index.md#install-cert-manager) for installation steps.

### What are the minimum resource requirements?

For a development or testing environment:

| Resource | Minimum |
|----------|---------|
| CPU | 500m per instance |
| Memory | 512Mi per instance |
| Storage | 10Gi per instance |

For production workloads, we recommend:

| Resource | Recommended |
|----------|-------------|
| CPU | 2+ cores per instance |
| Memory | 4Gi+ per instance |
| Storage | 100Gi+ per instance (SSD-backed) |

Configure storage via `spec.resource.storage.pvcSize` in the DocumentDB CR. CPU and memory are managed by the underlying CNPG cluster.

### Which storage classes should I use?

Use storage classes that support:

- **Volume expansion** (`allowVolumeExpansion: true`)
- **Volume snapshots** (for backup support)
- **SSD-backed storage** (for production performance)

Cloud-specific recommendations:

| Cloud | Recommended Storage Class |
|-------|--------------------------|
| AKS | `managed-csi-premium` |
| EKS | `gp3` |
| GKE | `premium-rwo` |

See [Storage Configuration](configuration/storage.md) for details.

## Operations

### How do I connect to my DocumentDB cluster?

Get the connection string from the cluster status:

```bash
kubectl get documentdb <name> -o jsonpath='{.status.connectionString}'
```

For external access via LoadBalancer, the connection string is available once the external IP is assigned.

See [Networking](configuration/networking.md) for connection details.

### How do I back up my DocumentDB cluster?

The operator supports both on-demand and scheduled backups using Kubernetes custom resources. See the [Backup and Restore](operations/backup-and-restore.md) guide for details.

### How does high availability work?

Set `spec.instancesPerNode` to 3 to deploy one primary and two replicas. The operator manages automatic failover — if the primary fails, a replica is promoted automatically. See the [Architecture Overview](architecture/overview.md) for details on the failover process.

### Can I deploy across multiple clouds?

Yes. The operator supports multi-cloud deployment with cross-cluster replication. See the [Multi-Cloud Deployment Guide](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md) for setup instructions.

### How do I upgrade the operator?

Upgrade the operator using Helm:

```bash
helm repo update
helm upgrade documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator
```

The operator performs rolling updates of DocumentDB clusters automatically. Always review the [CHANGELOG](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/CHANGELOG.md) before upgrading.

### How do I monitor my DocumentDB cluster?

The operator exposes PostgreSQL metrics via the [CNPG monitoring integration](https://cloudnative-pg.io/documentation/1.28/monitoring/). You can:

1. **Scrape Prometheus metrics** from pods on port 9187
2. **View cluster status** with `kubectl get documentdb <name>`
3. **Check pod health** with `kubectl describe pod <pod-name>`
4. **Use the kubectl plugin** for status: `kubectl documentdb status <name>`

See the [Telemetry setup](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/README.md) for Prometheus and Grafana configuration.
