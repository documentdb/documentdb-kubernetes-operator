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
ALL_MEMBERS=("$HUB_CLUSTER_NAME")

# Add k3s clusters from deployment info
IFS=' ' read -ra K3S_REGION_ARRAY <<< "$K3S_REGIONS"
for region in "${K3S_REGION_ARRAY[@]}"; do
  if kubectl config get-contexts "k3s-$region" &>/dev/null; then
    ALL_MEMBERS+=("k3s-$region")
  fi
done

echo "Members to join: ${ALL_MEMBERS[*]}"

# Clone KubeFleet repository
KUBFLEET_DIR=$(mktemp -d)
trap 'rm -rf "$KUBFLEET_DIR"' EXIT

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

# Check out the chart at the same tag as the image to avoid main-branch flag drift
# (main passes --enable-admission-policy-manager which v0.3.1 binary doesn't recognize)
git checkout --quiet "$FLEET_TAG"

# Switch to hub context
kubectl config use-context "$HUB_CLUSTER_NAME"

# Install hub-agent on the hub cluster
echo ""
echo "Creating fleet-system-hub namespace (chart references but doesn't create it)..."
kubectl create namespace fleet-system-hub --dry-run=client -o yaml | kubectl apply -f -

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

# Extract hub cluster CA — needed to patch the member-agent helm release after joinMC.sh
# (kubefleet joinMC.sh has a known bug: it never sets config.hubCA, so the chart default
#  placeholder "<certificate-authority-data>" reaches the pod and breaks base64-decoding.)
echo ""
echo "Extracting hub CA from kubeconfig..."
HUB_CA=$(kubectl config view --raw -o jsonpath="{.clusters[?(@.name==\"$HUB_CLUSTER_NAME\")].cluster.certificate-authority-data}")
if [ -z "$HUB_CA" ]; then
  echo "ERROR: failed to extract hub CA — is the AKS kubeconfig populated?"
  exit 1
fi
echo "  ✓ extracted ${#HUB_CA} bytes of base64-encoded CA"

# Join member clusters using KubeFleet's script
# Known issues:
#   1. joinMC.sh passes extra args to `kubectl config use-context`.
#   2. joinMC.sh never sets config.hubCA — we patch that below with a helm upgrade.
# If a member fails to join, see README troubleshooting for manual join steps.
echo ""
echo "Joining member clusters to fleet..."
chmod +x ./hack/membership/joinMC.sh
./hack/membership/joinMC.sh "$TAG" "$HUB_CLUSTER_NAME" "${ALL_MEMBERS[@]}"

# Workaround: patch each member-agent install with the real hub CA so the
# token refresher can actually talk to the hub API server.
echo ""
echo "Patching member-agent installs with proper hubCA (kubefleet joinMC.sh workaround)..."
for member in "${ALL_MEMBERS[@]}"; do
  echo "  -> $member"
  helm upgrade --kube-context "$member" --install member-agent ./charts/member-agent/ \
    --namespace fleet-system \
    --create-namespace \
    --reuse-values \
    --set config.hubCA="$HUB_CA" \
    > /dev/null
done
echo "  ✓ member-agent hubCA patched on ${#ALL_MEMBERS[@]} cluster(s)"

popd > /dev/null

# Note: fleet-networking is NOT installed because Istio handles all cross-cluster
# networking (mTLS, service discovery, east-west traffic). Installing both would
# create conflicting network configurations.

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
echo "Members: ${ALL_MEMBERS[*]}"
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
