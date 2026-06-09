---
title: Multi-cloud differences
description: Understand what changes when you extend a multi-region DocumentDB deployment across AKS, GKE, and EKS instead of staying within one cloud provider.
tags:
  - multi-region
  - multi-cloud
  - networking
  - istio
  - disaster-recovery
---

## Overview

A multi-cloud DocumentDB deployment is a multi-region deployment where the
participating Kubernetes clusters run in different cloud providers. The
[primary-replica model](overview.md#primary-replica-model), replication
configuration, and failover behavior are the same as any multi-region
deployment.

Use the multi-region docs as the baseline for topology, setup, and failover:

- [Multi-region deployment overview](overview.md)
- [Multi-region setup guide](setup.md)
- [Multi-region failover procedures](failover-procedures.md)

This page covers only the differences that matter when those Kubernetes clusters
span AKS, GKE, EKS, or another provider mix.

## What changes in multi-cloud deployments

### Provider deployment guides still apply

Start with the single-provider deployment guides when you need provider-specific
Kubernetes prerequisites, storage defaults, authentication setup, and service
exposure behavior. The multi-cloud layer changes how the Kubernetes clusters
connect to each other; it doesn't replace the provider-specific setup work for
each member Kubernetes cluster.

- [Deploy on AKS](../getting-started/deploy-on-aks.md)
- [Deploy on EKS](../getting-started/deploy-on-eks.md)
- [Deploy on GKE](../getting-started/deploy-on-gke.md)

Use the `clusterList[].environment` variables to indicate which Kubernetes clusters
use certain cloud providers for automatic additions from those guides.

```yaml title="documentdb.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  clusterReplication:
    primary: member-eastus2-cluster
    clusterList:
      - name: aws-cluster
        environment: eks
      - name: azure-cluster
        environment: aks
      - name: gcp-cluster
        environment: gke
```

### Networking becomes the primary design decision

In a single cloud provider, you can usually rely on a cohesive private
networking model such as VNet peering, VPC peering, private DNS, and provider
load balancers. In a multi-cloud deployment, those assumptions don't hold across
every Kubernetes cluster. You need an explicit cross-cloud networking design for
replication traffic, service discovery, and operational access.

Common approaches include:

- **Istio multi-Kubernetes-cluster mesh:** Use east-west gateways, shared trust,
  remote secrets, and mesh service discovery to route replication traffic across
  providers.
- **Site-to-site VPNs:** Connect cloud networks with VPN tunnels when your
  organization needs private IP connectivity but doesn't have one native
  provider network that spans every Kubernetes cluster.
- **Cloud interconnect or private WAN:** Use dedicated connectivity when latency,
  bandwidth, or compliance requirements exceed what internet-routed VPN or mesh
  gateways can provide.
- **Public load balancers with strict controls:** Use provider load balancers
  only when private connectivity isn't available, and pair them with TLS,
  firewall restrictions, and tightly scoped access.

The playground uses Istio as the reference approach because it works across AKS,
GKE, and EKS without requiring one provider's private networking model to cover
all member Kubernetes clusters. For implementation details, see the
[multi-cloud deployment playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/multi-cloud-deployment)
and the upstream [Istio multi-primary multi-network documentation](https://istio.io/latest/docs/setup/install/multicluster/multi-primary_multi-network/).

For operator-level networking configuration, including the
`crossCloudNetworkingStrategy` field, see the
[multi-region setup guide](setup.md#networking-management).

### Service discovery needs a cross-provider plan

DocumentDB replication depends on
[stable service names and reachable endpoints](overview.md#dns-and-service-discovery).
Within one provider, private DNS and network peering are often enough. Across
providers, decide how each Kubernetes cluster resolves and reaches the services
in the other Kubernetes clusters.

If you use Istio, remote secrets and east-west gateways provide the discovery
and routing path. If you use site-to-site VPNs or private WAN connectivity, make
sure DNS zones, conditional forwarding, firewall rules, and route tables are
configured consistently across providers.

### Identity and permissions are provider-specific

Multi-region deployments within one provider often share one IAM model. In a
multi-cloud deployment, each provider has its own identity system, permission
model, audit trail, and credential refresh behavior.

Plan for:

- Separate cloud identities for AKS, GKE, and EKS operations.
- Kubernetes RBAC on every member Kubernetes cluster.
- Fleet or GitOps permissions for resource propagation.
- Provider-specific permissions for load balancers, disks, DNS, and network
  changes.

The playground README lists the required provider tools and permissions in
[Prerequisites](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md#prerequisites).

### DNS and client routing need an external source of truth

After failover, clients must reach the promoted primary DocumentDB cluster. In a
single cloud provider, your DNS and traffic-routing options may be standardized.
Across providers, choose a source of truth that all clients can use, such as a
shared DNS zone, global traffic manager, or application configuration system.

The playground can create Azure DNS records, including MongoDB SRV records, as
one example. See
[Azure DNS configuration](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md#azure-dns-configuration)
and [Connect to database](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md#connect-to-database).

### Observability must include the network layer

Multi-cloud failures often show up first as networking symptoms: gateway health,
DNS resolution, route changes, packet loss, or certificate trust issues. In
addition to monitoring [replication lag](overview.md#replication-lag) and
DocumentDB cluster health, monitor:

- Istio control plane and east-west gateway health.
- VPN or interconnect tunnel status.
- Cross-provider latency and packet loss.
- Provider load balancer health.
- DNS record propagation and client resolution.

For a playground observability example, see the
[multi-cloud telemetry folder](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/multi-cloud-deployment/telemetry).

## When to use the playground

Use the multi-cloud playground when you want a runnable AKS, GKE, and EKS
reference deployment. Keep the public docs as the conceptual baseline, and use
the playground for exact commands, environment variables, templates, and cleanup
steps:

- [deploy.sh](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/deploy.sh)
- [deploy-documentdb.sh](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/deploy-documentdb.sh)
- [Multi-cloud failover operations](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md#failover-operations)
- [Multi-cloud troubleshooting](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md#troubleshooting)

## Next steps

- [Multi-region failover procedures](failover-procedures.md)
- [Multi-cloud playground](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/multi-cloud-deployment/README.md)
