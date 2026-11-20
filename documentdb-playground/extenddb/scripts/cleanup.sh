#!/usr/bin/env bash
# Tear down the ExtendDB playground resources. Does not delete the
# DocumentDB instance itself -- run that separately if desired.
set -euo pipefail

command -v kubectl >/dev/null || { echo "kubectl is required" >&2; exit 1; }

EXTENDDB_NAMESPACE="${EXTENDDB_NAMESPACE:-extenddb}"

echo "=== Cleaning up ExtendDB playground (namespace: $EXTENDDB_NAMESPACE) ==="
kubectl delete namespace "$EXTENDDB_NAMESPACE" --ignore-not-found

echo ""
echo "✓ Removed namespace '$EXTENDDB_NAMESPACE'."
echo "  DocumentDB instance was left untouched (delete it separately if desired):"
echo "    kubectl delete namespace \${DOCUMENTDB_NAMESPACE:-documentdb-test}"
