#!/bin/bash
# Verify Fleet-based DocumentDB deployment in Azure Portal
# Shows Fleet hub, members, and DocumentDB status

set -e

# Load environment if available
[[ -f .fleet-env ]] && source .fleet-env

# Configuration
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-fleet-rg}"
FLEET_NAME="${FLEET_NAME:-documentdb-fleet}"
AKS_CLUSTER="${AKS_CLUSTER:-documentdb-aks}"
ARC_CLUSTER="${ARC_CLUSTER:-documentdb-onprem}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log() { echo -e "${BLUE}[$(date +'%H:%M:%S')]${NC} $1"; }
success() { echo -e "${GREEN}✅ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }
header() { echo -e "\n${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"; echo -e "${CYAN}$1${NC}"; echo -e "${CYAN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"; }

# Check prerequisites
command -v az &> /dev/null || { echo "Azure CLI not found"; exit 1; }

# Check Azure login
if ! az account show &> /dev/null; then
    echo "Not logged into Azure. Run 'az login' first."
    exit 1
fi

SUBSCRIPTION_ID=$(az account show --query id -o tsv)
SUBSCRIPTION_NAME=$(az account show --query name -o tsv)

header "Azure Subscription"
echo "  Name: $SUBSCRIPTION_NAME"
echo "  ID:   $SUBSCRIPTION_ID"

header "Azure Fleet Manager"
if az fleet show --resource-group "$RESOURCE_GROUP" --name "$FLEET_NAME" &>/dev/null; then
    az fleet show --resource-group "$RESOURCE_GROUP" --name "$FLEET_NAME" \
      --query "{Name:name, State:provisioningState, Location:location}" \
      --output table
    success "Fleet hub found"
else
    warn "Fleet hub '$FLEET_NAME' not found in $RESOURCE_GROUP"
fi

header "Fleet Members"
if az fleet member list --resource-group "$RESOURCE_GROUP" --fleet-name "$FLEET_NAME" &>/dev/null; then
    az fleet member list --resource-group "$RESOURCE_GROUP" --fleet-name "$FLEET_NAME" \
      --query "[].{Name:name, State:provisioningState, ClusterId:clusterResourceId}" \
      --output table
else
    warn "Could not list Fleet members"
fi

header "AKS Cluster Status"
if az aks show --name "$AKS_CLUSTER" --resource-group "$RESOURCE_GROUP" &>/dev/null; then
    az aks show --name "$AKS_CLUSTER" --resource-group "$RESOURCE_GROUP" \
      --query "{Name:name, State:provisioningState, K8sVersion:kubernetesVersion, NodeCount:agentPoolProfiles[0].count, Location:location}" \
      --output table
    success "AKS cluster found"
else
    warn "AKS cluster '$AKS_CLUSTER' not found in $RESOURCE_GROUP"
fi

header "Arc-Enabled Cluster Status"
if az connectedk8s show --name "$ARC_CLUSTER" --resource-group "$RESOURCE_GROUP" &>/dev/null; then
    az connectedk8s show --name "$ARC_CLUSTER" --resource-group "$RESOURCE_GROUP" \
      --query "{Name:name, Connectivity:connectivityStatus, K8sVersion:kubernetesVersion, AgentVersion:agentVersion, Location:location}" \
      --output table
    success "Arc-enabled cluster found"
else
    warn "Arc-enabled cluster '$ARC_CLUSTER' not found in $RESOURCE_GROUP"
fi

header "DocumentDB Status on Each Cluster"
echo ""

# Check AKS
if az aks show -g "$RESOURCE_GROUP" -n "$AKS_CLUSTER" &>/dev/null; then
    log "=== $AKS_CLUSTER (AKS) ==="
    az aks get-credentials -g "$RESOURCE_GROUP" -n "$AKS_CLUSTER" --overwrite-existing 2>/dev/null
    echo "Operator:"
    kubectl get pods -n documentdb-operator --no-headers 2>/dev/null || echo "  (not installed)"
    echo "Instances:"
    kubectl get documentdb -A --no-headers 2>/dev/null || echo "  (none)"
    echo ""
fi

# Check Arc
if kubectl config use-context "kind-$ARC_CLUSTER" &>/dev/null; then
    log "=== $ARC_CLUSTER (Arc) ==="
    echo "Operator:"
    kubectl get pods -n documentdb-operator --no-headers 2>/dev/null || echo "  (not installed)"
    echo "Instances:"
    kubectl get documentdb -A --no-headers 2>/dev/null || echo "  (none)"
    echo ""
fi

header "Azure Portal Links"
echo ""
echo "Fleet Manager:"
echo "  https://portal.azure.com/#@/resource/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.ContainerService/fleets/$FLEET_NAME/overview"
echo ""
echo "AKS Cluster:"
echo "  https://portal.azure.com/#@/resource/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.ContainerService/managedClusters/$AKS_CLUSTER/overview"
echo ""
echo "Arc-Enabled Cluster:"
echo "  https://portal.azure.com/#@/resource/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.Kubernetes/connectedClusters/$ARC_CLUSTER/overview"
echo ""
echo "All Kubernetes Clusters:"
echo "  https://portal.azure.com/#view/Microsoft_Azure_HybridCompute/AzureArcCenterBlade/~/kubernetesServices"
echo ""

success "Verification complete!"
