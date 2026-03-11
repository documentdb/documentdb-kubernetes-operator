---
title: "Quickstart: Kind"
description: Set up a local DocumentDB cluster on Kind for development and testing.
tags:
  - getting-started
  - kind
  - local-development
search:
  boost: 2
---

# Quickstart: Kind

This guide walks you through deploying a DocumentDB cluster on a local [Kind](https://kind.sigs.k8s.io/) (Kubernetes in Docker) cluster. Kind is ideal for development, testing, and CI pipelines.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| [Docker](https://docs.docker.com/get-docker/) | 20.10+ | Container runtime for Kind |
| [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | v0.31+ | Local Kubernetes cluster |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | 1.35+ | Kubernetes CLI |
| [Helm](https://helm.sh/docs/intro/install/) | 3.x | Package manager |
| [mongosh](https://www.mongodb.com/docs/mongodb-shell/install/) | Latest | MongoDB shell for connecting |

!!! important
    The DocumentDB operator requires **Kubernetes 1.35+** for [ImageVolume](https://kubernetes.io/docs/concepts/storage/volumes/#image) GA support. Kind v0.31+ ships images for Kubernetes 1.35.

## Create a Kind Kubernetes cluster

```bash
kind create cluster --name documentdb-dev --image kindest/node:v1.35.0
```

Verify the Kubernetes cluster is running:

```bash
kubectl cluster-info
kubectl get nodes
```

```text
NAME                           STATUS   ROLES           AGE   VERSION
documentdb-dev-control-plane   Ready    control-plane   30s   v1.35.0
```

## Install cert-manager

The operator uses [cert-manager](https://cert-manager.io/) to manage TLS certificates.

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

All three pods (`cert-manager`, `cert-manager-cainjector`, `cert-manager-webhook`) should show `Running`.

## Install the DocumentDB operator

The operator Helm chart automatically installs the [CloudNativePG operator](https://cloudnative-pg.io/) as a dependency.

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

!!! tip
    The operator expects a Secret with `username` and `password` keys. The default Secret name is `documentdb-credentials`. To use a different name, set `spec.documentDbCredentialSecret` in your DocumentDB resource.

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

Verify all pods are running (each pod runs a PostgreSQL container and a DocumentDB Gateway sidecar):

```bash
kubectl get pods -n documentdb-ns
```

```text
NAME              READY   STATUS    RESTARTS   AGE
my-documentdb-1   2/2     Running   0          2m
```

## Connect to DocumentDB

Set up port forwarding to access the DocumentDB Gateway (port 10260):

```bash
kubectl port-forward pod/my-documentdb-1 10260:10260 -n documentdb-ns
```

In a new terminal, retrieve the connection string from the DocumentDB cluster status and connect:

```bash
# Get the connection string (contains embedded kubectl commands to resolve credentials)
kubectl get documentdb my-documentdb -n documentdb-ns -o jsonpath='{.status.connectionString}'
```

Use `mongosh` with the resolved credentials:

```bash
mongosh "mongodb://dev_user:DevPassword123@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
```

!!! tip
    The `CONNECTION STRING` column in `kubectl get documentdb` output contains embedded `kubectl` commands that extract the username and password from the credentials Secret at runtime. You can copy and `eval` the full string, or substitute your known credentials directly as shown above.

Try inserting and querying data:

```javascript
use testdb
db.users.insertOne({ name: "Alice", role: "admin" })
db.users.find()
```

For more connection options including application drivers in Python, Node.js, Go, and Java, see [Connecting to DocumentDB](connecting-to-documentdb.md).

## Local high availability

To test high availability locally, deploy with multiple instances:

```bash title="Deploy a 3-instance DocumentDB cluster for HA"
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: documentdb-ns
spec:
  nodeCount: 1
  instancesPerNode: 3
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
EOF
```

This creates one primary instance and two replicas with automatic failover.

```bash
kubectl get pods -n documentdb-ns
```

```text
NAME              READY   STATUS    RESTARTS   AGE
my-documentdb-1   2/2     Running   0          3m
my-documentdb-2   2/2     Running   0          2m
my-documentdb-3   2/2     Running   0          1m
```

## Development with a local registry

For operator development, you can create a Kind Kubernetes cluster with a local Docker registry. This allows you to build and push custom operator images without an external registry.

```bash
cd operator/src
DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh
```

This script:

1. Creates a Kind Kubernetes cluster with a local registry (`localhost:5001`)
2. Builds and pushes the operator and sidecar-injector images
3. Installs cert-manager and the DocumentDB operator
4. Optionally deploys a DocumentDB cluster

See the [Development Environment Guide](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/docs/developer-guides/development-environment.md) for more details.

## Clean up

```bash
# Delete the DocumentDB cluster
kubectl delete documentdb my-documentdb -n documentdb-ns

# Uninstall the operator
helm uninstall documentdb-operator -n documentdb-operator

# Delete the Kind Kubernetes cluster
kind delete cluster --name documentdb-dev
```

## Next steps

- [Connecting to DocumentDB](connecting-to-documentdb.md) — driver examples and connection pooling
- [Quickstart: K3s](quickstart-k3s.md) — lightweight alternative to Kind
- [Networking](../configuration/networking.md) — LoadBalancer and service configuration
- [TLS](../configuration/tls.md) — certificate management options
- [Storage](../configuration/storage.md) — persistent volume configuration
