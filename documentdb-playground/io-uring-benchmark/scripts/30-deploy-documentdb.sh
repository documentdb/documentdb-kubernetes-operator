#!/usr/bin/env bash
# 30-deploy-documentdb.sh — deploy the DocumentDB cluster + in-cluster benchmark runner.
#
# Deploys ONE io_method variant (INITIAL_METHOD, default io_uring so the seccomp path is
# validated immediately), waits for health, verifies io_method, then starts the runner.
#
# Env: INITIAL_METHOD=io_uring|worker|sync   STORAGE_CLASS=<fast-class>
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl
PG_DIR="$(cd "$(dirname "$0")/.." && pwd)"
INITIAL_METHOD="${INITIAL_METHOD:-io_uring}"
MANIFEST="$PG_DIR/manifests/documentdb-${INITIAL_METHOD}.yaml"
[ -f "$MANIFEST" ] || die "no manifest for INITIAL_METHOD='$INITIAL_METHOD'"

kubectl get ns "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"

log "applying gateway credentials..."
kubectl apply -f "$PG_DIR/manifests/credentials.yaml"

log "deploying DocumentDB '$NAME' (io_method=$INITIAL_METHOD)..."
if [ -n "${STORAGE_CLASS:-}" ]; then
  log "  using storageClass=$STORAGE_CLASS"
  sed "s|# storageClass: managed-csi-premium.*|storageClass: ${STORAGE_CLASS}|" "$MANIFEST" | kubectl apply -f -
else
  kubectl apply -f "$MANIFEST"
fi

# Give the operator a moment to create the CNPG Cluster, then wait for health.
sleep 15
wait_cluster_ready 900

GOT="$(current_io_method)"
if [ "$GOT" = "$INITIAL_METHOD" ]; then
  log "verified: io_method=$GOT is active in PostgreSQL."
else
  warn "expected io_method=$INITIAL_METHOD but postgres reports '$GOT'."
fi
log "postgres version: $(pgq 'SELECT version();' | head -1)"

log "deploying benchmark runner..."
kubectl apply -f "$PG_DIR/manifests/benchmark-runner.yaml"
log "waiting for runner to clone micro-benchmarks + install deps (this can take a few minutes)..."
kubectl rollout status deploy/bench-runner -n "$NS" --timeout=600s
kubectl wait --for=condition=Ready pod -l app=bench-runner -n "$NS" --timeout=600s

log "DocumentDB + runner ready. Next: ./40-load-data.sh"
