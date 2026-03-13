# Multi-Cloud DocumentDB Deployment Guide

This guide provides step-by-step instructions for setting up a multi-cloud
deployment of DocumentDB (see [here](https://github.com/microsoft/documentdb))
using KubeFleet (see [here](https://github.com/kubefleet-dev/kubefleet)) to 
manage clusters across clouds. This setup enables high availability and disaster
recovery. We assume the use of an AKS cluster and an on-prem Kubernetes
cluster that have network access to one another. Other combinations are
possible and will be documented as they are tested.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Architecture Overview](#architecture-overview)
3. [Setting Up the Hub Cluster](#setting-up-the-hub-cluster)
4. [Adding Clusters to Fleet](#adding-clusters-to-fleet)
5. [Installing Operators and Dependencies](#installing-operators-and-dependencies)
6. [Deploying DocumentDB Operator to Fleet](#deploying-documentdb-operator-to-fleet)
7. [Setting Up Replication](#setting-up-replication)
8. [Testing and Verification](#testing-and-verification)
9. [Failover Procedures](#failover-procedures)

## Prerequisites

- Azure account
- [Azure CLI installed and configured with appropriate permissions](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli?view=azure-cli-latest)
- [kubectl installed](https://kubernetes.io/docs/tasks/tools/)
- [helm installed](https://helm.sh/docs/intro/install/)
- [Git client](https://github.com/git-guides/install-git)
- [MongoSH (for testing connection)](https://www.mongodb.com/try/download/shell)
- Two kubernetes clusters that are network connected to each other. For example using
  - [Azure VPN Gatway](https://learn.microsoft.com/en-us/azure/vpn-gateway/vpn-gateway-about-vpngateways)
  - [Azure ExpressRoute](https://learn.microsoft.com/en-us/azure/expressroute/expressroute-introduction)
- ENV variables `$CLOUD_MEMBER` and `$ON_PREM_MEMBER` with the kubectl context names for your clusters 
  - (e.g. "azure-documentdb-cluster", "k3s-cluster-context")

## Architecture Overview

This multi-cloud deployment uses KubeFleet to manage DocumentDB instances across different cloud providers:

- **Hub Cluster**: Central control plane for managing all member clusters
- **Member Clusters**: Clusters in different cloud environments (Azure, On-prem)
- **DocumentDB Operator**: Custom operator for DocumentDB deployments
- **Fleet Networking**: Enables communication between clusters

## Setting Up the Hub Cluster

The hub cluster serves as the central controller for managing the member clusters, find setup instructions here:
https://learn.microsoft.com/en-us/azure/kubernetes-fleet/quickstart-create-fleet-and-members?tabs=without-hub-cluster

## Adding Clusters to Fleet

### Adding AKS cluster to the fleet

Adding an AKS cluster to the fleet is very simple with the Azure portal: 
https://learn.microsoft.com/en-us/azure/kubernetes-fleet/quickstart-create-fleet-and-members-portal

### Adding other Cluster to Fleet

See also the guide here: 
https://github.com/Azure/fleet/blob/main/docs/tutorials/Azure/JoinOnPremClustersToFleet.md

```bash
# Add the hub to your kubectl config file
az fleet get-credentials --resource-group fleet-resource-group --name fleet-hub-name

# This needs to match the member cluster name in your kubectl config file
clusterName="your-on-prem-cluster-name"

git clone https://github.com/kubefleet-dev/kubefleet.git
cd kubefleet
./hack/membership/joinMC.sh v0.14.8 hub $clusterName
cd ..
```

Wait until the cluster shows the correct number of nodes, usually about a minute,
by using the `NODE-COUNT` column from this command `kubectl get membercluster -A`

Then add it to the fleet network

```bash
git clone https://github.com/Azure/fleet-networking
cd fleet-networking
./hack/membership/joinMC.sh v0.14.8 v0.3.8 hub $clusterName
cd ..
```

These commands also will work to add clusters from other cloud providers. 
Run `kubectl get membercluster -A` again and see `True` under `JOINED` to confirm.

## Installing Operators and Dependencies

1. Install cert-manager on each cluster:

```bash
# Install on primary
kubectl config use-context $CLOUD_MEMBER
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager --namespace cert-manager --create-namespace --set installCRDs=true

# Install on replica
kubectl config use-context $ON_PREM_MEMBER
helm install cert-manager jetstack/cert-manager --namespace cert-manager --create-namespace --set installCRDs=true

# Install just the CRDs on the hub for propagation
kubectl config use-context hub
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.crds.yaml
```

Verify that `cert-manager` is installed correctly on each cluster:

```sh
kubectl get pods -n cert-manager
```

Output:

```text
NAMESPACE           NAME                                            READY   STATUS    RESTARTS
cert-manager        cert-manager-6795b8d569-d7lwd                   1/1     Running   0
cert-manager        cert-manager-cainjector-8f69cd69f7-pd9bc        1/1     Running   0          
cert-manager        cert-manager-webhook-7cc5dccc4b-7jmrh           1/1     Running   0          
```


2. Install the DocumentDB operator on the hub:

```bash
kubectl config use-context hub
helm install documentdb-operator oci://ghcr.io/documentdb/documentdb-operator --version 0.0.1-multi-arch --namespace documentdb-operator --create-namespace
```

Verify the namespaces cnpg-system and documentdb-operator were created

```bash
kubectl get namespaces
```

Output:

```
NAME                                                  STATUS   AGE
cnpg-system                                           Active   50m
...
documentdb-operator                                   Active   50m
...
```

## Deploying DocumentDB Operator to fleet

```bash
cat <<EOF > documentdb-base.yaml
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: documentdb-base
spec:
  resourceSelectors:
    - group: ""
      version: v1
      kind: Namespace
      name: documentdb-operator
    - group: ""
      version: v1
      kind: Namespace
      name: cnpg-system
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: dbs.documentdb.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: publications.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: poolers.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: clusterimagecatalogs.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: imagecatalogs.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: scheduledbackups.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: backups.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: subscriptions.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: databases.postgresql.cnpg.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: clusters.postgresql.cnpg.io
    # RBAC roles and bindings
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: documentdb-operator-cluster-role
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: documentdb-operator-cloudnative-pg
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: documentdb-operator-cloudnative-pg-edit
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: documentdb-operator-cloudnative-pg-view
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRoleBinding
      name: documentdb-operator-cluster-rolebinding
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRoleBinding
      name: documentdb-operator-cloudnative-pg
    - group: "admissionregistration.k8s.io"
      version: v1
      kind: MutatingWebhookConfiguration
      name: cnpg-mutating-webhook-configuration
    - group: "admissionregistration.k8s.io"
      version: v1
      kind: ValidatingWebhookConfiguration
      name: cnpg-validating-webhook-configuration
  policy:
    placementType: PickAll
  strategy:
    type: RollingUpdate
EOF

kubectl config use-context hub
kubectl apply -f ./documentdb-base.yaml
```

After a few seconds, ensure that the operator is running on both of the clusters

```sh
kubectl config use-context $CLOUD_MEMBER
kubectl get deployment -n documentdb-operator
kubectl config use-context $ON_PREM_MEMBER
kubectl get deployment -n documentdb-operator
```

Output:

```text
NAME                  READY   UP-TO-DATE   AVAILABLE   AGE
documentdb-operator   1/1     1            1           113s
```


## Setting Up Replication

Physical replication provides high availability and disaster recovery capabilities across clusters.

1. Create configuration maps to identify clusters:

```bash
kubectl config use-context $ON_PREM_MEMBER
kubectl create configmap cluster-name -n kube-system --from-literal=name=on-prem-cluster-name
kubectl config use-context $CLOUD_MEMBER
kubectl create configmap cluster-name -n kube-system --from-literal=name=cloud-cluster-name
```

OR

```bash
cat <<EOF > cloud-cluster-name.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-name
  namespace: kube-system
data:
  name: "cloud-cluster-name"
EOF

cat <<EOF > on-prem-name.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-name
  namespace: kube-system
data:
  name: "on-prem-cluster-name"
EOF

kubectl config use-context $CLOUD_MEMBER
kubectl apply -f ./primary-name.yaml
kubectl config use-context $ON_PREM_MEMBER
kubectl apply -f ./replica-name.yaml
```


2. Apply the DocumentDB resource configuration:

```bash
cat <<EOF > documentdb-resource.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-preview-ns

---

apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 10Gi
  clusterReplication:
    primary: cloud-cluster-name
    clusterList:
      - name: cloud-cluster-name
      - name: on-prem-cluster-name
  exposeViaService:
    serviceType: ClusterIP

---

apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: documentdb-crp
spec:
  resourceSelectors:
    - group: ""
      version: v1
      kind: Namespace
      name: documentdb-preview-ns
  policy:
    placementType: PickAll
  strategy:
    type: RollingUpdate
EOF

kubectl config use-context hub
kubectl apply -f ./documentdb-resource.yaml
```

After a few seconds, ensure that the operator is running on both of the clusters

```sh
kubectl config use-context $CLOUD_MEMBER
kubectl get pods -n documentdb-operator-ns
kubectl config use-context $ON_PREM_MEMBER
kubectl get pods -n documentdb-operator-ns
```

Output:

```text
NAME                   READY   STATUS    RESTARTS   AGE
cloud-cluster-name-1   2/2     Running   0          3m33s
```

## Testing and Verification

1. Test connection to DocumentDB:

```bash
# Get the service IP from primary (cloud)
kubectl config use-context $CLOUD_MEMBER
service_ip=$(kubectl get service documentdb-service-documentdb-preview -n documentdb-preview-ns -o jsonpath="{.status.loadBalancer.ingress[0].ip}")

# Connect using mongosh
mongosh $service_ip:10260 -u default_user -p Admin100 --authenticationMechanism SCRAM-SHA-256 --tls --tlsAllowInvalidCertificates

# Create a test collection and document
use test
db.testCollection.insertOne({ name: "Test Document", value: 1 })
db.testCollection.find()
```

2. For replication testing, verify data is correctly replicated:
   - Insert data on the primary
   - Verify it appears on the replica

## Failover Procedures

### Physical Replication Failover

To initiate a failover from the primary to a replica cluster, run this against the hub:

```bash
kubectl config use-context hub
kubectl patch documentdb documentdb-preview -n documentdb-preview-ns \
  --type='json' -p='[
  {"op": "replace", "path": "/spec/clusterReplication/primary", "value":"on-prem-cluster-name"},
  {"op": "replace", "path": "/spec/clusterReplication/clusterList", "value":[{"name": "on-prem-cluster-name"}]}
  ]'
```

---

**Note**: This guide assumes a basic understanding of Kubernetes, container orchestration, and distributed database concepts. Adjust resource sizes, node counts, and other parameters according to your production requirements.
