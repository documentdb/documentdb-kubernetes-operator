#!/usr/bin/env bash
set -euo pipefail

# Install DocumentDB operator on a hub (or a chosen member) cluster.
# Strategy:
#  - Prefer hub context if available and functional.
#  - Otherwise, pick the first member cluster in the fleet RG and fetch admin credentials.
#  - Package the local chart (if needed) and install the operator via Helm.

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
CHART_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/operator/documentdb-helm-chart"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION="${VERSION:-200}"
VALUES_FILE="${VALUES_FILE:-}"
BUILD_CHART="${BUILD_CHART:-true}"

# Make sure we have the basics
for cmd in az kubectl helm jq; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Error: required command '$cmd' not found. Install it and re-run." >&2
    exit 1
  fi
done

# Get the hub cluster context name
MEMBERS=$(az aks list -g "$RESOURCE_GROUP" -o json | jq -r '.[] | select(.name|startswith("member-")) | .name')
for cluster in $MEMBERS; do
  echo "Fetching creds for $cluster..."
  az aks get-credentials -g "$RESOURCE_GROUP" -n "$cluster" --overwrite-existing
  if [[ "$cluster" == *"$HUB_REGION"* ]]; then HUB_CLUSTER="$cluster"; fi
done

# Build/package chart, removing old version
if [ "$BUILD_CHART" == true ]; then
  CHART_PKG="./documentdb-operator-0.0.${VERSION}.tgz"
  if [ -f "$CHART_PKG" ]; then
    echo "Found existing chart package $CHART_PKG"
    rm -f "$CHART_PKG"
  fi
  echo "Packaging chart (helm dependency update && helm package)..."
  helm dependency update "$CHART_DIR"
  helm package "$CHART_DIR" --version 0.0."${VERSION}"

  echo "Installing operator from package $CHART_PKG into namespace documentdb-operator on context $HUB_CLUSTER"
  if [ -n "$VALUES_FILE" ] && [ -f "$VALUES_FILE" ]; then
    echo "Using values file: $VALUES_FILE"
    helm upgrade --install documentdb-operator "$CHART_PKG" \
      --namespace documentdb-operator \
      --kube-context "$HUB_CLUSTER" \
      --create-namespace \
      --values "$VALUES_FILE"
  else
    helm upgrade --install documentdb-operator "$CHART_PKG" \
      --namespace documentdb-operator \
      --kube-context "$HUB_CLUSTER" \
      --create-namespace
  fi
else
  echo "Installing from OCI registry (requires helm v3.8+)..."
  if [ -n "$VALUES_FILE" ] && [ -f "$VALUES_FILE" ]; then
    echo "Using values file: $VALUES_FILE"
    helm upgrade --install documentdb-operator \
      oci://ghcr.io/microsoft/documentdb-kubernetes-operator/documentdb-operator \
      --version 0.0.1 \
      --namespace documentdb-operator \
      --kube-context "$HUB_CLUSTER" \
      --create-namespace \
      --values "$VALUES_FILE"
  else
    helm upgrade --install documentdb-operator \
      oci://ghcr.io/microsoft/documentdb-kubernetes-operator/documentdb-operator \
      --version 0.0.1 \
      --namespace documentdb-operator \
      --kube-context "$HUB_CLUSTER" \
      --create-namespace
  fi
fi

kubectl --context "$HUB_CLUSTER" apply -f $SCRIPT_DIR/documentdb-operator-crp.yaml

# Get all member clusters

# Show status on all member clusters
echo "Checking operator status on all member clusters..."

for CLUSTER in $MEMBERS; do
  echo ""
  echo "======================================="
  echo "Cluster: $CLUSTER"
  echo "======================================="

  # Get the context name for this cluster
  CONTEXT="$CLUSTER"

  echo "Checking rollout status on $CLUSTER..."
  set +e
  kubectl --context "$CONTEXT" -n documentdb-operator rollout status deployment/documentdb-operator --timeout=300s
  rc=$?
  set -e
  if [ $rc -ne 0 ]; then
    echo "Warning: operator rollout didn't complete within timeout on $CLUSTER. Check pods:"
    kubectl --context "$CLUSTER" get pods -n documentdb-operator 2>/dev/null || echo "  Unable to get pods on $CLUSTER"
  else
    echo "✓ Operator rollout completed successfully on $CLUSTER"
  fi
  echo ""

  echo "Operator deployments in $CLUSTER:"
  kubectl --context "$CONTEXT" get deploy -n documentdb-operator -o wide 2>/dev/null || echo "  No deployments found or unable to connect"

  echo ""
  echo "Operator pods in $CLUSTER:"
  kubectl --context "$CONTEXT" get pods -n documentdb-operator -o wide 2>/dev/null || echo "  No pods found or unable to connect"

  echo ""
  echo "cnpg-system pods in $CLUSTER:"
  kubectl --context "$CONTEXT" get pods -n cnpg-system -o wide 2>/dev/null || echo "  No pods found or unable to connect"
done

echo ""
echo "======================================="
echo "Summary of operator status across all member clusters"
echo "======================================="

# Show a summary table
echo ""
echo "Deployment Status Summary:"
for CLUSTER in $MEMBERS; do
  READY=$(kubectl --context "$CLUSTER" get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
  DESIRED=$(kubectl --context "$CLUSTER" get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
  echo "  $CLUSTER: $READY/$DESIRED replicas ready"
done

echo ""
echo "Done. If any commands failed due to permissions, ensure your Azure account has contributor/AKS admin permissions in resource group '$RESOURCE_GROUP'."
