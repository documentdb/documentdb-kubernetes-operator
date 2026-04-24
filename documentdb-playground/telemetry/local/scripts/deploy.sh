#!/bin/bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOCAL_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(cd "$LOCAL_DIR/../../.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-telemetry}"
CONTEXT="kind-${CLUSTER_NAME}"
# Path to the operator Helm chart in this repo. Built from the same branch
# so the playground exercises in-tree operator changes (e.g. base_config.yaml
# updates) without needing a published release.
OPERATOR_CHART_DIR="${OPERATOR_CHART_DIR:-${REPO_ROOT}/operator/documentdb-helm-chart}"

echo "=== DocumentDB Telemetry Playground ==="

# Step 1: Create Kind cluster
echo "[1/6] Setting up Kind cluster..."
"$SCRIPT_DIR/setup-kind.sh"

# Step 2: Wait for cluster
echo "[2/6] Waiting for cluster to be ready..."
kubectl wait --for=condition=Ready nodes --all --context "$CONTEXT" --timeout=120s

# Step 3: Install cert-manager + DocumentDB operator (from this branch)
echo "[3/6] Installing cert-manager and DocumentDB operator..."
if helm list -n documentdb-operator --kube-context "$CONTEXT" 2>/dev/null | grep -q documentdb-operator; then
  echo "  DocumentDB operator already installed, skipping."
else
  # cert-manager
  if kubectl get namespace cert-manager --context "$CONTEXT" &>/dev/null; then
    echo "  cert-manager already installed, skipping."
  else
    echo "  Installing cert-manager..."
    helm repo add jetstack https://charts.jetstack.io --force-update 2>/dev/null
    helm repo update jetstack 2>/dev/null
    helm install cert-manager jetstack/cert-manager \
      --namespace cert-manager \
      --create-namespace \
      --set installCRDs=true \
      --kube-context "$CONTEXT" \
      --wait --timeout 120s
  fi

  # DocumentDB operator from the local chart in this branch.
  echo "  Building chart dependencies..."
  ( cd "$OPERATOR_CHART_DIR" && helm dependency update >/dev/null )

  # Build & load the in-tree operator and sidecar-injector images so the
  # playground exercises uncommitted code (e.g. spec.monitoring.kubeletstats
  # and the matching K8S_NODE_NAME downward-API injection). Without this the
  # chart's default values pull released GHCR images that predate in-flight
  # changes, leaving dashboards empty.
  USE_LOCAL_IMAGES="${USE_LOCAL_IMAGES:-true}"
  HELM_IMAGE_FLAGS=()
  if [[ "$USE_LOCAL_IMAGES" == "true" ]]; then
    LOCAL_TAG="playground-local"
    OPERATOR_IMG="documentdb-operator-local:${LOCAL_TAG}"
    SIDECAR_IMG="documentdb-sidecar-injector-local:${LOCAL_TAG}"

    echo "  Building local operator image (${OPERATOR_IMG})..."
    ( cd "${REPO_ROOT}/operator/src" && IMG="${OPERATOR_IMG}" make docker-build >/dev/null )

    echo "  Building local sidecar-injector image (${SIDECAR_IMG})..."
    ( cd "${REPO_ROOT}/operator/cnpg-plugins/sidecar-injector" && IMG="${SIDECAR_IMG}" make docker-build >/dev/null )

    echo "  Loading images into Kind cluster ${CLUSTER_NAME}..."
    kind load docker-image "${OPERATOR_IMG}" --name "${CLUSTER_NAME}"
    kind load docker-image "${SIDECAR_IMG}" --name "${CLUSTER_NAME}"

    HELM_IMAGE_FLAGS=(
      --set "image.documentdbk8soperator.repository=documentdb-operator-local"
      --set "image.documentdbk8soperator.tag=${LOCAL_TAG}"
      --set "image.documentdbk8soperator.pullPolicy=IfNotPresent"
      --set "image.sidecarinjector.repository=documentdb-sidecar-injector-local"
      --set "image.sidecarinjector.tag=${LOCAL_TAG}"
      --set "image.sidecarinjector.pullPolicy=IfNotPresent"
    )
  fi

  echo "  Installing DocumentDB operator from ${OPERATOR_CHART_DIR}..."
  helm install documentdb-operator "$OPERATOR_CHART_DIR" \
    --namespace documentdb-operator \
    --create-namespace \
    --kube-context "$CONTEXT" \
    ${HELM_IMAGE_FLAGS[@]+"${HELM_IMAGE_FLAGS[@]}"} \
    --wait --timeout 180s
fi

# Step 4: Deploy observability stack (Prometheus + Grafana only — no central collector;
# every DocumentDB pod runs its own OTel Collector sidecar via spec.monitoring).
echo "[4/6] Deploying observability stack..."
kubectl apply -f "$LOCAL_DIR/k8s/observability/namespace.yaml" --context "$CONTEXT"
kubectl apply -f "$LOCAL_DIR/k8s/observability/" --context "$CONTEXT"

# Create dashboard ConfigMap from JSON files
echo "  Loading Grafana dashboards..."
kubectl create configmap grafana-dashboards \
  --namespace=observability \
  --from-file=internals.json="$LOCAL_DIR/dashboards/internals.json" \
  --context "$CONTEXT" \
  --dry-run=client -o yaml | kubectl apply -f - --context "$CONTEXT"

# Step 5: Deploy DocumentDB
# spec.monitoring.enabled triggers the operator to create an OTel ConfigMap
# and inject the otel-collector sidecar via the CNPG sidecar-injector plugin.
# The CNPG-managed <cluster>-app secret is reused for the sidecar's PG creds —
# no dedicated monitoring role is needed.
echo "[5/6] Deploying DocumentDB..."
kubectl apply -f "$LOCAL_DIR/k8s/documentdb/" --context "$CONTEXT"

echo "  Waiting for observability stack..."
kubectl wait --for=condition=Available deployment --all -n observability --context "$CONTEXT" --timeout=180s

# Step 6: Deploy traffic generators
echo "[6/6] Deploying traffic generators..."
echo "  Waiting for DocumentDB pods (this may take a few minutes)..."
kubectl wait --for=condition=Ready pod -l cnpg.io/cluster=documentdb-preview -n documentdb-preview-ns --context "$CONTEXT" --timeout=300s 2>/dev/null || echo "  (DocumentDB pods not ready yet - deploy traffic manually later)"
kubectl apply -f "$LOCAL_DIR/k8s/traffic/" --context "$CONTEXT"

echo ""
echo "=== Deployment Complete ==="
echo "Grafana:    kubectl port-forward svc/grafana 3000:3000 -n observability --context $CONTEXT"
echo "Prometheus: kubectl port-forward svc/prometheus 9090:9090 -n observability --context $CONTEXT"
echo "Validate:   ./scripts/validate.sh"
