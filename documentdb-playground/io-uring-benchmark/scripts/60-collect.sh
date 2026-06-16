#!/usr/bin/env bash
# 60-collect.sh — copy benchmark CSVs out of the runner pod and summarize them.
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl
POD="$(bench_pod)"; [ -n "$POD" ] || die "benchmark runner pod not found"
PG_DIR="$(cd "$(dirname "$0")/.." && pwd)"

DEST="$RESULTS_DIR/raw"
mkdir -p "$DEST"
log "copying /results from runner pod -> $DEST ..."
kubectl cp "$NS/$POD:/results" "$DEST"

log "summarizing..."
if command -v python3 >/dev/null 2>&1; then
  python3 "$PG_DIR/analyze.py" "$DEST" --out "$RESULTS_DIR/summary.csv" | tee "$RESULTS_DIR/summary.txt"
  log "wrote $RESULTS_DIR/summary.csv and $RESULTS_DIR/summary.txt"
else
  warn "python3 not found locally; raw CSVs are in $DEST. Run analyze.py manually."
fi
