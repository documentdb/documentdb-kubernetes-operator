#!/bin/bash

# Multi-Tenant DocumentDB + Telemetry Deployment Script
# This script deploys complete DocumentDB clusters with isolated monitoring stacks for different teams

set -e

# Deployment options
SKIP_WAIT=true

# Parse command line arguments
usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --skip-wait         Skip waiting for deployments to be ready"
    echo "  --help              Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                    # Deploy everything (DocumentDB + Telemetry)"
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-wait)
            SKIP_WAIT=true
            shift
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
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

log() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} $1"
}

success() {
    echo -e "${GREEN}[$(date +'%Y-%m-%d %H:%M:%S')] ✅${NC} $1"
}

warn() {
    echo -e "${YELLOW}[$(date +'%Y-%m-%d %H:%M:%S')] ⚠️${NC} $1"
}

error() {
    echo -e "${RED}[$(date +'%Y-%m-%d %H:%M:%S')] ❌${NC} $1"
    exit 1
}

# Check if OpenTelemetry Operator is installed
check_prerequisites() {
    log "Checking prerequisites..."
    
    if ! helm version > /dev/null 2>&1; then
        error "Helm is not installed. Please install Helm first."
    fi
    
    # Add Prometheus Helm repo if not already added
    if ! helm repo list | grep -q prometheus-community; then
        log "Adding Prometheus Helm repository..."
        helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
        helm repo update
    fi
    
    # Add Grafana Helm repo if not already added
    if ! helm repo list | grep -q grafana; then
        log "Adding Grafana Helm repository..."
        helm repo add grafana https://grafana.github.io/helm-charts
        helm repo update
    fi
    
    success "Prerequisites check completed"
}

install_opentelemetry_operator() {

    kubectl config use-context hub

    log "Installing OpenTelemetry Operator (infrastructure component)..."
    
    # Check if already installed
    if kubectl get deployment opentelemetry-operator-controller-manager -n opentelemetry-operator-system &> /dev/null; then
        warn "OpenTelemetry Operator already installed. Skipping installation."
        return 0
    fi
    
    # Install OpenTelemetry Operator on hub
    log "Installing OpenTelemetry Operator from upstream..."
    kubectl apply -f https://github.com/open-telemetry/opentelemetry-operator/releases/latest/download/opentelemetry-operator.yaml

    # Create ClusterResourcePlacement to deploy operator to all member clusters
    log "Creating ClusterResourcePlacement for OpenTelemetry Operator..."
    cat <<EOF | kubectl apply -f -
apiVersion: placement.kubernetes-fleet.io/v1beta1
kind: ClusterResourcePlacement
metadata:
  name: opentelemetry-operator-crp
spec:
  resourceSelectors:
    - group: ""
      version: v1
      kind: Namespace
      name: opentelemetry-operator-system
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: instrumentations.opentelemetry.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: opampbridges.opentelemetry.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: opentelemetrycollectors.opentelemetry.io
    - group: "apiextensions.k8s.io"
      version: v1
      kind: CustomResourceDefinition
      name: targetallocators.opentelemetry.io
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: opentelemetry-operator-manager-role
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: opentelemetry-operator-metrics-reader
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRole
      name: opentelemetry-operator-proxy-role
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRoleBinding
      name: opentelemetry-operator-manager-rolebinding
    - group: "rbac.authorization.k8s.io"
      version: v1
      kind: ClusterRoleBinding
      name: opentelemetry-operator-proxy-rolebinding
    - group: "admissionregistration.k8s.io"
      version: v1
      kind: MutatingWebhookConfiguration
      name: opentelemetry-operator-mutating-webhook-configuration
    - group: "admissionregistration.k8s.io"
      version: v1
      kind: ValidatingWebhookConfiguration
      name: opentelemetry-operator-validating-webhook-configuration
  policy:
    placementType: PickAll
  strategy:
    type: RollingUpdate
EOF
    
    log "Waiting for OpenTelemetry Operator to be ready..."
    
    primary=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.primary}')
    # Wait for ns to propagate
    sleep 10
    kubectl --context "$primary" wait --for=condition=available deployment/opentelemetry-operator-controller-manager -n opentelemetry-operator-system --timeout=300s || warn "OpenTelemetry Operator may still be starting"

    success "OpenTelemetry Operator installed and ClusterResourcePlacement created"
}


# Deploy Prometheus for a namespace
deploy_prometheus() {
    local namespace=$1
    
    log "Deploying Prometheus in namespace: $namespace"
    
    # Get the directory where this script is located
    SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
    
    if [ ! -f "$SCRIPT_DIR/prometheus-values.yaml" ]; then
        error "Prometheus values file not found: $SCRIPT_DIR/prometheus-values.yaml"
    fi
    
    helm upgrade --install prometheus prometheus-community/prometheus \
        --namespace $namespace \
        --values "$SCRIPT_DIR/prometheus-values.yaml" \
        --wait --timeout=300s
    
    success "Prometheus deployed"
}

# Deploy Grafana for a namespace
deploy_grafana() {
    local namespace=$1
    
    log "Deploying Grafana in namespace: $namespace"
    
    # Get the directory where this script is located
    SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
    
    if [ ! -f "$SCRIPT_DIR/grafana-values.yaml" ]; then
        error "Grafana values file not found: $SCRIPT_DIR/grafana-values.yaml"
    fi
    
    helm upgrade --install grafana grafana/grafana \
        --namespace $namespace \
        --values "$SCRIPT_DIR/grafana-values.yaml" \
        --wait --timeout=300s
    
    success "Grafana deployed"
}

# Deploy OpenTelemetry collectors for each member
# TODO figure out how to do this with fleet, currently can't deploy without the operator running (opentelemetry-operator-webhook-service)
deploy_collectors() {
    log "Deploying OpenTelemetry collector to each member cluster..."
    
    # Get the directory where this script is located
    SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
    
    # Get member clusters and primary cluster from documentdb resource
    MEMBER_CLUSTERS=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o json 2>/dev/null | jq -r '.spec.clusterReplication.clusterList[].name' 2>/dev/null || echo "")
    
    # Deploy to each member cluster
    for cluster in $MEMBER_CLUSTERS; do
        log "Waiting for OpenTelemetry Operator webhook service on cluster: $cluster"
        kubectl --context "$cluster" wait --for=jsonpath='{.subsets[*].addresses[*].ip}' endpoints/opentelemetry-operator-webhook-service -n opentelemetry-operator-system --timeout=300s || warn "Webhook service not ready on $cluster, proceeding anyway..."
        
        log "Deploying OpenTelemetry Collector to cluster: $cluster"
        sed "s/{{CLUSTER_NAME}}/$cluster/g" "$SCRIPT_DIR/otel-collector.yaml" | kubectl --context "$cluster" apply -f -
    done
    success "All collectors deployed"
}

# Deploy monitoring stack only on primary
deploy_monitoring_stack() {

    primary=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.primary}')
    kubectl config use-context "$primary"

    log "Deploying monitoring stack to primary"
    
    deploy_prometheus documentdb-preview-ns
    deploy_grafana documentdb-preview-ns
    
    success "All monitoring stacks deployed"
}

# Create placeholder OTEL collector services on primary cluster for non-primary members
create_placeholder_prometheus_services() {
    log "Creating placeholder OTEL collector services on primary cluster..."
    
    # Get primary cluster and all member clusters
    #PRIMARY_CLUSTER=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.primary}' 2>/dev/null || echo "")
    PRIMARY_CLUSTER="azure-documentdb"
    MEMBER_CLUSTERS=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o json 2>/dev/null | jq -r '.spec.clusterReplication.clusterList[].name' 2>/dev/null || echo "")
    
    if [ -z "$PRIMARY_CLUSTER" ] || [ -z "$MEMBER_CLUSTERS" ]; then
        warn "Could not determine primary or member clusters, skipping placeholder services"
        return 0
    fi
    
    # Deploy placeholder services on primary cluster for each non-primary member
    for cluster in $MEMBER_CLUSTERS; do
        if [ "$cluster" = "$PRIMARY_CLUSTER" ]; then
            log "Skipping primary cluster: $cluster"
            continue
        fi
        
        log "Creating placeholder OTEL collector service for $cluster on primary cluster"
        cat <<EOF | kubectl --context "$PRIMARY_CLUSTER" apply -f - 2>/dev/null
apiVersion: v1
kind: Service
metadata:
  name: ${cluster}-collector
  namespace: documentdb-preview-ns
  labels:
    app: otel-collector
    cluster: ${cluster}
spec:
  type: ClusterIP
  ports:
  - name: prometheus
    port: 8889
    targetPort: 8889
    protocol: TCP
  selector:
    app: nonexistent-placeholder
EOF
        if [ $? -eq 0 ]; then
            success "Placeholder service ${cluster}-collector created on primary cluster"
        else
            warn "Failed to create placeholder service for $cluster on primary cluster"
        fi
    done
    
    success "Placeholder OTEL collector services created on primary cluster"
}

# Create Fleet ServiceExport and MultiClusterService for OTEL collectors
create_service_exports_and_imports() {
    log "Creating Fleet ServiceExport and MultiClusterService for OTEL collector endpoints..."
    
    # Get primary cluster and all member clusters
    #PRIMARY_CLUSTER=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.primary}' 2>/dev/null || echo "")
    PRIMARY_CLUSTER="azure-documentdb"
    MEMBER_CLUSTERS=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o json 2>/dev/null | jq -r '.spec.clusterReplication.clusterList[].name' 2>/dev/null || echo "")
    
    if [ -z "$PRIMARY_CLUSTER" ] || [ -z "$MEMBER_CLUSTERS" ]; then
        warn "Could not determine primary or member clusters, skipping service export/import"
        return 0
    fi
    
    # Create ServiceExport on each non-primary member cluster for their OTEL collector
    for cluster in $MEMBER_CLUSTERS; do
        if [ "$cluster" = "$PRIMARY_CLUSTER" ]; then
            log "Skipping ServiceExport on primary cluster: $cluster"
            continue
        fi

        log "Creating ServiceExport for documentdb-collector-collector on cluster: $cluster"
        cat <<EOF | kubectl --context "$cluster" apply -f - 
apiVersion: networking.fleet.azure.com/v1alpha1
kind: ServiceExport
metadata:
  name: $cluster-collector
  namespace: documentdb-preview-ns
EOF

    cat <<EOF | kubectl --context "$PRIMARY_CLUSTER" apply -f - 2>/dev/null
apiVersion: networking.fleet.azure.com/v1alpha1
kind: MultiClusterService
metadata:
  name: $cluster-collector
  namespace: documentdb-preview-ns
spec:
  serviceImport:
    name: $cluster-collector
EOF
    done
    
    # Create MultiClusterService on primary cluster to import all OTEL collector endpoints
    
    
    success "Fleet ServiceExport and MultiClusterService resources created for OTEL collectors"
}

# Main execution
main() {
    log "Starting Multi-Tenant DocumentDB + Telemetry Deployment..."
    log "========================================================="
    log "Configuration:"
    log "  Skip Wait: $SKIP_WAIT"
    log ""
    
    check_prerequisites

    install_opentelemetry_operator

    CROSS_CLOUD_STRATEGY=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.crossCloudNetworkingStrategy}' 2>/dev/null || echo "")
    
    deploy_collectors 
    
    deploy_monitoring_stack
    
    # Only create placeholder services if using Istio networking
    CROSS_CLOUD_STRATEGY=$(kubectl get documentdb documentdb-preview -n documentdb-preview-ns -o jsonpath='{.spec.clusterReplication.crossCloudNetworkingStrategy}' 2>/dev/null || echo "")
    if [ "$CROSS_CLOUD_STRATEGY" = "Istio" ]; then
        log "Cross-cloud networking strategy is Istio. Creating placeholder services..."
        create_placeholder_prometheus_services
    elif [ "$CROSS_CLOUD_STRATEGY" = "AzureFleet" ]; then
        log "Cross-cloud networking strategy is AzureFleet. Creating placeholder services..."
        create_service_exports_and_imports
    else
        log "Cross-cloud networking strategy is '$CROSS_CLOUD_STRATEGY', not 'Istio'. Skipping placeholder services."
    fi
    
    if [[ "$SKIP_WAIT" == "false" ]]; then
        error "Wait not yet implemented"
    fi
}

# Run main function
main "$@"
