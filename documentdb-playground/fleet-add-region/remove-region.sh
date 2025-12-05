#/bin/bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
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
done

kubectl --context $HUB_CLUSTER patch documentdb documentdb-preview -n documentdb-preview-ns \
  --type='json' -p='[
  {"op": "remove", "path": "/spec/clusterReplication/clusterList/2"},
  ]'

kubectl --context $HUB_CLUSTER patch resourceplacement documentdb-resource-rp -n documentdb-preview-ns \
  --type='json' -p='[
  {"op": "add", "path": "/spec/policy/clusterNames/2"}
  ]'
