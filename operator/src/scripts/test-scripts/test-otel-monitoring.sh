#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
#
# Tests OTel Collector sidecar injection: enables monitoring, verifies sidecar
# + ConfigMap + Prometheus metrics, then disables and verifies cleanup.
#
# Required environment variables:
#   DB_NS   - Kubernetes namespace of the DocumentDB cluster
#   DB_NAME - Name of the DocumentDB custom resource

set -euo pipefail

: "${DB_NS:?DB_NS must be set}"
: "${DB_NAME:?DB_NAME must be set}"

MAX_RETRIES=60
SLEEP_INTERVAL=5

# --- Step 1: Enable monitoring ---
echo "=== Step 1: Enable monitoring ==="
kubectl -n "$DB_NS" patch documentdb "$DB_NAME" --type=merge \
  -p '{"spec":{"monitoring":{"enabled":true,"exporter":{"prometheus":{"port":9090}}}}}'

# Wait for rolling restart to complete with 3 containers (postgres + gateway + otel-collector)
echo "Waiting for pod to have 3/3 containers..."
ITER=0
CONTAINER_COUNT=0
ALL_READY=0
while [ $ITER -lt $MAX_RETRIES ]; do
  READY=$(kubectl get pods -n "$DB_NS" -l cnpg.io/cluster="$DB_NAME" -o jsonpath='{.items[0].status.containerStatuses[*].ready}' 2>/dev/null || echo "")
  CONTAINER_COUNT=$(echo "$READY" | wc -w)
  ALL_READY=$(echo "$READY" | tr ' ' '\n' | grep -c "true" || echo "0")
  if [ "$CONTAINER_COUNT" -eq 3 ] && [ "$ALL_READY" -eq 3 ]; then
    echo "✓ Pod has 3/3 containers running"
    break
  fi
  echo "Containers: $ALL_READY/$CONTAINER_COUNT ready. Waiting..."
  sleep $SLEEP_INTERVAL
  ((++ITER))
done

if [ "$CONTAINER_COUNT" -ne 3 ] || [ "$ALL_READY" -ne 3 ]; then
  echo "❌ Pod did not reach 3/3 containers within expected time"
  kubectl get pods -n "$DB_NS" -o wide
  kubectl describe pods -n "$DB_NS" -l cnpg.io/cluster="$DB_NAME"
  exit 1
fi

# Verify OTel collector container exists
OTEL_CONTAINER=$(kubectl get pods -n "$DB_NS" -l cnpg.io/cluster="$DB_NAME" \
  -o jsonpath='{.items[0].spec.containers[?(@.name=="otel-collector")].name}')
if [ "$OTEL_CONTAINER" == "otel-collector" ]; then
  echo "✓ OTel Collector sidecar container is present"
else
  echo "❌ OTel Collector sidecar container not found"
  exit 1
fi

# Verify OTel ConfigMap exists
CM_NAME="${DB_NAME}-otel-config"
if kubectl get configmap "$CM_NAME" -n "$DB_NS" &>/dev/null; then
  echo "✓ OTel ConfigMap '$CM_NAME' exists"
else
  echo "❌ OTel ConfigMap '$CM_NAME' not found"
  exit 1
fi

# Verify Prometheus metrics endpoint responds with documentdb metric
POD_NAME=$(kubectl get pods -n "$DB_NS" -l cnpg.io/cluster="$DB_NAME" -o jsonpath='{.items[0].metadata.name}')
echo "Checking Prometheus metrics on pod $POD_NAME port 9090..."

# Port-forward to the OTel Collector's Prometheus exporter and curl from the runner
kubectl port-forward -n "$DB_NS" pod/"$POD_NAME" 19090:9090 &
PF_PID=$!
sleep 3

METRICS_FOUND=false
for attempt in $(seq 1 12); do
  METRICS=$(curl -sf http://localhost:19090/metrics 2>/dev/null || echo "")
  if echo "$METRICS" | grep -q "documentdb_postgres_up"; then
    echo "✓ Prometheus metrics endpoint returns documentdb_postgres_up metric"
    METRICS_FOUND=true
    break
  fi
  echo "Attempt $attempt: metrics not ready yet, retrying in 5s..."
  sleep 5
done

kill $PF_PID 2>/dev/null || true

if [ "$METRICS_FOUND" != "true" ]; then
  echo "❌ documentdb_postgres_up metric not found in Prometheus output"
  echo "Metrics output (first 20 lines):"
  echo "$METRICS" | head -20
  # Show OTel Collector logs for debugging
  echo "OTel Collector logs:"
  kubectl logs -n "$DB_NS" "$POD_NAME" -c otel-collector --tail=30 || true
  exit 1
fi

# --- Step 2: Disable monitoring ---
echo ""
echo "=== Step 2: Disable monitoring ==="
kubectl -n "$DB_NS" patch documentdb "$DB_NAME" --type=merge \
  -p '{"spec":{"monitoring":{"enabled":false}}}'

# Wait for rolling restart to complete with 2 containers (postgres + gateway only)
echo "Waiting for pod to have 2/2 containers (sidecar removed)..."
ITER=0
while [ $ITER -lt $MAX_RETRIES ]; do
  READY=$(kubectl get pods -n "$DB_NS" -l cnpg.io/cluster="$DB_NAME" -o jsonpath='{.items[0].status.containerStatuses[*].ready}' 2>/dev/null || echo "")
  CONTAINER_COUNT=$(echo "$READY" | wc -w)
  ALL_READY=$(echo "$READY" | tr ' ' '\n' | grep -c "true" || echo "0")
  if [ "$CONTAINER_COUNT" -eq 2 ] && [ "$ALL_READY" -eq 2 ]; then
    echo "✓ Pod has 2/2 containers running (sidecar removed)"
    break
  fi
  echo "Containers: $ALL_READY/$CONTAINER_COUNT ready. Waiting..."
  sleep $SLEEP_INTERVAL
  ((++ITER))
done

if [ "$CONTAINER_COUNT" -ne 2 ] || [ "$ALL_READY" -ne 2 ]; then
  echo "❌ Pod did not return to 2/2 containers within expected time"
  kubectl get pods -n "$DB_NS" -o wide
  kubectl describe pods -n "$DB_NS" -l cnpg.io/cluster="$DB_NAME"
  exit 1
fi

# Verify OTel ConfigMap was deleted
if kubectl get configmap "$CM_NAME" -n "$DB_NS" &>/dev/null; then
  echo "❌ OTel ConfigMap '$CM_NAME' should have been deleted"
  exit 1
else
  echo "✓ OTel ConfigMap '$CM_NAME' was deleted"
fi

echo ""
echo "✅ OTel monitoring sidecar enable/disable test passed"
