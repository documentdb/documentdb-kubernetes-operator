# DocumentDB Kubernetes Operator

The DocumentDB Kubernetes Operator is an open-source operator that runs and manages [DocumentDB](https://github.com/microsoft/documentdb) on Kubernetes.

DocumentDB is the engine powering vCore-based Azure Cosmos DB for MongoDB. Built on PostgreSQL, it provides a native document-oriented NoSQL database with support for CRUD operations on BSON data types.

When you deploy a DocumentDB cluster, the operator creates and manages PostgreSQL instances, the [DocumentDB Gateway](https://github.com/microsoft/documentdb/tree/main/pg_documentdb_gw), and supporting Kubernetes resources. The gateway enables you to connect with MongoDB-compatible drivers, APIs, and tools, while PostgreSQL serves as the underlying storage engine.

!!! note
    This project is under active development but not yet recommended for production use. We welcome your feedback and contributions!

## Quickstart

Follow these steps to install the operator, deploy a DocumentDB cluster, and connect using `mongosh`.

### Prerequisites

- [Helm](https://helm.sh/docs/intro/install/) installed.
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl-linux/) installed.
- A local Kubernetes cluster such as [minikube](https://minikube.sigs.k8s.io/docs/start/), or [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) (v0.31+) installed. You are free to use any other Kubernetes cluster, but that's not a requirement for this quickstart.
- Install [mongosh](https://www.mongodb.com/docs/mongodb-shell/install/) to connect to the DocumentDB cluster.

> **Kubernetes version:** **Kubernetes 1.35+** is recommended. The operator uses the [ImageVolume](https://kubernetes.io/docs/concepts/storage/volumes/#image) feature (GA in K8s 1.35) to mount the DocumentDB extension. On older clusters the operator falls back to a combined image automatically, but support for Kubernetes < 1.35 will be removed in a future release.

### Start a local Kubernetes cluster

If you're using `minikube`:

```sh
minikube start --kubernetes-version=v1.35.0
```

If you are using `kind` (v0.31+), use the following command:

```sh
kind create cluster --image kindest/node:v1.35.0
```

### Install cert-manager

The operator uses [cert-manager](https://cert-manager.io/docs/) to manage TLS certificates for the DocumentDB cluster.

!!! tip
    If you already have cert-manager installed, skip this step.

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager --namespace cert-manager --create-namespace --set installCRDs=true
```

Verify that cert-manager is running:

```bash
kubectl get pods -n cert-manager
```

```text
NAMESPACE           NAME                                            READY   STATUS    RESTARTS
cert-manager        cert-manager-6794b8d569-d7lwd                   1/1     Running   0
cert-manager        cert-manager-cainjector-7f69cd69f7-pd9bc        1/1     Running   0
cert-manager        cert-manager-webhook-6cc5dccc4b-7jmrh           1/1     Running   0
```

### Install the DocumentDB operator

The operator Helm chart automatically installs the [CloudNativePG operator](https://cloudnative-pg.io/docs/) as a dependency in the `cnpg-system` namespace.

!!! warning
    If CloudNativePG is already installed in your cluster, you may experience conflicts. See the Helm chart documentation for options to skip the CNPG dependency.

```bash
# Add the Helm repository
helm repo add documentdb https://documentdb.github.io/documentdb-kubernetes-operator
helm repo update

# Install the operator
helm install documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator \
  --create-namespace \
  --wait
```

Verify the operator is running:

```bash
kubectl get deployment -n documentdb-operator
```

```text
NAME                  READY   UP-TO-DATE   AVAILABLE   AGE
documentdb-operator   1/1     1            1           113s
```

Confirm the DocumentDB CRDs are installed:

```bash
kubectl get crd | grep documentdb
```

```text
dbs.documentdb.io
```

### Store DocumentDB credentials in a Kubernetes Secret

Before deploying a cluster, create a Secret with the credentials that the DocumentDB gateway will use. The operator's sidecar injector automatically projects these values as environment variables into the gateway container.

```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-preview-ns
---
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-preview-ns
type: Opaque
stringData:
  username: k8s_secret_user
  password: K8sSecret100
EOF
```

Verify the Secret:

```bash
kubectl get secret documentdb-credentials -n documentdb-preview-ns
```

```text
NAME                     TYPE     DATA   AGE
documentdb-credentials   Opaque   2      10s
```

!!! note
    By default, the operator expects a Secret named `documentdb-credentials` with `username` and `password` keys. To use a different Secret name, set `spec.documentDbCredentialSecret` in your DocumentDB resource.

### Deploy a DocumentDB cluster

Create a single-node DocumentDB cluster:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
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

Wait for the cluster to initialize, then verify it's running:

```bash
kubectl get pods -n documentdb-preview-ns
```

```text
NAME                   READY   STATUS    RESTARTS   AGE
documentdb-preview-1   2/2     Running   0          26m
```

Check the DocumentDB resource status:

```bash
kubectl get DocumentDB -n documentdb-preview-ns
```

```text
NAME                 STATUS                     CONNECTION STRING
documentdb-preview   Cluster in healthy state   mongodb://...
```

### Connect to the DocumentDB cluster

Choose a connection method based on your service type.

#### Option 1: ClusterIP service (default — for local development)

**Step 1:** Set up port forwarding (keep this terminal open):

```bash
kubectl port-forward pod/documentdb-preview-1 10260:10260 -n documentdb-preview-ns
```

**Step 2:** In a new terminal, connect with [mongosh](https://www.mongodb.com/docs/mongodb-shell/install/):

```bash
mongosh 127.0.0.1:10260 \
  -u k8s_secret_user \
  -p K8sSecret100 \
  --authenticationMechanism SCRAM-SHA-256 \
  --tls --tlsAllowInvalidCertificates
```

Or use a connection string:

```bash
mongosh "mongodb://k8s_secret_user:K8sSecret100@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
```

#### Option 2: LoadBalancer service (for cloud deployments)

For direct external access in cloud environments (AKS, EKS, GKE), deploy with a `LoadBalancer` service type:

**Step 1:** Deploy DocumentDB with LoadBalancer:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
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

**Step 2:** Wait for the external IP:

```bash
kubectl get services -n documentdb-preview-ns -w
```

```text
NAME                                    TYPE           CLUSTER-IP     EXTERNAL-IP     PORT(S)           AGE
documentdb-service-documentdb-preview   LoadBalancer   10.0.228.243   52.149.56.216   10260:30312/TCP   2m
```

**Step 3:** Connect using the external IP:

```bash
# Get the connection string
kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.status.connectionString}'

# Connect with mongosh
mongosh "<connection-string>"
```

!!! note
    LoadBalancer services are supported in cloud environments (AKS, EKS, GKE) and in local development with [minikube](https://minikube.sigs.k8s.io/docs/handbook/accessing/) and [kind](https://kind.sigs.k8s.io/docs/user/loadbalancer).

### Work with data

Once connected, create a database, a collection, and insert some documents:

```bash
use testdb

db.createCollection("test_collection")

db.test_collection.insertMany([
  { name: "Alice", age: 30 },
  { name: "Bob", age: 25 },
  { name: "Charlie", age: 35 }
])

db.test_collection.find()
```

```text
[direct: mongos] test> use testdb
switched to db testdb
[direct: mongos] testdb> db.createCollection("test_collection")
{ ok: 1 }
[direct: mongos] testdb> db.test_collection.insertMany([
...   { name: "Alice", age: 30 },
...   { name: "Bob", age: 25 },
...   { name: "Charlie", age: 35 }
... ])
{
  acknowledged: true,
  insertedIds: {
    '0': ObjectId('682c3b06491dc99ae02b3fed'),
    '1': ObjectId('682c3b06491dc99ae02b3fee'),
    '2': ObjectId('682c3b06491dc99ae02b3fef')
  }
}
[direct: mongos] testdb> db.test_collection.find()
[
  { _id: ObjectId('682c3b06491dc99ae02b3fed'), name: 'Alice', age: 30 },
  { _id: ObjectId('682c3b06491dc99ae02b3fee'), name: 'Bob', age: 25 },
  {
    _id: ObjectId('682c3b06491dc99ae02b3fef'),
    name: 'Charlie',
    age: 35
  }
]
```

### Try the sample Python app

You can also connect using the sample Python program (PyMongo) in the repository. It inserts a document into a `movies` collection in the `sample_mflix` database.

```bash
git clone https://github.com/documentdb/documentdb-kubernetes-operator
cd documentdb-kubernetes-operator/operator/src/scripts/test-scripts

pip3 install pymongo

python3 mongo-python-data-pusher.py
```

```text
Inserted document ID: 682c54f9505b85fba77ed154
{'_id': ObjectId('682c54f9505b85fba77ed154'),
 'cast': ['Olivia Colman', 'Emma Stone', 'Rachel Weisz'],
 'directors': ['Yorgos Lanthimos'],
 'genres': ['Drama', 'History'],
 'rated': 'R',
 'runtime': 121,
 'title': 'The Favourite MongoDB Movie',
 'type': 'movie',
 'year': 2018}
```

Verify using `mongosh`:

```bash
use sample_mflix
db.movies.find()
```

```text
[direct: mongos] testdb> use sample_mflix
switched to db sample_mflix
[direct: mongos] sample_mflix>

[direct: mongos] sample_mflix> db.movies.find()
[
  {
    _id: ObjectId('682c54f9505b85fba77ed154'),
    title: 'The Favourite MongoDB Movie',
    genres: [ 'Drama', 'History' ],
    runtime: 121,
    rated: 'R',
    year: 2018,
    directors: [ 'Yorgos Lanthimos' ],
    cast: [ 'Olivia Colman', 'Emma Stone', 'Rachel Weisz' ],
    type: 'movie'
  }
]
```

!!! tip
    Update the script's `host` variable to match your service type (`127.0.0.1` for ClusterIP with port-forward, or the external IP for LoadBalancer). Use environment variables for credentials instead of hardcoding them.

## Configuration and advanced topics

### Sidecar injector plugin configuration

The operator uses a sidecar injector plugin to automatically inject the DocumentDB Gateway container into PostgreSQL pods. You can customize the gateway image, pod labels, and annotations.

For details, see [Sidecar Injector Plugin Configuration](../../developer-guides/sidecar-injector-plugin-configuration.md).

### Local high-availability (HA)

Deploy multiple DocumentDB instances with automatic failover by setting `instancesPerNode` to a value greater than 1.

#### Enable local HA

```bash
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-ha
  namespace: <your-namespace>
spec:
  nodeCount: 1
  instancesPerNode: 3
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: LoadBalancer
EOF
```

This configuration creates:

- **1 primary instance** — handles all write operations
- **2 replica instances** — provide read scalability and automatic failover

### Multi-cloud deployment

The operator supports deployment across multiple cloud environments and Kubernetes distributions. For guidance, see the [Multi-Cloud Deployment Guide](../../../documentdb-playground/multi-cloud-deployment/README.md).

### TLS setup

For advanced TLS configuration and testing:

- [TLS Setup Guide](../../../documentdb-playground/tls/README.md) — Complete TLS configuration guide
- [E2E Testing](../../../documentdb-playground/tls/E2E-TESTING.md) — Comprehensive testing procedures


## Clean up

### Delete the DocumentDB cluster

```bash
kubectl delete DocumentDB documentdb-preview -n documentdb-preview-ns
```

Verify the pod is terminated:

```bash
kubectl get pods -n documentdb-preview-ns
```

### Uninstall the operator

```bash
helm uninstall documentdb-operator --namespace documentdb-operator
```

```text
These resources were kept due to the resource policy:
[CustomResourceDefinition] poolers.postgresql.cnpg.io
[CustomResourceDefinition] publications.postgresql.cnpg.io
[CustomResourceDefinition] scheduledbackups.postgresql.cnpg.io
[CustomResourceDefinition] subscriptions.postgresql.cnpg.io
[CustomResourceDefinition] backups.postgresql.cnpg.io
[CustomResourceDefinition] clusterimagecatalogs.postgresql.cnpg.io
[CustomResourceDefinition] clusters.postgresql.cnpg.io
[CustomResourceDefinition] databases.postgresql.cnpg.io
[CustomResourceDefinition] imagecatalogs.postgresql.cnpg.io

release "documentdb-operator" uninstalled
```

### Delete the namespace and CRDs

```bash
kubectl delete namespace documentdb-operator

kubectl delete crd backups.postgresql.cnpg.io \
  clusterimagecatalogs.postgresql.cnpg.io \
  clusters.postgresql.cnpg.io \
  databases.postgresql.cnpg.io \
  imagecatalogs.postgresql.cnpg.io \
  poolers.postgresql.cnpg.io \
  publications.postgresql.cnpg.io \
  scheduledbackups.postgresql.cnpg.io \
  subscriptions.postgresql.cnpg.io \
  dbs.documentdb.io
```
