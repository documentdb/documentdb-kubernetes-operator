#!/bin/bash

# DocumentDB AKS Connection Test Script
# This script tests connectivity to DocumentDB running on AKS

set -e

# Configuration
NAMESPACE="${NAMESPACE:-documentdb-instance-ns}"
DOCUMENTDB_NAME="${DOCUMENTDB_NAME:-sample-documentdb}"
SECRET_NAME="${SECRET_NAME:-documentdb-credentials}"

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
    
    if ! command -v kubectl &> /dev/null; then
        error "kubectl not found. Please install kubectl first."
        exit 1
    fi
    
    if ! command -v mongosh &> /dev/null; then
        error "mongosh not found. Please install MongoDB Shell first."
        exit 1
    fi
    
    # Check if cluster is accessible
    if ! kubectl cluster-info &> /dev/null; then
        error "Cannot access Kubernetes cluster. Please check your kubeconfig."
        exit 1
    fi
    
    success "Prerequisites met"
}

# Get DocumentDB status
get_documentdb_status() {
    log "Checking DocumentDB status..."
    
    # Check if DocumentDB resource exists
    if ! kubectl get documentdb $DOCUMENTDB_NAME -n $NAMESPACE &> /dev/null; then
        error "DocumentDB instance '$DOCUMENTDB_NAME' not found in namespace '$NAMESPACE'"
        exit 1
    fi
    
    # Get DocumentDB status
    STATUS=$(kubectl get documentdb $DOCUMENTDB_NAME -n $NAMESPACE -o jsonpath='{.status.status}')
    CONNECTION_STRING=$(kubectl get documentdb $DOCUMENTDB_NAME -n $NAMESPACE -o jsonpath='{.status.connectionString}')
    
    log "DocumentDB Status: $STATUS"
    if [ "$STATUS" != "Cluster in healthy state" ]; then
        warn "DocumentDB is not in healthy state. Current status: $STATUS"
        return 1
    fi
    
    success "DocumentDB is healthy"
    return 0
}

# Get service information
get_service_info() {
    log "Getting service information..."
    
    # Find the service
    SERVICE_NAME=$(kubectl get services -n $NAMESPACE -o name | grep documentdb-service | head -1 | cut -d'/' -f2)
    
    if [ -z "$SERVICE_NAME" ]; then
        error "DocumentDB service not found in namespace '$NAMESPACE'"
        exit 1
    fi
    
    # Get service details
    SERVICE_TYPE=$(kubectl get service $SERVICE_NAME -n $NAMESPACE -o jsonpath='{.spec.type}')
    EXTERNAL_IP=$(kubectl get service $SERVICE_NAME -n $NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
    PORT=$(kubectl get service $SERVICE_NAME -n $NAMESPACE -o jsonpath='{.spec.ports[0].port}')
    
    log "Service Name: $SERVICE_NAME"
    log "Service Type: $SERVICE_TYPE"
    log "External IP: ${EXTERNAL_IP:-Pending}"
    log "Port: $PORT"
    
    if [ "$SERVICE_TYPE" == "LoadBalancer" ] && [ -z "$EXTERNAL_IP" ]; then
        warn "LoadBalancer external IP is still pending"
        log "Waiting for external IP assignment..."
        
        # Wait up to 5 minutes for external IP
        for i in {1..30}; do
            EXTERNAL_IP=$(kubectl get service $SERVICE_NAME -n $NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
            if [ -n "$EXTERNAL_IP" ]; then
                success "External IP assigned: $EXTERNAL_IP"
                break
            fi
            log "Still waiting for external IP... (attempt $i/30)"
            sleep 10
        done
        
        if [ -z "$EXTERNAL_IP" ]; then
            error "External IP not assigned after 5 minutes. Check Azure LoadBalancer status."
            exit 1
        fi
    fi
    
    # Export for use in other functions
    export EXTERNAL_IP
    export SERVICE_PORT=$PORT
}

# Get credentials
get_credentials() {
    log "Getting DocumentDB credentials..."
    
    if ! kubectl get secret $SECRET_NAME -n $NAMESPACE &> /dev/null; then
        error "Secret '$SECRET_NAME' not found in namespace '$NAMESPACE'"
        exit 1
    fi
    
    USERNAME=$(kubectl get secret $SECRET_NAME -n $NAMESPACE -o jsonpath='{.data.username}' | base64 -d)
    PASSWORD=$(kubectl get secret $SECRET_NAME -n $NAMESPACE -o jsonpath='{.data.password}' | base64 -d)
    
    if [ -z "$USERNAME" ] || [ -z "$PASSWORD" ]; then
        error "Failed to retrieve credentials from secret"
        exit 1
    fi
    
    log "Username: $USERNAME"
    log "Password: [HIDDEN]"
    
    # Export for use in other functions
    export DB_USERNAME=$USERNAME
    export DB_PASSWORD=$PASSWORD
}

# Test connection
test_connection() {
    log "Testing DocumentDB connection..."
    
    if [ -z "$EXTERNAL_IP" ]; then
        error "External IP not available for connection test"
        exit 1
    fi
    
    # Build connection string
    CONNECTION_STRING="mongodb://${DB_USERNAME}:${DB_PASSWORD}@${EXTERNAL_IP}:${SERVICE_PORT}/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
    
    log "Testing connection to: mongodb://${DB_USERNAME}:***@${EXTERNAL_IP}:${SERVICE_PORT}/"
    
    # Test basic connection
    if echo 'db.runCommand({hello: 1})' | mongosh "$CONNECTION_STRING" --quiet; then
        success "Connection test successful!"
    else
        error "Connection test failed"
        return 1
    fi
    
    # Test basic operations
    log "Testing basic database operations..."
    
    cat << EOF | mongosh "$CONNECTION_STRING" --quiet
use testdb
db.testCollection.insertOne({message: "Hello from AKS!", timestamp: new Date()})
var count = db.testCollection.countDocuments()
print("Inserted document. Total documents: " + count)
var doc = db.testCollection.findOne()
print("Retrieved document: " + JSON.stringify(doc))
EOF
    
    if [ $? -eq 0 ]; then
        success "Basic operations test successful!"
    else
        error "Basic operations test failed"
        return 1
    fi
}

# Print connection information
print_connection_info() {
    echo ""
    echo "=================================================="
    echo "🎉 DocumentDB AKS Connection Information"
    echo "=================================================="
    echo "External IP: $EXTERNAL_IP"
    echo "Port: $SERVICE_PORT"
    echo "Username: $DB_USERNAME"
    echo "Password: [HIDDEN]"
    echo ""
    echo "Connection String:"
    echo "mongodb://<username>:<password>@${EXTERNAL_IP}:${SERVICE_PORT}/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
    echo ""
    echo "Connect using mongosh:"
    echo "mongosh 'mongodb://<username>:<password>@${EXTERNAL_IP}:${SERVICE_PORT}/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0'"
    echo "=================================================="
}

# Main execution
main() {
    log "Starting DocumentDB AKS connection test..."
    log "Namespace: $NAMESPACE"
    log "DocumentDB: $DOCUMENTDB_NAME"
    log "Secret: $SECRET_NAME"
    echo ""
    
    check_prerequisites
    get_documentdb_status
    get_service_info
    get_credentials
    test_connection
    print_connection_info
    
    success "All tests completed successfully!"
}

# Run main function
main "$@"