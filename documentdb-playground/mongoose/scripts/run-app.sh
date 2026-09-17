#!/usr/bin/env bash
# Run the Mongoose demo app LOCALLY against a DocumentDB instance running in a
# cluster (local kind or remote AKS). This is the primary path: no image build,
# no in-cluster deployment, just a local Node.js process reaching DocumentDB
# through a temporary kubectl port-forward.
#
# Prerequisites: kubectl, node, npm, and a deployed DocumentDB instance that
# your current kubectl context can reach.
set -euo pipefail

command -v kubectl >/dev/null || { echo "kubectl is required" >&2; exit 1; }
command -v node >/dev/null || { echo "node is required" >&2; exit 1; }
command -v npm >/dev/null || { echo "npm is required" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

APP_DIR="$SCRIPT_DIR/../app"
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-test}"
DOCUMENTDB_CLUSTER="${DOCUMENTDB_CLUSTER:-documentdb-cluster}"
LOCAL_PORT="${LOCAL_PORT:-10260}"
PORT="${PORT:-3000}"

# Resolve the in-cluster connection string, then point it at the local
# port-forward endpoint.
IN_CLUSTER_URI=$(resolve_mongo_uri "$DOCUMENTDB_NAMESPACE" "$DOCUMENTDB_CLUSTER")
GATEWAY_PORT=$(gateway_port "$IN_CLUSTER_URI")
LOCAL_URI=$(to_local_uri "$IN_CLUSTER_URI" "$LOCAL_PORT")

echo "Starting port-forward to documentdb-service-${DOCUMENTDB_CLUSTER}:${GATEWAY_PORT} -> localhost:${LOCAL_PORT} ..."
PF_PID=$(start_port_forward "$DOCUMENTDB_NAMESPACE" "$DOCUMENTDB_CLUSTER" \
    "$LOCAL_PORT" "$GATEWAY_PORT" /tmp/mongoose-pf-app.log)
trap 'kill "$PF_PID" 2>/dev/null || true' EXIT

echo "Installing app dependencies (express, mongoose)..."
(cd "$APP_DIR" && npm install --omit=dev --no-audit --no-fund >/dev/null)

echo ""
echo "=== Mongoose demo app running locally ==="
echo "API:    http://localhost:${PORT}"
echo "Health: curl http://localhost:${PORT}/health"
echo "Create: curl -X POST http://localhost:${PORT}/books -H 'Content-Type: application/json' \\"
echo "          -d '{\"title\":\"Dune\",\"author\":\"Herbert\",\"genres\":[\"sci-fi\"],\"pages\":412}'"
echo "Press Ctrl-C to stop (port-forward is cleaned up automatically)."
echo ""

MONGO_URI="$LOCAL_URI" PORT="$PORT" node "$APP_DIR/server.js"