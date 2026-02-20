#!/usr/bin/env bash
set -euo pipefail

# Setup KubeFleet hub and join all member clusters (AKS and k3s)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load deployment info
if [ -f "$SCRIPT_DIR/.deployment-info" ]; then
  source "$SCRIPT_DIR/.deployment-info"
else
  echo "Error: Deployment info not found. Run deploy-infrastructure.sh first."
  exit 1
fi

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-k3s-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
HUB_CLUSTER_NAME="hub-${HUB_REGION}"

echo "======================================="
echo "KubeFleet Setup"
echo "======================================="
echo "Resource Group: $RESOURCE_GROUP"
echo "Hub Cluster: $HUB_CLUSTER_NAME"
echo "======================================="

# Check prerequisites
for cmd in kubectl helm git jq curl; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: Required command '$cmd' not found."
    exit 1
  fi
done

# Get all member clusters (hub is also a member + k3s clusters)
ALL_MEMBERS="$HUB_CLUSTER_NAME"

# Add k3s clusters from deployment info
IFS=' ' read -ra K3S_REGION_ARRAY <<< "$K3S_REGIONS"
for region in "${K3S_REGION_ARRAY[@]}"; do
  if kubectl config get-contexts "k3s-$region" &>/dev/null; then
    ALL_MEMBERS="$ALL_MEMBERS k3s-$region"
  fi
done

echo "Members to join: $ALL_MEMBERS"

# Clone KubeFleet repository
KUBFLEET_DIR=$(mktemp -d)
FLEET_NET_DIR=""
trap 'rm -rf "$KUBFLEET_DIR" "$FLEET_NET_DIR"' EXIT

echo ""
echo "Cloning KubeFleet repository..."
if ! git clone --quiet https://github.com/kubefleet-dev/kubefleet.git "$KUBFLEET_DIR"; then
    echo "ERROR: Failed to clone KubeFleet repository"
    exit 1
fi

pushd "$KUBFLEET_DIR" > /dev/null

# Get latest tag
FLEET_TAG=$(curl -s "https://api.github.com/repos/kubefleet-dev/kubefleet/tags" | jq -r '.[0].name')
echo "Using KubeFleet version: $FLEET_TAG"

# Switch to hub context
kubectl config use-context "$HUB_CLUSTER_NAME"

# Install hub-agent on the hub cluster
echo ""
echo "Installing KubeFleet hub-agent on $HUB_CLUSTER_NAME..."
export REGISTRY="ghcr.io/kubefleet-dev/kubefleet"
export TAG="$FLEET_TAG"

helm upgrade --install hub-agent ./charts/hub-agent/ \
  --set image.pullPolicy=Always \
  --set image.repository=$REGISTRY/hub-agent \
  --set image.tag=$TAG \
  --set logVerbosity=5 \
  --set enableGuardRail=false \
  --set forceDeleteWaitTime="3m0s" \
  --set clusterUnhealthyThreshold="5m0s" \
  --set logFileMaxSize=100000 \
  --set MaxConcurrentClusterPlacement=200 \
  --set namespace=fleet-system-hub \
  --set enableWorkload=true \
  --wait

echo "✓ Hub-agent installed"

# Join member clusters using KubeFleet's script
# Known issue: joinMC.sh passes extra args to `kubectl config use-context`.
# If a member fails to join, see README troubleshooting for manual join steps.
echo ""
echo "Joining member clusters to fleet..."
chmod +x ./hack/membership/joinMC.sh
# Note: $ALL_MEMBERS is intentionally unquoted — joinMC.sh expects individual context names as separate args
./hack/membership/joinMC.sh "$TAG" "$HUB_CLUSTER_NAME" $ALL_MEMBERS

popd > /dev/null

# Install fleet-networking
echo ""
echo "Setting up fleet-networking..."
FLEET_NET_DIR=$(mktemp -d)
if ! git clone --quiet https://github.com/Azure/fleet-networking.git "$FLEET_NET_DIR"; then
    echo "ERROR: Failed to clone fleet-networking repository"
    exit 1
fi

pushd "$FLEET_NET_DIR" > /dev/null

NETWORKING_TAG=$(curl -s "https://api.github.com/repos/Azure/fleet-networking/tags" | jq -r '.[0].name')
echo "Using fleet-networking version: $NETWORKING_TAG"

# Install hub-net-controller-manager
kubectl config use-context "$HUB_CLUSTER_NAME"
echo "Installing hub-net-controller-manager..."

helm upgrade --install hub-net-controller-manager ./charts/hub-net-controller-manager/ \
  --set fleetSystemNamespace=fleet-system-hub \
  --set leaderElectionNamespace=fleet-system-hub \
  --set image.tag=$NETWORKING_TAG \
  --wait || echo "Warning: hub-net-controller-manager installation may have issues"

HUB_CLUSTER_ADDRESS=$(kubectl config view -o jsonpath="{.clusters[?(@.name==\"$HUB_CLUSTER_NAME\")].cluster.server}")

# Install networking on each member
for MEMBER_CLUSTER in $ALL_MEMBERS; do
  echo ""
  echo "Installing fleet-networking on $MEMBER_CLUSTER..."
  
  kubectl config use-context "$MEMBER_CLUSTER"
  
  # Apply CRDs
  kubectl apply -f config/crd/ 2>/dev/null || true
  
  # Install mcs-controller-manager
  helm upgrade --install mcs-controller-manager ./charts/mcs-controller-manager/ \
    --set refreshtoken.repository=$REGISTRY/refresh-token \
    --set refreshtoken.tag=$FLEET_TAG \
    --set image.tag=$NETWORKING_TAG \
    --set image.pullPolicy=Always \
    --set refreshtoken.pullPolicy=Always \
    --set config.hubURL=$HUB_CLUSTER_ADDRESS \
    --set config.memberClusterName=$MEMBER_CLUSTER \
    --set enableV1Beta1APIs=true \
    --set logVerbosity=5 || echo "Warning: mcs-controller-manager may have issues on $MEMBER_CLUSTER"
  
  # Install member-net-controller-manager
  helm upgrade --install member-net-controller-manager ./charts/member-net-controller-manager/ \
    --set refreshtoken.repository=$REGISTRY/refresh-token \
    --set refreshtoken.tag=$FLEET_TAG \
    --set image.tag=$NETWORKING_TAG \
    --set image.pullPolicy=Always \
    --set refreshtoken.pullPolicy=Always \
    --set config.hubURL=$HUB_CLUSTER_ADDRESS \
    --set config.memberClusterName=$MEMBER_CLUSTER \
    --set enableV1Beta1APIs=true \
    --set logVerbosity=5 || echo "Warning: member-net-controller-manager may have issues on $MEMBER_CLUSTER"
done

popd > /dev/null

# Verify fleet status
echo ""
echo "======================================="
echo "Fleet Status"
echo "======================================="
kubectl config use-context "$HUB_CLUSTER_NAME"

echo ""
echo "Member clusters:"
kubectl get membercluster 2>/dev/null || echo "No member clusters found yet (may take a moment)"

echo ""
echo "Fleet system pods on hub:"
kubectl get pods -n fleet-system-hub 2>/dev/null || echo "Fleet system not ready"

echo ""
echo "======================================="
echo "✅ KubeFleet Setup Complete!"
echo "======================================="
echo ""
echo "Hub: $HUB_CLUSTER_NAME"
echo "Members: $ALL_MEMBERS"
echo ""
echo "Commands:"
echo "  kubectl --context $HUB_CLUSTER_NAME get membercluster"
echo "  kubectl --context $HUB_CLUSTER_NAME get clusterresourceplacement"
echo ""
echo "Next steps:"
echo "  1. ./install-cert-manager.sh"
echo "  2. ./install-documentdb-operator.sh"
echo "  3. ./deploy-documentdb.sh"
echo "======================================="
