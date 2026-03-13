---
title: Networking Configuration
description: Configure service types, external access, DNS, load balancer annotations, and Network Policies for DocumentDB on AKS, EKS, and GKE.
tags:
  - configuration
  - networking
  - load-balancer
---

# Networking Configuration

## Overview

Networking controls how clients connect to your DocumentDB cluster. Configure it to choose between internal-only or external access, and to secure traffic with Network Policies.

The operator creates a Kubernetes [Service](https://kubernetes.io/docs/concepts/services-networking/service/) named `documentdb-service-<cluster-name>` to provide a stable endpoint for your applications. Since pod IPs change whenever pods restart, the Service gives you a fixed address that automatically routes traffic to the active primary pod on port **10260**. You can control how this service is exposed by setting the `exposeViaService` field:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
spec:
  environment: aks               # Optional: aks | eks | gke
  exposeViaService:
    serviceType: LoadBalancer     # ClusterIP (default) | LoadBalancer
```

For the full field reference, see [ExposeViaService](../api-reference.md#exposeviaservice) in the API Reference.

## Service Types

=== "ClusterIP (Internal)"

    Use [ClusterIP](https://kubernetes.io/docs/concepts/services-networking/service/#type-clusterip) when your applications run **inside** the same Kubernetes cluster. This is the default and most secure option — the database is not exposed outside the Kubernetes cluster. For local development, use `kubectl port-forward`.

    ```yaml
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-documentdb
      namespace: default
    spec:
      nodeCount: 1
      instancesPerNode: 1
      resource:
        storage:
          pvcSize: 10Gi
      exposeViaService:
        serviceType: ClusterIP
    ```

=== "LoadBalancer (External)"

    Use [LoadBalancer](https://kubernetes.io/docs/concepts/services-networking/service/#loadbalancer) when your applications run **outside** the Kubernetes cluster, or you need a public or cloud-accessible endpoint. The cloud provider provisions an external IP (AKS, GKE) or hostname (EKS).

    Setting `environment` is optional but recommended — it adds cloud-specific annotations to the LoadBalancer service:

    - **`aks`**: Explicitly marks the load balancer as external (`azure-load-balancer-external: true`)
    - **`eks`**: Uses AWS Network Load Balancer (NLB) with cross-zone balancing and IP-based targeting for lower latency
    - **`gke`**: Sets the load balancer type to External

    Without `environment`, a generic LoadBalancer is created that relies on the cloud provider's default behavior.

    === "AKS"

        ```yaml
        apiVersion: documentdb.io/preview
        kind: DocumentDB
        metadata:
          name: my-documentdb
          namespace: default
        spec:
          nodeCount: 1
          instancesPerNode: 1
          resource:
            storage:
              pvcSize: 10Gi
          environment: aks
          exposeViaService:
            serviceType: LoadBalancer
        ```

    === "EKS"

        ```yaml
        apiVersion: documentdb.io/preview
        kind: DocumentDB
        metadata:
          name: my-documentdb
          namespace: default
        spec:
          nodeCount: 1
          instancesPerNode: 1
          resource:
            storage:
              pvcSize: 10Gi
          environment: eks
          exposeViaService:
            serviceType: LoadBalancer
        ```

    === "GKE"

        ```yaml
        apiVersion: documentdb.io/preview
        kind: DocumentDB
        metadata:
          name: my-documentdb
          namespace: default
        spec:
          nodeCount: 1
          instancesPerNode: 1
          resource:
            storage:
              pvcSize: 10Gi
          environment: gke
          exposeViaService:
            serviceType: LoadBalancer
        ```

## Connect with mongosh

=== "Connection String"

    Retrieve the connection string from the DocumentDB resource status and connect. This works with both ClusterIP and LoadBalancer service types — the operator automatically populates it with the correct service address.

    The connection string contains embedded `kubectl` commands that resolve your credentials automatically:

    ```bash
    CONNECTION_STRING=$(eval echo "$(kubectl get documentdb my-documentdb -n default -o jsonpath='{.status.connectionString}')")
    mongosh "$CONNECTION_STRING"
    ```

    !!! warning
        The `eval` command executes shell expansions in the connection string. This is safe when the string comes from your own DocumentDB resource, but never pipe untrusted input through `eval`.

=== "Port Forwarding"

    Port forwarding works with any service type and is useful for local development. It connects directly to the pod, bypassing the Kubernetes Service. Run `kubectl port-forward` in one terminal and `mongosh` in a separate terminal, since port forwarding must stay running.

    ```bash
    # Terminal 1 — keep this running
    kubectl port-forward svc/documentdb-service-my-documentdb -n default 10260:10260
    ```

    ```bash
    # Terminal 2
    mongosh "mongodb://<username>:<password>@localhost:10260/?directConnection=true"
    ```

## Network Policies

If your Kubernetes cluster uses restrictive [NetworkPolicies](https://kubernetes.io/docs/concepts/services-networking/network-policies/), ensure the following traffic is allowed:

| Traffic | From | To | Port |
|---------|------|----|------|
| Application → Gateway | Application namespace | DocumentDB pods | 10260 |
| CNPG instance manager (upstream) | CNPG operator / DocumentDB pods | DocumentDB pods | 8000 |
| Database replication | DocumentDB pods | DocumentDB pods | 5432 |

!!! note
    Port 8000 is defined by [CloudNativePG](https://cloudnative-pg.io/) (the underlying PostgreSQL operator), not by the DocumentDB operator itself.

!!! note
    The replication rule (port 5432) is only needed when `instancesPerNode > 1`.

### Example NetworkPolicy Configuration

If your Kubernetes cluster enforces a default-deny ingress policy, apply the following to allow DocumentDB traffic.

=== "Gateway Access (port 10260)"

    Allow application traffic to the DocumentDB gateway:

    ```yaml
    apiVersion: networking.k8s.io/v1
    kind: NetworkPolicy
    metadata:
      name: allow-documentdb-gateway
      namespace: <documentdb-namespace>
    spec:
      podSelector:
        matchLabels:
          app: <documentdb-name>          # matches your DocumentDB CR name
      policyTypes:
      - Ingress
      ingress:
      - ports:
        - protocol: TCP
          port: 10260
    ```

=== "CNPG Instance Manager (port 8000)"

    Allow CNPG operator health checks. **Required** — without this, CNPG cannot manage pod lifecycle.

    ```yaml
    apiVersion: networking.k8s.io/v1
    kind: NetworkPolicy
    metadata:
      name: allow-cnpg-status
      namespace: <documentdb-namespace>
    spec:
      podSelector:
        matchLabels:
          app: <documentdb-name>
      policyTypes:
      - Ingress
      ingress:
      - ports:
        - protocol: TCP
          port: 8000
    ```

=== "Replication (port 5432)"

    Allow pod-to-pod replication traffic. Only needed when `instancesPerNode > 1`.

    ```yaml
    apiVersion: networking.k8s.io/v1
    kind: NetworkPolicy
    metadata:
      name: allow-documentdb-replication
      namespace: <documentdb-namespace>
    spec:
      podSelector:
        matchLabels:
          app: <documentdb-name>
      policyTypes:
      - Ingress
      ingress:
      - from:
        - podSelector:
            matchLabels:
              app: <documentdb-name>
        ports:
        - protocol: TCP
          port: 5432
    ```

See the [Kubernetes NetworkPolicy documentation](https://kubernetes.io/docs/concepts/services-networking/network-policies/) for more details.
