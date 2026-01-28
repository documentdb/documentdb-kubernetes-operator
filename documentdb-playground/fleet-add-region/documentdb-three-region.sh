#!/bin/bash
set -euo pipefail

# Environment variables:
#   RESOURCE_GROUP: Azure resource group (default: documentdb-aks-fleet-rg)
#   DOCUMENTDB_PASSWORD: Database password (will be generated if not provided)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
EXCLUDE_REGION="${EXCLUDE_REGION:-westus2}"

# Set password from argument or environment variable
DOCUMENTDB_PASSWORD="${1:-${DOCUMENTDB_PASSWORD:-}}"

# If no password provided, generate a secure one
if [ -z "$DOCUMENTDB_PASSWORD" ]; then
  echo "No password provided. Generating a secure password..."
  DOCUMENTDB_PASSWORD=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)
  echo "Generated password: $DOCUMENTDB_PASSWORD"
  echo "(Save this password - you'll need it to connect to the database)"
  echo ""
fi

# Export for envsubst
export DOCUMENTDB_PASSWORD

# Dynamically get member clusters from Azure
echo "Discovering member clusters in resource group: $RESOURCE_GROUP..."
MEMBER_CLUSTERS=$(az aks list -g "$RESOURCE_GROUP" -o json | jq -r '.[] | select(.name|startswith("member-")) | .name' | sort)

if [ -z "$MEMBER_CLUSTERS" ]; then
  echo "Error: No member clusters found in resource group $RESOURCE_GROUP"
  echo "Please ensure the fleet is deployed first"
  exit 1
fi

# Convert to array
CLUSTER_ARRAY=($MEMBER_CLUSTERS)
echo "Found ${#CLUSTER_ARRAY[@]} member clusters:"
for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo "  - $cluster"
  if [[ "$cluster" == *"$HUB_REGION"* ]]; then HUB_CLUSTER="$cluster"; fi
done

# Select primary cluster and excluded cluster
PRIMARY_CLUSTER="${CLUSTER_ARRAY[0]}"
EXCLUDE_CLUSTER=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [[ "$cluster" == *"eastus2"* ]]; then
    PRIMARY_CLUSTER="$cluster"
  fi
  if [[ "$cluster" == *"$EXCLUDE_REGION"* ]]; then
    EXCLUDE_CLUSTER="$cluster"
  fi
done

echo ""
echo "Selected primary cluster: $PRIMARY_CLUSTER"

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

# Step 1: Create cluster identification ConfigMaps on each member cluster
echo ""
echo "======================================="
echo "Creating cluster identification ConfigMaps..."
echo "======================================="

for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo ""
  echo "Processing ConfigMap for $cluster..."
  
  # Check if context exists
  if ! kubectl config get-contexts "$cluster" &>/dev/null; then
    echo "✗ Context $cluster not found, skipping"
    continue
  fi
  
  # Create or update the cluster-name ConfigMap
  kubectl --context "$cluster" create configmap cluster-name \
    -n kube-system \
    --from-literal=name="$cluster" \
    --dry-run=client -o yaml | kubectl --context "$cluster" apply -f -
  
  # Verify the ConfigMap was created
  if kubectl --context "$cluster" get configmap cluster-name -n kube-system &>/dev/null; then
    echo "✓ ConfigMap created/updated for $cluster"
  else
    echo "✗ Failed to create ConfigMap for $cluster"
  fi
done

# Step 2: Deploy DocumentDB resources via Fleet
echo ""
echo "======================================="
echo "Deploying DocumentDB multi-region configuration..."
echo "======================================="

# Determine hub context
HUB_CLUSTER="${HUB_CLUSTER:-hub}"
if ! kubectl config get-contexts "$HUB_CLUSTER" &>/dev/null; then
  echo "Error: Hub context not found. Please ensure you have credentials for the fleet."
  exit 1
fi

echo "Using hub context: $HUB_CLUSTER"

# Create a temporary file with substituted values
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

# Debug: show the generated YAML section with clusterReplication
echo ""
echo "Generated configuration preview:"
echo "--------------------------------"
echo "Primary cluster: $PRIMARY_CLUSTER"
echo "Cluster list:"
echo "$CLUSTER_LIST"
echo "--------------------------------"

# Apply the configuration
echo ""
echo "Applying DocumentDB multi-region configuration..."
kubectl --context "$HUB_CLUSTER" apply -f "$TEMP_YAML"

# Clean up temp file
rm -f "$TEMP_YAML"

echo ""
echo "Connection Information:"
echo "  Username: default_user"
echo "  Password: $DOCUMENTDB_PASSWORD"
echo ""
