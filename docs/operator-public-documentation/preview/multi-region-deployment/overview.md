---
title: Multi-region deployment overview
description: Understand multi-region DocumentDB deployments for disaster
  recovery, low-latency access, and compliance with geographic data residency
  requirements.
tags:
  - multi-region
  - disaster-recovery
  - high-availability
  - architecture
---

## Use cases

### Disaster recovery (DR)

Protect against regional outages by maintaining database replicas in separate
geographic regions. If the primary region fails, promote a replica in another
region to maintain service availability.

### Low-latency global access

Reduce application response times and distribute load by deploying read replicas
closer to end users.

### Compliance and data residency

Meet regulatory requirements for data storage location by deploying replicas in
specific regions. Ensure that data resides within required geographic
boundaries while maintaining availability.

## Architecture

### Primary-replica model

DocumentDB uses a primary-replica architecture where:

- **Primary cluster:** Accepts both read and write operations
- **Replica clusters:** Accept read-only operations and replicate changes from
  the primary Kubernetes cluster
- **Replication:** PostgreSQL streaming replication propagates changes from
  the primary Kubernetes cluster to replica Kubernetes clusters

### DocumentDB cluster components

Each regional Kubernetes cluster includes:

- **Gateway containers:** Provide MongoDB-compatible API and connection management
- **PostgreSQL containers:** Store data and handle replication (managed by
  CloudNative-PG)
- **Persistent storage:** Regional block storage for data persistence
- **Service endpoints:** LoadBalancer or ClusterIP for client connections
- **Self-name ConfigMap:** A ConfigMap that stores the Kubernetes cluster name
  (must match `clusterList[].name`)

### Replication configuration

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

- Creating replica Kubernetes clusters in specified regions
- Establishing streaming replication from the primary to replicas
- Monitoring replication lag and health
- Coordinating failover operations

## Network requirements

### Inter-region connectivity

Use cloud-native VNet/VPC peering for direct Kubernetes cluster-to-cluster communication:

- **Azure:** VNet peering between AKS clusters
- **AWS:** VPC peering between EKS clusters
- **GCP:** VPC peering between GKE clusters

### Port requirements

DocumentDB replication requires these ports between Kubernetes clusters:

| Port | Protocol | Purpose                                  |
|      |          |                                          |
| 5432 | TCP      | PostgreSQL streaming replication         |
| 443  | TCP      | Kubernetes API (for KubeFleet, optional) |

Ensure firewall rules and network security groups allow traffic on these ports
between regional Kubernetes clusters.

### DNS and service discovery

The operator uses the DocumentDB cluster name and the generated service for the
corresponding CNPG cluster to connect regional deployments. You must make sure
those connections can resolve and route correctly between Kubernetes clusters.
You can also use either of the built-in networking integrations.

#### Istio networking

If Istio is installed on the Kubernetes cluster, Istio networking is enabled,
and an east-west gateway is present connecting each Kubernetes cluster, then
the operator generates services that automatically route the default service
names across regions.

#### Fleet networking

If Fleet networking is installed on each Kubernetes cluster, instead of using
default service names, the operator creates ServiceExports and
MultiClusterServices on each Kubernetes cluster. It then uses those generated
cross-regional services to connect CNPG instances to one another.

## Deployment models

### Managed fleet orchestration

Use a multi-cluster orchestration system such as KubeFleet to manage
deployments of resources across Kubernetes clusters and centrally manage
changes, ensuring your topology stays synchronized between regions.

**Example:** [AKS Fleet Deployment](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md)

### Manual multi-cluster management

Deploy DocumentDB resources individually to each Kubernetes cluster, manually
ensuring that each DocumentDB CRD is in sync.

## Performance considerations

### Replication lag

Distance between regions affects replication lag. Monitor replication lag with
PostgreSQL metrics and adjust application read patterns accordingly.

### Storage performance

Each region requires independent storage resources, and each replica must have
an equal or greater volume of available storage compared to the primary.

## Security considerations

### TLS encryption

Enable TLS for all connections:

- **Client-to-gateway:** Encrypt application connections (see [TLS configuration](../configuration/tls.md))
- **Replication traffic:** PostgreSQL SSL for inter-cluster replication
- **Service mesh:** mTLS for cross-cluster service communication

### Authentication and authorization

Credentials must be synchronized across regions:

- **Kubernetes Secrets:** Replicate secrets to all Kubernetes clusters
  (KubeFleet handles this automatically)
- **RBAC policies:** Apply consistent access controls across regions
- **Credential rotation:** Coordinate credential changes across all Kubernetes
  clusters

### Network security

Restrict network access between regions:

- **Private connectivity:** Use VNet/VPC peering instead of public internet
- **Network policies:** Kubernetes NetworkPolicy to limit pod-to-pod
  communication
- **Firewall rules:** Allow only required ports between regional Kubernetes
  clusters

## Monitoring and observability

Track multi-region health and performance:

- **Replication lag:** Monitor `pg_stat_replication` metrics
- **Kubernetes cluster health:** Pod status, resource usage, and connection counts
- **Network metrics:** Bandwidth, latency, packet loss between regions
- **Application performance:** Request latency, error rates per region

See [Telemetry examples](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/telemetry/README.md)
for OpenTelemetry, Prometheus, and Grafana setup.

## Next steps

- [Multi-region setup guide](setup.md) - Deploy your first multi-region
  DocumentDB cluster
- [Failover procedures](failover-procedures.md) - Learn how to handle planned
  and unplanned failovers
- [AKS Fleet deployment example](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/aks-fleet-deployment/README.md)
