---
title: "Quickstart: K3s"
description: Deploy a DocumentDB cluster on K3s, a lightweight Kubernetes distribution.
tags:
  - getting-started
  - k3s
  - local-development
  - edge
search:
  boost: 2
---

# Quickstart: K3s

This guide walks you through deploying a DocumentDB cluster on [K3s](https://k3s.io/), a lightweight Kubernetes distribution. K3s is designed for resource-constrained environments, edge deployments, and scenarios where a full Kubernetes distribution is not needed.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Linux system | Ubuntu 20.04+, Debian 11+, or similar | K3s runs on Linux natively |
| [curl](https://curl.se/) | Any | K3s installer |
| [Helm](https://helm.sh/docs/intro/install/) | 3.x | Package manager |
| [mongosh](https://www.mongodb.com/docs/mongodb-shell/install/) | Latest | MongoDB shell for connecting |

!!! important
    The DocumentDB operator requires **Kubernetes 1.35+** for [ImageVolume](https://kubernetes.io/docs/concepts/storage/volumes/#image) GA support. Use K3s v1.35.0+k3s1 or later.

!!! note "macOS and Windows users"
    K3s runs natively on Linux only. On macOS or Windows, use a Linux VM (for example, [Multipass](https://multipass.run/), [Lima](https://lima-vm.io/), or WSL2) or consider using [Kind](quickstart-kind.md) instead.

## Install K3s

Install K3s with the required Kubernetes version:

```bash
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="v1.35.0+k3s1" sh -
```

Wait for the node to become ready:

```bash
sudo k3s kubectl get nodes
```

```text
NAME       STATUS   ROLES                  AGE   VERSION
my-host    Ready    control-plane,master   30s   v1.35.0+k3s1
```

### Configure kubectl access

Set up kubectl to use the K3s kubeconfig:

```bash
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown $(id -u):$(id -g) ~/.kube/config
chmod 600 ~/.kube/config
```

Verify:

```bash
kubectl get nodes
```

### Install Helm

If Helm is not already installed:

```bash
curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
```

## Install cert-manager

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.install=true \
  --wait
```

Verify that cert-manager is running:

```bash
kubectl get pods -n cert-manager
```

## Install the DocumentDB operator

```bash
helm repo add documentdb https://documentdb.github.io/documentdb-kubernetes-operator
helm repo update
helm install documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator \
  --create-namespace \
  --wait
```

Verify:

```bash
kubectl get deployment -n documentdb-operator
```

```text
NAME                  READY   UP-TO-DATE   AVAILABLE   AGE
documentdb-operator   1/1     1            1           60s
```

## Deploy a DocumentDB cluster

### Create credentials

```bash title="Create namespace and credentials Secret"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-ns
---
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-ns
type: Opaque
stringData:
  username: dev_user
  password: DevPassword123
EOF
```

### Create the DocumentDB cluster

```bash title="Deploy a single-node DocumentDB cluster"
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: documentdb-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
EOF
```

Wait for the DocumentDB cluster to become healthy:

```bash
kubectl get documentdb my-documentdb -n documentdb-ns -w
```

```text
NAME            STATUS                     CONNECTION STRING
my-documentdb   Cluster in healthy state   mongodb://...
```

## Connect to DocumentDB

### Option 1: Port forwarding

```bash
kubectl port-forward pod/my-documentdb-1 10260:10260 -n documentdb-ns
```

In another terminal, get the connection string and connect:

```bash
# View the connection string from the DocumentDB cluster status
kubectl get documentdb my-documentdb -n documentdb-ns -o jsonpath='{.status.connectionString}'

# Connect with mongosh (substitute your credentials)
mongosh "mongodb://dev_user:DevPassword123@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
```

### Option 2: K3s built-in load balancer

K3s includes [ServiceLB](https://docs.k3s.io/networking/networking-services#service-load-balancer) (formerly Klipper), which provides LoadBalancer service support without an external cloud provider. Deploy DocumentDB with a LoadBalancer service:

```bash title="Deploy with LoadBalancer service type"
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: documentdb-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: LoadBalancer
EOF
```

Get the external IP:

```bash
kubectl get svc -n documentdb-ns
```

```text
NAME                                  TYPE           CLUSTER-IP    EXTERNAL-IP     PORT(S)           AGE
documentdb-service-my-documentdb      LoadBalancer   10.43.x.x    192.168.1.100   10260:3xxxx/TCP   60s
```

Connect using the connection string from the DocumentDB cluster status:

```bash
kubectl get documentdb my-documentdb -n documentdb-ns
```

```text
NAME            STATUS                     CONNECTION STRING
my-documentdb   Cluster in healthy state   mongodb://...@192.168.1.100:10260/...
```

Connect with `mongosh` using the external IP:

```bash
mongosh "mongodb://dev_user:DevPassword123@192.168.1.100:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
```

For more connection options including application drivers, see [Connecting to DocumentDB](connecting-to-documentdb.md).

## Resource considerations

K3s is designed for constrained environments. Consider these minimums for running DocumentDB:

| Component | CPU | Memory | Storage |
|-----------|-----|--------|---------|
| K3s system | 1 core | 512 MB | — |
| DocumentDB (single instance) | 1 core | 1 GB | 10 Gi |
| cert-manager | 0.1 core | 128 MB | — |
| Operator + CNPG | 0.2 core | 256 MB | — |
| **Total (recommended)** | **2+ cores** | **2+ GB** | **20+ Gi** |

!!! tip
    For resource-constrained environments, use `instancesPerNode: 1` (no HA) to minimize overhead.

## Clean up

```bash
# Delete the DocumentDB cluster
kubectl delete documentdb my-documentdb -n documentdb-ns

# Uninstall the operator
helm uninstall documentdb-operator -n documentdb-operator

# Uninstall K3s
/usr/local/bin/k3s-uninstall.sh
```

## Next steps

- [Connecting to DocumentDB](connecting-to-documentdb.md) — driver examples and connection pooling
- [Quickstart: Kind](quickstart-kind.md) — Docker-based local development
- [Networking](../configuration/networking.md) — service types and load balancer configuration
- [TLS](../configuration/tls.md) — certificate management options
- [k3s Azure Fleet playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/k3s-azure-fleet) — multi-region K3s on Azure VMs with Istio
