#!/usr/bin/env bash
set -euo pipefail

# Install DocumentDB operator on all clusters
# - AKS hub: installed via Helm from local chart package
# - k3s VMs: installed via Azure VM Run Command (CNPG from upstream, operator manifests via base64)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load deployment info
if [ -f "$SCRIPT_DIR/.deployment-info" ]; then
  source "$SCRIPT_DIR/.deployment-info"
else
  echo "Error: Deployment info not found. Run deploy-infrastructure.sh first."
  exit 1
fi

CHART_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)/operator/documentdb-helm-chart"
VERSION="${VERSION:-200}"
VALUES_FILE="${VALUES_FILE:-}"
HUB_CLUSTER_NAME="${HUB_CLUSTER_NAME:-hub-${HUB_REGION}}"

echo "======================================="
echo "DocumentDB Operator Installation"
echo "======================================="
echo "Hub Cluster: $HUB_CLUSTER_NAME"
echo "Chart Directory: $CHART_DIR"
echo "======================================="

# Check prerequisites
for cmd in kubectl helm az base64 awk curl; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: Required command '$cmd' not found."
    exit 1
  fi
done

# ─── Step 1: Install on AKS hub via Helm ───
echo ""
echo "======================================="
echo "Step 1: Installing operator on AKS hub ($HUB_CLUSTER_NAME)"
echo "======================================="

kubectl config use-context "$HUB_CLUSTER_NAME"

CHART_PKG="$SCRIPT_DIR/documentdb-operator-0.0.${VERSION}.tgz"
rm -f "$CHART_PKG"

echo "Packaging Helm chart..."
helm dependency update "$CHART_DIR"
helm package "$CHART_DIR" --version "0.0.${VERSION}" --destination "$SCRIPT_DIR"

echo ""
echo "Installing operator..."
HELM_ARGS=(
  --namespace documentdb-operator
  --create-namespace
  --wait --timeout 10m
)
if [ -n "$VALUES_FILE" ] && [ -f "$VALUES_FILE" ]; then
  HELM_ARGS+=(--values "$VALUES_FILE")
fi
helm upgrade --install documentdb-operator "$CHART_PKG" "${HELM_ARGS[@]}"
echo "✓ Operator installed on $HUB_CLUSTER_NAME"

# ─── Step 2: Install on k3s clusters via Run Command ───
echo ""
echo "======================================="
echo "Step 2: Installing operator on k3s clusters via Run Command"
echo "======================================="

# Generate DocumentDB-specific manifests (excluding CNPG subchart)
echo "Generating DocumentDB operator manifests..."
DOCDB_MANIFESTS=$(mktemp)

# Add documentdb-operator namespace
cat > "$DOCDB_MANIFESTS" << 'NSEOF'
---
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-operator
NSEOF

# Extract DocumentDB-specific templates (non-CNPG)
helm template documentdb-operator "$CHART_PKG" \
  --namespace documentdb-operator \
  --include-crds 2>/dev/null | \
  awk '
    /^# Source: documentdb-operator\/crds\/documentdb\.io/{p=1}
    /^# Source: documentdb-operator\/templates\//{p=1}
    /^# Source: documentdb-operator\/charts\//{p=0}
    p
  ' >> "$DOCDB_MANIFESTS"

MANIFEST_B64=$(base64 < "$DOCDB_MANIFESTS")
MANIFEST_SIZE=$(wc -c < "$DOCDB_MANIFESTS" | tr -d ' ')
rm -f "$DOCDB_MANIFESTS"

if [ "$MANIFEST_SIZE" -lt 100 ]; then
  echo "Error: Generated manifest is too small (${MANIFEST_SIZE} bytes) — Helm template may have failed."
  exit 1
fi

echo "Manifest size: $(echo "$MANIFEST_B64" | wc -c | tr -d ' ') bytes (base64), ${MANIFEST_SIZE} bytes (raw)"

IFS=' ' read -ra K3S_REGION_ARRAY <<< "${K3S_REGIONS:-}"
for region in "${K3S_REGION_ARRAY[@]}"; do
  VM_NAME="k3s-$region"
  echo ""
  echo "--- Installing on $VM_NAME ---"

  # Step 2a: Ensure Helm is installed
  echo "  Ensuring Helm is available..."
  az vm run-command invoke -g "$RESOURCE_GROUP" -n "$VM_NAME" --command-id RunShellScript \
    --scripts 'which helm || (curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash)' \
    --query 'value[0].message' -o tsv 2>/dev/null | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag'

  # Step 2b: Install CNPG from upstream release manifest
  echo "  Installing CloudNative-PG..."
  az vm run-command invoke -g "$RESOURCE_GROUP" -n "$VM_NAME" --command-id RunShellScript \
    --scripts '
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl apply --server-side -f https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/main/releases/cnpg-1.27.1.yaml 2>&1 | tail -3
echo "Waiting for CNPG..."
kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=120s 2>&1 || true
echo "CNPG ready"
' \
    --query 'value[0].message' -o tsv 2>/dev/null | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag'

  # Step 2c: Apply DocumentDB operator manifests
  echo "  Applying DocumentDB operator manifests..."
  az vm run-command invoke -g "$RESOURCE_GROUP" -n "$VM_NAME" --command-id RunShellScript \
    --scripts "
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
echo '${MANIFEST_B64}' | base64 -d > /tmp/docdb-manifests.yaml
kubectl apply --server-side -f /tmp/docdb-manifests.yaml 2>&1 | tail -5
rm -f /tmp/docdb-manifests.yaml
echo 'Waiting for operator...'
kubectl -n documentdb-operator rollout status deployment/documentdb-operator --timeout=120s 2>&1 || true
echo 'Done'
" \
    --query 'value[0].message' -o tsv 2>/dev/null | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag'

  echo "  ✓ Operator installed on $VM_NAME"
done

# ─── Step 3: Verify ───
echo ""
echo "======================================="
echo "Verification"
echo "======================================="

echo ""
echo "=== $HUB_CLUSTER_NAME ==="
kubectl --context "$HUB_CLUSTER_NAME" get pods -n documentdb-operator -o wide 2>/dev/null || echo "  No pods"
kubectl --context "$HUB_CLUSTER_NAME" get pods -n cnpg-system -o wide 2>/dev/null || echo "  No pods"

for region in "${K3S_REGION_ARRAY[@]}"; do
  VM_NAME="k3s-$region"
  echo ""
  echo "=== $VM_NAME ==="
  az vm run-command invoke -g "$RESOURCE_GROUP" -n "$VM_NAME" --command-id RunShellScript \
    --scripts '
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
' \
    --query 'value[0].message' -o tsv 2>/dev/null | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag'
done

echo ""
echo "======================================="
echo "✅ DocumentDB Operator Installation Complete!"
echo "======================================="
echo ""
echo "Next step:"
echo "  ./deploy-documentdb.sh"
echo "======================================="
