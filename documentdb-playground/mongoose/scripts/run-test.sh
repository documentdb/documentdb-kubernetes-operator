#!/usr/bin/env bash
# Run the Mongoose CRUD/compatibility test suite against the deployed
# DocumentDB instance. The test runs locally (Node.js) and reaches DocumentDB
# through a temporary kubectl port-forward.
#
# Prerequisites: kubectl, node, npm, a deployed DocumentDB instance.
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

# Resolve the in-cluster URI, then rewrite the host:port to the local
# port-forward endpoint. tlsAllowInvalidCertificates lets the self-signed cert
# pass even though the hostname is now localhost.
IN_CLUSTER_URI=$(resolve_mongo_uri "$DOCUMENTDB_NAMESPACE" "$DOCUMENTDB_CLUSTER")
GATEWAY_PORT=$(gateway_port "$IN_CLUSTER_URI")
LOCAL_URI=$(to_local_uri "$IN_CLUSTER_URI" "$LOCAL_PORT")

echo "Starting port-forward to documentdb-service-${DOCUMENTDB_CLUSTER}:${GATEWAY_PORT} -> localhost:${LOCAL_PORT} ..."
PF_PID=$(start_port_forward "$DOCUMENTDB_NAMESPACE" "$DOCUMENTDB_CLUSTER" \
    "$LOCAL_PORT" "$GATEWAY_PORT" /tmp/mongoose-pf-test.log)
trap 'kill "$PF_PID" 2>/dev/null || true' EXIT

echo "Installing test dependencies (mongoose)..."
(cd "$APP_DIR" && npm install --omit=dev --no-audit --no-fund >/dev/null)

echo ""
MONGO_URI="$LOCAL_URI" node "$APP_DIR/mongoose-crud-test.js"
