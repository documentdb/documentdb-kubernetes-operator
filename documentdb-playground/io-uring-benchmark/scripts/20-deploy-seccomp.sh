#!/usr/bin/env bash
# 20-deploy-seccomp.sh — relax seccomp on CNPG postgres pods so io_uring is allowed.
#
# The DocumentDB CR has no seccomp field and CNPG runs postgres with
# seccompProfile=RuntimeDefault, which modern containerd strips io_uring from. We use a
# Kyverno mutate policy to override seccomp on the CNPG-managed pods.
#
#   SECCOMP_MODE=unconfined (default)  -> Unconfined; no node profile needed (quick).
#   SECCOMP_MODE=localhost             -> Localhost profiles/postgres.json; installs the
#                                         curated io_uring profile on every node first.
#
# Run this BEFORE deploying the DocumentDB CR so the mutation applies at pod admission.
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl helm
PG_DIR="$(cd "$(dirname "$0")/.." && pwd)"

kubectl get ns "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"

# --- Kyverno ---
if kubectl get pods -n kyverno 2>/dev/null | grep -q Running; then
  log "Kyverno already running."
else
  log "installing Kyverno..."
  helm repo add kyverno https://kyverno.github.io/kyverno/ --force-update
  helm repo update
  helm install kyverno kyverno/kyverno -n kyverno --create-namespace --wait --timeout 5m
fi
# Kyverno admission controller must be Ready before its policies take effect.
kubectl wait --for=condition=Available deploy -n kyverno --all --timeout=300s 2>/dev/null || true

case "$SECCOMP_MODE" in
  localhost)
    log "installing the io_uring Localhost seccomp profile on all nodes (DaemonSet)..."
    kubectl apply -k "$PG_DIR/seccomp"
    kubectl rollout status ds/io-uring-seccomp-installer -n kube-system --timeout=180s 2>/dev/null || true
    log "applying Kyverno Localhost mutate policy..."
    kubectl apply -f "$PG_DIR/policy/kyverno-seccomp-localhost.yaml"
    kubectl delete -f "$PG_DIR/policy/kyverno-seccomp-unconfined.yaml" --ignore-not-found
    ;;
  unconfined)
    log "applying Kyverno Unconfined mutate policy..."
    kubectl apply -f "$PG_DIR/policy/kyverno-seccomp-unconfined.yaml"
    kubectl delete -f "$PG_DIR/policy/kyverno-seccomp-localhost.yaml" --ignore-not-found
    ;;
  *) die "unknown SECCOMP_MODE='$SECCOMP_MODE' (expected: unconfined | localhost)" ;;
esac

log "seccomp solution (${SECCOMP_MODE}) deployed."
warn "The policy mutates pods labeled cnpg.io/cluster in namespace '$NS' only."
