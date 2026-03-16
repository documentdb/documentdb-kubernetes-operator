---
title: Multi-Region Deployment Overview
description: Understand multi-region DocumentDB deployments for disaster
  recovery, low-latency access, and compliance with geographic data residency
  requirements.
tags:
  - multi-region
  - disaster-recovery
  - high-availability
  - architecture
---

## Use Cases

### Disaster Recovery (DR)

Protect against regional outages by maintaining database replicas in separate
geographic regions. If the primary region fails, promote a replica in another
region to maintain service availability.

### Low-Latency Global Access

Reduce application response times and distribute load by deploying read replicas
closer to end users.

### Compliance and Data Residency

Meet regulatory requirements for data storage location by deploying replicas in
specific regions. Ensure that data resides within required geographic
boundaries while maintaining availability.

## Architecture

### Primary-Replica Model

DocumentDB uses a primary-replica architecture where:

- **Primary cluster:** Accepts both read and write operations
- **Replica clusters:** Accept read-only operations and replicate changes from
  primary
- **Replication:** PostgreSQL streaming replication propagates changes from
  primary to replicas

### Cluster Components

Each regional DocumentDB cluster includes:

- **Gateway containers:** Provide MongoDB-compatible API and connection management
- **PostgreSQL containers:** Store data and handle replication (managed by
  CloudNative-PG)
- **Persistent storage:** Regional block storage for data persistence
- **Service endpoints:** LoadBalancer or ClusterIP for client connections
- **Self-name ConfigMap:** A ConfigMap giving the name of the cluster (must
  match clusterList[].name)

### Replication Configuration

Multi-region replication is configured in the `DocumentDB` resource:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  clusterReplication:
    primary: member-eastus2-cluster
    clusterList:
      - name: member-westus3-cluster
      - name: member-uksouth-cluster
      - name: member-eastus2-cluster
```

The operator handles:

- Creating replica clusters in specified regions
- Establishing streaming replication from primary to replicas
- Monitoring replication lag and health
- Coordinating failover operations

## Network Requirements

### Inter-Region Connectivity

Use cloud-native VNet/VPC peering for direct cluster-to-cluster communication:

- **Azure:** VNet peering between AKS clusters
- **AWS:** VPC peering between EKS clusters
- **GCP:** VPC peering between GKE clusters

### Port Requirements

DocumentDB replication requires these ports between Kubernetes clusters:

| Port | Protocol | Purpose |
|------|----------|---------|
| 5432 | TCP | PostgreSQL streaming replication |
| 443 | TCP | Kubernetes API (for KubeFleet, optional) |

Ensure firewall rules and network security groups allow traffic on these ports
between regional clusters.

### DNS and Service Discovery

The operator uses the DocumentDB cluster name and its corresponding CNPG
cluster's generated service to connect the multi-regional clusters, and you
must coordinate that connection between the clusters. Alternatively, there are
two integrations with existing services.

#### Istio Networking

If Istio is installed on the Kubernetes cluster, Istio networking is enabled,
and an east-west gateway is present connecting each Kubernetes cluster, then
the operator generates services that automatically route the default service
names across regions.

#### Fleet Networking

If Fleet networking is installed on each of your Kubernetes clusters, then
instead of populating the connections with default service names, the operator
creates ServiceExports and MultiClusterServices on each Kubernetes cluster,
then use those generated cross-regional services to connect the CNPG instances
to one another.

## Deployment Models

### Managed Fleet Orchestration

Use a multi-cluster orchestration system such as KubeFleet to manage
deployments of resources across Kubernetes clusters and centrally manage
changes, ensuring your topology stays synchronized between regions.

**Example:** [AKS Fleet Deployment](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md)

### Manual Multi-Cluster Management

Deploy DocumentDB resources individually to each Kubernetes cluster, manually
ensuring that each DocumentDB CRD is in sync.

## Performance Considerations

### Replication Lag

Distance between regions affects replication lag. Monitor replication lag with
PostgreSQL metrics and adjust application read patterns accordingly.

### Storage Performance

Each region requires independent storage resources, and each replica must have
an equal or greater volume of available storage compared to the primary.

## Security Considerations

### TLS Encryption

Enable TLS for all connections:

- **Client-to-gateway:** Encrypt application connections (see [TLS Configuration](../configuration/tls.md))
- **Replication traffic:** PostgreSQL SSL for inter-cluster replication
- **Service mesh:** mTLS for cross-cluster service communication

### Authentication and Authorization

Credentials must be synchronized across regions:

- **Kubernetes Secrets:** Replicate secrets to all Kubernetes clusters
  (KubeFleet handles this automatically)
- **RBAC policies:** Apply consistent access controls across regions
- **Credential rotation:** Coordinate credential changes across all clusters

### Network Security

Restrict network access between regions:

- **Private connectivity:** Use VNet/VPC peering instead of public internet
- **Network policies:** Kubernetes NetworkPolicy to limit pod-to-pod
  communication
- **Firewall rules:** Allow only required ports between regional clusters

## Monitoring and Observability

Track multi-region health and performance:

- **Replication lag:** Monitor `pg_stat_replication` metrics
- **Cluster health:** Pod status, resource usage, connection counts
- **Network metrics:** Bandwidth, latency, packet loss between regions
- **Application performance:** Request latency, error rates per region

See [Telemetry Examples](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/README.md)
for OpenTelemetry, Prometheus, and Grafana setup.

## Next Steps

- [Multi-Region Setup Guide](setup.md) - Deploy your first multi-region
  DocumentDB cluster
- [Failover Procedures](failover-procedures.md) - Learn how to handle planned
  and unplanned failovers
- [AKS Fleet Deployment Example](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md)
  - Complete Azure multi-region automation
