#!/usr/bin/env bash
set -euo pipefail

# Deploy multi-region DocumentDB with cross-cluster replication using Istio

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load deployment info
if [ -f "$SCRIPT_DIR/.deployment-info" ]; then
  source "$SCRIPT_DIR/.deployment-info"
else
  echo "Error: Deployment info not found. Run deploy-infrastructure.sh first."
  exit 1
fi

# Password from argument or environment
DOCUMENTDB_PASSWORD="${1:-${DOCUMENTDB_PASSWORD:-}}"

# Generate password if not provided
if [ -z "$DOCUMENTDB_PASSWORD" ]; then
  echo "No password provided. Generating a secure password..."
  DOCUMENTDB_PASSWORD=$(openssl rand -base64 32 | tr -d "=+/" | cut -c1-25)
  echo "Generated password: $DOCUMENTDB_PASSWORD"
  echo "(Save this password - you'll need it to connect to the database)"
  echo ""
fi

HUB_CLUSTER_NAME="hub-${HUB_REGION}"

echo "======================================="
echo "DocumentDB Multi-Region Deployment"
echo "======================================="
echo "Hub Cluster: $HUB_CLUSTER_NAME"
echo "Cross-cluster networking: Istio"
echo "======================================="

# Build list of all clusters
ALL_CLUSTERS="$HUB_CLUSTER_NAME"

# Add k3s clusters
IFS=' ' read -ra K3S_REGION_ARRAY <<< "$K3S_REGIONS"
for region in "${K3S_REGION_ARRAY[@]}"; do
  if kubectl config get-contexts "k3s-$region" &>/dev/null; then
    ALL_CLUSTERS="$ALL_CLUSTERS k3s-$region"
  fi
done

CLUSTER_ARRAY=($ALL_CLUSTERS)
echo "Discovered ${#CLUSTER_ARRAY[@]} clusters:"
for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo "  - $cluster"
done

# Select primary cluster (prefer hub cluster)
PRIMARY_CLUSTER="$HUB_CLUSTER_NAME"
echo ""
echo "Selected primary cluster: $PRIMARY_CLUSTER"

# Build cluster list YAML
CLUSTER_LIST=""
for cluster in "${CLUSTER_ARRAY[@]}"; do
  # Note: DocumentDB only supports 'aks', 'eks', 'gke' environments.
  # k3s clusters use 'aks' environment since they behave similarly.
  ENV="aks"
  
  if [ -z "$CLUSTER_LIST" ]; then
    CLUSTER_LIST="      - name: ${cluster}"
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"        environment: ${ENV}"
  else
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"      - name: ${cluster}"
    CLUSTER_LIST="${CLUSTER_LIST}"$'\n'"        environment: ${ENV}"
  fi
done

# Create cluster identification ConfigMaps
echo ""
echo "======================================="
echo "Creating cluster identification ConfigMaps..."
echo "======================================="

for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo "Processing $cluster..."
  
  if ! kubectl config get-contexts "$cluster" &>/dev/null; then
    echo "  ✗ Context not found, skipping"
    continue
  fi
  
  kubectl --context "$cluster" create configmap cluster-name \
    -n kube-system \
    --from-literal=name="$cluster" \
    --dry-run=client -o yaml | kubectl --context "$cluster" apply -f -
  
  echo "  ✓ ConfigMap created"
done

# Deploy DocumentDB resources
echo ""
echo "======================================="
echo "Deploying DocumentDB resources..."
echo "======================================="

kubectl config use-context "$HUB_CLUSTER_NAME"

# Check for existing resources
EXISTING=""
if kubectl get namespace documentdb-preview-ns &>/dev/null 2>&1; then
  EXISTING="${EXISTING}namespace "
fi
if kubectl get secret documentdb-credentials -n documentdb-preview-ns &>/dev/null 2>&1; then
  EXISTING="${EXISTING}secret "
fi
if kubectl get documentdb documentdb-preview -n documentdb-preview-ns &>/dev/null 2>&1; then
  EXISTING="${EXISTING}documentdb "
fi

if [ -n "$EXISTING" ]; then
  echo ""
  echo "⚠️  Warning: Existing resources found: $EXISTING"
  echo ""
  echo "Options:"
  echo "1. Delete existing resources and redeploy"
  echo "2. Update existing deployment"
  echo "3. Cancel"
  read -p "Choose (1/2/3): " CHOICE
  
  case $CHOICE in
    1)
      echo "Deleting existing resources..."
      kubectl delete clusterresourceplacement documentdb-namespace-crp --ignore-not-found=true
      kubectl delete namespace documentdb-preview-ns --ignore-not-found=true
      sleep 10
      ;;
    2)
      echo "Updating existing deployment..."
      ;;
    3|*)
      echo "Cancelled."
      exit 0
      ;;
  esac
fi

# Generate manifest with substitutions
TEMP_YAML=$(mktemp)

sed -e "s/{{DOCUMENTDB_PASSWORD}}/$DOCUMENTDB_PASSWORD/g" \
    -e "s/{{PRIMARY_CLUSTER}}/$PRIMARY_CLUSTER/g" \
    "$SCRIPT_DIR/documentdb-resource-crp.yaml" | \
while IFS= read -r line; do
  if [[ "$line" == '{{CLUSTER_LIST}}' ]]; then
    echo "$CLUSTER_LIST"
  else
    echo "$line"
  fi
done > "$TEMP_YAML"

echo ""
echo "Generated configuration:"
echo "------------------------"
echo "Primary: $PRIMARY_CLUSTER"
echo "Clusters:"
echo "$CLUSTER_LIST"
echo "------------------------"

# Apply configuration
echo ""
echo "Applying DocumentDB configuration..."
kubectl apply -f "$TEMP_YAML"
rm -f "$TEMP_YAML"

# Check ClusterResourcePlacement
echo ""
echo "Checking ClusterResourcePlacement status..."
kubectl get clusterresourceplacement documentdb-namespace-crp -o wide

# Wait for propagation
echo ""
echo "Waiting for resources to propagate..."
sleep 15

# Verify deployment
echo ""
echo "======================================="
echo "Deployment Verification"
echo "======================================="

for cluster in "${CLUSTER_ARRAY[@]}"; do
  echo ""
  echo "=== $cluster ==="
  
  if ! kubectl config get-contexts "$cluster" &>/dev/null; then
    echo "  ✗ Context not found"
    continue
  fi
  
  # Check namespace
  if kubectl --context "$cluster" get namespace documentdb-preview-ns &>/dev/null; then
    echo "  ✓ Namespace exists"
    
    # Check DocumentDB
    if kubectl --context "$cluster" get documentdb documentdb-preview -n documentdb-preview-ns &>/dev/null; then
      STATUS=$(kubectl --context "$cluster" get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
      ROLE="REPLICA"
      [ "$cluster" = "$PRIMARY_CLUSTER" ] && ROLE="PRIMARY"
      echo "  ✓ DocumentDB: $STATUS (Role: $ROLE)"
    else
      echo "  ✗ DocumentDB not found"
    fi
    
    # Check pods
    PODS=$(kubectl --context "$cluster" get pods -n documentdb-preview-ns --no-headers 2>/dev/null | wc -l || echo "0")
    echo "  Pods: $PODS"
    
    if [ "$PODS" -gt 0 ]; then
      kubectl --context "$cluster" get pods -n documentdb-preview-ns 2>/dev/null | head -5
    fi
  else
    echo "  ✗ Namespace not found (propagating...)"
  fi
done

# Connection information
echo ""
echo "======================================="
echo "Connection Information"
echo "======================================="
echo ""
echo "Username: default_user"
echo "Password: $DOCUMENTDB_PASSWORD"
echo ""
echo "To connect via port-forward:"
echo "  kubectl --context $PRIMARY_CLUSTER port-forward -n documentdb-preview-ns svc/documentdb-preview 10260:10260"
echo ""
echo "Connection string:"
echo "  mongodb://default_user:$DOCUMENTDB_PASSWORD@localhost:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
echo ""

# Failover commands
echo "Failover commands:"
for cluster in "${CLUSTER_ARRAY[@]}"; do
  if [ "$cluster" != "$PRIMARY_CLUSTER" ]; then
    echo ""
    echo "# Failover to $cluster:"
    echo "kubectl --context $HUB_CLUSTER_NAME patch documentdb documentdb-preview -n documentdb-preview-ns \\"
    echo "  --type='merge' -p '{\"spec\":{\"clusterReplication\":{\"primary\":\"$cluster\"}}}'"
  fi
done

echo ""
echo "======================================="
echo "✅ DocumentDB Deployment Complete!"
echo "======================================="
echo ""
echo "Monitor deployment:"
echo "  watch 'kubectl --context $HUB_CLUSTER_NAME get clusterresourceplacement documentdb-namespace-crp -o wide'"
echo ""
echo "Check all clusters:"
CLUSTER_STRING=$(IFS=' '; echo "${CLUSTER_ARRAY[*]}")
echo "  for c in $CLUSTER_STRING; do echo \"=== \$c ===\"; kubectl --context \$c get documentdb,pods -n documentdb-preview-ns; done"
echo "======================================="
