#!/usr/bin/env bash
# Deploy the Mongoose demo app against an existing DocumentDB instance.
#
# Prerequisites: kubectl, docker, a running cluster with the DocumentDB
# operator installed and a DocumentDB instance deployed (see documentdb.yaml).
# If your cluster is kind, the locally built image is loaded automatically.
set -euo pipefail

command -v kubectl >/dev/null || { echo "kubectl is required" >&2; exit 1; }
command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

APP_DIR="$SCRIPT_DIR/../app"
MANIFEST="$SCRIPT_DIR/../k8s/mongoose-app.yaml"

APP_NAMESPACE="${APP_NAMESPACE:-mongoose-demo}"
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-test}"
DOCUMENTDB_CLUSTER="${DOCUMENTDB_CLUSTER:-documentdb-cluster}"
IMAGE="${IMAGE:-documentdb/mongoose-demo:local}"
KIND_CLUSTER="${KIND_CLUSTER:-}"

echo "=== Mongoose + DocumentDB Deployment ==="

# 1. Build the demo image.
echo ""
echo "--- Step 1: Build app image ($IMAGE) ---"
docker build -t "$IMAGE" "$APP_DIR"

# 2. Load the image into kind if a kind cluster is detected/named.
if command -v kind >/dev/null 2>&1; then
    if [ -z "$KIND_CLUSTER" ]; then
        KIND_CLUSTER=$(kind get clusters 2>/dev/null | head -1 || true)
    fi
    if [ -n "$KIND_CLUSTER" ]; then
        echo ""
        echo "--- Step 2: Load image into kind cluster '$KIND_CLUSTER' ---"
        kind load docker-image "$IMAGE" --name "$KIND_CLUSTER"
    fi
fi

# 3. Resolve the DocumentDB connection string.
echo ""
echo "--- Step 3: Resolve DocumentDB connection ---"
MONGO_URI=$(resolve_mongo_uri "$DOCUMENTDB_NAMESPACE" "$DOCUMENTDB_CLUSTER")
echo "Connection string resolved from documentdb/$DOCUMENTDB_CLUSTER status."

# 4. Create namespace + secret.
echo ""
echo "--- Step 4: Create namespace and secret ---"
kubectl create namespace "$APP_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
kubectl create secret generic mongoose-app-secret \
    --namespace "$APP_NAMESPACE" \
    --from-literal=MONGO_URI="$MONGO_URI" \
    --dry-run=client -o yaml | kubectl apply -f -

# 5. Deploy the app (patch namespace + image).
echo ""
echo "--- Step 5: Deploy Mongoose demo app ---"
sed -e "s#documentdb/mongoose-demo:local#${IMAGE}#g" \
    -e "s#namespace: mongoose-demo#namespace: ${APP_NAMESPACE}#g" \
    "$MANIFEST" \
    | kubectl apply -f -

echo "Waiting for the app pod to become ready..."
kubectl rollout status deployment/mongoose-demo -n "$APP_NAMESPACE" --timeout=180s

echo ""
echo "=== Deployment complete ==="
echo ""
echo "Try the API:"
echo "  kubectl port-forward svc/mongoose-demo -n $APP_NAMESPACE 3000:3000"
echo "  curl http://localhost:3000/health"
echo "  curl -X POST http://localhost:3000/books -H 'Content-Type: application/json' \\"
echo "    -d '{\"title\":\"Dune\",\"author\":\"Herbert\",\"genres\":[\"sci-fi\"],\"pages\":412}'"
echo "  curl http://localhost:3000/books"
echo ""
echo "Run the Mongoose CRUD test suite against the cluster:"
echo "  ./scripts/run-test.sh"
