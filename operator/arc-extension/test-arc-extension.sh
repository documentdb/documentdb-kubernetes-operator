#!/bin/bash

# Azure Extension Test Script for DocumentDB Operator
#
# This script helps test the extension locally before Azure registration.
# It uses Kind clusters to simulate Arc-enabled clusters (connectedClusters).
#
# For AKS (managedClusters) testing, use an actual AKS cluster and run:
#   az k8s-extension create --cluster-type managedClusters ...
#
# This script can:
# 1. Create a Kind cluster for testing
# 2. Connect the cluster to Azure Arc
# 3. Simulate extension deployment (before registration)
# 4. Test full Arc extension flow (after registration)
#
# Usage:
#   ./test-arc-extension.sh --setup-kind         # Create Kind cluster only
#   ./test-arc-extension.sh --connect-arc        # Connect to Azure Arc
#   ./test-arc-extension.sh --simulate-install   # Simulate extension install (no Arc registration)
#   ./test-arc-extension.sh --install-extension  # Install via az k8s-extension (requires registration)
#   ./test-arc-extension.sh --cleanup            # Delete Kind cluster

set -e

# Configuration
CLUSTER_NAME="${ARC_CLUSTER_NAME:-arc-test-cluster}"
RESOURCE_GROUP="${ARC_RESOURCE_GROUP:-arc-test-rg}"
LOCATION="${ARC_LOCATION:-eastus}"
CHART_VERSION="${CHART_VERSION:-0.1.3}"
GITHUB_ORG="${GITHUB_ORG:-documentdb}"

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

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    command -v kubectl &>/dev/null || error "kubectl not found"
    command -v helm &>/dev/null || error "helm not found"
    command -v az &>/dev/null || error "Azure CLI not found"
    
    success "Prerequisites met"
}

# Create Kind cluster
setup_kind() {
    log "Setting up Kind cluster: $CLUSTER_NAME"
    
    command -v kind &>/dev/null || error "kind not found. Install: https://kind.sigs.k8s.io/"
    
    if kind get clusters | grep -q "^${CLUSTER_NAME}$"; then
        warn "Cluster $CLUSTER_NAME already exists"
    else
        cat <<EOF | kind create cluster --name "$CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
EOF
    fi
    
    kubectl cluster-info --context "kind-${CLUSTER_NAME}"
    success "Kind cluster ready"
}

# Connect cluster to Azure Arc
connect_arc() {
    log "Connecting cluster to Azure Arc..."
    
    # Check Azure login
    az account show &>/dev/null || error "Not logged into Azure. Run: az login"
    
    # Install/update extensions
    log "Installing Azure CLI extensions..."
    az extension add --name connectedk8s --upgrade --yes 2>/dev/null || true
    az extension add --name k8s-extension --upgrade --yes 2>/dev/null || true
    
    # Create resource group
    log "Creating resource group: $RESOURCE_GROUP"
    az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none 2>/dev/null || true
    
    # Connect to Arc
    log "Connecting to Azure Arc (this may take a few minutes)..."
    az connectedk8s connect \
        --name "$CLUSTER_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --location "$LOCATION"
    
    # Verify
    log "Verifying Arc agent..."
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=clusterconnect-agent -n azure-arc --timeout=300s
    
    success "Cluster connected to Azure Arc"
    log "View in portal: https://portal.azure.com/#view/Microsoft_Azure_HybridCompute/AzureArcCenterBlade/~/overview"
}

# Simulate extension install (before Azure registration)
simulate_install() {
    log "Simulating Arc extension install (direct Helm)..."
    log "This bypasses Arc and installs directly - use for local testing"
    
    # Check for GitHub credentials
    if [ -z "$GITHUB_TOKEN" ] || [ -z "$GITHUB_USERNAME" ]; then
        warn "GITHUB_TOKEN and GITHUB_USERNAME not set"
        warn "Set them if ghcr.io requires authentication"
    else
        log "Authenticating with ghcr.io..."
        echo "$GITHUB_TOKEN" | helm registry login ghcr.io --username "$GITHUB_USERNAME" --password-stdin
    fi
    
    # Install using Helm (simulates what Arc does)
    log "Installing DocumentDB operator via Helm..."
    helm upgrade --install documentdb-operator \
        oci://ghcr.io/${GITHUB_ORG}/documentdb-operator \
        --version "$CHART_VERSION" \
        --namespace documentdb-operator \
        --create-namespace \
        --values "$(dirname "$0")/values-arc.yaml" \
        --wait \
        --timeout 10m
    
    # Verify
    log "Verifying installation..."
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=documentdb-operator -n documentdb-operator --timeout=300s
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=cloudnative-pg -n cnpg-system --timeout=300s
    
    success "Simulated extension install complete"
    
    echo ""
    log "Installed components:"
    kubectl get pods -n documentdb-operator
    kubectl get pods -n cnpg-system
}

# Install via Arc extension (requires registration)
install_extension() {
    log "Installing DocumentDB operator via Azure Arc extension..."
    
    # Check Arc connection
    az connectedk8s show --name "$CLUSTER_NAME" --resource-group "$RESOURCE_GROUP" &>/dev/null \
        || error "Cluster not connected to Arc. Run: $0 --connect-arc"
    
    # Install extension
    az k8s-extension create \
        --name documentdb-operator \
        --extension-type Microsoft.DocumentDB.Operator \
        --cluster-name "$CLUSTER_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --cluster-type connectedClusters \
        --release-train stable \
        --configuration-settings documentDbVersion="$CHART_VERSION"
    
    # Verify
    log "Verifying extension..."
    az k8s-extension show \
        --name documentdb-operator \
        --cluster-name "$CLUSTER_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --cluster-type connectedClusters \
        --output table
    
    success "Extension installed via Azure Arc"
}

# Show extension status
show_status() {
    log "Extension status:"
    
    echo ""
    echo "=== Azure Arc Extension ==="
    az k8s-extension show \
        --name documentdb-operator \
        --cluster-name "$CLUSTER_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --cluster-type connectedClusters \
        --output table 2>/dev/null || warn "Extension not found in Azure"
    
    echo ""
    echo "=== Kubernetes Pods ==="
    echo "DocumentDB Operator:"
    kubectl get pods -n documentdb-operator 2>/dev/null || warn "Namespace not found"
    echo ""
    echo "CNPG Operator:"
    kubectl get pods -n cnpg-system 2>/dev/null || warn "Namespace not found"
    echo ""
    echo "Azure Arc Agent:"
    kubectl get pods -n azure-arc 2>/dev/null || warn "Arc agent not installed"
}

# Uninstall extension
uninstall_extension() {
    log "Uninstalling extension..."
    
    # Try Arc uninstall first
    az k8s-extension delete \
        --name documentdb-operator \
        --cluster-name "$CLUSTER_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --cluster-type connectedClusters \
        --yes 2>/dev/null || warn "Arc extension not found"
    
    # Helm uninstall (if simulated install)
    helm uninstall documentdb-operator -n documentdb-operator 2>/dev/null || true
    
    # Cleanup namespaces
    kubectl delete namespace documentdb-operator --ignore-not-found
    kubectl delete namespace cnpg-system --ignore-not-found
    
    success "Extension uninstalled"
}

# Cleanup everything
cleanup() {
    log "Cleaning up..."
    
    # Disconnect from Arc
    az connectedk8s delete \
        --name "$CLUSTER_NAME" \
        --resource-group "$RESOURCE_GROUP" \
        --yes 2>/dev/null || warn "Cluster not connected to Arc"
    
    # Delete Kind cluster
    kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || warn "Kind cluster not found"
    
    # Optionally delete resource group
    read -p "Delete resource group $RESOURCE_GROUP? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        az group delete --name "$RESOURCE_GROUP" --yes --no-wait
        log "Resource group deletion initiated"
    fi
    
    success "Cleanup complete"
}

# Print usage
usage() {
    cat <<EOF
Azure Arc Extension Test Script for DocumentDB Operator

Usage: $0 [COMMAND]

Commands:
  --setup-kind          Create Kind cluster for testing
  --connect-arc         Connect cluster to Azure Arc
  --simulate-install    Install via Helm (simulates Arc, no registration needed)
  --install-extension   Install via az k8s-extension (requires Azure registration)
  --status              Show extension status
  --uninstall           Uninstall extension
  --cleanup             Delete Kind cluster and Arc resources

Environment Variables:
  ARC_CLUSTER_NAME      Cluster name (default: arc-test-cluster)
  ARC_RESOURCE_GROUP    Resource group (default: arc-test-rg)
  ARC_LOCATION          Azure location (default: eastus)
  CHART_VERSION         Helm chart version (default: 0.1.3)
  GITHUB_ORG            GitHub org for chart (default: documentdb)
  GITHUB_USERNAME       GitHub username (for private registry)
  GITHUB_TOKEN          GitHub token (for private registry)

Examples:
  # Full local test (no Azure registration)
  $0 --setup-kind
  $0 --simulate-install
  $0 --status

  # Full Arc test (requires Azure registration)
  $0 --setup-kind
  $0 --connect-arc
  $0 --install-extension
  $0 --status
EOF
}

# Main
main() {
    case "${1:-}" in
        --setup-kind)
            check_prerequisites
            setup_kind
            ;;
        --connect-arc)
            check_prerequisites
            connect_arc
            ;;
        --simulate-install)
            check_prerequisites
            simulate_install
            ;;
        --install-extension)
            check_prerequisites
            install_extension
            ;;
        --status)
            show_status
            ;;
        --uninstall)
            uninstall_extension
            ;;
        --cleanup)
            cleanup
            ;;
        -h|--help|"")
            usage
            ;;
        *)
            error "Unknown command: $1"
            ;;
    esac
}

main "$@"
