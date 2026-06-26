#!/bin/bash
# Setup Azure Fleet Manager hub + AKS member cluster
# Creates Fleet hub and AKS cluster, then joins AKS to Fleet

set -e

# WSL fix: Use separate Azure config to avoid Windows/Linux CLI conflicts
export AZURE_CONFIG_DIR="${AZURE_CONFIG_DIR:-$HOME/azure-linux}"

# Configuration (can be overridden via environment variables)
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-fleet-rg}"
LOCATION="${LOCATION:-eastus}"
FLEET_NAME="${FLEET_NAME:-documentdb-fleet}"
AKS_CLUSTER="${AKS_CLUSTER:-documentdb-aks}"
NODE_COUNT="${NODE_COUNT:-2}"
NODE_SIZE="${NODE_SIZE:-Standard_D4s_v3}"

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
    echo "Creates Azure Fleet Manager hub and AKS member cluster"
    echo ""
    echo "Environment variables:"
    echo "  RESOURCE_GROUP   Azure resource group (default: documentdb-fleet-rg)"
    echo "  LOCATION         Azure region (default: eastus)"
    echo "  FLEET_NAME       Fleet hub name (default: documentdb-fleet)"
    echo "  AKS_CLUSTER      AKS cluster name (default: documentdb-aks)"
    echo "  NODE_COUNT       Number of nodes (default: 2)"
    echo "  NODE_SIZE        VM size (default: Standard_D4s_v3)"
    exit 0
fi

# Check prerequisites
log "Checking prerequisites..."
log "DEBUG: AZURE_CONFIG_DIR=$AZURE_CONFIG_DIR"
log "DEBUG: which az = $(which az)"
log "DEBUG: az version = $(az --version 2>&1 | head -1)"
command -v az &> /dev/null || error "Azure CLI not found"
command -v kubectl &> /dev/null || error "kubectl not found"
command -v helm &> /dev/null || error "Helm not found"

# Check Azure login
log "DEBUG: Running az account show..."
az account show 2>&1 | head -5
if ! az account show &> /dev/null; then
    log "DEBUG: az account show failed"
    error "Not logged into Azure. Run 'az login' first."
fi

# # Install Fleet extension
# log "Checking Azure CLI Fleet extension..."
# az extension add --name fleet --upgrade --yes 2>/dev/null || true

# SUBSCRIPTION=$(az account show --query name -o tsv)
# log "Using Azure subscription: $SUBSCRIPTION"

# # Create resource group
# log "Creating resource group: $RESOURCE_GROUP in $LOCATION..."
# az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none
# success "Resource group created"

# Create Fleet hub
log "Creating Azure Fleet Manager hub: $FLEET_NAME..."
az fleet create \
  --resource-group "$RESOURCE_GROUP" \
  --name "$FLEET_NAME" \
  --location "$LOCATION" \
  --output none
success "Fleet hub created"

# Create AKS cluster
log "Creating AKS cluster: $AKS_CLUSTER (this takes ~5-10 minutes)..."
az aks create \
  --resource-group "$RESOURCE_GROUP" \
  --name "$AKS_CLUSTER" \
  --node-count "$NODE_COUNT" \
  --node-vm-size "$NODE_SIZE" \
  --enable-managed-identity \
  --generate-ssh-keys \
  --tags purpose=documentdb environment=aks fleet="$FLEET_NAME" \
  --output none
success "AKS cluster created"

# Join AKS to Fleet
log "Joining AKS cluster to Fleet..."
AKS_ID=$(az aks show -g "$RESOURCE_GROUP" -n "$AKS_CLUSTER" --query id -o tsv)
az fleet member create \
  --resource-group "$RESOURCE_GROUP" \
  --fleet-name "$FLEET_NAME" \
  --name "$AKS_CLUSTER" \
  --member-cluster-id "$AKS_ID" \
  --output none
success "AKS joined to Fleet"

# Get AKS credentials
log "Getting AKS cluster credentials..."
az aks get-credentials --resource-group "$RESOURCE_GROUP" --name "$AKS_CLUSTER" --overwrite-existing

# Verify connectivity
log "Verifying AKS cluster connectivity..."
kubectl cluster-info
kubectl get nodes

# Install cert-manager on AKS
log "Installing cert-manager on AKS..."
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
log "Waiting for cert-manager to be ready..."
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
success "cert-manager installed on AKS"

# Summary
echo ""
echo "=============================================="
success "Fleet Hub + AKS Member Setup Complete!"
echo "=============================================="
echo ""
echo "Fleet Details:"
echo "  Resource Group:  $RESOURCE_GROUP"
echo "  Fleet Hub:       $FLEET_NAME"
echo "  AKS Member:      $AKS_CLUSTER"
echo "  Location:        $LOCATION"
echo ""
echo "Azure Portal Links:"
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
echo "  Fleet Hub:  https://portal.azure.com/#@/resource/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.ContainerService/fleets/$FLEET_NAME/overview"
echo "  AKS:        https://portal.azure.com/#@/resource/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.ContainerService/managedClusters/$AKS_CLUSTER/overview"
echo ""
echo "Next step: Run ./setup-arc-member.sh to add Arc-enabled on-prem cluster"
echo ""

# Export variables for next script
echo "export RESOURCE_GROUP=$RESOURCE_GROUP" > .fleet-env
echo "export LOCATION=$LOCATION" >> .fleet-env
echo "export FLEET_NAME=$FLEET_NAME" >> .fleet-env
echo "export AKS_CLUSTER=$AKS_CLUSTER" >> .fleet-env
log "Variables saved to .fleet-env (source it for next scripts)"
