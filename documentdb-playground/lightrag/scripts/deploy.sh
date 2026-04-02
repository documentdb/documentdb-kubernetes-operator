#!/usr/bin/env bash
# Deploy LightRAG with DocumentDB backend on a Kubernetes cluster.
# Prerequisites: kubectl, helm, a running cluster with DocumentDB deployed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CHART_DIR="$SCRIPT_DIR/../helm/lightrag"
VALUES_FILE="$SCRIPT_DIR/../helm/lightrag-values.yaml"
OLLAMA_MANIFEST="$SCRIPT_DIR/../helm/ollama.yaml"
NAMESPACE="${LIGHTRAG_NAMESPACE:-lightrag}"
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-demo}"
DOCUMENTDB_CLUSTER="${DOCUMENTDB_CLUSTER:-my-cluster}"

echo "=== LightRAG + DocumentDB Deployment ==="

# 1. Create namespace and deploy Ollama
echo ""
echo "--- Step 1: Deploy Ollama ---"
kubectl apply -f "$OLLAMA_MANIFEST"
echo "Waiting for Ollama pod to be ready..."
kubectl wait --for=condition=Ready pod -l app=ollama -n "$NAMESPACE" --timeout=120s

# 2. Pull models
echo ""
echo "--- Step 2: Pull LLM and embedding models ---"
OLLAMA_POD=$(kubectl get pod -l app=ollama -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}')
echo "Pulling nomic-embed-text (embedding, ~274MB)..."
kubectl exec -n "$NAMESPACE" "$OLLAMA_POD" -- ollama pull nomic-embed-text
echo "Pulling qwen2.5:3b (LLM, ~1.9GB)..."
kubectl exec -n "$NAMESPACE" "$OLLAMA_POD" -- ollama pull qwen2.5:3b

# 3. Get DocumentDB connection string from resource status
echo ""
echo "--- Step 3: DocumentDB connection ---"
RAW_CONN=$(kubectl get documentdb "$DOCUMENTDB_CLUSTER" -n "$DOCUMENTDB_NAMESPACE" \
    -o jsonpath='{.status.connectionString}' 2>/dev/null) || true

if [ -n "$RAW_CONN" ]; then
    # The connection string contains embedded kubectl commands for credentials.
    # eval resolves them into a usable URI.
    MONGO_URI=$(eval echo "$RAW_CONN")
    echo "Connection string retrieved from DocumentDB status."
else
    echo "Could not read status.connectionString from DocumentDB resource."
    echo "Please set MONGO_URI in $VALUES_FILE manually."
    MONGO_URI=""
fi

# 4. Deploy LightRAG via Helm
echo ""
echo "--- Step 4: Deploy LightRAG ---"
helm upgrade --install lightrag "$CHART_DIR" \
    -n "$NAMESPACE" \
    -f "$VALUES_FILE" \
    --set "env.MONGO_URI=$MONGO_URI" \
    --wait --timeout 5m
echo "Waiting for LightRAG pod to be ready..."
kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=lightrag -n "$NAMESPACE" --timeout=300s

echo ""
echo "=== Deployment complete ==="
echo ""
echo "Access LightRAG:"
echo "  kubectl port-forward svc/lightrag -n $NAMESPACE 9621:9621"
echo "  open http://localhost:9621"
echo ""
echo "Insert a document:"
echo "  curl -X POST http://localhost:9621/documents/text \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"text\": \"Your text here\"}'"
echo ""
echo "Query:"
echo "  curl -X POST http://localhost:9621/query \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"query\": \"Your question\", \"mode\": \"hybrid\"}'"
