#!/bin/bash

set -euo pipefail

export MEMBER_REGIONS="westus3,uksouth,eastus2"
export RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-bcdr-test-rg}"
SCRIPT_DIR="$(dirname "$0")"

# Deploy the AKS fleet with three regions 
$SCRIPT_DIR/../aks-fleet-deployment/deploy-fleet-bicep.sh 
$SCRIPT_DIR/../aks-fleet-deployment/install-cert-manager.sh 
$SCRIPT_DIR/../aks-fleet-deployment/install-documentdb-operator.sh 
$SCRIPT_DIR/../aks-fleet-deployment/deploy-multi-region.sh 

# Install Chaos Mesh on each cluster
helm repo add chaos-mesh https://charts.chaos-mesh.org
MEMBER_CLUSTERS=$(az aks list -g "$RESOURCE_GROUP" -o json | jq -r '.[] | select(.name|startswith("member-")) | .name' | sort)
CLUSTER_ARRAY=($MEMBER_CLUSTERS)
for cluster in "${CLUSTER_ARRAY[@]}"; do
    kubectl create ns chaos-mesh --context "$cluster"
    # Default to /var/run/docker.sock
    helm install chaos-mesh chaos-mesh/chaos-mesh \
        -n=chaos-mesh \
        --set dashboard.create=false \
        --version 2.8.1 \
        --kube-context "$cluster" 
done