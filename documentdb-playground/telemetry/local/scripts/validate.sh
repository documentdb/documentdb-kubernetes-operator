#!/bin/bash
set -euo pipefail
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-telemetry}"
CONTEXT="kind-${CLUSTER_NAME}"
PASS=0
FAIL=0

green() { echo -e "\033[32m✓ $1\033[0m"; PASS=$((PASS + 1)); }
red()   { echo -e "\033[31m✗ $1\033[0m"; FAIL=$((FAIL + 1)); }
warn()  { echo -e "\033[33m⚠ $1\033[0m"; }

echo "=== DocumentDB Telemetry Playground - Validation ==="
echo ""

# 1. Check observability deployments (no central OTel collector — every
# DocumentDB pod runs its own sidecar via spec.monitoring).
echo "--- Observability Stack ---"
for deploy in prometheus grafana; do
  if kubectl get deployment "$deploy" -n observability --context "$CONTEXT" &>/dev/null; then
    ready=$(kubectl get deployment "$deploy" -n observability --context "$CONTEXT" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
    if [ "${ready:-0}" -ge 1 ]; then
      green "$deploy is running"
    else
      red "$deploy is not ready (readyReplicas=${ready:-0})"
    fi
  else
    red "$deploy deployment not found"
  fi
done

# 2. Check DocumentDB pods AND that the otel-collector sidecar is injected.
echo ""
echo "--- DocumentDB ---"
running=$(kubectl get pods -l cnpg.io/cluster=documentdb-preview -n documentdb-preview-ns --context "$CONTEXT" --no-headers 2>/dev/null | grep -c "Running" || true)
if [ "$running" -ge 1 ]; then
  green "DocumentDB pods running ($running)"
else
  red "No DocumentDB pods running"
fi

# Verify each running pod has 3/3 containers (postgres + documentdb-gateway + otel-collector).
short=$(kubectl get pods -l cnpg.io/cluster=documentdb-preview -n documentdb-preview-ns --context "$CONTEXT" \
  -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.containerStatuses[*].name}{"\n"}{end}' 2>/dev/null || true)
missing_sidecar=0
while IFS= read -r line; do
  [ -z "$line" ] && continue
  if ! echo "$line" | grep -q "otel-collector"; then
    red "pod $(echo "$line" | awk '{print $1}') is missing the otel-collector sidecar"
    missing_sidecar=$((missing_sidecar + 1))
  fi
done <<< "$short"
if [ "$missing_sidecar" -eq 0 ] && [ "$running" -ge 1 ]; then
  green "otel-collector sidecar injected on all DocumentDB pods"
fi

# 3. Check Prometheus targets and key metrics
echo ""
echo "--- Data Flow ---"
PROM_POD=$(kubectl get pod -l app=prometheus -n observability --context "$CONTEXT" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
if [ -n "$PROM_POD" ]; then
  query() {
    kubectl exec "$PROM_POD" -n observability --context "$CONTEXT" -- \
      wget -qO- "http://localhost:9090/api/v1/query?query=$1" 2>/dev/null || echo ""
  }

  # Prometheus has at least one UP target?
  target_up=$(query "up")
  if echo "$target_up" | grep -q '"value"'; then
    up_count=$(echo "$target_up" | grep -o '"value":\[' | wc -l)
    green "Prometheus has $up_count active targets"
  else
    red "Cannot query Prometheus targets"
  fi

  # Sidecar scrape job is up?
  sidecar_up=$(query 'up{job="documentdb-otel-sidecar"}')
  if echo "$sidecar_up" | grep -q '"value":\["[^"]*","1"\]'; then
    green "OTel sidecar scrape targets are UP"
  else
    warn "OTel sidecar scrape targets not UP yet (sidecar may still be starting)"
  fi

  # Container resource metric collected by the OTel sidecar's kubeletstats
  # receiver and exported as Prometheus. The receiver scrapes the local
  # kubelet for the pod the sidecar runs in. Wait up to ~120s for the first
  # scrape interval + RBAC propagation.
  echo "Waiting up to 120s for kubeletstats container metrics to appear..."
  container_metric_found=0
  for _ in $(seq 1 24); do
    if echo "$(query 'container_cpu_usage{k8s_namespace_name=\"documentdb-preview-ns\"}')" | grep -q '"result":\[{'; then
      container_metric_found=1
      break
    fi
    sleep 5
  done
  if [ "$container_metric_found" -eq 1 ]; then
    green "Container metric container_cpu_usage present (via OTel sidecar kubeletstats)"
  else
    red "kubeletstats container metrics absent after 120s — check sidecar logs and ClusterRoleBinding for nodes/stats RBAC."
  fi
else
  red "Prometheus pod not found"
fi

# Summary
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  echo "Some checks failed. Components may still be starting up — retry in a minute."
  exit 1
fi
