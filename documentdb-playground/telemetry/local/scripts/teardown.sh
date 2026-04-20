#!/bin/bash
set -euo pipefail
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-telemetry}"

echo "=== Tearing down DocumentDB Telemetry Playground ==="

# Delete Kind cluster
echo "Deleting Kind cluster '$CLUSTER_NAME'..."
kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true

echo "Done. Registry container kept for reuse."
