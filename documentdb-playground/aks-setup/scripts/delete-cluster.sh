#!/bin/bash

# DocumentDB AKS Cluster Deletion Script
# This script comprehensively deletes the AKS cluster and all associated Azure resources

set -e  # Exit on any error

# Configuration (should match create-cluster.sh)
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-cluster}"
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-rg}"
LOCATION="${LOCATION:-westus2}"

# Deletion scope flags
DELETE_INSTANCE="${DELETE_INSTANCE:-false}"
DELETE_OPERATOR="${DELETE_OPERATOR:-false}"
DELETE_CLUSTER="${DELETE_CLUSTER:-false}"
DELETE_ALL="${DELETE_ALL:-false}"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --cluster-name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --resource-group)
            RESOURCE_GROUP="$2"
            shift 2
            ;;
        --delete-instance)
            DELETE_INSTANCE="true"
            shift
            ;;
        --delete-operator)
            DELETE_OPERATOR="true"
            shift
            ;;
        --delete-cluster)
            DELETE_CLUSTER="true"
            shift
            ;;
        --delete-all)
            DELETE_ALL="true"
            DELETE_INSTANCE="true"
            DELETE_OPERATOR="true"
            DELETE_CLUSTER="true"
            shift
            ;;
        --force)
            FORCE_DELETE="true"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --delete-instance       Delete DocumentDB instance only"
            echo "  --delete-operator       Delete DocumentDB operator only"
            echo "  --delete-cluster        Delete AKS cluster only"
            echo "  --delete-all           Delete everything (instance + operator + cluster)"
            echo "  --cluster-name NAME     AKS cluster name (default: documentdb-cluster)"
            echo "  --resource-group RG     Azure resource group (default: documentdb-rg)"
            echo "  --force                 Skip confirmation prompts"
            echo "  -h, --help             Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 --delete-instance               # Delete DocumentDB instance only"
            echo "  $0 --delete-operator               # Delete operator only"
            echo "  $0 --delete-cluster                # Delete cluster only"
            echo "  $0 --delete-all                    # Delete everything"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging function
log() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')] ✅ $1${NC}"
}

warn() {
    echo -e "${YELLOW}[$(date +'%Y-%m-%d %H:%M:%S')] ⚠️  $1${NC}"
}

error() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ❌ $1${NC}"
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    # Check Azure CLI
    if ! command -v az &> /dev/null; then
        error "Azure CLI not found. Cannot proceed with deletion."
        exit 1
    fi
    
    # Check kubectl
    if ! command -v kubectl &> /dev/null; then
        warn "kubectl not found. Some cleanup steps may be skipped."
    fi
    
    # Check Azure login
    if ! az account show &> /dev/null; then
        error "Not logged into Azure. Please run 'az login' first."
        exit 1
    fi
    
    success "Prerequisites met"
}

# Confirmation prompt
confirm_deletion() {
    if [ "$FORCE_DELETE" == "true" ]; then
        return 0
    fi
    
    echo ""
    echo "⚠️  WARNING: This will permanently delete the following resources:"
    
    if [ "$DELETE_INSTANCE" == "true" ]; then
        echo "  - DocumentDB instances and namespaces"
    fi
    
    if [ "$DELETE_OPERATOR" == "true" ]; then
        echo "  - DocumentDB operator"
    fi
    
    if [ "$DELETE_CLUSTER" == "true" ]; then
        echo "  - AKS Cluster: $CLUSTER_NAME"
        echo "  - Resource Group: $RESOURCE_GROUP (and ALL resources within it)"
        echo "  - All associated Azure resources (LoadBalancers, Disks, Network Security Groups, etc.)"
        echo ""
        echo "💰 This action will stop all Azure charges for these resources."
    fi
    
    echo ""
    read -p "Are you sure you want to proceed? Type 'yes' to confirm: " confirmation
    
    if [ "$confirmation" != "yes" ]; then
        echo "Deletion cancelled."
        exit 0
    fi
}

# Delete DocumentDB instances
delete_documentdb_instances() {
    log "Deleting DocumentDB instances..."
    
    if command -v kubectl &> /dev/null && kubectl cluster-info &> /dev/null; then
        # Delete DocumentDB instances
        kubectl delete documentdb --all --all-namespaces --ignore-not-found=true || warn "Failed to delete some DocumentDB instances"
        
        # Delete DocumentDB namespaces
        kubectl delete namespace documentdb-instance-ns --ignore-not-found=true || warn "Failed to delete DocumentDB instance namespace"
        
        success "DocumentDB instances deleted"
    else
        warn "kubectl not available or cluster not accessible. Skipping DocumentDB cleanup."
    fi
}

# Delete DocumentDB operator
delete_documentdb_operator() {
    log "Deleting DocumentDB operator..."
    
    if command -v kubectl &> /dev/null && kubectl cluster-info &> /dev/null; then
        # Delete operator using Helm if available
        if command -v helm &> /dev/null; then
            helm uninstall documentdb-operator -n documentdb-operator --ignore-not-found 2>/dev/null || warn "DocumentDB operator Helm release not found"
        fi
        
        # Delete operator namespace
        kubectl delete namespace documentdb-operator --ignore-not-found=true || warn "Failed to delete DocumentDB operator namespace"
        
        success "DocumentDB operator deleted"
    else
        warn "kubectl not available or cluster not accessible. Skipping operator cleanup."
    fi
}

# Delete cert-manager
delete_cert_manager() {
    log "Deleting cert-manager..."
    
    if command -v kubectl &> /dev/null && kubectl cluster-info &> /dev/null && command -v helm &> /dev/null; then
        helm uninstall cert-manager -n cert-manager --ignore-not-found 2>/dev/null || warn "cert-manager Helm release not found"
        kubectl delete namespace cert-manager --ignore-not-found=true || warn "Failed to delete cert-manager namespace"
        success "cert-manager deleted"
    else
        warn "kubectl or helm not available. Skipping cert-manager cleanup."
    fi
}

# Delete Load Balancer services
delete_load_balancer_services() {
    log "Deleting LoadBalancer services..."
    
    if command -v kubectl &> /dev/null && kubectl cluster-info &> /dev/null; then
        # Delete all LoadBalancer services to trigger Azure LoadBalancer cleanup
        kubectl get services --all-namespaces -o json | \
            jq -r '.items[] | select(.spec.type=="LoadBalancer") | "\(.metadata.namespace) \(.metadata.name)"' | \
            while read namespace name; do
                if [ -n "$namespace" ] && [ -n "$name" ]; then
                    log "Deleting LoadBalancer service: $name in namespace: $namespace"
                    kubectl delete service "$name" -n "$namespace" --ignore-not-found=true || warn "Failed to delete service $name"
                fi
            done 2>/dev/null || warn "Failed to query LoadBalancer services"
        
        # Wait a moment for Azure to process the deletions
        log "Waiting for Azure LoadBalancer cleanup..."
        sleep 30
        
        success "LoadBalancer services deleted"
    else
        warn "kubectl not available. Skipping LoadBalancer service cleanup."
    fi
}

# Delete AKS cluster
delete_aks_cluster() {
    log "Deleting AKS cluster: $CLUSTER_NAME"
    
    # Check if cluster exists
    if ! az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME &> /dev/null; then
        warn "AKS cluster $CLUSTER_NAME not found. Skipping cluster deletion."
        return 0
    fi
    
    # Delete the AKS cluster
    log "This may take 10-15 minutes..."
    az aks delete --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --yes --no-wait
    
    # Wait for deletion to complete
    log "Waiting for AKS cluster deletion to complete..."
    while az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME &> /dev/null; do
        log "Cluster still exists, waiting..."
        sleep 30
    done
    
    success "AKS cluster deleted"
}

# Delete resource group and all resources
delete_resource_group() {
    log "Deleting resource group: $RESOURCE_GROUP"
    
    # Check if resource group exists
    if ! az group show --name $RESOURCE_GROUP &> /dev/null; then
        warn "Resource group $RESOURCE_GROUP not found. Skipping resource group deletion."
        return 0
    fi
    
    # Delete the entire resource group (this removes all resources within it)
    log "This may take 10-20 minutes..."
    az group delete --name $RESOURCE_GROUP --yes --no-wait
    
    # Wait for deletion to complete
    log "Waiting for resource group deletion to complete..."
    while az group show --name $RESOURCE_GROUP &> /dev/null; do
        log "Resource group still exists, waiting..."
        sleep 60
    done
    
    success "Resource group deleted"
}

# Clean up local kubectl context
cleanup_kubectl_context() {
    log "Cleaning up local kubectl context..."
    
    if command -v kubectl &> /dev/null; then
        # Remove the cluster context
        kubectl config delete-context "$CLUSTER_NAME" 2>/dev/null || warn "kubectl context not found"
        kubectl config delete-cluster "$CLUSTER_NAME" 2>/dev/null || warn "kubectl cluster config not found"
        kubectl config unset "users.clusterUser_${RESOURCE_GROUP}_${CLUSTER_NAME}" 2>/dev/null || warn "kubectl user config not found"
        
        success "kubectl context cleaned up"
    else
        warn "kubectl not available. Skipping kubectl context cleanup."
    fi
}

# Verify cleanup
verify_cleanup() {
    log "Verifying cleanup..."
    
    # Check if resource group still exists
    if az group show --name $RESOURCE_GROUP &> /dev/null; then
        error "Resource group $RESOURCE_GROUP still exists. Manual cleanup may be required."
        return 1
    fi
    
    success "✅ All Azure resources have been successfully deleted"
    success "✅ No Azure charges should be incurred for these resources"
}

# Print summary
print_summary() {
    echo ""
    echo "=================================================="
    echo "🗑️  SELECTIVE DELETION COMPLETE!"
    echo "=================================================="
    echo "Deleted Resources:"
    
    if [ "$DELETE_INSTANCE" == "true" ]; then
        echo "  - DocumentDB instances and namespaces"
    fi
    
    if [ "$DELETE_OPERATOR" == "true" ]; then
        echo "  - DocumentDB operator"
    fi
    
    if [ "$DELETE_CLUSTER" == "true" ]; then
        echo "  - AKS Cluster: $CLUSTER_NAME"
        echo "  - Resource Group: $RESOURCE_GROUP"
        echo "  - All associated Azure resources"
    fi
    
    echo ""
    echo "✅ Cleanup completed successfully"
    
    if [ "$DELETE_CLUSTER" == "true" ]; then
        echo "✅ All Azure charges for these resources have been stopped"
        echo ""
        echo "💡 If you need to recreate the cluster:"
        echo "  ./create-cluster.sh --install-all"
    else
        echo ""
        echo "💡 Next steps based on what's still running:"
        if [ "$DELETE_INSTANCE" == "true" ] && [ "$DELETE_OPERATOR" == "false" ]; then
            echo "  - Deploy new instance: ./create-cluster.sh --deploy-instance"
        fi
        if [ "$DELETE_OPERATOR" == "true" ] && [ "$DELETE_CLUSTER" == "false" ]; then
            echo "  - Install operator: ./create-cluster.sh --install-operator"
            echo "  - Deploy instance: ./create-cluster.sh --deploy-instance"
        fi
    fi
    echo "=================================================="
}

# Main execution
main() {
    log "Starting DocumentDB AKS selective deletion..."
    log "Target cluster: $CLUSTER_NAME in resource group: $RESOURCE_GROUP"
    log "Deletion scope:"
    log "  Instance: $DELETE_INSTANCE"
    log "  Operator: $DELETE_OPERATOR" 
    log "  Cluster: $DELETE_CLUSTER"
    echo ""
    
    # Check if any deletion flag is set
    if [ "$DELETE_INSTANCE" != "true" ] && [ "$DELETE_OPERATOR" != "true" ] && [ "$DELETE_CLUSTER" != "true" ]; then
        error "No deletion scope specified. Use --delete-instance, --delete-operator, --delete-cluster, or --delete-all"
        exit 1
    fi
    
    # Execute deletion steps
    check_prerequisites
    confirm_deletion
    
    log "🗑️  Beginning selective deletion process..."
    
    # Selective deletion based on flags
    if [ "$DELETE_INSTANCE" == "true" ]; then
        delete_documentdb_instances
    fi
    
    if [ "$DELETE_OPERATOR" == "true" ]; then
        delete_documentdb_operator
    fi
    
    if [ "$DELETE_CLUSTER" == "true" ]; then
        delete_cert_manager
        delete_load_balancer_services
        delete_aks_cluster
        delete_resource_group
        cleanup_kubectl_context
        verify_cleanup
    fi
    
    # Show summary
    print_summary
}

# Handle script interruption
trap 'echo -e "\n${RED}Script interrupted. Some resources may not have been deleted.${NC}"; exit 1' INT

# Run main function
main "$@"