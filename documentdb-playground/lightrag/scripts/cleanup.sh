#!/usr/bin/env bash
# Remove LightRAG and Ollama from the cluster.
set -euo pipefail

NAMESPACE="${LIGHTRAG_NAMESPACE:-lightrag}"

echo "=== Cleaning up LightRAG deployment ==="

echo "Uninstalling LightRAG Helm release..."
helm uninstall lightrag -n "$NAMESPACE" 2>/dev/null || true

echo "Deleting PVCs..."
kubectl delete pvc -l app.kubernetes.io/name=lightrag -n "$NAMESPACE" 2>/dev/null || true

echo "Deleting Ollama..."
kubectl delete deployment ollama -n "$NAMESPACE" 2>/dev/null || true
kubectl delete service ollama -n "$NAMESPACE" 2>/dev/null || true
# ollama-models PVC is not labeled app.kubernetes.io/name=lightrag, so the
# label-selector delete above skips it. Delete it explicitly so a Ctrl-C
# before the namespace delete does not orphan the PV.
kubectl delete pvc ollama-models -n "$NAMESPACE" 2>/dev/null || true

echo "Deleting namespace..."
kubectl delete namespace "$NAMESPACE" 2>/dev/null || true

echo "Cleanup complete."
