#!/usr/bin/env bash
# filepath: /operator/src/scripts/aks-fleet-deployment/deploy-multi-region.sh
set -euo pipefail

# Deploy multi-region DocumentDB using Fleet with Azure DNS
# Usage: ./deploy-multi-region.sh [password]
#
# Environment variables:
#   RESOURCE_GROUP: Azure resource group (default: documentdb-aks-fleet-rg)
#   DOCUMENTDB_PASSWORD: Database password (will be generated if not provided)
#   ENABLE_AZURE_DNS: Enable Azure DNS creation (default: true)
#   AZURE_DNS_ZONE_NAME: Azure DNS zone name (default: same as resource group)
#   AZURE_DNS_PARENT_ZONE_RESOURCE_ID: Azure DNS parent zone resource ID (default: multi-cloud.pgmongo-dev.cosmos.windows-int.net)
#
# Examples:
#   ./deploy-multi-region.sh
#   ENABLE_AZURE_DNS=false ./deploy-multi-region.sh mypassword

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Resource group
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"

# Azure DNS configuration
AZURE_DNS_ZONE_NAME="${AZURE_DNS_ZONE_NAME:-${RESOURCE_GROUP}}"
AZURE_DNS_PARENT_ZONE_RESOURCE_ID="${AZURE_DNS_PARENT_ZONE_RESOURCE_ID:-/subscriptions/81901d5e-31aa-46c5-b61a-537dbd5df1e7/resourceGroups/alaye-documentdb-dns/providers/Microsoft.Network/dnszones/multi-cloud.pgmongo-dev.cosmos.windows-int.net}"
ENABLE_AZURE_DNS="${ENABLE_AZURE_DNS:-true}"

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
  echo "Please ensure the fleet is deployed first using ./deploy-fleet-bicep.sh"
  exit 1
fi

# Convert to array
CLUSTER_ARRAY=($MEMBER_CLUSTERS)
echo "Found ${#CLUSTER_ARRAY[@]} member clusters:"
for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo "  - $cluster"
done

# Select primary cluster (prefer eastus2, or use first cluster)
PRIMARY_CLUSTER=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [[ "$cluster" == *"eastus2"* ]]; then
    PRIMARY_CLUSTER="$cluster"
    break
  fi
done

# If no eastus2 cluster found, use the first one
if [ -z "$PRIMARY_CLUSTER" ]; then
  PRIMARY_CLUSTER="${CLUSTER_ARRAY[0]}"
fi

echo ""
echo "Selected primary cluster: $PRIMARY_CLUSTER"

# Build the cluster list YAML with proper indentation
CLUSTER_LIST=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [ -z "$CLUSTER_LIST" ]; then
    CLUSTER_LIST="      - name: ${cluster}"
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"        environment: aks"
  else
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"      - name: ${cluster}"
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"        environment: aks"
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
  
  # Extract region from cluster name (member-<region>-<suffix>)
  REGION=$(echo "$cluster" | awk -F- '{print $2}')
  
  # Create or update the cluster-name ConfigMap
  kubectl --context "$cluster" create configmap cluster-name \
    -n kube-system \
    --from-literal=name="$cluster" \
    --from-literal=region="$REGION" \
    --dry-run=client -o yaml | kubectl --context "$cluster" apply -f -
  
  # Verify the ConfigMap was created
  if kubectl --context "$cluster" get configmap cluster-name -n kube-system &>/dev/null; then
    echo "✓ ConfigMap created/updated for $cluster (region: $REGION)"
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
HUB_CONTEXT="${HUB_CONTEXT:-hub}"
if ! kubectl config get-contexts "$HUB_CONTEXT" &>/dev/null; then
  echo "Error: Hub context not found. Please ensure you have credentials for the fleet."
  exit 1
fi

echo "Using hub context: $HUB_CONTEXT"

# Check if resources already exist
EXISTING_RESOURCES=""
if kubectl --context "$HUB_CONTEXT" get namespace documentdb-preview-ns; then
  EXISTING_RESOURCES="${EXISTING_RESOURCES}namespace "
fi
if kubectl --context "$HUB_CONTEXT" get secret documentdb-credentials -n documentdb-preview-ns &>/dev/null 2>&1; then
  EXISTING_RESOURCES="${EXISTING_RESOURCES}secret "
fi
if kubectl --context "$HUB_CONTEXT" get documentdb documentdb-preview -n documentdb-preview-ns &>/dev/null 2>&1; then
  EXISTING_RESOURCES="${EXISTING_RESOURCES}documentdb "
fi
if kubectl --context "$HUB_CONTEXT" get clusterresourceplacement documentdb-crp &>/dev/null 2>&1; then
  EXISTING_RESOURCES="${EXISTING_RESOURCES}clusterresourceplacement "
fi

if [ -n "$EXISTING_RESOURCES" ]; then
  echo ""
  echo "⚠️  Warning: The following resources already exist: $EXISTING_RESOURCES"
  echo ""
  echo "Options:"
  echo "1. Delete existing resources and redeploy (data will be lost)"
  echo "2. Update existing deployment (preserve data)"
  echo "3. Cancel"
  echo ""
  read -p "Choose an option (1/2/3): " CHOICE
  
  case $CHOICE in
    1)
      echo "Deleting existing resources..."
      kubectl --context "$HUB_CONTEXT" delete clusterresourceplacement documentdb-crp --ignore-not-found=true
      kubectl --context "$HUB_CONTEXT" delete namespace documentdb-preview-ns --ignore-not-found=true
      echo "Waiting for namespace deletion to complete..."
      for cluster in "${CLUSTER_ARRAY[@]}"; do
        kubectl --context "$cluster" wait --for=delete namespace/documentdb-preview-ns --timeout=60s 2>/dev/null || true
      done
      ;;
    2)
      echo "Updating existing deployment..."
      ;;
    3)
      echo "Cancelled."
      exit 0
      ;;
    *)
      echo "Invalid choice. Cancelled."
      exit 1
      ;;
  esac
fi

# Create a temporary file with substituted values
TEMP_YAML=$(mktemp)

# Use sed for safer substitution
sed -e "s/{{DOCUMENTDB_PASSWORD}}/$DOCUMENTDB_PASSWORD/g" \
    -e "s/{{PRIMARY_CLUSTER}}/$PRIMARY_CLUSTER/g" \
    "$SCRIPT_DIR/multi-region.yaml" | \
while IFS= read -r line; do
  if [[ "$line" == '{{CLUSTER_LIST}}' ]]; then
    echo "$CLUSTER_LIST"
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

# cat "$TEMP_YAML" 

# Apply the configuration
echo ""
echo "Applying DocumentDB multi-region configuration..."
kubectl --context "$HUB_CONTEXT" apply -f "$TEMP_YAML"

# Clean up temp file
rm -f "$TEMP_YAML"

# Check the ClusterResourcePlacement status
echo ""
echo "Checking ClusterResourcePlacement status..."
kubectl --context "$HUB_CONTEXT" get clusterresourceplacement documentdb-crp -o wide

# Wait a bit for propagation
echo ""
echo "Waiting for resources to propagate to member clusters..."
sleep 10

# Step 3: Verify deployment on each member cluster
echo ""
echo "======================================="
echo "Checking deployment status on member clusters..."
echo "======================================="

for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo ""
  echo "=== $cluster ==="
  
  # Check if context exists
  if ! kubectl config get-contexts "$cluster" &>/dev/null; then
    echo "✗ Context not found, skipping"
    continue
  fi
  
  # Check ConfigMap
  if kubectl --context "$cluster" get configmap cluster-name -n kube-system &>/dev/null; then
    CLUSTER_ID=$(kubectl --context "$cluster" get configmap cluster-name -n kube-system -o jsonpath='{.data.name}')
    REGION=$(kubectl --context "$cluster" get configmap cluster-name -n kube-system -o jsonpath='{.data.region}')
    echo "✓ Cluster identified as: $CLUSTER_ID (region: $REGION)"
  else
    echo "✗ Cluster identification ConfigMap not found"
  fi
  
  # Check if namespace exists
  if kubectl --context "$cluster" get namespace documentdb-preview-ns &>/dev/null; then
    echo "✓ Namespace exists"
    
    # Check if secret exists
    if kubectl --context "$cluster" get secret documentdb-credentials -n documentdb-preview-ns &>/dev/null; then
      echo "✓ Secret exists"
    else
      echo "✗ Secret not found"
    fi
    
    # Check if DocumentDB exists
    if kubectl --context "$cluster" get documentdb documentdb-preview -n documentdb-preview-ns &>/dev/null; then
      echo "✓ DocumentDB resource exists"
      
      # Get DocumentDB status
      STATUS=$(kubectl --context "$cluster" get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
      echo "  Status: $STATUS"
      
      # Check if this is the primary or replica
      if [ "$cluster" = "$PRIMARY_CLUSTER" ]; then
        echo "  Role: PRIMARY"
      else
        echo "  Role: REPLICA"
      fi
    else
      echo "✗ DocumentDB resource not found"
    fi
    
    # Check pods
    PODS=$(kubectl --context "$cluster" get pods -n documentdb-preview-ns --no-headers 2>/dev/null | wc -l || echo "0")
    echo "  Pods: $PODS"
    
    # Show pod status if any exist
    if [ "$PODS" -gt 0 ]; then
      kubectl --context "$cluster" get pods -n documentdb-preview-ns 2>/dev/null | head -5
    fi
  else
    echo "✗ Namespace not found (resources may still be propagating)"
  fi
done

# Step 4: Create Azure DNS zone for DocumentDB
if [ "$ENABLE_AZURE_DNS" = "true" ]; then
  echo ""
  echo "======================================="
  echo "Creating Azure DNS zone for DocumentDB..."
  echo "======================================="
  
  parentName=$(az network dns zone show --id $AZURE_DNS_PARENT_ZONE_RESOURCE_ID | jq -r ".name")
  fullName="${AZURE_DNS_ZONE_NAME}.${parentName}"
  
  # Create Azure DNS zone
  if az network dns zone show --name "$fullName" --resource-group "$RESOURCE_GROUP" &>/dev/null; then
    echo "Azure DNS zone already exists, updating..."
  else
    az network dns zone create \
      --name "$fullName" \
      --resource-group "$RESOURCE_GROUP" \
      --parent-name "$AZURE_DNS_PARENT_ZONE_RESOURCE_ID"
  fi
  
  # Wait for DocumentDB services to be ready and create endpoints
  echo ""
  echo "Waiting for DocumentDB services to be ready..."
  sleep 30

  # Create DNS records for each cluster
  for cluster in "${CLUSTER_ARRAY[@]}"; do
    echo "Creating DNS record: $cluster"

    # Create service name by concatenating documentdb-preview with cluster name (max 63 chars)
    SERVICE_NAME="documentdb-service-documentdb-preview"
    
    # Get the external IP of the DocumentDB service
    EXTERNAL_IP=""
    for attempt in {1..12}; do  # Try for 2 minutes
      EXTERNAL_IP=$(kubectl --context "$cluster" get svc "$SERVICE_NAME" -n documentdb-preview-ns -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
      if [ -n "$EXTERNAL_IP" ] && [ "$EXTERNAL_IP" != "<pending>" ]; then
        break
      fi
      echo "  Waiting for external IP for $cluster (service: $SERVICE_NAME, attempt $attempt/12)..."
      sleep 10
    done
    
    if [ -n "$EXTERNAL_IP" ] && [ "$EXTERNAL_IP" != "<pending>" ]; then
      echo "  External IP for $cluster: $EXTERNAL_IP"

      # Delete existing DNS record if it exists
      az network dns record-set a delete \
        --name "$cluster" \
        --zone-name "$fullName" \
        --resource-group "$RESOURCE_GROUP" \
        --yes
      
      # Create DNS record
      az network dns record-set a create \
        --name "$cluster" \
        --zone-name "$fullName" \
        --resource-group "$RESOURCE_GROUP" \
        --ttl 5
      az network dns record-set a add-record \
        --record-set-name "$cluster" \
        --zone-name "$fullName" \
        --resource-group "$RESOURCE_GROUP" \
        --ipv4-address "$EXTERNAL_IP" \
        --ttl 5

      echo "  ✓ Created DNS record $cluster"
    else
      echo "  ✗ Failed to get external IP for $cluster"
    fi
  done

  # Delete and recreate SRV record for MongoDB
  az network dns record-set srv delete \
    --name "_mongodb._tcp" \
    --zone-name "$fullName" \
    --resource-group "$RESOURCE_GROUP" \
    --yes 
  
  az network dns record-set srv create \
    --name "_mongodb._tcp" \
    --zone-name "$fullName" \
    --resource-group "$RESOURCE_GROUP" \
    --ttl 1

  mongoFQDN=$(az network dns record-set srv add-record \
    --record-set-name "_mongodb._tcp" \
    --zone-name "$fullName" \
    --resource-group "$RESOURCE_GROUP" \
    --priority 0 \
    --weight 0 \
    --port 10260 \
    --target "$PRIMARY_CLUSTER.$fullName" | jq -r ".fqdn")
  
  echo ""
  echo "✓ DNS zone created successfully!"
  echo "  Zone Name: $fullName"
  echo "  MongoDB FQDN: $mongoFQDN"
fi

echo ""
echo "Connection Information:"
echo "  Username: default_user"
echo "  Password: $DOCUMENTDB_PASSWORD"
echo ""

# Generate failover commands for all non-primary clusters
echo "To initiate failover to a different region:"
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [ "$cluster" != "$PRIMARY_CLUSTER" ]; then
    REGION=$(echo "$cluster" | awk -F- '{print $2}')
    echo ""
    echo "# Failover to $REGION:"
    echo "kubectl --context $HUB_CONTEXT patch documentdb documentdb-preview -n documentdb-preview-ns \\"
    echo "  --type='merge' -p '{\"spec\":{\"clusterReplication\":{\"primary\":\"$cluster\"}}}'"
  fi
done

echo ""
echo "To monitor the deployment:"
echo "watch 'kubectl --context $HUB_CONTEXT get clusterresourceplacement documentdb-crp -o wide'"

echo ""
echo "To check DocumentDB status across all clusters:"
# Create a space-separated string from the array
CLUSTER_STRING=$(IFS=' '; echo "${CLUSTER_ARRAY[*]}")
echo "for c in $CLUSTER_STRING; do echo \"=== \$c ===\"; kubectl --context \$c get documentdb,pods -n documentdb-preview-ns 2>/dev/null || echo 'Not deployed yet'; echo; done"