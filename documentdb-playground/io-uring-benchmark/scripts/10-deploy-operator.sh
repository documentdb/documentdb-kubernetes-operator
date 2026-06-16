#!/usr/bin/env bash
# 10-deploy-operator.sh — install cert-manager and the DocumentDB operator.
#
# REQUIREMENT: the operator build must include spec.postgres.parameters passthrough
# (operator PR #307). Released images >= the version containing #307 work out of the
# box; to use a custom build set OPERATOR_REPO / OPERATOR_TAG.
#
# For a local kind cluster, prefer the repo's dev script instead:
#   cd operator/src && DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl helm
REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
CHART_DIR="$REPO_ROOT/operator/documentdb-helm-chart"
[ -d "$CHART_DIR" ] || die "operator helm chart not found at $CHART_DIR"

OPERATOR_NS="${OPERATOR_NS:-documentdb-operator}"
OPERATOR_REPO="${OPERATOR_REPO:-}"
OPERATOR_TAG="${OPERATOR_TAG:-}"

# --- cert-manager (required by the operator's webhooks + CNPG plugin certs) ---
if kubectl get pods -n cert-manager 2>/dev/null | grep -q Running; then
  log "cert-manager already running."
else
  log "installing cert-manager..."
  helm repo add jetstack https://charts.jetstack.io --force-update
  helm repo update
  helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace --set installCRDs=true
  kubectl wait --for=condition=Ready pod -l app.kubernetes.io/instance=cert-manager \
    -n cert-manager --timeout=300s
fi

# --- DocumentDB operator (bundles CloudNative-PG as a chart dependency) ---
log "building chart dependencies (CloudNative-PG)..."
helm dependency build "$CHART_DIR" >/dev/null

EXTRA=()
[ -n "$OPERATOR_REPO" ] && EXTRA+=(--set "image.documentdbk8soperator.repository=$OPERATOR_REPO")
[ -n "$OPERATOR_TAG" ]  && EXTRA+=(--set-string "image.documentdbk8soperator.tag=$OPERATOR_TAG")

log "installing/upgrading documentdb-operator into namespace $OPERATOR_NS..."
helm upgrade --install documentdb-operator "$CHART_DIR" \
  --namespace "$OPERATOR_NS" --create-namespace \
  "${EXTRA[@]}" --wait --timeout 10m

log "waiting for operator rollout..."
kubectl rollout status deploy -n "$OPERATOR_NS" --timeout=300s 2>/dev/null || true
kubectl get pods -n "$OPERATOR_NS"
log "operator install complete."
warn "Confirm the operator supports spec.postgres.parameters:"
warn "  kubectl explain documentdb.spec.postgres.parameters"
