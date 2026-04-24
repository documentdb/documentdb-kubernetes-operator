#!/usr/bin/env bash
set -euo pipefail

APP_NAMESPACE="${APP_NAMESPACE:-app}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS_DIR="${SCRIPT_DIR}/../manifests"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${GREEN}[INFO]${NC} $*"; }
step() { echo -e "\n${CYAN}=== $* ===${NC}\n"; }

pause() {
    echo -e "${YELLOW}Press Enter to continue...${NC}"
    read -r
}

step "Current state"
kubectl get scaledobject,hpa -n "$APP_NAMESPACE" 2>/dev/null || true
echo ""
kubectl get pods -n "$APP_NAMESPACE" 2>/dev/null || true

pause

step "Seeding 10 pending jobs into DocumentDB"
kubectl delete job seed-pending-jobs -n "$APP_NAMESPACE" --ignore-not-found=true 2>/dev/null
kubectl apply -n "$APP_NAMESPACE" -f "${MANIFESTS_DIR}/seed-jobs.yaml"
kubectl wait --for=condition=complete job/seed-pending-jobs -n "$APP_NAMESPACE" --timeout=120s 2>/dev/null || true
kubectl logs job/seed-pending-jobs -n "$APP_NAMESPACE" 2>/dev/null || true

step "Watching pods scale up (Ctrl+C to continue)"
log "KEDA polls every 5 seconds. Worker pods should appear within 15-30 seconds."
# Use perl-based timeout for macOS compatibility (coreutils timeout not available by default)
perl -e 'alarm 60; exec @ARGV' kubectl get pods -n "$APP_NAMESPACE" -w 2>/dev/null || true

pause

step "Draining all pending jobs (marking as completed)"
kubectl delete job drain-pending-jobs -n "$APP_NAMESPACE" --ignore-not-found=true 2>/dev/null
kubectl apply -n "$APP_NAMESPACE" -f "${MANIFESTS_DIR}/drain-jobs.yaml"
kubectl wait --for=condition=complete job/drain-pending-jobs -n "$APP_NAMESPACE" --timeout=120s 2>/dev/null || true
kubectl logs job/drain-pending-jobs -n "$APP_NAMESPACE" 2>/dev/null || true

step "Watching pods scale down (cooldown: 30 seconds)"
perl -e 'alarm 90; exec @ARGV' kubectl get pods -n "$APP_NAMESPACE" -w 2>/dev/null || true

step "Final state"
kubectl get scaledobject,hpa -n "$APP_NAMESPACE" 2>/dev/null || true
echo ""
kubectl get pods -n "$APP_NAMESPACE" 2>/dev/null || true

echo ""
log "Demo complete!"
