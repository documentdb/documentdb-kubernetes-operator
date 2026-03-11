#!/usr/bin/env bash
set -euo pipefail

# Test DocumentDB connectivity across all clusters

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load deployment info
if [ -f "$SCRIPT_DIR/.deployment-info" ]; then
  source "$SCRIPT_DIR/.deployment-info"
else
  echo "Error: Deployment info not found. Run deploy-infrastructure.sh first."
  exit 1
fi

echo "======================================="
echo "DocumentDB Connectivity Test"
echo "======================================="

# Get all clusters
HUB_CLUSTER_NAME="${HUB_CLUSTER_NAME:-hub-${HUB_REGION}}"
ALL_CLUSTERS="$HUB_CLUSTER_NAME"

IFS=' ' read -ra K3S_REGION_ARRAY <<< "${K3S_REGIONS:-}"
for region in "${K3S_REGION_ARRAY[@]}"; do
  if kubectl config get-contexts "k3s-$region" &>/dev/null; then
    ALL_CLUSTERS="$ALL_CLUSTERS k3s-$region"
  fi
done

CLUSTER_ARRAY=($ALL_CLUSTERS)

echo "Testing ${#CLUSTER_ARRAY[@]} clusters..."
echo ""

# Test each cluster
PASSED=0
FAILED=0

for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo "======================================="
  echo "Testing: $cluster"
  echo "======================================="
  
  if ! kubectl config get-contexts "$cluster" &>/dev/null; then
    echo "  ✗ Context not found"
    ((FAILED++))
    continue
  fi
  
  # Check namespace
  echo -n "  Namespace: "
  if kubectl --context "$cluster" get namespace documentdb-preview-ns &>/dev/null; then
    echo "✓"
  else
    echo "✗ Not found"
    ((FAILED++))
    continue
  fi
  
  # Check DocumentDB resource
  echo -n "  DocumentDB resource: "
  if kubectl --context "$cluster" get documentdb documentdb-preview -n documentdb-preview-ns &>/dev/null; then
    STATUS=$(kubectl --context "$cluster" get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
    echo "✓ (Status: $STATUS)"
  else
    echo "✗ Not found"
    ((FAILED++))
    continue
  fi
  
  # Check pods
  echo -n "  Pods: "
  PODS=$(kubectl --context "$cluster" get pods -n documentdb-preview-ns --no-headers 2>/dev/null | wc -l | tr -d ' ')
  READY_PODS=$(kubectl --context "$cluster" get pods -n documentdb-preview-ns --no-headers 2>/dev/null | grep -c "Running" || echo "0")
  echo "$READY_PODS/$PODS running"
  
  # Check service (try common naming patterns)
  echo -n "  Service: "
  SVC_NAME=""
  for name in "documentdb-preview" "documentdb-service-documentdb-preview"; do
    if kubectl --context "$cluster" get svc "$name" -n documentdb-preview-ns &>/dev/null; then
      SVC_NAME="$name"
      break
    fi
  done
  if [ -n "$SVC_NAME" ]; then
    SVC_TYPE=$(kubectl --context "$cluster" get svc "$SVC_NAME" -n documentdb-preview-ns -o jsonpath='{.spec.type}')
    if [ "$SVC_TYPE" = "LoadBalancer" ]; then
      EXTERNAL_IP=$(kubectl --context "$cluster" get svc "$SVC_NAME" -n documentdb-preview-ns -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
      if [ -n "$EXTERNAL_IP" ] && [ "$EXTERNAL_IP" != "<pending>" ]; then
        echo "✓ ($SVC_TYPE: $EXTERNAL_IP)"
      else
        echo "✓ ($SVC_TYPE: IP pending)"
      fi
    else
      echo "✓ ($SVC_TYPE)"
    fi
  else
    echo "✗ Not found"
    ((FAILED++))
  fi
  
  # Check secret
  echo -n "  Credentials secret: "
  if kubectl --context "$cluster" get secret documentdb-credentials -n documentdb-preview-ns &>/dev/null; then
    echo "✓"
  else
    echo "✗ Not found"
    ((FAILED++))
  fi
  
  # Check operator
  echo -n "  Operator: "
  OPERATOR_READY=$(kubectl --context "$cluster" get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
  OPERATOR_DESIRED=$(kubectl --context "$cluster" get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
  if [ "$OPERATOR_READY" = "$OPERATOR_DESIRED" ] && [ "$OPERATOR_READY" != "0" ]; then
    echo "✓ ($OPERATOR_READY/$OPERATOR_DESIRED)"
    ((PASSED++))
  else
    echo "✗ ($OPERATOR_READY/$OPERATOR_DESIRED)"
    ((FAILED++))
  fi
  
  echo ""
done

# Summary
echo "======================================="
echo "Summary"
echo "======================================="
echo "Total clusters: ${#CLUSTER_ARRAY[@]}"
echo "Passed: $PASSED"
echo "Failed: $FAILED"
echo ""

if [ $FAILED -eq 0 ]; then
  echo "✅ All tests passed!"
  exit 0
else
  echo "⚠️  Some tests failed. Check the output above."
  exit 1
fi
