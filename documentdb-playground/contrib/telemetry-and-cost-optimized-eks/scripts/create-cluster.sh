#!/bin/bash

# DocumentDB EKS Cluster Creation Script
# This script creates a complete EKS cluster with all dependencies for DocumentDB

set -e  # Exit on any error

# Configuration
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-contrib-cluster}"
REGION="us-west-2"
K8S_VERSION="${K8S_VERSION:-1.35}"
NODE_TYPE="${NODE_TYPE:-m7g.large}"
NODES=3
NODES_MIN=1
NODES_MAX=4

# Cost-optimization configuration
# USE_SPOT: when "true", eksctl provisions Spot-backed managed nodes (dev/test only).
# CLUSTER_TAGS: comma-separated key=value pairs passed to AWS for cost allocation in Cost Explorer.
USE_SPOT="${USE_SPOT:-false}"
CLUSTER_TAGS="${CLUSTER_TAGS:-project=documentdb-playground,environment=dev,managed-by=eksctl}"

# DocumentDB Operator Configuration
# For production: use documentdb/documentdb-operator (official)
OPERATOR_GITHUB_ORG="documentdb"
OPERATOR_CHART_VERSION="0.1.0"

# Feature flags - set to "true" to enable, "false" to skip
INSTALL_OPERATOR="${INSTALL_OPERATOR:-false}"
DEPLOY_INSTANCE="${DEPLOY_INSTANCE:-false}"

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
            INSTALL_OPERATOR="true"  # Auto-enable operator when instance is requested
            shift
            ;;
        --cluster-name)
            CLUSTER_NAME="$2"
            shift 2
            ;;
        --region)
            REGION="$2"
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
        --node-type)
            NODE_TYPE="$2"
            shift 2
            ;;
        --eks-version|--k8s-version)
            K8S_VERSION="$2"
            shift 2
            ;;
        --spot)
            USE_SPOT="true"
            shift
            ;;
        --tags)
            CLUSTER_TAGS="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --skip-operator       Skip DocumentDB operator installation (default)"
            echo "  --skip-instance       Skip DocumentDB instance deployment (default)"
            echo "  --install-operator    Install DocumentDB operator"
            echo "  --deploy-instance     Deploy DocumentDB instance"
            echo "  --cluster-name NAME   EKS cluster name (default: documentdb-contrib-cluster)"
            echo "  --region REGION       AWS region (default: us-west-2)"
            echo "  --github-username     GitHub username for operator installation"
            echo "  --github-token        GitHub token for operator installation"
            echo ""
            echo "Cost-optimization options:"
            echo "  --node-type TYPE      EC2 instance type (default: m7g.large, Graviton/ARM)"
            echo "  --eks-version VER     Kubernetes/EKS version (default: 1.35)"
            echo "  --spot                Use Spot-backed managed nodes (DEV/TEST ONLY - can be terminated)"
            echo "  --tags TAGS           Cost allocation tags as key=value pairs (comma-separated)"
            echo "                        (default: project=documentdb-playground,environment=dev,managed-by=eksctl)"
            echo ""
            echo "  -h, --help           Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                    # Create basic cluster only (no operator, no instance)"
            echo "  $0 --install-operator                 # Create cluster with operator, no instance"
            echo "  $0 --deploy-instance                  # Create cluster with instance (auto-enables operator)"
            echo "  $0 --github-username user --github-token ghp_xxx --install-operator  # With GitHub auth"
            echo "  $0 --node-type m5.large               # Use x86 instance type instead of Graviton"
            echo "  $0 --spot --tags \"project=myproj,team=platform\"  # Spot dev cluster with custom tags"
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

error_no_exit() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ❌ $1${NC}"
}

# Check prerequisites
check_prerequisites() {
    log "Checking prerequisites..."

    # Check AWS CLI
    if ! command -v aws &> /dev/null; then
        error "AWS CLI not found. Please install AWS CLI first."
    fi

    # Check eksctl
    if ! command -v eksctl &> /dev/null; then
        error "eksctl not found. Please install eksctl first."
    fi

    # Check kubectl
    if ! command -v kubectl &> /dev/null; then
        error "kubectl not found. Please install kubectl first."
    fi

    # Check Helm
    if ! command -v helm &> /dev/null; then
        error "Helm not found. Please install Helm first."
    fi

    # Check AWS credentials
    if ! aws sts get-caller-identity &> /dev/null; then
        error "AWS credentials not configured. Please run 'aws configure' first."
    fi

    # Optional but recommended: mongosh for endpoint validation. Mirrors the DocumentDB
    # troubleshooting best practice of verifying local client tooling before blaming the server.
    if ! command -v mongosh &> /dev/null; then
        warn "mongosh not found. Install with: brew install mongosh (macOS) or see https://www.mongodb.com/docs/mongodb-shell/install/"
        warn "Local connection validation (kubectl port-forward + mongosh) won't work until mongosh is installed."
    fi

    success "All prerequisites met"
}

# Show CloudFormation stack events for eksctl-managed stacks
show_cloudformation_events() {
    local status_filter="$1"  # "CREATE_FAILED" or "CREATE_COMPLETE"
    local stacks
    stacks=$(aws cloudformation list-stacks --region "$REGION" \
        --stack-status-filter CREATE_COMPLETE CREATE_FAILED ROLLBACK_COMPLETE ROLLBACK_IN_PROGRESS \
        --query "StackSummaries[?starts_with(StackName, 'eksctl-${CLUSTER_NAME}')].StackName" \
        --output text 2>/dev/null)

    if [ -z "$stacks" ]; then
        warn "No CloudFormation stacks found for cluster $CLUSTER_NAME"
        return
    fi

    for stack in $stacks; do
        log "CloudFormation stack: $stack"
        if [ "$status_filter" == "CREATE_FAILED" ]; then
            local failures
            failures=$(aws cloudformation describe-stack-events --region "$REGION" \
                --stack-name "$stack" \
                --query "StackEvents[?ResourceStatus=='CREATE_FAILED'].[Timestamp,LogicalResourceId,ResourceStatusReason]" \
                --output table 2>/dev/null)
            if [ -n "$failures" ] && ! echo "$failures" | grep -q "^$"; then
                error_no_exit "Failed resources in $stack:"
                echo "$failures"
            else
                success "No failures in $stack"
            fi
        else
            local stack_status
            stack_status=$(aws cloudformation describe-stacks --region "$REGION" \
                --stack-name "$stack" \
                --query "Stacks[0].StackStatus" --output text 2>/dev/null)
            log "  Status: $stack_status"
        fi
    done
}

# Create EKS cluster
create_cluster() {
    log "Creating EKS cluster: $CLUSTER_NAME in region: $REGION"

    # Check if cluster already exists
    if eksctl get cluster --name $CLUSTER_NAME --region $REGION &> /dev/null; then
        warn "Cluster $CLUSTER_NAME already exists. Skipping cluster creation."
        return 0
    fi

    if [ "$USE_SPOT" == "true" ]; then
        warn "============================================================"
        warn "SPOT INSTANCES ENABLED - FOR DEV/TEST USE ONLY"
        warn "AWS can terminate Spot instances at any time with 2 minutes"
        warn "notice. This WILL interrupt your database and require recovery."
        warn "Do NOT use Spot for production or long-running workloads."
        warn "============================================================"
    fi

    # 2 AZs is the minimum EKS supports, reduced from eksctl default of 3 for cost reasons.
    local EKSCTL_ARGS=(
        --name "$CLUSTER_NAME"
        --region "$REGION"
        --version "$K8S_VERSION"
        --nodes "$NODES"
        --nodes-min "$NODES_MIN"
        --nodes-max "$NODES_MAX"
        --managed
        --with-oidc
        --tags "$CLUSTER_TAGS"
    )

    if [ "$USE_SPOT" == "true" ]; then
        # Multiple instance types improve Spot availability; all Graviton to match the default.
        EKSCTL_ARGS+=(--spot --instance-types "m7g.large,m6g.large,r7g.large,r6g.large,c7g.large,c6g.large")
    else
        EKSCTL_ARGS+=(--node-type "$NODE_TYPE")
    fi

    eksctl create cluster "${EKSCTL_ARGS[@]}" --zones "${REGION}a,${REGION}b"
    local exit_code=$?

    log "Retrieving CloudFormation stack events..."
    if [ $exit_code -eq 0 ]; then
        show_cloudformation_events "CREATE_COMPLETE"
        success "EKS cluster created successfully"
    else
        show_cloudformation_events "CREATE_FAILED"
        error "Failed to create EKS cluster"
    fi
}

# Create VPC endpoints for cost optimization (S3 Gateway endpoint is free)
create_vpc_endpoints() {
    log "Creating VPC endpoints for cost optimization..."

    VPC_ID=$(aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" \
        --query 'cluster.resourcesVpcConfig.vpcId' --output text)

    if [ -z "$VPC_ID" ] || [ "$VPC_ID" = "None" ]; then
        warn "Could not determine VPC ID. Skipping VPC endpoint creation."
        return 0
    fi

    ROUTE_TABLE_IDS=$(aws ec2 describe-route-tables --region "$REGION" \
        --filters "Name=vpc-id,Values=$VPC_ID" \
        --query 'RouteTables[].RouteTableId' --output text)

    # S3 Gateway Endpoint (free - reduces NAT Gateway data transfer costs)
    if aws ec2 describe-vpc-endpoints --region "$REGION" \
        --filters "Name=vpc-id,Values=$VPC_ID" "Name=service-name,Values=com.amazonaws.$REGION.s3" \
        --query 'VpcEndpoints[0].VpcEndpointId' --output text 2>/dev/null | grep -q "vpce-"; then
        warn "S3 VPC endpoint already exists. Skipping creation."
    else
        aws ec2 create-vpc-endpoint \
            --vpc-id "$VPC_ID" \
            --service-name "com.amazonaws.$REGION.s3" \
            --route-table-ids $ROUTE_TABLE_IDS \
            --region "$REGION" 2>/dev/null && success "S3 Gateway VPC endpoint created (free)" || warn "Could not create S3 VPC endpoint"
    fi
}

# Install EBS CSI Driver
install_ebs_csi() {
    log "Installing EBS CSI Driver..."
    
    # Create EBS CSI service account with IAM role
    eksctl create iamserviceaccount \
        --cluster $CLUSTER_NAME \
        --namespace kube-system \
        --name ebs-csi-controller-sa \
        --attach-policy-arn arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy \
        --override-existing-serviceaccounts \
        --approve \
        --region $REGION
    
    # Install EBS CSI driver addon
    eksctl create addon \
        --name aws-ebs-csi-driver \
        --cluster $CLUSTER_NAME \
        --region $REGION \
        --force
    
    # Wait for EBS CSI driver to be ready
    log "Waiting for EBS CSI driver to be ready..."
    sleep 30
    kubectl wait --for=condition=ready pod -l app=ebs-csi-controller -n kube-system --timeout=300s || warn "EBS CSI driver pods may still be starting"
    
    success "EBS CSI Driver installed"
}

# Install AWS Load Balancer Controller
install_load_balancer_controller() {
    log "Installing AWS Load Balancer Controller..."
    
    # Check if already installed
    if helm list -n kube-system | grep -q aws-load-balancer-controller; then
        warn "AWS Load Balancer Controller already installed. Skipping installation."
        return 0
    fi
    
    # Get VPC ID for the cluster
    VPC_ID=$(aws eks describe-cluster --name $CLUSTER_NAME --region $REGION --query 'cluster.resourcesVpcConfig.vpcId' --output text)
    log "Using VPC ID: $VPC_ID"
    
    # Verify subnet tags for Load Balancer Controller
    log "Verifying subnet tags for Load Balancer Controller..."
    PUBLIC_SUBNETS=$(aws ec2 describe-subnets \
        --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=true" \
        --query 'Subnets[].SubnetId' --output text --region $REGION)
    
    PRIVATE_SUBNETS=$(aws ec2 describe-subnets \
        --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=false" \
        --query 'Subnets[].SubnetId' --output text --region $REGION)
    
    # Tag public subnets for internet-facing load balancers
    if [ -n "$PUBLIC_SUBNETS" ]; then
        log "Tagging public subnets for internet-facing load balancers..."
        for subnet in $PUBLIC_SUBNETS; do
            aws ec2 create-tags --resources "$subnet" --tags Key=kubernetes.io/role/elb,Value=1 --region $REGION 2>/dev/null || true
            log "Tagged public subnet: $subnet"
        done
    fi
    
    # Tag private subnets for internal load balancers
    if [ -n "$PRIVATE_SUBNETS" ]; then
        log "Tagging private subnets for internal load balancers..."
        for subnet in $PRIVATE_SUBNETS; do
            aws ec2 create-tags --resources "$subnet" --tags Key=kubernetes.io/role/internal-elb,Value=1 --region $REGION 2>/dev/null || true
            log "Tagged private subnet: $subnet"
        done
    fi
    
    # Download the official IAM policy (latest version)
    log "Downloading AWS Load Balancer Controller IAM policy (latest version)..."
    curl -o /tmp/iam_policy.json https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/main/docs/install/iam_policy.json
    
    # Get account ID
    ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
    
    # Check if policy exists and create/update as needed
    if aws iam get-policy --policy-arn arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy &>/dev/null; then
        log "IAM policy already exists, updating to latest version..."
        # Delete and recreate to ensure we have the latest version
        aws iam delete-policy --policy-arn arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy 2>/dev/null || true
        sleep 5  # Wait for deletion to propagate
    fi
    
    # Create IAM policy with latest permissions
    log "Creating IAM policy with latest permissions..."
    aws iam create-policy \
        --policy-name AWSLoadBalancerControllerIAMPolicy \
        --policy-document file:///tmp/iam_policy.json 2>/dev/null || \
    log "IAM policy already exists or was just created"
    
    # Wait a moment for policy to be available
    sleep 5
    
    # Create IAM service account with proper permissions using eksctl
    log "Creating IAM service account with proper permissions..."
    eksctl create iamserviceaccount \
        --cluster=$CLUSTER_NAME \
        --namespace=kube-system \
        --name=aws-load-balancer-controller \
        --role-name "AmazonEKSLoadBalancerControllerRole-$CLUSTER_NAME" \
        --attach-policy-arn=arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy \
        --approve \
        --override-existing-serviceaccounts \
        --region=$REGION
    
    # Add EKS Helm repository
    helm repo add eks https://aws.github.io/eks-charts
    helm repo update eks
    
    # Install Load Balancer Controller using the existing service account
    helm install aws-load-balancer-controller eks/aws-load-balancer-controller \
        -n kube-system \
        --set clusterName=$CLUSTER_NAME \
        --set serviceAccount.create=false \
        --set serviceAccount.name=aws-load-balancer-controller \
        --set region=$REGION \
        --set vpcId=$VPC_ID
    
    # Wait for Load Balancer Controller to be ready
    log "Waiting for Load Balancer Controller to be ready..."
    sleep 30
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=aws-load-balancer-controller -n kube-system --timeout=300s || warn "Load Balancer Controller pods may still be starting"
    
    # Clean up temp file
    rm -f /tmp/iam_policy.json
    
    success "AWS Load Balancer Controller installed"
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

# Create optimized storage class
create_storage_class() {
    log "Creating DocumentDB storage class..."
    
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
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
  iops: "3000"
  throughput: "125"
  fsType: ext4
  encrypted: "true"
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Retain
EOF
    
    success "DocumentDB storage class created"
}

# Install DocumentDB operator (optional)
install_documentdb_operator() {
    if [ "$INSTALL_OPERATOR" != "true" ]; then
        warn "Skipping DocumentDB operator installation (--skip-operator specified)"
        return 0
    fi
    
    log "Installing DocumentDB operator..."
    
    # Check if operator is already installed
    if helm list -n documentdb-operator | grep -q documentdb-operator; then
        warn "DocumentDB operator already installed. Skipping installation."
        return 0
    fi
    
    # Try public Helm repository first (no authentication required)
    log "Adding DocumentDB public Helm repository..."
    helm repo add documentdb https://documentdb.github.io/documentdb-kubernetes-operator 2>/dev/null || true
    helm repo update documentdb
    
    log "Installing DocumentDB operator from public Helm repository..."
    if helm install documentdb-operator documentdb/documentdb-operator \
        --namespace documentdb-operator \
        --create-namespace \
        --wait \
        --timeout 10m 2>/dev/null; then
        success "DocumentDB operator installed successfully from public Helm repository"
    else
        # Fallback to OCI registry with GitHub authentication
        warn "Public Helm repository installation failed. Falling back to OCI registry..."
        
        # Check for GitHub authentication
        if [ -z "$GITHUB_TOKEN" ] || [ -z "$GITHUB_USERNAME" ]; then
            error "DocumentDB operator installation requires GitHub authentication as fallback.

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
            success "DocumentDB operator installed successfully from OCI registry: ${OPERATOR_GITHUB_ORG}/documentdb-operator:${OPERATOR_CHART_VERSION}"
        else
            error "Failed to install DocumentDB operator. Please verify:
- Your GitHub token has 'read:packages' scope
- You have access to ${OPERATOR_GITHUB_ORG}/documentdb-operator repository  
- The chart version ${OPERATOR_CHART_VERSION} exists"
        fi
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
    
    # Deploy DocumentDB instance
    kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: sample-documentdb
  namespace: documentdb-instance-ns
spec:
  environment: eks
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
      storageClass: documentdb-storage
  exposeViaService:
    serviceType: LoadBalancer
  sidecarInjectorPluginName: cnpg-i-sidecar-injector.documentdb.io
EOF
    
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
    log "📝 Note: It takes 2-5 minutes for AWS to provision the LoadBalancer and assign a public IP"
    log "📝 AWS LoadBalancer annotations are automatically applied by the operator based on environment: eks"
}

# Print summary
print_summary() {
    echo ""
    echo "=================================================="
    echo "🎉 CLUSTER SETUP COMPLETE!"
    echo "=================================================="
    echo "Cluster Name: $CLUSTER_NAME"
    echo "Region: $REGION"
    echo "Kubernetes: $K8S_VERSION"
    echo "Node Type: $NODE_TYPE"
    echo "Spot Instances: $USE_SPOT"
    echo "Tags: $CLUSTER_TAGS"
    echo "Operator Installed: $INSTALL_OPERATOR"
    echo "Instance Deployed: $DEPLOY_INSTANCE"
    echo ""
    echo "✅ Components installed:"
    echo "  - EKS cluster with managed nodes ($NODE_TYPE)"
    echo "  - S3 Gateway VPC endpoint (cost optimization)"
    echo "  - EBS CSI driver"
    echo "  - AWS Load Balancer Controller"
    echo "  - cert-manager"
    echo "  - DocumentDB storage class"
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
    echo "⚠️  IMPORTANT: Run './delete-cluster.sh' when done to avoid AWS charges!"
    echo "=================================================="
}

# Main execution
main() {
    log "Starting DocumentDB EKS cluster setup..."
    log "Configuration:"
    log "  Cluster: $CLUSTER_NAME"
    log "  Region: $REGION"
    log "  Kubernetes: $K8S_VERSION"
    log "  Node Type: $NODE_TYPE"
    log "  Spot Instances: $USE_SPOT"
    log "  Tags: $CLUSTER_TAGS"
    log "  Install Operator: $INSTALL_OPERATOR"
    log "  Deploy Instance: $DEPLOY_INSTANCE"
    echo ""
    
    # Execute setup steps
    check_prerequisites
    create_cluster
    create_vpc_endpoints
    install_ebs_csi
    install_load_balancer_controller
    install_cert_manager
    create_storage_class
    
    # Optional components
    install_documentdb_operator
    deploy_documentdb_instance
    
    # Show summary
    print_summary
}

# Run main function
main "$@"
