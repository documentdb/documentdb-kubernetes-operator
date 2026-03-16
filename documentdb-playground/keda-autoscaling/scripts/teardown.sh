#!/bin/bash
set -euo pipefail

# Configuration
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-ns}"
DOCUMENTDB_NAME="${DOCUMENTDB_NAME:-keda-demo}"
APP_NAMESPACE="${APP_NAMESPACE:-app}"
KEDA_NAMESPACE="${KEDA_NAMESPACE:-keda}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()   { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }

UNINSTALL_KEDA=false
DELETE_DOCUMENTDB=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --uninstall-keda)
            UNINSTALL_KEDA=true
            shift
            ;;
        --delete-documentdb)
            DELETE_DOCUMENTDB=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --uninstall-keda       Also uninstall KEDA Helm release"
            echo "  --delete-documentdb    Also delete the DocumentDB instance"
            echo "  -h, --help             Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

echo ""
echo "============================================="
echo "  KEDA + DocumentDB Playground Teardown"
echo "============================================="
echo ""

log "Removing KEDA demo resources..."

# Delete ScaledObject first (avoids KEDA errors when HPA targets disappear)
kubectl delete scaledobject documentdb-worker-scaler -n "$APP_NAMESPACE" --ignore-not-found=true 2>/dev/null || true
kubectl delete clustertriggerauthentication documentdb-trigger-auth --ignore-not-found=true 2>/dev/null || true

# Delete jobs and worker
kubectl delete job seed-pending-jobs drain-pending-jobs -n "$APP_NAMESPACE" --ignore-not-found=true 2>/dev/null || true
kubectl delete deployment job-worker -n "$APP_NAMESPACE" --ignore-not-found=true 2>/dev/null || true

# Delete connection secrets
kubectl delete secret documentdb-keda-connection -n "$APP_NAMESPACE" --ignore-not-found=true 2>/dev/null || true
kubectl delete secret documentdb-keda-connection -n "$KEDA_NAMESPACE" --ignore-not-found=true 2>/dev/null || true

# Delete app namespace
if kubectl get namespace "$APP_NAMESPACE" &>/dev/null; then
    log "Deleting namespace ${APP_NAMESPACE}..."
    kubectl delete namespace "$APP_NAMESPACE" --wait=false 2>/dev/null || true
fi

if [ "$UNINSTALL_KEDA" = true ]; then
    log "Uninstalling KEDA..."
    helm uninstall keda -n "$KEDA_NAMESPACE" 2>/dev/null || warn "KEDA not found or already removed"
    kubectl delete namespace "$KEDA_NAMESPACE" --wait=false 2>/dev/null || true
fi

if [ "$DELETE_DOCUMENTDB" = true ]; then
    log "Deleting DocumentDB instance..."
    kubectl delete documentdb "$DOCUMENTDB_NAME" -n "$DOCUMENTDB_NAMESPACE" --ignore-not-found=true 2>/dev/null || true
    kubectl delete secret documentdb-credentials -n "$DOCUMENTDB_NAMESPACE" --ignore-not-found=true 2>/dev/null || true
fi

log "Teardown complete"
