#!/usr/bin/env bash
# Shared helpers and configuration for the io_uring benchmark playground.
# Source this from the numbered scripts:  source "$(dirname "$0")/lib.sh"
set -euo pipefail

# --------------------------------------------------------------------------
# Configuration (override via environment)
# --------------------------------------------------------------------------
NS="${NS:-iouring-bench}"                       # namespace for the DocumentDB + runner
NAME="${NAME:-iouring-bench}"                   # DocumentDB / CNPG cluster name
GATEWAY_PORT="${GATEWAY_PORT:-10260}"
DOCUMENT_COUNT="${DOCUMENT_COUNT:-10000000}"    # dataset size; MUST exceed RAM to expose I/O
LOAD_USERS="${LOAD_USERS:-50}"
LOAD_BATCH_SIZE="${LOAD_BATCH_SIZE:-100}"
QUERY_USERS="${QUERY_USERS:-1}"
RUN_TIME="${RUN_TIME:-180s}"                    # per query cell (must cover index build)
REPEATS="${REPEATS:-3}"                         # repeats per cell
IO_METHODS="${IO_METHODS:-sync worker io_uring}"
WORKLOADS="${WORKLOADS:-range_scalar range_arr point_scalar point_arr}"
SELECTIVITIES="${SELECTIVITIES:-100 1k 5k}"
IO_WORKERS="${IO_WORKERS:-3}"
SECCOMP_MODE="${SECCOMP_MODE:-unconfined}"      # unconfined | localhost
RESULTS_DIR="${RESULTS_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/results}"

# --------------------------------------------------------------------------
# Logging
# --------------------------------------------------------------------------
log()  { printf '\033[1;34m[io-uring]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[io-uring][warn]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[io-uring][error]\033[0m %s\n' "$*" >&2; exit 1; }

require() {
  for bin in "$@"; do
    command -v "$bin" >/dev/null 2>&1 || die "required tool not found on PATH: $bin"
  done
}

# --------------------------------------------------------------------------
# Cluster helpers
# --------------------------------------------------------------------------

# Name of the CNPG primary postgres pod for our cluster.
pg_primary_pod() {
  kubectl get pods -n "$NS" \
    -l "cnpg.io/cluster=$NAME,cnpg.io/instanceRole=primary" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
}

# Run psql inside the primary postgres container (trust auth over the local socket).
pgq() {
  local pod; pod="$(pg_primary_pod)"
  [ -n "$pod" ] || die "no primary postgres pod found for cluster $NAME in $NS"
  kubectl exec -n "$NS" "$pod" -c postgres -- psql -U postgres -tAc "$1"
}

# Current effective io_method inside postgres.
current_io_method() { pgq "SHOW io_method;" | tr -d '[:space:]'; }

# Name of the benchmark runner pod.
bench_pod() {
  kubectl get pods -n "$NS" -l app=bench-runner \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
}

# Wait until the DocumentDB-backed CNPG cluster reports a healthy primary.
wait_cluster_ready() {
  local timeout="${1:-600}" waited=0
  log "waiting for CNPG cluster '$NAME' to become healthy (timeout ${timeout}s)..."
  while :; do
    local ready instances
    ready="$(kubectl get cluster.postgresql.cnpg.io "$NAME" -n "$NS" -o jsonpath='{.status.readyInstances}' 2>/dev/null || true)"
    instances="$(kubectl get cluster.postgresql.cnpg.io "$NAME" -n "$NS" -o jsonpath='{.spec.instances}' 2>/dev/null || true)"
    if [ -n "$ready" ] && [ "$ready" = "$instances" ]; then
      log "cluster healthy: ${ready}/${instances} instances ready."
      return 0
    fi
    [ "$waited" -ge "$timeout" ] && die "timed out waiting for cluster '$NAME' (ready='$ready' want='$instances')"
    sleep 5; waited=$((waited+5))
  done
}

# Switch io_method on the live DocumentDB CR and wait for the rolling restart to apply.
# Usage: switch_io_method <sync|worker|io_uring>
switch_io_method() {
  local method="$1"
  log "switching io_method -> ${method} (patching DocumentDB/${NAME})..."
  kubectl patch documentdb "$NAME" -n "$NS" --type merge -p \
    "{\"spec\":{\"postgres\":{\"parameters\":{\"io_method\":\"${method}\",\"io_workers\":\"${IO_WORKERS}\",\"track_io_timing\":\"on\"}}}}"

  # Give the operator + CNPG time to reconcile and roll the instance, then verify.
  local timeout=420 waited=0
  while :; do
    sleep 10; waited=$((waited+10))
    wait_cluster_ready 600 >/dev/null 2>&1 || true
    local got; got="$(current_io_method 2>/dev/null || true)"
    if [ "$got" = "$method" ]; then
      log "io_method now active: ${got}"
      return 0
    fi
    [ "$waited" -ge "$timeout" ] && die "timed out waiting for io_method=${method} (saw '${got}')"
    log "  ...still '${got:-?}', waiting for restart (${waited}s)"
  done
}

# Reset pg_stat_io so a fresh benchmark cell starts from zero counters.
reset_io_stats() { pgq "SELECT pg_stat_reset_shared('io');" >/dev/null 2>&1 || true; }

# Dump pg_stat_io (reads + read_time) to stdout as CSV.
dump_io_stats() {
  pgq "COPY (SELECT backend_type,object,context,reads,read_bytes,read_time,writes,write_time,extends FROM pg_stat_io WHERE reads>0 OR writes>0 ORDER BY reads DESC) TO STDOUT WITH CSV HEADER"
}
