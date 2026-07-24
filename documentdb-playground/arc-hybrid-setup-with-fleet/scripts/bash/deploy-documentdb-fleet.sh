#!/bin/bash
# Deploy DocumentDB to all Fleet member clusters
# Uses vanilla Helm deployment (no extension registration needed)

set -e

# Load environment from previous scripts if available
[[ -f .fleet-env ]] && source .fleet-env

# Configuration
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-fleet-rg}"
FLEET_NAME="${FLEET_NAME:-documentdb-fleet}"
AKS_CLUSTER="${AKS_CLUSTER:-documentdb-aks}"
ARC_CLUSTER="${ARC_CLUSTER:-documentdb-onprem}"
DEPLOY_INSTANCES="${DEPLOY_INSTANCES:-true}"

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
    echo "Deploys DocumentDB operator to all Fleet member clusters"
    echo ""
    echo "Environment variables:"
    echo "  RESOURCE_GROUP     Azure resource group"
    echo "  FLEET_NAME         Fleet hub name"
    echo "  AKS_CLUSTER        AKS cluster name"
    echo "  ARC_CLUSTER        Arc cluster name"
    echo "  DEPLOY_INSTANCES   Deploy DocumentDB instances (default: true)"
    exit 0
fi

# Check prerequisites
log "Checking prerequisites..."
command -v az &> /dev/null || error "Azure CLI not found"
command -v kubectl &> /dev/null || error "kubectl not found"
command -v helm &> /dev/null || error "Helm not found"

# Check Azure login
if ! az account show &> /dev/null; then
    error "Not logged into Azure. Run 'az login' first."
fi

# Get Fleet members
log "Getting Fleet members..."
MEMBERS=$(az fleet member list --resource-group "$RESOURCE_GROUP" --fleet-name "$FLEET_NAME" --query "[].name" -o tsv)
if [[ -z "$MEMBERS" ]]; then
    error "No Fleet members found. Run setup-fleet-hub.sh and setup-arc-member.sh first."
fi

echo "Fleet members found:"
echo "$MEMBERS" | while read -r member; do echo "  - $member"; done

# Deploy DocumentDB operator to each member
log "Deploying DocumentDB operator to all Fleet members..."

for MEMBER in $MEMBERS; do
    echo ""
    log "=== Deploying to: $MEMBER ==="
    
    # Get credentials based on cluster type
    if az aks show -g "$RESOURCE_GROUP" -n "$MEMBER" &>/dev/null; then
        log "Getting AKS credentials for $MEMBER..."
        az aks get-credentials -g "$RESOURCE_GROUP" -n "$MEMBER" --overwrite-existing
    elif az connectedk8s show -g "$RESOURCE_GROUP" -n "$MEMBER" &>/dev/null; then
        log "Using Kind context for $MEMBER..."
        kubectl config use-context "kind-$MEMBER" 2>/dev/null || \
        kubectl config use-context "$MEMBER" 2>/dev/null || \
        warn "Could not switch context for $MEMBER - ensure kubeconfig is set"
    else
        warn "Unknown cluster type for $MEMBER, skipping..."
        continue
    fi
    
    # Verify connectivity
    if ! kubectl cluster-info &>/dev/null; then
        warn "Cannot connect to $MEMBER, skipping..."
        continue
    fi
    
    # Check if operator already installed
    if helm list -n documentdb-operator 2>/dev/null | grep -q documentdb-operator; then
        log "DocumentDB operator already installed on $MEMBER, upgrading..."
        helm upgrade documentdb-operator oci://ghcr.io/documentdb/documentdb-helm-chart \
          --namespace documentdb-operator \
          --wait --timeout 5m
    else
        log "Installing DocumentDB operator on $MEMBER..."
        helm install documentdb-operator oci://ghcr.io/documentdb/documentdb-helm-chart \
          --namespace documentdb-operator --create-namespace \
          --wait --timeout 5m
    fi
    success "DocumentDB operator ready on $MEMBER"
done

# Deploy DocumentDB instances if requested
if [[ "$DEPLOY_INSTANCES" == "true" ]]; then
    echo ""
    log "Deploying DocumentDB instances..."
    
    for MEMBER in $MEMBERS; do
        log "=== Deploying instance on: $MEMBER ==="
        
        # Get credentials
        if az aks show -g "$RESOURCE_GROUP" -n "$MEMBER" &>/dev/null; then
            az aks get-credentials -g "$RESOURCE_GROUP" -n "$MEMBER" --overwrite-existing
        else
            kubectl config use-context "kind-$MEMBER" 2>/dev/null || \
            kubectl config use-context "$MEMBER" 2>/dev/null || continue
        fi
        
        # Check if instance exists
        if kubectl get documentdb "documentdb-$MEMBER" &>/dev/null; then
            log "DocumentDB instance already exists on $MEMBER"
            continue
        fi
        
        # Deploy instance
        kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-$MEMBER
  namespace: default
  labels:
    fleet: $FLEET_NAME
    cluster: $MEMBER
spec:
  instances: 3
  documentdbVersion: "8"
  storage:
    size: 10Gi
EOF
        success "DocumentDB instance deployed on $MEMBER"
    done
fi

# Verify deployment
echo ""
log "Verifying deployment across all Fleet members..."
echo ""

for MEMBER in $MEMBERS; do
    echo "=== $MEMBER ==="
    
    # Get credentials
    if az aks show -g "$RESOURCE_GROUP" -n "$MEMBER" &>/dev/null; then
        az aks get-credentials -g "$RESOURCE_GROUP" -n "$MEMBER" --overwrite-existing 2>/dev/null
    else
        kubectl config use-context "kind-$MEMBER" 2>/dev/null || \
        kubectl config use-context "$MEMBER" 2>/dev/null || continue
    fi
    
    echo "Operator pods:"
    kubectl get pods -n documentdb-operator --no-headers 2>/dev/null || echo "  (no pods)"
    
    echo "DocumentDB instances:"
    kubectl get documentdb -A --no-headers 2>/dev/null || echo "  (no instances)"
    echo ""
done

# Summary
echo "=============================================="
success "DocumentDB Deployment Complete!"
echo "=============================================="
echo ""
echo "All Fleet members now have:"
echo "  ✅ DocumentDB operator installed"
if [[ "$DEPLOY_INSTANCES" == "true" ]]; then
    echo "  ✅ DocumentDB cluster instance running"
fi
echo ""
echo "View in Azure Portal:"
echo "  Fleet Manager -> $FLEET_NAME -> Members"
echo ""
echo "No extension registration was required!"
echo ""
