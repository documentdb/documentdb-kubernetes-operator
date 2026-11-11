#!/bin/bash

# Multi-Tenant DocumentDB + Telemetry Deployment Script
# This script deploys complete DocumentDB clusters with isolated monitoring stacks for different teams

set -e

# Configuration
SALES_NAMESPACE="sales-namespace"
ACCOUNTS_NAMESPACE="accounts-namespace"
TELEMETRY_NAMESPACE="documentdb-telemetry"

# Deployment options
DEPLOY_DOCUMENTDB=true
DEPLOY_TELEMETRY=true
SKIP_WAIT=false

# Parse command line arguments
usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --telemetry-only    Deploy only telemetry stack (skip DocumentDB)"
    echo "  --documentdb-only   Deploy only DocumentDB (skip telemetry)"
    echo "  --skip-wait         Skip waiting for deployments to be ready"
    echo "  --help              Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                    # Deploy everything (DocumentDB + Telemetry)"
    echo "  $0 --telemetry-only   # Deploy only collectors, Prometheus, Grafana"
    echo "  $0 --documentdb-only  # Deploy only DocumentDB clusters"
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --telemetry-only)
            DEPLOY_DOCUMENTDB=false
            shift
            ;;
        --documentdb-only)
            DEPLOY_TELEMETRY=false
            shift
            ;;
        --skip-wait)
            SKIP_WAIT=true
            shift
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            error "Unknown option: $1"
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
    
    if ! kubectl get namespace opentelemetry-operator-system > /dev/null 2>&1; then
        error "OpenTelemetry Operator is not installed. Please install it first."
    fi
    
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

# Create namespaces for teams
create_namespaces() {
    log "Creating team namespaces..."
    
    # Sales namespace
    if ! kubectl get namespace $SALES_NAMESPACE > /dev/null 2>&1; then
        kubectl create namespace $SALES_NAMESPACE
        kubectl label namespace $SALES_NAMESPACE team=sales
        success "Created sales namespace: $SALES_NAMESPACE"
    else
        log "Sales namespace already exists: $SALES_NAMESPACE"
    fi
    
    # Accounts namespace  
    if ! kubectl get namespace $ACCOUNTS_NAMESPACE > /dev/null 2>&1; then
        kubectl create namespace $ACCOUNTS_NAMESPACE
        kubectl label namespace $ACCOUNTS_NAMESPACE team=accounts
        success "Created accounts namespace: $ACCOUNTS_NAMESPACE"
    else
        log "Accounts namespace already exists: $ACCOUNTS_NAMESPACE"
    fi
}

# Deploy Prometheus for a namespace
deploy_prometheus() {
    local namespace=$1
    local team=$2
    
    log "Deploying Prometheus for $team team in namespace: $namespace"
    
    helm upgrade --install prometheus-$team prometheus-community/prometheus \
        --namespace $namespace \
        --set server.persistentVolume.size=10Gi \
        --set server.retention=15d \
        --set server.global.scrape_interval=15s \
        --set server.global.evaluation_interval=15s \
        --set alertmanager.enabled=false \
        --set prometheus-node-exporter.enabled=false \
        --set prometheus-pushgateway.enabled=false \
        --set kube-state-metrics.enabled=false \
        --set server.service.type=ClusterIP \
        --set server.ingress.enabled=false \
        --wait --timeout=300s
    
    success "Prometheus deployed for $team team"
}

# Deploy Grafana for a namespace
deploy_grafana() {
    local namespace=$1
    local team=$2
    local prometheus_url="http://prometheus-$team-server.$namespace.svc.cluster.local"
    
    log "Deploying Grafana for $team team in namespace: $namespace"
    
    # Create Grafana values for this team
    cat > /tmp/grafana-$team-values.yaml <<EOF
datasources:
  datasources.yaml:
    apiVersion: 1
    datasources:
    - name: Prometheus-$team
      type: prometheus
      url: $prometheus_url
      access: proxy
      isDefault: true
      
adminPassword: admin123

service:
  type: ClusterIP
  port: 3000

ingress:
  enabled: false

persistence:
  enabled: true
  size: 1Gi

dashboardProviders:
  dashboardproviders.yaml:
    apiVersion: 1
    providers:
    - name: 'default'
      orgId: 1
      folder: ''
      type: file
      disableDeletion: false
      editable: true
      options:
        path: /var/lib/grafana/dashboards/default

dashboards:
  default:
    documentdb-overview:
      json: |
        {
          "dashboard": {
            "id": null,
            "title": "DocumentDB Overview - $team Team",
            "tags": ["documentdb", "$team"],
            "timezone": "browser",
            "panels": [
              {
                "id": 1,
                "title": "CPU Usage",
                "type": "graph",
                "targets": [
                  {
                    "expr": "rate(container_cpu_usage_seconds_total{tenant=\"$team\",container!=\"POD\",container!=\"\",name!=\"\"}[5m]) * 100",
                    "legendFormat": "{{pod}} - {{container}}"
                  }
                ],
                "gridPos": {"h": 9, "w": 12, "x": 0, "y": 0},
                "yAxes": [{"unit": "percent"}]
              },
              {
                "id": 2, 
                "title": "Memory Usage",
                "type": "graph",
                "targets": [
                  {
                    "expr": "container_memory_usage_bytes{tenant=\"$team\",container!=\"POD\",container!=\"\",name!=\"\"} / 1024 / 1024",
                    "legendFormat": "{{pod}} - {{container}}"
                  }
                ],
                "gridPos": {"h": 9, "w": 12, "x": 12, "y": 0},
                "yAxes": [{"unit": "bytes"}]
              },
              {
                "id": 3,
                "title": "Pod Status",
                "type": "stat",
                "targets": [
                  {
                    "expr": "count(container_memory_usage_bytes{tenant=\"$team\",container!=\"POD\",container!=\"\",name!=\"\"})",
                    "legendFormat": "Running Containers"
                  }
                ],
                "gridPos": {"h": 6, "w": 12, "x": 0, "y": 9}
              },
              {
                "id": 4, 
                "title": "Network I/O",
                "type": "graph",
                "targets": [
                  {
                    "expr": "rate(container_network_receive_bytes_total{tenant=\"$team\"}[5m])",
                    "legendFormat": "{{pod}} RX"
                  },
                  {
                    "expr": "rate(container_network_transmit_bytes_total{tenant=\"$team\"}[5m])",
                    "legendFormat": "{{pod}} TX"
                  }
                ],
                "gridPos": {"h": 6, "w": 12, "x": 12, "y": 9}
              }
            ],
            "time": {"from": "now-1h", "to": "now"},
            "refresh": "30s"
          }
        }
EOF
    
    helm upgrade --install grafana-$team grafana/grafana \
        --namespace $namespace \
        --values /tmp/grafana-$team-values.yaml \
        --wait --timeout=300s
    
    # Clean up temp file
    rm -f /tmp/grafana-$team-values.yaml
    
    success "Grafana deployed for $team team"
}

# Deploy OpenTelemetry collectors for each team
deploy_collectors() {
    log "Deploying multi-tenant OpenTelemetry collectors..."
    
    # Get the directory where this script is located
    SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
    TELEMETRY_DIR="$(dirname "$SCRIPT_DIR")"
    
    # Deploy Sales collector
    if [ -f "$TELEMETRY_DIR/otel-collector-sales.yaml" ]; then
        log "Deploying Sales team OpenTelemetry Collector..."
        kubectl apply -f "$TELEMETRY_DIR/otel-collector-sales.yaml"
        success "Sales collector deployed"
    else
        error "Sales collector configuration not found: $TELEMETRY_DIR/otel-collector-sales.yaml"
    fi
    
    # Deploy Accounts collector
    if [ -f "$TELEMETRY_DIR/otel-collector-accounts.yaml" ]; then
        log "Deploying Accounts team OpenTelemetry Collector..."
        kubectl apply -f "$TELEMETRY_DIR/otel-collector-accounts.yaml"
        success "Accounts collector deployed"
    else
        error "Accounts collector configuration not found: $TELEMETRY_DIR/otel-collector-accounts.yaml"
    fi
}

# Deploy monitoring stack for each team
deploy_monitoring_stacks() {
    log "Deploying monitoring stacks for each team..."
    
    # Deploy Sales monitoring stack
    deploy_prometheus $SALES_NAMESPACE "sales"
    deploy_grafana $SALES_NAMESPACE "sales"
    
    # Deploy Accounts monitoring stack  
    deploy_prometheus $ACCOUNTS_NAMESPACE "accounts"
    deploy_grafana $ACCOUNTS_NAMESPACE "accounts"
    
    success "All monitoring stacks deployed"
}

# Deploy DocumentDB instance for a team
deploy_documentdb() {
    local namespace=$1
    local team=$2
    local cluster_name="documentdb-$team"
    
    log "Deploying DocumentDB cluster for $team team in namespace: $namespace"
    
    # Create DocumentDB credentials secret (must be named 'documentdb-credentials')
    cat > /tmp/documentdb-$team-secret.yaml <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: $namespace
type: Opaque
stringData:
  username: $team
  password: ${team^}Password123
EOF
    
    # Create DocumentDB cluster manifest
    cat > /tmp/documentdb-$team-cluster.yaml <<EOF
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: $cluster_name
  namespace: $namespace
  labels:
    team: $team
    tenant: $team
    cnpg.io/cluster: $cluster_name
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
EOF
    
    # Apply the configurations
    kubectl apply -f /tmp/documentdb-$team-secret.yaml
    kubectl apply -f /tmp/documentdb-$team-cluster.yaml
    
    # Clean up temp files
    rm -f /tmp/documentdb-$team-secret.yaml /tmp/documentdb-$team-cluster.yaml
    
    success "DocumentDB cluster deployed for $team team: $cluster_name"
}

# Deploy DocumentDB instances for all teams
deploy_documentdb_instances() {
    log "Deploying DocumentDB instances for each team..."
    
    # Deploy Sales DocumentDB
    deploy_documentdb $SALES_NAMESPACE "sales"
    
    # Deploy Accounts DocumentDB
    deploy_documentdb $ACCOUNTS_NAMESPACE "accounts"
    
    success "All DocumentDB instances deployed"
}

# Wait for collectors to be ready
wait_for_collectors() {
    log "Waiting for OpenTelemetry collectors to be ready..."
    
    # Wait for Sales collector
    kubectl wait --for=condition=available deployment/documentdb-sales-collector-collector -n $SALES_NAMESPACE --timeout=300s
    success "Sales collector is ready"
    
    # Wait for Accounts collector  
    kubectl wait --for=condition=available deployment/documentdb-accounts-collector-collector -n $ACCOUNTS_NAMESPACE --timeout=300s
    success "Accounts collector is ready"
}

# Wait for monitoring stacks to be ready
wait_for_monitoring_stacks() {
    log "Waiting for monitoring stacks to be ready..."
    
    # Wait for Sales monitoring stack
    kubectl wait --for=condition=available deployment/prometheus-sales-server -n $SALES_NAMESPACE --timeout=300s
    kubectl wait --for=condition=available deployment/grafana-sales -n $SALES_NAMESPACE --timeout=300s
    success "Sales monitoring stack is ready"
    
    # Wait for Accounts monitoring stack
    kubectl wait --for=condition=available deployment/prometheus-accounts-server -n $ACCOUNTS_NAMESPACE --timeout=300s 
    kubectl wait --for=condition=available deployment/grafana-accounts -n $ACCOUNTS_NAMESPACE --timeout=300s
    success "Accounts monitoring stack is ready"
}

# Wait for DocumentDB instances to be ready
wait_for_documentdb_instances() {
    log "Waiting for DocumentDB instances to be ready..."
    
    # Wait for Sales DocumentDB
    log "Waiting for Sales DocumentDB cluster..."
    kubectl wait --for=condition=ready documentdb/documentdb-sales -n $SALES_NAMESPACE --timeout=600s
    success "Sales DocumentDB is ready"
    
    # Wait for Accounts DocumentDB
    log "Waiting for Accounts DocumentDB cluster..."
    kubectl wait --for=condition=ready documentdb/documentdb-accounts -n $ACCOUNTS_NAMESPACE --timeout=600s
    success "Accounts DocumentDB is ready"
}

# Show deployment status
show_status() {
    log ""
    log "🎯 Multi-Tenant Telemetry Deployment Status:"
    log "============================================="
    log ""
    
    log "📊 Sales Team (Namespace: $SALES_NAMESPACE):"
    log "  DocumentDB Cluster:"
    kubectl get documentdb -n $SALES_NAMESPACE || true
    kubectl get pods -n $SALES_NAMESPACE -l cnpg.io/cluster=documentdb-sales || true
    log "  OpenTelemetry Collector:"
    kubectl get pods -n $SALES_NAMESPACE -l app.kubernetes.io/name=documentdb-sales-collector-collector || true
    log "  Prometheus:"
    kubectl get pods -n $SALES_NAMESPACE -l app.kubernetes.io/name=prometheus-server || true
    log "  Grafana:"
    kubectl get pods -n $SALES_NAMESPACE -l app.kubernetes.io/name=grafana || true
    log ""
    
    log "📊 Accounts Team (Namespace: $ACCOUNTS_NAMESPACE):"
    log "  DocumentDB Cluster:"
    kubectl get documentdb -n $ACCOUNTS_NAMESPACE || true
    kubectl get pods -n $ACCOUNTS_NAMESPACE -l cnpg.io/cluster=documentdb-accounts || true
    log "  OpenTelemetry Collector:"
    kubectl get pods -n $ACCOUNTS_NAMESPACE -l app.kubernetes.io/name=documentdb-accounts-collector-collector || true
    log "  Prometheus:"
    kubectl get pods -n $ACCOUNTS_NAMESPACE -l app.kubernetes.io/name=prometheus-server || true
    log "  Grafana:"
    kubectl get pods -n $ACCOUNTS_NAMESPACE -l app.kubernetes.io/name=grafana || true
    log ""
    
    # Get Grafana admin credentials and URLs
    log "� Grafana Access Information:"
    log "  Sales Grafana:"
    log "    URL: kubectl port-forward -n $SALES_NAMESPACE svc/grafana-sales 3001:80"
    log "    Admin Password: admin123"
    log "  Accounts Grafana:"
    log "    URL: kubectl port-forward -n $ACCOUNTS_NAMESPACE svc/grafana-accounts 3002:80"
    log "    Admin Password: admin123"
    log ""
    
    log "🔗 DocumentDB Connection Strings:"
    log "  Sales: kubectl get secret documentdb-credentials -n $SALES_NAMESPACE -o jsonpath='{.data.username}' | base64 -d"
    log "  Accounts: kubectl get secret documentdb-credentials -n $ACCOUNTS_NAMESPACE -o jsonpath='{.data.username}' | base64 -d"
    log ""
    
    log "�🔍 How to check metrics per team:"
    log "  Sales metrics: kubectl logs -n $SALES_NAMESPACE -l app.kubernetes.io/name=documentdb-sales-collector-collector"
    log "  Accounts metrics: kubectl logs -n $ACCOUNTS_NAMESPACE -l app.kubernetes.io/name=documentdb-accounts-collector-collector"
    log ""
    
    log "📝 Prometheus URLs (internal):"
    log "  Sales: http://prometheus-sales-server.$SALES_NAMESPACE.svc.cluster.local"
    log "  Accounts: http://prometheus-accounts-server.$ACCOUNTS_NAMESPACE.svc.cluster.local"
}

# Main execution
main() {
    log "Starting Multi-Tenant DocumentDB + Telemetry Deployment..."
    log "========================================================="
    log "Configuration:"
    log "  Deploy DocumentDB: $DEPLOY_DOCUMENTDB"
    log "  Deploy Telemetry: $DEPLOY_TELEMETRY"
    log "  Skip Wait: $SKIP_WAIT"
    log ""
    
    check_prerequisites
    create_namespaces
    
    if [[ "$DEPLOY_DOCUMENTDB" == "true" ]]; then
        deploy_documentdb_instances
    fi
    
    if [[ "$DEPLOY_TELEMETRY" == "true" ]]; then
        deploy_collectors
        deploy_monitoring_stacks
    fi
    
    if [[ "$SKIP_WAIT" == "false" ]]; then
        if [[ "$DEPLOY_DOCUMENTDB" == "true" ]]; then
            wait_for_documentdb_instances
        fi
        
        if [[ "$DEPLOY_TELEMETRY" == "true" ]]; then
            wait_for_collectors
            wait_for_monitoring_stacks
        fi
    fi
    
    show_status
    
    success "Multi-tenant deployment completed successfully!"
    log ""
    if [[ "$DEPLOY_DOCUMENTDB" == "true" && "$DEPLOY_TELEMETRY" == "true" ]]; then
        log "💡 What was deployed:"
        log "  ✅ DocumentDB clusters with proper cluster labels"
        log "  ✅ OpenTelemetry collectors for auto-discovery"
        log "  ✅ Prometheus instances for metrics storage"
        log "  ✅ Grafana dashboards for visualization"
        log ""
        log "🚀 Ready to use:"
        log "  - Sales team has complete isolated stack in sales-namespace"
        log "  - Accounts team has complete isolated stack in accounts-namespace"
        log "  - Metrics are automatically collected and displayed"
    elif [[ "$DEPLOY_TELEMETRY" == "true" ]]; then
        log "💡 Telemetry stack deployed - ready for DocumentDB instances"
    elif [[ "$DEPLOY_DOCUMENTDB" == "true" ]]; then
        log "💡 DocumentDB clusters deployed - add telemetry with --telemetry-only"
    fi
}

# Run main function
main "$@"