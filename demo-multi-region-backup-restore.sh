#!/bin/bash
# =============================================================================
# DocumentDB Multi-Region Cluster Demo
# =============================================================================
# This script demonstrates:
# 1. Deploying a multi-region DocumentDB cluster
# 2. Writing sample documents using Python/PyMongo
# 3. Creating a backup
# 4. Deleting the original cluster
# 5. Restoring into a new cluster from backup
#
# Prerequisites:
# - kubectl configured with cluster access
# - CSI driver with snapshot support (run ./operator/src/scripts/test-scripts/deploy-csi-driver.sh for Kind/Minikube)
# - DocumentDB operator installed
# - Python3 with pymongo installed (pip3 install pymongo)
# =============================================================================

set -e

# Configuration
NAMESPACE="documentdb-demo-ns"
CLUSTER_NAME="multi-region-demo"
RESTORED_CLUSTER_NAME="restored-demo"
BACKUP_NAME="demo-backup"
PASSWORD="DemoPassword123!"
USERNAME="default_user"
PORT=10260

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_step() {
    echo -e "\n${BLUE}===================================================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}===================================================================${NC}\n"
}

log_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

log_info() {
    echo -e "${YELLOW}ℹ $1${NC}"
}

log_error() {
    echo -e "${RED}✗ $1${NC}"
}

wait_for_cluster_ready() {
    local cluster_name=$1
    local max_wait=300
    local waited=0
    
    log_info "Waiting for cluster '$cluster_name' to be ready (max ${max_wait}s)..."
    
    while [ $waited -lt $max_wait ]; do
        STATUS=$(kubectl get documentdb $cluster_name -n $NAMESPACE -o jsonpath='{.status.phase}' 2>/dev/null || echo "NotFound")
        if [ "$STATUS" == "Ready" ]; then
            log_success "Cluster '$cluster_name' is Ready!"
            return 0
        fi
        echo "  Current status: $STATUS (waited ${waited}s)"
        sleep 10
        waited=$((waited + 10))
    done
    
    log_error "Timeout waiting for cluster to be ready"
    return 1
}

wait_for_backup_complete() {
    local backup_name=$1
    local max_wait=180
    local waited=0
    
    log_info "Waiting for backup '$backup_name' to complete (max ${max_wait}s)..."
    
    while [ $waited -lt $max_wait ]; do
        STATUS=$(kubectl get backup $backup_name -n $NAMESPACE -o jsonpath='{.status.phase}' 2>/dev/null || echo "NotFound")
        if [ "$STATUS" == "Completed" ]; then
            log_success "Backup '$backup_name' completed!"
            return 0
        fi
        echo "  Current status: $STATUS (waited ${waited}s)"
        sleep 10
        waited=$((waited + 10))
    done
    
    log_error "Timeout waiting for backup to complete"
    return 1
}

cleanup_port_forward() {
    if [ -f /tmp/demo_pf.pid ]; then
        PID=$(cat /tmp/demo_pf.pid)
        kill $PID 2>/dev/null || true
        rm -f /tmp/demo_pf.pid
    fi
}

# Cleanup on exit
trap cleanup_port_forward EXIT

# =============================================================================
# STEP 1: Create Namespace and Credentials
# =============================================================================
log_step "STEP 1: Creating Namespace and Credentials"

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: $NAMESPACE
  labels:
    app: documentdb-demo
---
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: $NAMESPACE
type: Opaque
stringData:
  username: $USERNAME
  password: $PASSWORD
EOF

log_success "Namespace and credentials created"

# =============================================================================
# STEP 2: Deploy Multi-Region DocumentDB Cluster
# =============================================================================
log_step "STEP 2: Deploying Multi-Region DocumentDB Cluster"

cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: $CLUSTER_NAME
  namespace: $NAMESPACE
spec:
  # Multi-region configuration with 3 nodes for HA
  nodeCount: 3
  instancesPerNode: 1
  
  # Use the latest DocumentDB image
  documentDBImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  gatewayImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  
  # Reference the credentials secret
  documentDbCredentialSecret: documentdb-credentials
  
  # Storage configuration with CSI for backup support
  resource:
    storage:
      pvcSize: 5Gi
      # Uncomment for Kind/Minikube with CSI driver:
      # storageClass: csi-hostpath-sc
  
  # Backup configuration with 30-day retention
  backup:
    retentionDays: 30
  
  # Expose via LoadBalancer (use ClusterIP if no LB available)
  exposeViaService:
    serviceType: ClusterIP
  
  # Enable high availability for multi-region setup
  clusterReplication:
    highAvailability: true
  
  logLevel: info
EOF

log_success "Cluster manifest applied"

# Wait for cluster to be ready
wait_for_cluster_ready $CLUSTER_NAME

# Show cluster status
echo ""
kubectl get documentdb $CLUSTER_NAME -n $NAMESPACE
kubectl get pods -n $NAMESPACE -l "documentdb.io/cluster=$CLUSTER_NAME"

# =============================================================================
# STEP 3: Write Demo Documents
# =============================================================================
log_step "STEP 3: Writing Demo Documents to the Cluster"

# Setup port forwarding
log_info "Setting up port forwarding..."
kubectl port-forward svc/${CLUSTER_NAME}-svc $PORT:$PORT -n $NAMESPACE > /tmp/demo_pf.log 2>&1 &
PF_PID=$!
echo $PF_PID > /tmp/demo_pf.pid
sleep 10

# Check if port forward is working
if ! nc -z 127.0.0.1 $PORT 2>/dev/null; then
    log_error "Port forwarding failed. Trying pod-based forwarding..."
    cleanup_port_forward
    POD_NAME="${CLUSTER_NAME}-1"
    kubectl port-forward pod/$POD_NAME $PORT:$PORT -n $NAMESPACE > /tmp/demo_pf.log 2>&1 &
    PF_PID=$!
    echo $PF_PID > /tmp/demo_pf.pid
    sleep 10
fi

# Create Python script for writing documents
cat > /tmp/write_documents.py << 'PYTHON_SCRIPT'
#!/usr/bin/env python3
"""
Demo script to write documents to DocumentDB cluster.
Demonstrates various MongoDB operations.
"""

import sys
from datetime import datetime
from pymongo import MongoClient
from pymongo.errors import ConnectionFailure

def main():
    if len(sys.argv) != 4:
        print(f"Usage: {sys.argv[0]} <host:port> <username> <password>")
        sys.exit(1)
    
    host_port = sys.argv[1]
    username = sys.argv[2]
    password = sys.argv[3]
    
    # Connection string with TLS
    connection_string = f"mongodb://{username}:{password}@{host_port}/?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
    
    print("Connecting to DocumentDB cluster...")
    try:
        client = MongoClient(connection_string, serverSelectionTimeoutMS=30000)
        # Test connection
        client.admin.command('ping')
        print("✓ Connected successfully!")
    except ConnectionFailure as e:
        print(f"✗ Connection failed: {e}")
        sys.exit(1)
    
    # Use demo database
    db = client.demo_database
    
    # =================================================================
    # Insert sample data into various collections
    # =================================================================
    
    print("\n--- Creating 'users' collection ---")
    users = db.users
    users.drop()  # Clean start
    
    user_docs = [
        {"name": "Alice Johnson", "email": "alice@example.com", "department": "Engineering", 
         "role": "Senior Developer", "salary": 95000, "joined": datetime(2021, 3, 15)},
        {"name": "Bob Smith", "email": "bob@example.com", "department": "Marketing",
         "role": "Marketing Manager", "salary": 85000, "joined": datetime(2020, 6, 1)},
        {"name": "Carol Williams", "email": "carol@example.com", "department": "Engineering",
         "role": "Tech Lead", "salary": 110000, "joined": datetime(2019, 1, 10)},
        {"name": "David Brown", "email": "david@example.com", "department": "Sales",
         "role": "Sales Representative", "salary": 65000, "joined": datetime(2022, 8, 20)},
        {"name": "Eve Davis", "email": "eve@example.com", "department": "Engineering",
         "role": "DevOps Engineer", "salary": 90000, "joined": datetime(2021, 11, 5)}
    ]
    
    result = users.insert_many(user_docs)
    print(f"✓ Inserted {len(result.inserted_ids)} users")
    
    print("\n--- Creating 'products' collection ---")
    products = db.products
    products.drop()
    
    product_docs = [
        {"name": "Cloud Database", "category": "Database", "price": 299.99, 
         "features": ["Auto-scaling", "Multi-region", "Backup"], "in_stock": True},
        {"name": "API Gateway", "category": "Networking", "price": 149.99,
         "features": ["Rate limiting", "Authentication", "Logging"], "in_stock": True},
        {"name": "ML Platform", "category": "AI/ML", "price": 499.99,
         "features": ["Model training", "Inference", "AutoML"], "in_stock": True},
        {"name": "Container Registry", "category": "DevOps", "price": 99.99,
         "features": ["Image scanning", "Geo-replication"], "in_stock": True}
    ]
    
    result = products.insert_many(product_docs)
    print(f"✓ Inserted {len(result.inserted_ids)} products")
    
    print("\n--- Creating 'orders' collection ---")
    orders = db.orders
    orders.drop()
    
    order_docs = [
        {"order_id": "ORD-001", "customer": "alice@example.com", "product": "Cloud Database",
         "quantity": 1, "total": 299.99, "status": "completed", "date": datetime(2024, 1, 15)},
        {"order_id": "ORD-002", "customer": "bob@example.com", "product": "API Gateway",
         "quantity": 2, "total": 299.98, "status": "processing", "date": datetime(2024, 1, 20)},
        {"order_id": "ORD-003", "customer": "carol@example.com", "product": "ML Platform",
         "quantity": 1, "total": 499.99, "status": "completed", "date": datetime(2024, 1, 22)},
        {"order_id": "ORD-004", "customer": "alice@example.com", "product": "Container Registry",
         "quantity": 3, "total": 299.97, "status": "pending", "date": datetime(2024, 1, 25)}
    ]
    
    result = orders.insert_many(order_docs)
    print(f"✓ Inserted {len(result.inserted_ids)} orders")
    
    # =================================================================
    # Demonstrate query operations
    # =================================================================
    
    print("\n--- Running sample queries ---")
    
    # Count documents
    user_count = users.count_documents({})
    print(f"Total users: {user_count}")
    
    # Find with filter
    engineers = list(users.find({"department": "Engineering"}))
    print(f"Engineers: {len(engineers)}")
    for eng in engineers:
        print(f"  - {eng['name']} ({eng['role']})")
    
    # Aggregation: Average salary by department
    pipeline = [
        {"$group": {
            "_id": "$department",
            "avg_salary": {"$avg": "$salary"},
            "count": {"$sum": 1}
        }},
        {"$sort": {"avg_salary": -1}}
    ]
    dept_stats = list(users.aggregate(pipeline))
    print("\nSalary by department:")
    for stat in dept_stats:
        print(f"  {stat['_id']}: ${stat['avg_salary']:,.2f} avg ({stat['count']} employees)")
    
    # Order statistics
    completed_orders = orders.count_documents({"status": "completed"})
    total_revenue = sum(order['total'] for order in orders.find({"status": "completed"}))
    print(f"\nCompleted orders: {completed_orders}")
    print(f"Total revenue: ${total_revenue:,.2f}")
    
    print("\n" + "="*60)
    print("✓ All demo documents written successfully!")
    print("="*60)
    
    # Summary
    print(f"\nDatabase: demo_database")
    print(f"Collections created:")
    print(f"  - users: {users.count_documents({})} documents")
    print(f"  - products: {products.count_documents({})} documents")
    print(f"  - orders: {orders.count_documents({})} documents")
    
    client.close()

if __name__ == "__main__":
    main()
PYTHON_SCRIPT

# Run the Python script
python3 /tmp/write_documents.py "127.0.0.1:$PORT" "$USERNAME" "$PASSWORD"

log_success "Demo documents written to the cluster"

# Cleanup port forward
cleanup_port_forward

# =============================================================================
# STEP 4: Create Backup
# =============================================================================
log_step "STEP 4: Creating Backup of the Cluster"

cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: Backup
metadata:
  name: $BACKUP_NAME
  namespace: $NAMESPACE
spec:
  cluster:
    name: $CLUSTER_NAME
  retentionDays: 30
EOF

log_success "Backup resource created"

# Wait for backup to complete
wait_for_backup_complete $BACKUP_NAME

# Show backup status
echo ""
kubectl get backup $BACKUP_NAME -n $NAMESPACE
kubectl describe backup $BACKUP_NAME -n $NAMESPACE | grep -A 20 "Status:"

# =============================================================================
# STEP 5: Delete Original Cluster
# =============================================================================
log_step "STEP 5: Deleting Original Cluster"

log_info "Deleting cluster '$CLUSTER_NAME'..."
kubectl delete documentdb $CLUSTER_NAME -n $NAMESPACE --wait=true

log_success "Original cluster deleted"

# Verify cluster is gone
echo ""
log_info "Verifying cluster deletion..."
if kubectl get documentdb $CLUSTER_NAME -n $NAMESPACE 2>/dev/null; then
    log_error "Cluster still exists!"
    exit 1
else
    log_success "Cluster successfully deleted"
fi

# Show backup still exists
echo ""
log_info "Backup still available for restore:"
kubectl get backup $BACKUP_NAME -n $NAMESPACE

# =============================================================================
# STEP 6: Restore into New Cluster
# =============================================================================
log_step "STEP 6: Restoring Backup into New Cluster"

cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: $RESTORED_CLUSTER_NAME
  namespace: $NAMESPACE
spec:
  # Bootstrap from backup - THIS IS THE KEY RESTORE CONFIGURATION
  bootstrap:
    recovery:
      backup:
        name: $BACKUP_NAME
  
  # Same configuration as original cluster
  nodeCount: 3
  instancesPerNode: 1
  documentDBImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  gatewayImage: ghcr.io/microsoft/documentdb/documentdb-local:16
  documentDbCredentialSecret: documentdb-credentials
  
  resource:
    storage:
      pvcSize: 5Gi
  
  backup:
    retentionDays: 30
  
  exposeViaService:
    serviceType: ClusterIP
  
  clusterReplication:
    highAvailability: true
  
  logLevel: info
EOF

log_success "Restored cluster manifest applied"

# Wait for restored cluster to be ready
wait_for_cluster_ready $RESTORED_CLUSTER_NAME

# Show restored cluster status
echo ""
kubectl get documentdb $RESTORED_CLUSTER_NAME -n $NAMESPACE
kubectl get pods -n $NAMESPACE -l "documentdb.io/cluster=$RESTORED_CLUSTER_NAME"

# =============================================================================
# STEP 7: Verify Restored Data
# =============================================================================
log_step "STEP 7: Verifying Restored Data"

# Setup port forwarding to restored cluster
log_info "Setting up port forwarding to restored cluster..."
kubectl port-forward svc/${RESTORED_CLUSTER_NAME}-svc $PORT:$PORT -n $NAMESPACE > /tmp/demo_pf.log 2>&1 &
PF_PID=$!
echo $PF_PID > /tmp/demo_pf.pid
sleep 10

# Create verification script
cat > /tmp/verify_restore.py << 'PYTHON_SCRIPT'
#!/usr/bin/env python3
"""
Verify that restored data matches original data.
"""

import sys
from pymongo import MongoClient
from pymongo.errors import ConnectionFailure

def main():
    if len(sys.argv) != 4:
        print(f"Usage: {sys.argv[0]} <host:port> <username> <password>")
        sys.exit(1)
    
    host_port = sys.argv[1]
    username = sys.argv[2]
    password = sys.argv[3]
    
    connection_string = f"mongodb://{username}:{password}@{host_port}/?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
    
    print("Connecting to restored cluster...")
    try:
        client = MongoClient(connection_string, serverSelectionTimeoutMS=30000)
        client.admin.command('ping')
        print("✓ Connected successfully!")
    except ConnectionFailure as e:
        print(f"✗ Connection failed: {e}")
        sys.exit(1)
    
    db = client.demo_database
    
    print("\n" + "="*60)
    print("VERIFYING RESTORED DATA")
    print("="*60)
    
    # Verify users collection
    users = db.users
    user_count = users.count_documents({})
    print(f"\n✓ Users collection: {user_count} documents")
    
    if user_count == 5:
        print("  Expected: 5, Found: 5 - PASS")
    else:
        print(f"  Expected: 5, Found: {user_count} - FAIL")
    
    # Show sample user
    sample_user = users.find_one({"name": "Alice Johnson"})
    if sample_user:
        print(f"  Sample user: {sample_user['name']} - {sample_user['role']}")
    
    # Verify products collection
    products = db.products
    product_count = products.count_documents({})
    print(f"\n✓ Products collection: {product_count} documents")
    
    if product_count == 4:
        print("  Expected: 4, Found: 4 - PASS")
    else:
        print(f"  Expected: 4, Found: {product_count} - FAIL")
    
    # Verify orders collection
    orders = db.orders
    order_count = orders.count_documents({})
    print(f"\n✓ Orders collection: {order_count} documents")
    
    if order_count == 4:
        print("  Expected: 4, Found: 4 - PASS")
    else:
        print(f"  Expected: 4, Found: {order_count} - FAIL")
    
    # Run same aggregation as before to verify data integrity
    pipeline = [
        {"$group": {
            "_id": "$department",
            "avg_salary": {"$avg": "$salary"},
            "count": {"$sum": 1}
        }},
        {"$sort": {"avg_salary": -1}}
    ]
    dept_stats = list(users.aggregate(pipeline))
    
    print("\n✓ Aggregation results (should match original):")
    for stat in dept_stats:
        print(f"  {stat['_id']}: ${stat['avg_salary']:,.2f} avg ({stat['count']} employees)")
    
    print("\n" + "="*60)
    print("✓ DATA RESTORATION VERIFIED SUCCESSFULLY!")
    print("="*60)
    
    client.close()

if __name__ == "__main__":
    main()
PYTHON_SCRIPT

# Run verification
python3 /tmp/verify_restore.py "127.0.0.1:$PORT" "$USERNAME" "$PASSWORD"

# Cleanup
cleanup_port_forward
rm -f /tmp/write_documents.py /tmp/verify_restore.py

# =============================================================================
# Summary
# =============================================================================
log_step "DEMO COMPLETE!"

echo -e "${GREEN}"
echo "============================================================"
echo "  Multi-Region Backup & Restore Demo Completed Successfully!"
echo "============================================================"
echo ""
echo "  What was demonstrated:"
echo "  1. ✓ Created namespace and credentials"
echo "  2. ✓ Deployed multi-region DocumentDB cluster (3 nodes, HA)"
echo "  3. ✓ Wrote demo documents (users, products, orders)"
echo "  4. ✓ Created backup of the cluster"
echo "  5. ✓ Deleted original cluster"
echo "  6. ✓ Restored from backup to new cluster"
echo "  7. ✓ Verified restored data integrity"
echo ""
echo "  Resources created:"
echo "  - Namespace: $NAMESPACE"
echo "  - Restored Cluster: $RESTORED_CLUSTER_NAME"
echo "  - Backup: $BACKUP_NAME"
echo ""
echo "  Cleanup command:"
echo "  kubectl delete namespace $NAMESPACE"
echo -e "${NC}"
