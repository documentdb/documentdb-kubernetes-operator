#!/usr/bin/env bash
# 99-teardown.sh — remove the benchmark. By default only the iouring-bench namespace.
#
# Flags:
#   --all          also remove Kyverno policies + seccomp DaemonSet
#   --operator     also uninstall the DocumentDB operator + Kyverno
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"
require kubectl
PG_DIR="$(cd "$(dirname "$0")/.." && pwd)"

ALL=false; OPERATOR=false
for a in "$@"; do
  case "$a" in
    --all) ALL=true ;;
    --operator) ALL=true; OPERATOR=true ;;
    *) die "unknown flag: $a" ;;
  esac
done

log "deleting DocumentDB '$NAME' and namespace '$NS'..."
kubectl delete documentdb "$NAME" -n "$NS" --ignore-not-found --wait=false 2>/dev/null || true
kubectl delete namespace "$NS" --ignore-not-found

if $ALL; then
  log "removing Kyverno policies + seccomp profile installer..."
  kubectl delete -f "$PG_DIR/policy/kyverno-seccomp-unconfined.yaml" --ignore-not-found
  kubectl delete -f "$PG_DIR/policy/kyverno-seccomp-localhost.yaml" --ignore-not-found
  kubectl delete -k "$PG_DIR/seccomp" --ignore-not-found 2>/dev/null || true
fi

if $OPERATOR; then
  log "uninstalling Kyverno + DocumentDB operator..."
  helm uninstall kyverno -n kyverno 2>/dev/null || true
  helm uninstall documentdb-operator -n "${OPERATOR_NS:-documentdb-operator}" 2>/dev/null || true
fi

log "teardown complete."
