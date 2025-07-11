#!/bin/bash

PROJECT_ID="${PROJECT_ID:-gke-documentdb-demo}"
GKE_USER="${GKE_USER:-alexanderlaye57@gmail.com}"
CLUSTER_NAME="${CLUSTER_NAME:-gcp-documentdb}"
ZONE="${ZONE:-us-central1-a}"

# one time
#gcloud projects create $PROJECT_ID
#sudo apt-get install google-cloud-cli-gke-gcloud-auth-plugin

gcloud config set project $PROJECT_ID
gcloud config set account $USER
gcloud auth login --brief

gcloud services enable container.googleapis.com
gcloud projects add-iam-policy-binding $PROJECT_ID --member="user:$USER" --role="roles/container.admin"
gcloud projects add-iam-policy-binding $PROJECT_ID --member="user:$USER" --role="roles/compute.networkAdmin"
gcloud projects add-iam-policy-binding $PROJECT_ID --member="user:$USER" --role="roles/iam.serviceAccountUser"

gcloud container clusters create "$CLUSTER_NAME" \
    --zone "$ZONE" \
    --num-nodes "2" \
    --machine-type "e2-standard-4" \
    --enable-ip-access \
    --project $PROJECT_ID

gcloud container clusters get-credentials "$CLUSTER_NAME" \
    --location="$ZONE"
kubectl config rename-context "$(kubectl config current-context)" $CLUSTER_NAME

helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --create-namespace \
        --version v1.13.2 \
        --set installCRDs=true \
        --set prometheus.enabled=false \
        --set webhook.timeoutSeconds=30


cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-operator
  labels:
    istio-injection: enabled
EOF

export VERSION="201"
CHART_PKG="./documentdb-operator-0.0.${VERSION}.tgz"
rm ${CHART_PKG}
helm dependency update ../../documentdb-chart 
helm package ../../documentdb-chart --version 0.0."${VERSION}" --destination . 

export VALUES_FILE=/home/alaye/scripts/kube/values.yaml
helm upgrade --install documentdb-operator "$CHART_PKG" \
      --namespace documentdb-operator \
      --values "$VALUES_FILE"

# Create DocumentDB namespace
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-preview-ns
EOF

kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-preview-ns
type: Opaque
stringData:
  username: default_user
  password: TestPassword
EOF

    # Deploy DocumentDB instance
kubectl apply -f - <<EOF
apiVersion: db.microsoft.com/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDBImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  gatewayImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  documentDbCredentialSecret: documentdb-credentials
  resource:
    pvcSize: 10Gi
  exposeViaService:
    serviceType: LoadBalancer
EOF
