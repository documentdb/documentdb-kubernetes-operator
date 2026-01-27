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
    echo "Excluding cluster $cluster from DocumentDB configuration"
    continue
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
kubectl --context "$HUB_CLUSTER" apply -f "$TEMP_YAML"