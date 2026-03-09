#!/bin/bash

# DocumentDB AKS Cluster Creation Script
# This script creates a complete AKS cluster with all dependencies for DocumentDB

#set -e  # Exit on any error

# Configuration
CLUSTER_NAME="guanzhou-101401-cluster"
RESOURCE_GROUP="guanzhou-101401-rg"
LOCATION="East US 2"
NODE_COUNT=2
NODE_SIZE="Standard_D8s_v5"
KUBERNETES_VERSION="1.31.11"

# DocumentDB Operator Configuration
# For testing: use hossain-rayhan/documentdb-operator (fork with Azure enhancements)
# For production: use microsoft/documentdb-operator (official)
OPERATOR_GITHUB_ORG="documentdb"
OPERATOR_CHART_VERSION="0.1.0"
# Feature flags - set to "true" to enable, "false" to skip
INSTALL_OPERATOR="${INSTALL_OPERATOR:-false}"
DEPLOY_INSTANCE="${DEPLOY_INSTANCE:-false}"
CREATE_STORAGE_CLASS="${CREATE_STORAGE_CLASS:-false}"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-operator)
            INSTALL_OPERATOR="false"
            shift
            ;;
                --skip-instance)
            DEPLOY_INSTANCE="false"
            shift
            ;;
        --install-operator)
            INSTALL_OPERATOR="true"
            shift
            ;;
        --deploy-instance)
            DEPLOY_INSTANCE="true"
            shift
            ;;
        --install-all)
            INSTALL_OPERATOR="true"
            DEPLOY_INSTANCE="true"
            shift
            ;;
        --create-storage-class)
            CREATE_STORAGE_CLASS="true"
            shift
            ;;
        --skip-storage-class)
            CREATE_STORAGE_CLASS="false"
            shift
            ;;
        --cluster-name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --resource-group)
            RESOURCE_GROUP="$2"
            shift 2
            ;;
        --location)
            LOCATION="$2"
            shift 2
            ;;
        --github-username)
            GITHUB_USERNAME="$2"
            shift 2
            ;;
        --github-token)
            GITHUB_TOKEN="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --skip-operator         Skip DocumentDB operator installation (default)"
            echo "  --skip-instance         Skip DocumentDB instance deployment (default)"
            echo "  --install-operator      Install DocumentDB operator only (assumes cluster exists)"
            echo "  --deploy-instance       Deploy DocumentDB instance only (assumes cluster+operator exist)"
            echo "  --install-all           Create cluster + install operator + deploy instance"
            echo "  --create-storage-class  Create custom Premium SSD storage class"
            echo "  --skip-storage-class    Use AKS default storage (StandardSSD_LRS) - default"
            echo "  --cluster-name NAME     AKS cluster name (default: documentdb-cluster)"
            echo "  --resource-group RG     Azure resource group (default: documentdb-rg)"
            echo "  --location LOCATION     Azure location (default: East US)"
            echo "  --github-username       GitHub username for operator installation"
            echo "  --github-token          GitHub token for operator installation"
            echo "  -h, --help             Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                    # Create cluster only"
            echo "  $0 --install-operator                 # Install operator only (assumes cluster exists)"
            echo "  $0 --deploy-instance                  # Deploy DocumentDB only (assumes cluster+operator exist)"
            echo "  $0 --install-all                      # Create cluster + install operator + deploy instance"
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
    exit 1
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."
    
    # Check Azure CLI
    if ! command -v az &> /dev/null; then
        error "Azure CLI not found. Please install Azure CLI first."
    fi
    
    # Check kubectl
    if ! command -v kubectl &> /dev/null; then
        error "kubectl not found. Please install kubectl first."
    fi
    
    # Check Helm
    if ! command -v helm &> /dev/null; then
        error "Helm not found. Please install Helm first."
    fi
    
    # Check Azure login
    if ! az account show &> /dev/null; then
        error "Not logged into Azure. Please run 'az login' first."
    fi
    
    success "All prerequisites met"
}

# Create resource group
create_resource_group() {
    log "Creating resource group: $RESOURCE_GROUP in location: $LOCATION"
    
    # Check if resource group already exists
    if az group show --name $RESOURCE_GROUP &> /dev/null; then
        warn "Resource group $RESOURCE_GROUP already exists. Skipping creation."
        return 0
    fi
    
    # Create resource group
    az group create --name $RESOURCE_GROUP --location "$LOCATION"
    
    if [ $? -eq 0 ]; then
        success "Resource group created successfully"
    else
        error "Failed to create resource group"
    fi
}

# Create AKS cluster
create_cluster() {
    log "Creating AKS cluster: $CLUSTER_NAME"
    
    # Check if cluster already exists
    if az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME &> /dev/null; then
        warn "Cluster $CLUSTER_NAME already exists. Skipping cluster creation."
    else
        # Create AKS cluster with managed identity and required addons
        az aks create \
            --resource-group $RESOURCE_GROUP \
            --name $CLUSTER_NAME \
            --node-count $NODE_COUNT \
            --node-vm-size $NODE_SIZE \
            --kubernetes-version $KUBERNETES_VERSION \
            --enable-managed-identity \
            --enable-addons monitoring \
            --enable-cluster-autoscaler \
            --min-count 2 \
            --max-count 5 \
            --generate-ssh-keys \
            --network-plugin azure \
            --network-policy azure \
            --load-balancer-sku standard
        
        if [ $? -eq 0 ]; then
            success "AKS cluster created successfully"
        else
            error "Failed to create AKS cluster"
        fi
    fi
    
    # Get cluster credentials
    log "Getting cluster credentials..."
    az aks get-credentials --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --overwrite-existing
    
    # Handle WSL case - copy Windows kubeconfig to WSL
    if grep -qi microsoft /proc/version 2>/dev/null; then
        log "Detected WSL environment, copying kubeconfig from Windows to WSL..."
        WIN_KUBE_CONFIG="/mnt/c/Users/$(whoami)/.kube/config"
        if [ -f "$WIN_KUBE_CONFIG" ]; then
            mkdir -p ~/.kube
            cp "$WIN_KUBE_CONFIG" ~/.kube/config
            chmod 600 ~/.kube/config
            log "Kubeconfig copied to WSL"
        else
            warn "Windows kubeconfig not found at expected location"
        fi
    fi
    
    success "Cluster credentials configured"
}

# Install Azure CSI drivers
install_azure_csi_drivers() {
    log "Checking Azure CSI drivers..."
    
    # Check if CSI drivers are already enabled (modern AKS clusters have them by default)
    CSI_STATUS=$(az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --query "storageProfile" -o json 2>/dev/null)
    DISK_CSI_ENABLED=$(echo "$CSI_STATUS" | jq -r '.diskCsiDriver.enabled // false')
    FILE_CSI_ENABLED=$(echo "$CSI_STATUS" | jq -r '.fileCsiDriver.enabled // false')
    
    if [ "$DISK_CSI_ENABLED" == "true" ] && [ "$FILE_CSI_ENABLED" == "true" ]; then
        success "Azure CSI drivers already enabled (Disk: ✅, File: ✅)"
        return 0
    fi
    
    log "CSI drivers not fully enabled - installing..."
    log "Current status: Disk=$DISK_CSI_ENABLED, File=$FILE_CSI_ENABLED"
    
    # Azure Disk CSI driver (only if not enabled)
    if [ "$DISK_CSI_ENABLED" != "true" ]; then
        log "Enabling Azure Disk CSI driver..."
        az aks update --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --enable-disk-driver >/dev/null 2>&1
    fi
    
    # Azure File CSI driver (only if not enabled)
    if [ "$FILE_CSI_ENABLED" != "true" ]; then
        log "Enabling Azure File CSI driver..."
        az aks update --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --enable-file-driver >/dev/null 2>&1
    fi
    
    success "Azure CSI drivers configured"
}

# Verify Azure Load Balancer (built-in to AKS)
configure_load_balancer() {
    log "Verifying Azure Load Balancer..."
    
    # Azure Load Balancer is built into AKS, just verify it's working
    if kubectl get service kubernetes -n default >/dev/null 2>&1; then
        success "Azure Load Balancer verified (built-in to AKS)"
    else
        warn "Unable to verify Kubernetes API service"
    fi
}

# Install cert-manager
install_cert_manager() {
    log "Installing cert-manager..."
    
    # Check if already installed
    if helm list -n cert-manager | grep -q cert-manager; then
        warn "cert-manager already installed. Skipping installation."
        return 0
    fi
    
    # Add Jetstack Helm repository
    helm repo add jetstack https://charts.jetstack.io
    helm repo update
    
    # Install cert-manager
    helm install cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --create-namespace \
        --version v1.13.2 \
        --set installCRDs=true \
        --set prometheus.enabled=false \
        --set webhook.timeoutSeconds=30
    
    # Wait for cert-manager to be ready
    log "Waiting for cert-manager to be ready..."
    sleep 30
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/instance=cert-manager -n cert-manager --timeout=300s || warn "cert-manager pods may still be starting"
    
    success "cert-manager installed"
}

# Create optimized storage class for Azure (optional)
create_storage_class() {
    if [ "$CREATE_STORAGE_CLASS" != "true" ]; then
        warn "Skipping custom storage class creation (using AKS default StandardSSD_LRS)"
        return 0
    fi
    
    log "Creating DocumentDB custom Premium SSD storage class..."
    
    # Check if storage class already exists
    if kubectl get storageclass documentdb-storage &> /dev/null; then
        warn "DocumentDB storage class already exists. Skipping creation."
        return 0
    fi
    
    kubectl apply -f - <<EOF
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: documentdb-storage
  annotations:
    storageclass.kubernetes.io/is-default-class: "false"
provisioner: disk.csi.azure.com
parameters:
  skuName: Premium_LRS
  kind: Managed
  diskEncryptionSetID: ""
  writeAcceleratorEnabled: "false"
  networkAccessPolicy: AllowAll
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Retain
EOF
    
    success "DocumentDB Premium SSD storage class created"
}

# Install DocumentDB operator (optional)
install_documentdb_operator() {
    if [ "$INSTALL_OPERATOR" != "true" ]; then
        warn "Skipping DocumentDB operator installation (--skip-operator specified)"
        return 0
    fi
    
    log "Installing DocumentDB operator from GitHub registry..."
    
    # Check if operator is already installed
    if helm list -n documentdb-operator | grep -q documentdb-operator; then
        warn "DocumentDB operator already installed. Skipping installation."
        return 0
    fi
    
    # Test internet connectivity to GitHub registry
    log "Testing connectivity to GitHub Container Registry..."
    if ! curl -s --connect-timeout 10 https://ghcr.io > /dev/null; then
        error "Cannot reach ghcr.io. Please check your internet connection and firewall settings."
    fi
    
    # Install DocumentDB operator using enhanced fork with Azure support
    log "Installing DocumentDB operator from GitHub Container Registry (enhanced fork with Azure support)..."
    
    # Check for GitHub authentication
    if [ -z "$GITHUB_TOKEN" ] || [ -z "$GITHUB_USERNAME" ]; then
        error "DocumentDB operator installation requires GitHub authentication.

Please set the following environment variables:
  export GITHUB_USERNAME='your-github-username'
  export GITHUB_TOKEN='your-github-token'

To create a GitHub token:
1. Go to https://github.com/settings/tokens
2. Generate a new token with 'read:packages' scope
3. Export the token as shown above

Then run the script again with --install-operator"
    fi
    
    # Authenticate with GitHub Container Registry
    log "Authenticating with GitHub Container Registry..."
    if ! echo "$GITHUB_TOKEN" | helm registry login ghcr.io --username "$GITHUB_USERNAME" --password-stdin; then
        error "Failed to authenticate with GitHub Container Registry. Please verify your GITHUB_TOKEN and GITHUB_USERNAME."
    fi
    
    # Install DocumentDB operator from OCI registry
    log "Pulling and installing DocumentDB operator from ghcr.io/${OPERATOR_GITHUB_ORG}/documentdb-operator..."
    helm install documentdb-operator \
        oci://ghcr.io/${OPERATOR_GITHUB_ORG}/documentdb-operator \
        --version ${OPERATOR_CHART_VERSION} \
        --namespace documentdb-operator \
        --create-namespace \
        --wait \
        --timeout 10m

    if [ $? -eq 0 ]; then
        success "DocumentDB operator installed successfully from ${OPERATOR_GITHUB_ORG}/documentdb-operator:${OPERATOR_CHART_VERSION}"
    else
        error "Failed to install DocumentDB operator from OCI registry. Please verify:
- Your GitHub token has 'read:packages' scope
- You have access to ${OPERATOR_GITHUB_ORG}/documentdb-operator repository  
- The chart version ${OPERATOR_CHART_VERSION} exists"
    fi
    
    # Wait for operator to be ready
    log "Waiting for DocumentDB operator to be ready..."
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=documentdb-operator -n documentdb-operator --timeout=300s || warn "DocumentDB operator pods may still be starting"
    
    success "DocumentDB operator installed"
}

# Deploy DocumentDB instance (optional)
deploy_documentdb_instance() {
    if [ "$DEPLOY_INSTANCE" != "true" ]; then
        warn "Skipping DocumentDB instance deployment (--skip-instance specified or not enabled)"
        return 0
    fi
    
    log "Deploying DocumentDB instance..."
    
    # Check if operator is installed
    if ! kubectl get deployment -n documentdb-operator documentdb-operator &> /dev/null; then
        error "DocumentDB operator not found. Cannot deploy instance without operator."
    fi
    
    # Create DocumentDB namespace
    kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-instance-ns
EOF
    
    # Create credentials secret
    kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-instance-ns
type: Opaque
stringData:
  username: docdbadmin
  password: SecurePassword123!
EOF
    
    # Deploy DocumentDB instance with appropriate storage configuration
    if [ "$CREATE_STORAGE_CLASS" = "true" ]; then
        # Use custom Premium SSD storage class
        kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: sample-documentdb
  namespace: documentdb-instance-ns
spec:
  environment: aks
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
      storageClass: documentdb-storage  # Custom Premium SSD
  exposeViaService:
    serviceType: LoadBalancer
  sidecarInjectorPluginName: cnpg-i-sidecar-injector.documentdb.io
EOF
    else
        # Use AKS default storage (StandardSSD_LRS)
        kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: sample-documentdb
  namespace: documentdb-instance-ns
spec:
  environment: aks
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
      # storageClass omitted - uses AKS default (StandardSSD_LRS)
  exposeViaService:
    serviceType: LoadBalancer
  sidecarInjectorPluginName: cnpg-i-sidecar-injector.documentdb.io
EOF
    fi
    
    # Wait for DocumentDB to be ready
    log "Waiting for DocumentDB instance to be ready (this may take several minutes)..."
    kubectl wait --for=condition=ready documentdb sample-documentdb --timeout=600s || warn "DocumentDB instance may still be starting"
    
    success "DocumentDB instance deployed"
    
    # Show connection info
    log "DocumentDB instance connection information:"
    kubectl get documentdb sample-documentdb -o wide
    
    log ""
    log "🔍 To monitor the service and get the external IP:"
    log "  kubectl get service -n documentdb-instance-ns"
    log ""
    log "📝 Note: It takes 2-5 minutes for Azure to provision the LoadBalancer and assign a public IP"
    log "📝 Azure LoadBalancer annotations are automatically applied by the operator based on environment: aks"
}

# Print summary
print_summary() {
    echo ""
    echo "=================================================="
    echo "🎉 AKS CLUSTER SETUP COMPLETE!"
    echo "=================================================="
    echo "Cluster Name: $CLUSTER_NAME"
    echo "Resource Group: $RESOURCE_GROUP"
    echo "Location: $LOCATION"
    echo "Operator Installed: $INSTALL_OPERATOR"
    echo "Instance Deployed: $DEPLOY_INSTANCE"
    echo "Custom Storage Class: $CREATE_STORAGE_CLASS"
    echo ""
    echo "✅ Components installed:"
    echo "  - AKS cluster with managed nodes"
    echo "  - Azure CSI drivers (Disk & File)"
    echo "  - Azure Load Balancer (built-in)"
    echo "  - cert-manager"
    if [ "$CREATE_STORAGE_CLASS" == "true" ]; then
        echo "  - DocumentDB Premium SSD storage class"
    else
        echo "  - Using AKS default StandardSSD_LRS storage"
    fi
    if [ "$INSTALL_OPERATOR" == "true" ]; then
        echo "  - DocumentDB operator"
    fi
    if [ "$DEPLOY_INSTANCE" == "true" ]; then
        echo "  - DocumentDB instance (sample-documentdb)"
    fi
    echo ""
    echo "💡 Next steps:"
    echo "  - Verify cluster: kubectl get nodes"
    echo "  - Check all pods: kubectl get pods --all-namespaces"
    if [ "$INSTALL_OPERATOR" == "true" ]; then
        echo "  - Check operator: kubectl get pods -n documentdb-operator"
    fi
    if [ "$DEPLOY_INSTANCE" == "true" ]; then
        echo "  - Check DocumentDB: kubectl get documentdb -n documentdb-instance-ns"
        echo "  - Check service status: kubectl get svc -n documentdb-instance-ns"
        echo "  - Wait for LoadBalancer IP: kubectl get svc documentdb-service-sample-documentdb -n documentdb-instance-ns -w"
        echo "  - Once IP is assigned, connect: mongodb://docdbadmin:SecurePassword123!@<EXTERNAL-IP>:10260/"
    fi
    echo ""
    echo "⚠️  IMPORTANT: Run './delete-cluster.sh' when done to avoid Azure charges!"
    echo "=================================================="
}

# Main execution
main() {
    log "Starting DocumentDB AKS cluster setup..."
    log "Configuration:"
    log "  Cluster: $CLUSTER_NAME"
    log "  Resource Group: $RESOURCE_GROUP"
    log "  Location: $LOCATION"
    log "  Install Operator: $INSTALL_OPERATOR"
    log "  Deploy Instance: $DEPLOY_INSTANCE"
    echo ""
    
    check_prerequisites
    
    # Simple logic based on parameters
    if [ "$INSTALL_OPERATOR" == "true" ] && [ "$DEPLOY_INSTANCE" != "true" ]; then
        # Case 1: --install-operator only
        log "🔧 Installing operator only (assumes cluster exists)"
        setup_kubeconfig
        install_documentdb_operator
        
    elif [ "$DEPLOY_INSTANCE" == "true" ] && [ "$INSTALL_OPERATOR" != "true" ]; then
        # Case 2: --deploy-instance only  
        log "🚀 Deploying DocumentDB instance only (assumes cluster+operator exist)"
        setup_kubeconfig
        deploy_documentdb_instance
        
    elif [ "$INSTALL_OPERATOR" == "true" ] && [ "$DEPLOY_INSTANCE" == "true" ]; then
        # Case 3: --install-all (both flags set)
        log "🎯 Installing everything: cluster + operator + instance"
        setup_cluster_infrastructure
        install_documentdb_operator
        deploy_documentdb_instance
        
    else
        # Case 4: No flags - create cluster only
        log "🏗️  Creating cluster only (no operator, no instance)"
        setup_cluster_infrastructure
    fi
    
    print_summary
}

# Helper function to set up cluster infrastructure
setup_cluster_infrastructure() {
    # Check if cluster already exists
    CLUSTER_EXISTS=$(az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --query "name" -o tsv 2>/dev/null)
    
    if [ "$CLUSTER_EXISTS" == "$CLUSTER_NAME" ]; then
        log "✅ Cluster $CLUSTER_NAME already exists, skipping infrastructure setup"
        setup_kubeconfig
    else
        log "Creating new cluster and infrastructure..."
        create_resource_group
        create_cluster
        install_azure_csi_drivers
        configure_load_balancer
        install_cert_manager
        create_storage_class
    fi
}

# Helper function to set up kubeconfig
setup_kubeconfig() {
    # Verify cluster exists
    if ! az aks show --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME >/dev/null 2>&1; then
        error "Cluster $CLUSTER_NAME not found. Create cluster first."
    fi
    
    # Get cluster credentials
    log "Getting cluster credentials..."
    az aks get-credentials --resource-group $RESOURCE_GROUP --name $CLUSTER_NAME --overwrite-existing
    
    # Handle WSL case
    if grep -qi microsoft /proc/version 2>/dev/null; then
        log "Detected WSL environment, copying kubeconfig from Windows to WSL..."
        WIN_KUBE_CONFIG="/mnt/c/Users/$(whoami)/.kube/config"
        if [ -f "$WIN_KUBE_CONFIG" ]; then
            mkdir -p ~/.kube
            cp "$WIN_KUBE_CONFIG" ~/.kube/config
            chmod 600 ~/.kube/config
            log "Kubeconfig copied to WSL"
        fi
    fi
    
    success "Cluster credentials configured"
}

# Run main function
main "$@"