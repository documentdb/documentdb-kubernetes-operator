#!/usr/bin/env bash
set -euo pipefail

# Install cert-manager on all clusters (AKS hub via kubectl, k3s via kubectl context)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load deployment info
if [ -f "$SCRIPT_DIR/.deployment-info" ]; then
  source "$SCRIPT_DIR/.deployment-info"
else
  echo "Error: Deployment info not found. Run deploy-infrastructure.sh first."
  exit 1
fi

CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.14.4}"
HUB_CLUSTER_NAME="${HUB_CLUSTER_NAME:-hub-${HUB_REGION}}"

echo "======================================="
echo "cert-manager Installation"
echo "======================================="
echo "Version: $CERT_MANAGER_VERSION"
echo "Hub Cluster: $HUB_CLUSTER_NAME"
echo "======================================="

# Get all member clusters
ALL_MEMBERS="$HUB_CLUSTER_NAME"

# Add k3s clusters from deployment info
IFS=' ' read -ra K3S_REGION_ARRAY <<< "${K3S_REGIONS:-}"
for region in "${K3S_REGION_ARRAY[@]}"; do
  if kubectl config get-contexts "k3s-$region" &>/dev/null; then
    ALL_MEMBERS="$ALL_MEMBERS k3s-$region"
  fi
done

echo "Installing on: $ALL_MEMBERS"

# Add Jetstack Helm repo
echo ""
echo "Adding Jetstack Helm repository..."
helm repo add jetstack https://charts.jetstack.io --force-update
helm repo update

# Install cert-manager on each member cluster
for cluster in $ALL_MEMBERS; do
  echo ""
  echo "======================================="
  echo "Installing cert-manager on $cluster"
  echo "======================================="
  
  kubectl config use-context "$cluster"
  
  # Check if already installed
  if helm list -n cert-manager 2>/dev/null | grep -q cert-manager; then
    echo "cert-manager already installed on $cluster, upgrading..."
    HELM_CMD="upgrade"
  else
    HELM_CMD="install"
  fi
  
  # Apply CRDs explicitly (helm crds.enabled can fail silently)
  echo "Applying cert-manager CRDs..."
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.crds.yaml"

  # Install/upgrade cert-manager
  helm $HELM_CMD cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --version "$CERT_MANAGER_VERSION" \
    --set crds.enabled=true \
    --set prometheus.enabled=false \
    --set webhook.timeoutSeconds=30 \
    --set startupapicheck.enabled=false \
    --wait --timeout 5m || echo "Warning: cert-manager may not be fully ready on $cluster"
  
  echo "✓ cert-manager installed on $cluster"
done

# Apply ClusterResourcePlacement on hub for future clusters
echo ""
echo "Applying cert-manager ClusterResourcePlacement on hub..."
kubectl config use-context "$HUB_CLUSTER_NAME"
kubectl apply -f "$SCRIPT_DIR/cert-manager-crp.yaml"

# Verify installation
echo ""
echo "======================================="
echo "Verification"
echo "======================================="

for cluster in $ALL_MEMBERS; do
  echo ""
  echo "=== $cluster ==="
  kubectl --context "$cluster" get pods -n cert-manager 2>/dev/null || echo "  Pods not ready"
done

echo ""
echo "======================================="
echo "✅ cert-manager Installation Complete!"
echo "======================================="
echo ""
echo "Next steps:"
echo "  1. ./install-documentdb-operator.sh"
echo "  2. ./deploy-documentdb.sh"
echo "======================================="
