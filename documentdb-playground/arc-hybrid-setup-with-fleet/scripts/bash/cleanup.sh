#!/bin/bash
# Cleanup script for Fleet-based hybrid setup
# Removes Fleet hub, AKS, Arc clusters, and resources

set -e

# Load environment if available
[[ -f .fleet-env ]] && source .fleet-env

# Configuration
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-fleet-rg}"
FLEET_NAME="${FLEET_NAME:-documentdb-fleet}"
AKS_CLUSTER="${AKS_CLUSTER:-documentdb-aks}"
ARC_CLUSTER="${ARC_CLUSTER:-documentdb-onprem}"
FORCE="${FORCE:-false}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { echo -e "${BLUE}[$(date +'%H:%M:%S')]${NC} $1"; }
success() { echo -e "${GREEN}[$(date +'%H:%M:%S')] ✅ $1${NC}"; }
warn() { echo -e "${YELLOW}[$(date +'%H:%M:%S')] ⚠️  $1${NC}"; }

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --force|-f)
            FORCE="true"
            shift
            ;;
        --resource-group)
            RESOURCE_GROUP="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --force, -f         Skip confirmation prompts"
            echo "  --resource-group    Resource group to clean up"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

echo ""
echo "This will delete:"
echo "  - Kind cluster: $ARC_CLUSTER"
echo "  - Arc registration: $ARC_CLUSTER"
echo "  - Fleet members (all)"
echo "  - Fleet hub: $FLEET_NAME"
echo "  - AKS cluster: $AKS_CLUSTER"
echo "  - Resource group: $RESOURCE_GROUP (and all resources)"
echo ""

if [[ "$FORCE" != "true" ]]; then
    read -p "Are you sure? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Cancelled."
        exit 0
    fi
fi

# Delete Kind cluster
if command -v kind &>/dev/null; then
    log "Deleting Kind cluster: $ARC_CLUSTER..."
    kind delete cluster --name "$ARC_CLUSTER" 2>/dev/null || warn "Kind cluster not found"
fi

# Delete Fleet members first
log "Deleting Fleet members..."
MEMBERS=$(az fleet member list --resource-group "$RESOURCE_GROUP" --fleet-name "$FLEET_NAME" --query "[].name" -o tsv 2>/dev/null) || true
for MEMBER in $MEMBERS; do
    log "Deleting Fleet member: $MEMBER..."
    az fleet member delete \
      --resource-group "$RESOURCE_GROUP" \
      --fleet-name "$FLEET_NAME" \
      --name "$MEMBER" \
      --yes 2>/dev/null || warn "Fleet member $MEMBER not found"
done

# Delete Arc registration
log "Deleting Arc cluster registration..."
az connectedk8s delete \
  --name "$ARC_CLUSTER" \
  --resource-group "$RESOURCE_GROUP" \
  --yes 2>/dev/null || warn "Arc cluster not found"

# Delete Fleet hub
log "Deleting Fleet hub: $FLEET_NAME..."
az fleet delete \
  --resource-group "$RESOURCE_GROUP" \
  --name "$FLEET_NAME" \
  --yes 2>/dev/null || warn "Fleet hub not found"

# Delete AKS cluster
log "Deleting AKS cluster: $AKS_CLUSTER..."
az aks delete \
  --name "$AKS_CLUSTER" \
  --resource-group "$RESOURCE_GROUP" \
  --yes --no-wait 2>/dev/null || warn "AKS cluster not found"

# Delete resource group
log "Deleting resource group: $RESOURCE_GROUP..."
az group delete \
  --name "$RESOURCE_GROUP" \
  --yes --no-wait 2>/dev/null || warn "Resource group not found"

# Clean up local files
rm -f .fleet-env 2>/dev/null || true

success "Cleanup initiated!"
echo ""
echo "Note: AKS and resource group deletion runs in background."
echo "Check Azure Portal to confirm completion."
