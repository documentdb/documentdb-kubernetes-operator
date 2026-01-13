#!/bin/bash
# Install cert-manager on all member clusters in the fleet

set -euo pipefail

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
MEMBERS=$(az aks list -g "$RESOURCE_GROUP" -o json | jq -r '.[] | select(.name|startswith("member-")) | .name')
SCRIPT_DIR="$(dirname "$0")"

echo -e "Members:\n$MEMBERS"

# Ensure contexts and get hub name
for cluster in $MEMBERS; do
  echo "Fetching creds for $cluster..."
  az aks get-credentials -g "$RESOURCE_GROUP" -n "$cluster" --overwrite-existing
  if [[ "$cluster" == *"$HUB_REGION"* ]]; then HUB_CLUSTER="$cluster"; fi
done

helm repo add jetstack https://charts.jetstack.io 
helm repo update 

# Install cert manager on hub cluster
echo -e "\nInstalling cert-manager on $HUB_CLUSTER..."
kubectl config use-context "$HUB_CLUSTER" 
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true 
kubectl rollout status deployment/cert-manager -n cert-manager --timeout=240s || true
echo "Pods ($HUB_CLUSTER):"
kubectl get pods -n cert-manager -o wide || true

# Create ClusterResourcePlacement to deploy cert-manager to all member clusters
echo -e "\nCreating ClusterResourcePlacement for cert-manager..."
kubectl apply -f "$SCRIPT_DIR/cert-manager-crp.yaml"

echo -e "\nChecking ClusterResourcePlacement status..."
kubectl get clusterresourceplacement cert-manager-crp -o wide || true

echo -e "\nDone. Cert-manager deployed to hub and will be propagated to all member clusters."
echo "Monitor with: kubectl --context $HUB_CLUSTER get clusterresourceplacement cert-manager-crp -o wide"
