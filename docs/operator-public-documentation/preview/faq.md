# Frequently Asked Questions (FAQ)

## General

### Can I run stateful workloads like databases on Kubernetes?

Yes. An [independent research survey](https://dok.community/data-on-kubernetes-2021/) commissioned by the Data on Kubernetes Community found that 90% of respondents believe Kubernetes is ready for stateful workloads, and 70% run databases in production. The DocumentDB Kubernetes Operator builds on this foundation by providing a purpose-built operator that handles the complexity of deploying and managing DocumentDB clusters.

### What is the relationship between DocumentDB and PostgreSQL?

DocumentDB uses PostgreSQL as its underlying storage engine. The DocumentDB Gateway provides MongoDB-compatible APIs on top of PostgreSQL, so you connect to your cluster using standard MongoDB drivers and tools (such as `mongosh`) while the data is stored in PostgreSQL.

### Which Kubernetes distributions are supported?

The operator works on any conformant Kubernetes distribution (version 1.30 or later). It has been tested on:

- **Azure Kubernetes Service (AKS)**
- **Amazon Elastic Kubernetes Service (EKS)**
- **Google Kubernetes Engine (GKE)**
- **kind** and **minikube** for local development

### Is this project production-ready?

The operator is under active development and currently in **preview**. We don't yet recommend it for production workloads. We welcome feedback and contributions as we work toward general availability.

## Installation

### Do I need to install CloudNativePG separately?

No. The DocumentDB operator Helm chart includes CloudNativePG as a dependency. It is installed automatically when you install the operator.

### What is cert-manager used for?

[cert-manager](https://cert-manager.io) manages TLS certificates for the DocumentDB gateway. It must be installed before the operator. See the [quickstart guide](index.md#install-cert-manager) for installation steps.

## Operations

### How do I back up my DocumentDB cluster?

The operator supports both on-demand and scheduled backups using Kubernetes custom resources. See the [Backup and Restore](backup-and-restore.md) guide for details.

### How does high availability work?

Set `spec.instancesPerNode` to 3 to deploy one primary and two replicas. The operator manages automatic failover — if the primary fails, a replica is promoted automatically. See [Advanced Configuration](advanced-configuration/README.md#high-availability) for details.

### Can I deploy across multiple clouds?

Yes. The operator supports multi-cloud deployment with cross-cluster replication. See the [Multi-Cloud Deployment Guide](../../../documentdb-playground/multi-cloud-deployment/README.md) for setup instructions.
