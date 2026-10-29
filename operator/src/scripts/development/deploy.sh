#!/bin/bash

set -e

# Configuration
REGISTRY=${REGISTRY:-"localhost:5001"}
# hard coded - only change if you Nknow what you are doing
OPERATOR_IMAGE=${OPERATOR_IMAGE:-"${REGISTRY}/operator"}
PLUGIN_IMAGE=${PLUGIN_IMAGE:-"${REGISTRY}/sidecar-injector"}
TAG=${TAG:-"0.1.1"}
SKIP_KIND_SETUP=${SKIP_KIND_SETUP:-"false"}
SKIP_CERT_MANAGER=${SKIP_CERT_MANAGER:-"false"}
DEPLOY_CLUSTER=${DEPLOY_CLUSTER:-"false"}
DEPLOY=${DEPLOY:-"false"}
DEPLOYMENT_METHOD=${DEPLOYMENT_METHOD:-"helm"}  # "helm" or "kustomize"

echo "=== DocumentDB Kubernetes Operator Deployment Script ==="
echo "Registry: ${REGISTRY}"
echo "Operator Image: ${OPERATOR_IMAGE}:${TAG}"
echo "Plugin Image: ${PLUGIN_IMAGE}:${TAG}"
echo "Deployment Method: ${DEPLOYMENT_METHOD}"
echo "Deploy DocumentDB Cluster: ${DEPLOY_CLUSTER}"
echo "Deploy DocumentDB Operator: ${DEPLOY}"
echo ""

# Function to setup Kind cluster with registry
setup_kind_cluster() {
    echo "=== Setting up Kind Cluster with Registry ==="
    
    # Check if Kind cluster already exists
    if kind get clusters 2>/dev/null | grep -q '^kind$'; then
        echo "Kind cluster 'kind' already exists"
        
        # Check if registry is running
        if docker ps --format '{{.Names}}' | grep -q '^kind-registry$'; then
            echo "Registry container 'kind-registry' is already running"
        else
            echo "Registry container not found, will recreate it"
            ./scripts/development/kind_with_registry.sh
        fi
    else
        echo "Creating new Kind cluster with registry..."
        ./scripts/development/kind_with_registry.sh
    fi
    
    # Wait a moment for cluster to be ready
    echo "Waiting for cluster to be ready..."
    kubectl cluster-info || {
        echo "Error: Failed to connect to Kind cluster"
        exit 1
    }
    
    echo "✓ Kind cluster with registry is ready"
}

# Function to install cert-manager
install_cert_manager() {
    echo "=== Installing cert-manager ==="
    
    # Check if cert-manager is already installed
    if kubectl get namespace cert-manager >/dev/null 2>&1; then
        echo "cert-manager namespace already exists, checking if it's running..."
        
        # Check if cert-manager pods are running
        if kubectl get pods -n cert-manager --no-headers 2>/dev/null | grep -q "Running"; then
            echo "cert-manager is already installed and running"
            return 0
        else
            echo "cert-manager namespace exists but pods are not running, reinstalling..."
        fi
    fi
    
    echo "Installing cert-manager using Helm..."
    
    # Add jetstack Helm repository
    helm repo add jetstack https://charts.jetstack.io --force-update
    helm repo update
    
    # Install cert-manager
    helm install cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --create-namespace \
        --set installCRDs=true
    
    # Wait for cert-manager to be ready
    echo "Waiting for cert-manager to be ready..."
    kubectl wait --for=condition=Ready pod -l app.kubernetes.io/instance=cert-manager -n cert-manager --timeout=300s
    
    # Verify cert-manager installation
    echo "Verifying cert-manager installation..."
    kubectl get pods -n cert-manager
    
    echo "✓ cert-manager installed and ready"
}

# Function to deploy DocumentDB credentials secret
deploy_documentdb_credentials() {
    echo "=== Deploying DocumentDB Credentials Secret ==="
    
    # Check if namespace exists
    if ! kubectl get namespace documentdb-preview-ns >/dev/null 2>&1; then
        echo "Creating documentdb-preview-ns namespace..."
        kubectl create namespace documentdb-preview-ns
    fi
    
    # Check if secret already exists
    if kubectl get secret documentdb-credentials -n documentdb-preview-ns >/dev/null 2>&1; then
        echo "DocumentDB credentials secret already exists"
        return 0
    fi
    
    echo "Creating DocumentDB credentials secret..."
    kubectl apply -f scripts/deployment-examples/documentdb-credentials-secret.yaml
    
    echo "✓ DocumentDB credentials secret deployed"
}

# Function to deploy DocumentDB cluster
deploy_documentdb_cluster() {
    echo "=== Deploying DocumentDB Cluster ==="
    
    # Deploy credentials first
    deploy_documentdb_credentials
    
    # Check if DocumentDB cluster already exists
    if kubectl get documentdb documentdb-preview -n documentdb-preview-ns >/dev/null 2>&1; then
        echo "DocumentDB cluster 'documentdb-preview' already exists"
        return 0
    fi
    
    echo "Deploying DocumentDB cluster..."
    cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-preview-ns
---
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
EOF
    
    # Wait for DocumentDB cluster to be ready
    echo "Waiting for DocumentDB cluster to be ready..."
    echo "This may take several minutes as the cluster initializes..."
    
    # Wait for the DocumentDB resource to be created
    kubectl wait --for=condition=Ready documentdb/documentdb-preview -n documentdb-preview-ns --timeout=600s || {
        echo "DocumentDB cluster is taking longer than expected to be ready."
        echo "You can check the status with:"
        echo "  kubectl get documentdb documentdb-preview -n documentdb-preview-ns"
        echo "  kubectl get pods -n documentdb-preview-ns"
        echo "  kubectl logs -n documentdb-operator -l control-plane=controller-manager"
    }
    
    echo "✓ DocumentDB cluster deployed"
}

# Function to deploy mongosh client
deploy_mongosh_client() {
    echo "=== Deploying MongoDB Shell Client ==="
    
    # Check if mongosh client already exists
    if kubectl get deployment mongosh-client -n documentdb-preview-ns >/dev/null 2>&1; then
        echo "MongoDB shell client already exists"
        return 0
    fi
    
    echo "Deploying MongoDB shell client..."
    kubectl apply -f scripts/deployment-examples/mongosh-client.yaml
    
    # Wait for mongosh client to be ready
    echo "Waiting for MongoDB shell client to be ready..."
    kubectl wait --for=condition=Available deployment/mongosh-client -n documentdb-preview-ns --timeout=300s
    
    echo "✓ MongoDB shell client deployed"
}

# Function to check if registry is reachable
check_registry() {
    echo "Checking registry connectivity..."
    if ! curl -f -s "http://${REGISTRY}/v2/" > /dev/null 2>&1; then
        echo "Warning: Registry ${REGISTRY} may not be reachable"
        if [ "${SKIP_KIND_SETUP}" = "false" ]; then
            echo "Setting up Kind cluster with registry..."
            setup_kind_cluster
        else
            echo "Make sure your registry is running (e.g., via kind_with_registry.sh)"
        fi
    else
        echo "Registry ${REGISTRY} is reachable"
    fi
}

# Build and push operator image
build_push_operator() {
    echo "=== Building and pushing DocumentDB Operator ==="
    echo "Building operator Docker image..."
    
    # Build the operator image using the main Makefile
    IMG="${OPERATOR_IMAGE}:${TAG}" make docker-build
    
    echo "Pushing operator image to registry..."
    IMG="${OPERATOR_IMAGE}:${TAG}" make docker-push
    
    echo "✓ Operator image built and pushed successfully"
}

# Build and push sidecar-injector plugin image
build_push_plugin() {
    echo "=== Building and pushing Sidecar Injector Plugin ==="
    echo "Building plugin Docker image..."
    
    # Change to plugin directory and build
    cd ../cnpg-plugins/sidecar-injector
    
    IMG="${PLUGIN_IMAGE}:${TAG}" make docker-build
    
    echo "Pushing plugin image to registry..."
    IMG="${PLUGIN_IMAGE}:${TAG}" make docker-push
    
    # Return to root directory
    cd ../..
    
    echo "✓ Plugin image built and pushed successfully"
}

# Deploy using Helm (install_operator.sh)
deploy_with_helm() {
    echo "=== Deploying with Helm ==="
    
    # Check if install_operator.sh exists
    if [ ! -f "scripts/operator/install_operator.sh" ]; then
        echo "Error: scripts/operator/install_operator.sh not found"
        exit 1
    fi
    
    # Make sure the script is executable
    chmod +x scripts/operator/install_operator.sh

    if helm status documentdb-operator -n documentdb-operator >/dev/null 2>&1; then
        echo "Existing DocumentDB operator release detected; uninstalling before redeploying..."
        if [ ! -f "scripts/operator/uninstall_operator.sh" ]; then
            echo "Error: scripts/operator/uninstall_operator.sh not found"
            exit 1
        fi
        chmod +x scripts/operator/uninstall_operator.sh
        if ! ./scripts/operator/uninstall_operator.sh; then
            echo "Error: Failed to uninstall existing DocumentDB operator release"
            exit 1
        fi
        echo "Previous DocumentDB operator release uninstalled"
    fi
    
    # Set environment variables for the Helm deployment
    export VERSION="1"
    export IMAGE_REGISTRY="${REGISTRY}"
    export IMAGE_TAG="${TAG}"
    
    echo "Using Helm deployment with:"
    echo "  Image Registry: ${IMAGE_REGISTRY}"
    echo "  Image Tag: ${IMAGE_TAG}"
    echo "  Version: ${VERSION}"
    
    # Run the install_operator.sh script
    ./scripts/operator/install_operator.sh
    
    echo "✓ Helm deployment completed successfully"
}

# Deploy using Kustomize (make deploy)
deploy_with_kustomize() {
    echo "=== Deploying with Kustomize ==="
    
    # Update the deployment to use the pushed images
    echo "Updating deployment manifests with new image references..."
    
    # Deploy using the main Makefile with custom image
    IMG="${OPERATOR_IMAGE}:${TAG}" make deploy
    
    echo "✓ Kustomize deployment completed successfully"
}

# Deploy to Kubernetes cluster
deploy_to_cluster() {
    echo "=== Deploying to Kubernetes Cluster ==="
    
    # Install cert-manager first (required dependency)
    if [ "${SKIP_CERT_MANAGER}" = "false" ]; then
        install_cert_manager
    else
        echo "Skipping cert-manager installation (SKIP_CERT_MANAGER=true)"
    fi
    
    # Deploy based on the chosen method
    case "${DEPLOYMENT_METHOD}" in
        "helm")
            deploy_with_helm
            ;;
        "kustomize")
            deploy_with_kustomize
            ;;
        *)
            echo "Error: Unknown deployment method '${DEPLOYMENT_METHOD}'. Use 'helm' or 'kustomize'"
            exit 1
            ;;
    esac
    
    # Wait a moment for operator to be ready before deploying DocumentDB cluster
    if [ "${DEPLOY_CLUSTER}" = "true" ]; then
        echo ""
        echo "Waiting for operator to be ready before deploying DocumentDB cluster..."
        kubectl wait --for=condition=Available deployment -l control-plane=controller-manager -n documentdb-operator --timeout=300s || {
            echo "Warning: Operator may not be fully ready yet, but continuing with cluster deployment..."
        }
        
        # Deploy DocumentDB cluster
        deploy_documentdb_cluster
        
        # Deploy mongosh client for easy testing
        deploy_mongosh_client
    fi
    
    # Show deployment status
    echo ""
    echo "Checking deployment status..."
    kubectl get pods -n documentdb-operator || echo "No pods found in documentdb-operator namespace yet"
    
    # Show cert-manager status
    echo ""
    echo "cert-manager status:"
    kubectl get pods -n cert-manager || echo "cert-manager not found"
    
    # Show DocumentDB cluster status if deployed
    if [ "${DEPLOY_CLUSTER}" = "true" ]; then
        echo ""
        echo "DocumentDB cluster status:"
        kubectl get documentdb -n documentdb-preview-ns || echo "No DocumentDB clusters found"
        kubectl get pods -n documentdb-preview-ns || echo "No pods found in documentdb-preview-ns namespace yet"
    fi
    
    # Show Helm releases if using Helm
    if [ "${DEPLOYMENT_METHOD}" = "helm" ]; then
        echo ""
        echo "Helm releases:"
        helm list -n documentdb-operator || echo "No Helm releases found in documentdb-operator namespace"
        helm list -n cert-manager || echo "No Helm releases found in cert-manager namespace"
    fi
}

# Function to show helpful commands
show_helpful_commands() {
    echo ""
    echo "=== Helpful Commands ==="
    echo "Check cluster status:"
    echo "  kubectl cluster-info "
    echo ""
    echo "Check registry contents:"
    echo "  curl http://localhost:5001/v2/_catalog"
    echo ""
    echo "Check operator logs:"
    echo "  kubectl logs -n documentdb-operator -l control-plane=controller-manager"
    echo ""
    echo "Check cert-manager status:"
    echo "  kubectl get pods -n cert-manager"
    echo "  kubectl logs -n cert-manager -l app.kubernetes.io/instance=cert-manager"
    echo ""
    
    if [ "${DEPLOY_CLUSTER}" = "true" ]; then
        echo "DocumentDB cluster commands:"
        echo "  kubectl get documentdb -n documentdb-preview-ns"
        echo "  kubectl get pods -n documentdb-preview-ns"
        echo "  kubectl logs -n documentdb-preview-ns -l postgresql=documentdb-preview"
        echo ""
        echo "Connect to DocumentDB using mongosh client:"
        echo "  kubectl exec -it deployment/mongosh-client -n documentdb-preview-ns -- sh"
        echo "  # Then inside the container:"
        echo "  mongosh \"mongodb://\$(kubectl get secret documentdb-credentials -n documentdb-preview-ns -o jsonpath='{.data.username}' | base64 -d):\$(kubectl get secret documentdb-credentials -n documentdb-preview-ns -o jsonpath='{.data.password}' | base64 -d)@documentdb-service-documentdb-preview:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0\""
        echo ""
        echo "Port forward for external access:"
        echo "  kubectl port-forward pod/documentdb-preview-1 10260:10260 -n documentdb-preview-ns"
        echo "  mongosh 127.0.0.1:10260 -u your_documentdb_user -p YourDocumentDBPassword100 --authenticationMechanism SCRAM-SHA-256 --tls --tlsAllowInvalidCertificates"
        echo ""
    fi
    
    if [ "${DEPLOYMENT_METHOD}" = "helm" ]; then
        echo "Helm-specific commands:"
        echo "  helm list -n documentdb-operator"
        echo "  helm list -n cert-manager"
        echo "  helm uninstall documentdb-operator -n documentdb-operator"
        echo "  helm uninstall cert-manager -n cert-manager"
        echo ""
    fi
    echo "Delete Kind cluster:"
    echo "  kind delete cluster"
    echo "  docker stop kind-registry && docker rm kind-registry"
    echo ""
    echo "Deployment options:"
    echo "  DEPLOYMENT_METHOD=helm ./scripts/development/deploy.sh"
    echo "  DEPLOYMENT_METHOD=kustomize ./scripts/development/deploy.sh"
    echo "  DEPLOY_CLUSTER=true ./scripts/development/deploy.sh"
    echo "  SKIP_CERT_MANAGER=true ./scripts/development/deploy.sh"
}

# Main execution
main() {
    # Check if we're in the right directory
    if [ ! -f "Makefile" ] || [ ! -d "../cnpg-plugins/sidecar-injector" ]; then
        echo "Error: This script must be run from the operator/src directory of the documentdb-kubernetes-operator repository"
        exit 1
    fi
    
    # Check if kind_with_registry.sh exists
    if [ ! -f "scripts/development/kind_with_registry.sh" ]; then
        echo "Error: scripts/development/kind_with_registry.sh not found"
        exit 1
    fi
    
    # Check if deployment examples exist
    if [ ! -f "scripts/deployment-examples/single-node-documentdb.yaml" ]; then
        echo "Error: scripts/deployment-examples/single-node-documentdb.yaml not found"
        exit 1
    fi
    
    # Make sure the script is executable
    chmod +x scripts/development/kind_with_registry.sh
    
    # Setup Kind cluster unless skipped
    if [ "${SKIP_KIND_SETUP}" = "false" ]; then
        setup_kind_cluster
    else
        echo "Skipping Kind cluster setup (SKIP_KIND_SETUP=true)"
        check_registry
    fi
    
    # Build and push both images
    build_push_operator
    build_push_plugin
    
    # Optionally deploy to cluster
    if [ "${DEPLOY:-false}" = "true" ]; then
        deploy_to_cluster
    else
        echo ""
        echo "Images built and pushed successfully!"
        echo "To deploy to your cluster, run:"
        echo "  DEPLOY=true $0"
        echo "Or manually deploy with:"
        if [ "${DEPLOYMENT_METHOD}" = "helm" ]; then
            echo "  IMAGE_REGISTRY=${REGISTRY} IMAGE_TAG=${TAG} ./scripts/operator/install_operator.sh"
        else
            echo "  IMG=${OPERATOR_IMAGE}:${TAG} make deploy"
        fi
        echo ""
        echo "To also deploy a DocumentDB cluster:"
        echo "  DEPLOY=true DEPLOY_CLUSTER=true $0"
        echo ""
        echo "Note: cert-manager will be installed automatically during deployment unless SKIP_CERT_MANAGER=true"
    fi
    
    echo ""
    echo "=== Deployment Summary ==="
    echo "Operator Image: ${OPERATOR_IMAGE}:${TAG}"
    echo "Plugin Image: ${PLUGIN_IMAGE}:${TAG}"
    echo "Registry: ${REGISTRY}"
    echo "Deployment Method: ${DEPLOYMENT_METHOD}"
    echo "Kind Cluster: $(kind get clusters 2>/dev/null | grep '^kind$' || echo 'Not running')"
    echo "cert-manager: $([ "${SKIP_CERT_MANAGER}" = "true" ] && echo "Skipped" || echo "Will be installed")"
    echo "DocumentDB Cluster: $([ "${DEPLOY_CLUSTER}" = "true" ] && echo "Will be deployed" || echo "Not deployed")"
    
    show_helpful_commands
}

# Run main function
main "$@"