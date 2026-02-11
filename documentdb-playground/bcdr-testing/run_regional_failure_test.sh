#!/bin/bash
set -euo pipefail

# Usage:
#   RESOURCE_GROUP=... ./run_ha_failure_test.sh

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-bcdr-test-rg}"
DOCUMENTDB_NAME="${DOCUMENTDB_NAME:-documentdb-preview}"
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-preview-ns}"
SERVICE_NAME="${SERVICE_NAME:-documentdb-service-documentdb-preview}"
CHAOS_NAMESPACE="${CHAOS_NAMESPACE:-chaos-mesh}"
TOTAL_DURATION_SECONDS="${TOTAL_DURATION_SECONDS:-360}"
CHAOS_DELAY_SECONDS="${CHAOS_DELAY_SECONDS:-30}"
PYTHON_BIN="${PYTHON_BIN:-python3}"
PRIMARY_CONTEXT="${PRIMARY_CONTEXT:-}"
USE_DNS_ENDPOINTS="${USE_DNS_ENDPOINTS:-false}"
DNS_ZONE_FQDN="${DNS_ZONE_FQDN:-}"
HUB_REGION="${HUB_REGION:-westus3}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHAOS_FILE="$SCRIPT_DIR/regional-failure.yaml"

fail() {
  echo "Error: $1" >&2
  exit 1
}

get_service_endpoint() {
  local context="$1"
  local ip
  local host
  ip=$(kubectl --context "$context" get svc "$SERVICE_NAME" -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
  if [[ -z "$ip" ]]; then
    host=$(kubectl --context "$context" get svc "$SERVICE_NAME" -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
    if [[ -n "$host" ]]; then
      echo "$host"
      return 0
    fi
  else
    echo "$ip"
    return 0
  fi

  return 1
}

if ! command -v az >/dev/null 2>&1; then
  fail "az is required"
fi
if ! command -v jq >/dev/null 2>&1; then
  fail "jq is required"
fi
if ! command -v kubectl >/dev/null 2>&1; then
  fail "kubectl is required"
fi

MEMBER_CLUSTERS=$(az aks list -g "$RESOURCE_GROUP" -o json | jq -r '.[] | select(.name|startswith("member-")) | .name' | sort)
if [[ -z "$MEMBER_CLUSTERS" ]]; then
  fail "No member-* clusters found in resource group $RESOURCE_GROUP"
fi
CLUSTER_ARRAY=($MEMBER_CLUSTERS)

primary_context="$PRIMARY_CONTEXT"
if [[ -z "$primary_context" ]]; then
  if kubectl --context "${CLUSTER_ARRAY[0]}" get documentdb "$DOCUMENTDB_NAME" -n "$DOCUMENTDB_NAMESPACE" >/dev/null 2>&1; then
    primary_context=$(kubectl --context "${CLUSTER_ARRAY[0]}" get documentdb "$DOCUMENTDB_NAME" -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.spec.clusterReplication.primary}')
  fi
fi

read_clusters=()
hub_context=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [[ "$cluster" != "$primary_context" ]]; then
    read_clusters+=("$cluster")
  fi
  if [[ "$cluster" == *"$HUB_REGION"* ]]; then
    hub_context="$cluster"
  fi
done

if [[ ${#read_clusters[@]} -lt 2 ]]; then
  fail "Need at least two non-primary clusters for read endpoints"
fi

use_srv=false
if [[ "$USE_DNS_ENDPOINTS" == "true" ]]; then
  if [[ -z "$DNS_ZONE_FQDN" ]]; then
    fail "DNS_ZONE_FQDN must be set when USE_DNS_ENDPOINTS is true"
  fi
  insert_endpoint="${DNS_ZONE_FQDN}"
  read_endpoint_1="${read_clusters[0]}.${DNS_ZONE_FQDN}"
  read_endpoint_2="${read_clusters[1]}.${DNS_ZONE_FQDN}"
  use_srv=true
else
  insert_endpoint=$(get_service_endpoint "$primary_context") || fail "Failed to resolve insert endpoint on $primary_context"
  read_endpoint_1=$(get_service_endpoint "${read_clusters[0]}") || fail "Failed to resolve read endpoint on ${read_clusters[0]}"
  read_endpoint_2=$(get_service_endpoint "${read_clusters[1]}") || fail "Failed to resolve read endpoint on ${read_clusters[1]}"
fi

username=$(kubectl --context "$primary_context" get secret documentdb-credentials -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.data.username}' | base64 -d 2>/dev/null || true)
password=$(kubectl --context "$primary_context" get secret documentdb-credentials -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.data.password}' | base64 -d 2>/dev/null || true)
if [[ -z "$username" ]]; then
  echo "Warning: username not found in secret, defaulting to default_user" >&2
  username="default_user"
fi
if [[ -z "$password" ]]; then
  fail "Password not found in secret documentdb-credentials"
fi

# Create plugin and add to path
pushd "$SCRIPT_DIR/../../operator/src" 
make build-kubectl-plugin
export PATH="$(pwd)/bin:$PATH"
popd

echo "Primary context: $primary_context"
echo "Read contexts: ${read_clusters[0]}, ${read_clusters[1]}"
if [[ "$USE_DNS_ENDPOINTS" == "true" ]]; then
  echo "DNS zone: $DNS_ZONE_FQDN"
fi
echo "Insert endpoint: $insert_endpoint"
echo "Read endpoint 1: $read_endpoint_1"
echo "Read endpoint 2: $read_endpoint_2"
echo "Total duration (s): $TOTAL_DURATION_SECONDS"
echo "Chaos delay (s): $CHAOS_DELAY_SECONDS"

echo "Starting workload..."
"$PYTHON_BIN" "$SCRIPT_DIR/failure_insert_test.py" \
  "$insert_endpoint" "$read_endpoint_1" "$read_endpoint_2" \
  "$username" "$password" \
  --duration-seconds "$TOTAL_DURATION_SECONDS" \
  --use-srv $use_srv &
python_pid=$!

sleep "$CHAOS_DELAY_SECONDS"

echo "Applying chaos..."
kubectl --context "$primary_context" apply -n "$CHAOS_NAMESPACE" -f "$CHAOS_FILE"

# Perform manual failover
kubectl documentdb promote \
  --documentdb documentdb-preview \
  --namespace documentdb-preview-ns \
  --hub-context $hub_context \
  --target-cluster ${read_clusters[0]} \
  --cluster-context ${read_clusters[0]} \
  --skip-wait \
  --failover

if [[ "$USE_DNS_ENDPOINTS" == "true" ]]; then
  # Repoint srv url
  az network dns record-set srv remove-record \
    --record-set-name "_mongodb._tcp" \
    --zone-name "$DNS_ZONE_FQDN" \
    --resource-group "$RESOURCE_GROUP" \
    --priority 0 \
    --weight 0 \
    --port 10260 \
    --target "$primary_context.$DNS_ZONE_FQDN" \
    --keep-empty-record-set > /dev/null

#create new one
  az network dns record-set srv add-record \
    --record-set-name "_mongodb._tcp" \
    --zone-name "$DNS_ZONE_FQDN" \
    --resource-group "$RESOURCE_GROUP" \
    --priority 0 \
    --weight 0 \
    --port 10260 \
    --target "${read_clusters[0]}.$DNS_ZONE_FQDN" > /dev/null
fi

wait "$python_pid"

kubectl --context "$primary_context" delete -n "$CHAOS_NAMESPACE" -f "$CHAOS_FILE"
