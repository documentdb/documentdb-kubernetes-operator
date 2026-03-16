#!/bin/bash
set -euo pipefail

# Configuration
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-ns}"
DOCUMENTDB_NAME="${DOCUMENTDB_NAME:-keda-demo}"
DOCUMENTDB_SECRET="${DOCUMENTDB_SECRET:-documentdb-credentials}"
APP_NAMESPACE="${APP_NAMESPACE:-app}"
KEDA_NAMESPACE="${KEDA_NAMESPACE:-keda}"
KEDA_VERSION="${KEDA_VERSION:-2.17.0}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/../manifests"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()   { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

check_prerequisites() {
    log "Checking prerequisites..."
    local missing=0

    for cmd in kubectl helm; do
        if ! command -v "$cmd" &>/dev/null; then
            error "$cmd is required but not installed"
            missing=1
        fi
    done

    if ! command -v mongosh &>/dev/null; then
        warn "mongosh not found — seed/drain jobs run inside the Kubernetes cluster, so this is optional for local debugging"
    fi

    if ! kubectl cluster-info &>/dev/null; then
        error "Cannot connect to a Kubernetes cluster. Ensure your kubeconfig is set up."
        exit 1
    fi

    if [ "$missing" -eq 1 ]; then
        exit 1
    fi

    log "Prerequisites OK"
}

install_keda() {
    log "Installing KEDA ${KEDA_VERSION}..."

    if helm list -n "$KEDA_NAMESPACE" 2>/dev/null | grep -q keda; then
        warn "KEDA is already installed in namespace ${KEDA_NAMESPACE}. Skipping."
    else
        helm repo add kedacore https://kedacore.github.io/charts 2>/dev/null || true
        helm repo update kedacore

        helm install keda kedacore/keda \
            --namespace "$KEDA_NAMESPACE" \
            --create-namespace \
            --version "$KEDA_VERSION" \
            --wait \
            --timeout 120s
    fi

    log "Waiting for KEDA operator to be ready..."
    kubectl wait --for=condition=available deployment/keda-operator \
        -n "$KEDA_NAMESPACE" --timeout=120s

    log "KEDA is ready"
}

deploy_documentdb() {
    log "Deploying DocumentDB instance '${DOCUMENTDB_NAME}'..."

    kubectl create namespace "$DOCUMENTDB_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

    if kubectl get secret "$DOCUMENTDB_SECRET" -n "$DOCUMENTDB_NAMESPACE" &>/dev/null; then
        warn "Credentials secret already exists. Skipping."
    else
        kubectl create secret generic "$DOCUMENTDB_SECRET" \
            --namespace "$DOCUMENTDB_NAMESPACE" \
            --from-literal=username=docdbadmin \
            --from-literal=password='KedaDemo2024!'
    fi

    kubectl apply -f "${MANIFESTS_DIR}/documentdb-instance.yaml"

    wait_for_documentdb
}

wait_for_documentdb() {
    local timeout=300
    local interval=10
    local elapsed=0
    local svc_name="documentdb-service-${DOCUMENTDB_NAME}"

    log "Waiting for DocumentDB instance to be ready (timeout: ${timeout}s)..."

    while [ "$elapsed" -lt "$timeout" ]; do
        local ready_pods
        ready_pods=$(kubectl get pods -n "$DOCUMENTDB_NAMESPACE" \
            -l "documentdb.io/cluster=${DOCUMENTDB_NAME}" \
            --field-selector=status.phase=Running \
            -o name 2>/dev/null | wc -l | tr -d ' ')

        if [ "$ready_pods" -ge 1 ]; then
            if kubectl get svc "$svc_name" -n "$DOCUMENTDB_NAMESPACE" &>/dev/null; then
                log "DocumentDB instance is ready (${ready_pods} pod(s) running, service exists)"
                return 0
            fi
        fi

        echo -n "."
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    echo ""
    error "Timed out waiting for DocumentDB instance after ${timeout}s"
    error "Check pods: kubectl get pods -n ${DOCUMENTDB_NAMESPACE}"
    error "Check events: kubectl get events -n ${DOCUMENTDB_NAMESPACE} --sort-by=.lastTimestamp"
    exit 1
}

create_keda_connection_secret() {
    log "Creating KEDA connection secrets..."

    local user pass conn_string svc_name
    user=$(kubectl get secret "$DOCUMENTDB_SECRET" -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.data.username}' | base64 -d)
    pass=$(kubectl get secret "$DOCUMENTDB_SECRET" -n "$DOCUMENTDB_NAMESPACE" -o jsonpath='{.data.password}' | base64 -d)
    svc_name="documentdb-service-${DOCUMENTDB_NAME}.${DOCUMENTDB_NAMESPACE}.svc.cluster.local"

    conn_string="mongodb://${user}:${pass}@${svc_name}:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true"

    # Create in keda namespace (for ClusterTriggerAuthentication)
    kubectl create secret generic documentdb-keda-connection \
        --namespace "$KEDA_NAMESPACE" \
        --from-literal=connectionString="$conn_string" \
        --dry-run=client -o yaml | kubectl apply -f -

    # Create in app namespace (for seed/drain jobs)
    kubectl create secret generic documentdb-keda-connection \
        --namespace "$APP_NAMESPACE" \
        --from-literal=connectionString="$conn_string" \
        --dry-run=client -o yaml | kubectl apply -f -

    log "Connection secrets created in ${KEDA_NAMESPACE} and ${APP_NAMESPACE} namespaces"
}

deploy_keda_resources() {
    log "Deploying KEDA resources..."

    kubectl create namespace "$APP_NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

    create_keda_connection_secret

    kubectl apply -f "${MANIFESTS_DIR}/keda-trigger-auth.yaml"
    kubectl apply -f "${MANIFESTS_DIR}/job-worker.yaml"
    kubectl apply -f "${MANIFESTS_DIR}/keda-scaled-object.yaml"

    log "KEDA resources deployed"
    kubectl get scaledobject -n "$APP_NAMESPACE" 2>/dev/null || true
}

seed_test_data() {
    log "Seeding test data (10 pending jobs)..."

    # Delete previous seed job if it exists
    kubectl delete job seed-pending-jobs -n "$APP_NAMESPACE" --ignore-not-found=true

    kubectl apply -f "${MANIFESTS_DIR}/seed-jobs.yaml"

    log "Waiting for seed job to complete..."
    if kubectl wait --for=condition=complete job/seed-pending-jobs \
        -n "$APP_NAMESPACE" --timeout=120s 2>/dev/null; then
        log "Seed job completed"
        kubectl logs job/seed-pending-jobs -n "$APP_NAMESPACE" 2>/dev/null || true
    else
        warn "Seed job did not complete within timeout. Check logs:"
        warn "  kubectl logs job/seed-pending-jobs -n ${APP_NAMESPACE}"
    fi
}

main() {
    echo ""
    echo "============================================="
    echo "  KEDA + DocumentDB Autoscaling Playground"
    echo "============================================="
    echo ""

    check_prerequisites
    install_keda
    deploy_documentdb
    deploy_keda_resources
    seed_test_data

    echo ""
    log "Setup complete! KEDA is now monitoring DocumentDB for pending jobs."
    echo ""
    echo "  Verify autoscaling:"
    echo "    kubectl get scaledobject -n ${APP_NAMESPACE}"
    echo "    kubectl get hpa -n ${APP_NAMESPACE}"
    echo "    kubectl get pods -n ${APP_NAMESPACE} -w"
    echo ""
    echo "  Add more pending jobs:"
    echo "    kubectl delete job seed-pending-jobs -n ${APP_NAMESPACE} --ignore-not-found"
    echo "    kubectl apply -f ${MANIFESTS_DIR}/seed-jobs.yaml"
    echo ""
    echo "  Drain all pending jobs (scale back to 0):"
    echo "    kubectl delete job drain-pending-jobs -n ${APP_NAMESPACE} --ignore-not-found"
    echo "    kubectl apply -f ${MANIFESTS_DIR}/drain-jobs.yaml"
    echo ""
    echo "  Tear down:"
    echo "    ./scripts/teardown.sh"
    echo ""
}

main "$@"
