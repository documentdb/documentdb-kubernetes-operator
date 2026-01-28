#!/bin/bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
PRIMARY_REGION="${PRIMARY_REGION:-eastus2}"
EXCLUDE_REGION="${EXCLUDE_REGION:-westus2}"

# Dynamically get member clusters from Azure
echo "Discovering member clusters in resource group: $RESOURCE_GROUP..."
MEMBER_CLUSTERS=$(az aks list -g "$RESOURCE_GROUP" -o json | jq -r '.[] | select(.name|startswith("member-")) | .name' | sort)

if [ -z "$MEMBER_CLUSTERS" ]; then
  echo "Error: No member clusters found in resource group $RESOURCE_GROUP"
  echo "Please ensure the fleet is deployed first"
  exit 1
fi

CLUSTER_ARRAY=($MEMBER_CLUSTERS)
echo "Found ${#CLUSTER_ARRAY[@]} member clusters:"
EXCLUDE_CLUSTER=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo "  - $cluster"
  if [[ "$cluster" == *"$HUB_REGION"* ]]; then HUB_CLUSTER="$cluster"; fi
  if [[ "$cluster" == *"$EXCLUDE_REGION"* ]]; then EXCLUDE_CLUSTER="$cluster"; fi
  if [[ "$cluster" == *"$PRIMARY_REGION"* ]]; then PRIMARY_CLUSTER="$cluster"; fi
done

# Build the cluster list YAML with proper indentation
CLUSTER_LIST=""
CLUSTER_LIST_CRP=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [ "$cluster" == "$EXCLUDE_CLUSTER" ]; then
    echo "Including cluster $cluster in DocumentDB configuration"
  fi
  if [ -z "$CLUSTER_LIST" ]; then
    CLUSTER_LIST="      - name: ${cluster}"
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"        environment: aks"
    CLUSTER_LIST_CRP="      - ${cluster}"
  else
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"      - name: ${cluster}"
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"        environment: aks"
    CLUSTER_LIST_CRP="${CLUSTER_LIST_CRP}"$'\n'"      - ${cluster}"
  fi
done

TEMP_YAML=$(mktemp)

# Use sed for safer substitution
sed -e "s/{{DOCUMENTDB_PASSWORD}}/$DOCUMENTDB_PASSWORD/g" \
    -e "s/{{PRIMARY_CLUSTER}}/$PRIMARY_CLUSTER/g" \
    "$SCRIPT_DIR/documentdb-resource-crp.yaml" | \
while IFS= read -r line; do
  if [[ "$line" == '{{CLUSTER_LIST}}' ]]; then
    echo "$CLUSTER_LIST"
  elif [[ "$line" == '{{CLUSTER_LIST_CRP}}' ]]; then
    echo "$CLUSTER_LIST_CRP"
  else
    echo "$line"
  fi
done > "$TEMP_YAML"

echo ""
echo "Applying DocumentDB multi-region configuration..."

MAX_RETRIES=60
RETRY_INTERVAL=3
RETRY_COUNT=0

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
  kubectl --context "$HUB_CLUSTER" apply -f "$TEMP_YAML" &> /dev/null
  
  echo "Checking if $EXCLUDE_CLUSTER has been added to clusterReplication on the excluded cluster..."
  
  # Get the clusterReplication.clusters field from the DocumentDB object on the excluded cluster
  CLUSTER_LIST_JSON=$(kubectl --context "$EXCLUDE_CLUSTER" get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.clusterList[*].name}' 2>/dev/null)
  
  if echo "$CLUSTER_LIST_JSON" | grep -q "$EXCLUDE_CLUSTER"; then
    echo "Success: $EXCLUDE_CLUSTER is now included in clusterReplication field"
    break
  fi
  
  RETRY_COUNT=$((RETRY_COUNT + 1))
  echo "Cluster not yet in clusterReplication (attempt $RETRY_COUNT/$MAX_RETRIES). Retrying in ${RETRY_INTERVAL}s..."
  sleep $RETRY_INTERVAL
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
  echo "Error: Timed out waiting for $EXCLUDE_CLUSTER to appear in clusterReplication"
  exit 1
fi

rm -f "$TEMP_YAML"
echo "Done."