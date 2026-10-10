#!/usr/bin/env bash
# Remove the Mongoose demo app from the cluster.
set -euo pipefail

APP_NAMESPACE="${APP_NAMESPACE:-mongoose-demo}"

echo "=== Cleaning up Mongoose demo deployment ==="

echo "Deleting app resources..."
kubectl delete deployment mongoose-demo -n "$APP_NAMESPACE" 2>/dev/null || true
kubectl delete service mongoose-demo -n "$APP_NAMESPACE" 2>/dev/null || true
kubectl delete secret mongoose-app-secret -n "$APP_NAMESPACE" 2>/dev/null || true

echo "Deleting namespace..."
kubectl delete namespace "$APP_NAMESPACE" 2>/dev/null || true

echo "Cleanup complete."
echo ""
echo "Note: the DocumentDB instance (documentdb.yaml) is left running."
echo "Remove it with: kubectl delete -f documentdb.yaml"
