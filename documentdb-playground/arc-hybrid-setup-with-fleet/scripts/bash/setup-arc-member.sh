#!/bin/bash
# Setup Arc-enabled on-prem cluster and join to Fleet
# Creates Kind cluster, Arc-enables it, and joins to existing Fleet

set -e

# Load environment from previous script if available
[[ -f .fleet-env ]] && source .fleet-env

# Configuration (can be overridden via environment variables)
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-fleet-rg}"
LOCATION="${LOCATION:-eastus}"
FLEET_NAME="${FLEET_NAME:-documentdb-fleet}"
ARC_CLUSTER="${ARC_CLUSTER:-documentdb-onprem}"
CLUSTER_TYPE="${CLUSTER_TYPE:-kind}"  # kind, k3d, or existing

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { echo -e "${BLUE}[$(date +'%H:%M:%S')]${NC} $1"; }
success() { echo -e "${GREEN}[$(date +'%H:%M:%S')] ✅ $1${NC}"; }
warn() { echo -e "${YELLOW}[$(date +'%H:%M:%S')] ⚠️  $1${NC}"; }
error() { echo -e "${RED}[$(date +'%H:%M:%S')] ❌ $1${NC}"; exit 1; }

# Help
if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Creates Arc-enabled on-prem cluster and joins to Fleet"
    echo ""
    echo "Environment variables:"
    echo "  RESOURCE_GROUP   Azure resource group (default: documentdb-fleet-rg)"
    echo "  LOCATION         Azure region for Arc metadata (default: eastus)"
    echo "  FLEET_NAME       Fleet hub name (default: documentdb-fleet)"
    echo "  ARC_CLUSTER      Arc cluster name (default: documentdb-onprem)"
    echo "  CLUSTER_TYPE     kind, k3d, or existing (default: kind)"
    exit 0
fi

# Check prerequisites
log "Checking prerequisites..."
command -v az &> /dev/null || error "Azure CLI not found"
command -v kubectl &> /dev/null || error "kubectl not found"
command -v helm &> /dev/null || error "Helm not found"

if [[ "$CLUSTER_TYPE" == "kind" ]]; then
    command -v kind &> /dev/null || error "Kind not found. Install: https://kind.sigs.k8s.io/docs/user/quick-start/"
    command -v docker &> /dev/null || error "Docker not found"
fi

# Check Azure login
if ! az account show &> /dev/null; then
    error "Not logged into Azure. Run 'az login' first."
fi

# Install required extensions
log "Checking Azure CLI extensions..."
az extension add --name connectedk8s --upgrade --yes 2>/dev/null || true
az extension add --name fleet --upgrade --yes 2>/dev/null || true

# Register providers
log "Registering Azure providers (if needed)..."
az provider register --namespace Microsoft.Kubernetes --wait 2>/dev/null || true
az provider register --namespace Microsoft.KubernetesConfiguration --wait 2>/dev/null || true

# Verify Fleet exists
log "Verifying Fleet hub exists..."
if ! az fleet show --resource-group "$RESOURCE_GROUP" --name "$FLEET_NAME" &>/dev/null; then
    error "Fleet hub '$FLEET_NAME' not found. Run ./setup-fleet-hub.sh first."
fi
success "Fleet hub found"

# Create local cluster
if [[ "$CLUSTER_TYPE" == "kind" ]]; then
    log "Creating Kind cluster: $ARC_CLUSTER..."
    
    # Delete existing if present
    kind delete cluster --name "$ARC_CLUSTER" 2>/dev/null || true
    
    kind create cluster --name "$ARC_CLUSTER" --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
EOF
    success "Kind cluster created"
    
    # Switch to Kind context
    kubectl config use-context "kind-$ARC_CLUSTER"
    
elif [[ "$CLUSTER_TYPE" == "k3d" ]]; then
    log "Creating k3d cluster: $ARC_CLUSTER..."
    k3d cluster delete "$ARC_CLUSTER" 2>/dev/null || true
    k3d cluster create "$ARC_CLUSTER" --agents 2
    success "k3d cluster created"
    
elif [[ "$CLUSTER_TYPE" == "existing" ]]; then
    log "Using existing cluster context..."
    kubectl cluster-info || error "Cannot connect to existing cluster"
else
    error "Unknown CLUSTER_TYPE: $CLUSTER_TYPE"
fi

# Verify cluster
log "Verifying cluster connectivity..."
kubectl cluster-info
kubectl get nodes

# Arc-enable the cluster
log "Arc-enabling cluster (connecting to Azure)..."
az connectedk8s connect \
  --name "$ARC_CLUSTER" \
  --resource-group "$RESOURCE_GROUP" \
  --location "$LOCATION" \
  --tags environment=onprem purpose=documentdb fleet="$FLEET_NAME" cluster-type="$CLUSTER_TYPE"
success "Cluster Arc-enabled"

# Verify Arc connection
log "Verifying Arc connection..."
az connectedk8s show --name "$ARC_CLUSTER" --resource-group "$RESOURCE_GROUP" \
  --query "{name:name, connectivityStatus:connectivityStatus}" -o table

# Join Arc cluster to Fleet
log "Joining Arc cluster to Fleet..."
ARC_ID=$(az connectedk8s show -g "$RESOURCE_GROUP" -n "$ARC_CLUSTER" --query id -o tsv)
az fleet member create \
  --resource-group "$RESOURCE_GROUP" \
  --fleet-name "$FLEET_NAME" \
  --name "$ARC_CLUSTER" \
  --member-cluster-id "$ARC_ID" \
  --output none
success "Arc cluster joined to Fleet"

# Install cert-manager on Arc cluster
log "Installing cert-manager on Arc cluster..."
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
log "Waiting for cert-manager to be ready..."
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
success "cert-manager installed"

# Show Arc agent pods
log "Arc agent pods:"
kubectl get pods -n azure-arc

# Create service account for Azure Portal viewing
log "Creating service account for Azure Portal access..."
kubectl create serviceaccount arc-portal-viewer -n default 2>/dev/null || true
kubectl create clusterrolebinding arc-portal-viewer-binding \
  --clusterrole=cluster-admin \
  --serviceaccount=default:arc-portal-viewer 2>/dev/null || true

log "Generating bearer token for Azure Portal (valid for 1 year)..."
BEARER_TOKEN=$(kubectl create token arc-portal-viewer -n default --duration=8760h)
success "Bearer token generated"

# Summary
echo ""
echo "=============================================="
success "Arc-Enabled Member Setup Complete!"
echo "=============================================="
echo ""
echo "Cluster Details:"
echo "  Resource Group:     $RESOURCE_GROUP"
echo "  Fleet Hub:          $FLEET_NAME"
echo "  Arc Cluster Name:   $ARC_CLUSTER"
echo "  Cluster Type:       $CLUSTER_TYPE"
echo "  Azure Location:     $LOCATION (metadata only)"
echo ""
echo "Fleet Members:"
az fleet member list --resource-group "$RESOURCE_GROUP" --fleet-name "$FLEET_NAME" -o table
echo ""
echo "Azure Portal Links:"
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
echo "  Arc Cluster:  https://portal.azure.com/#@/resource/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.Kubernetes/connectedClusters/$ARC_CLUSTER/overview"
echo ""
echo "=============================================="
echo "BEARER TOKEN FOR AZURE PORTAL"
echo "=============================================="
echo "Use this token in Azure Portal to view Kubernetes resources:"
echo "1. Go to Arc cluster -> Kubernetes resources"
echo "2. Click 'Sign in with service account token'"
echo "3. Paste this token:"
echo ""
echo "$BEARER_TOKEN"
echo ""
echo "=============================================="
echo ""
echo "Next step: Run ./deploy-documentdb-fleet.sh to deploy DocumentDB"
echo ""

# Update environment file
echo "export ARC_CLUSTER=$ARC_CLUSTER" >> .fleet-env
echo "export BEARER_TOKEN='$BEARER_TOKEN'" >> .fleet-env

