#!/usr/bin/env bash
# 50-run-matrix.sh — run the io_method x workload x selectivity x repeat matrix.
#
# For each io_method: patch the CR + wait for the rolling restart, then run each read
# workload at each selectivity REPEATS times. Locust CSVs and a pg_stat_io snapshot are
# written inside the runner pod under /results/<method>/...; pull them out with
# ./60-collect.sh.
#
# Tunables (see lib.sh): IO_METHODS, WORKLOADS, SELECTIVITIES, REPEATS, RUN_TIME,
#   QUERY_USERS, DOCUMENT_COUNT, IO_WORKERS.
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl
POD="$(bench_pod)"; [ -n "$POD" ] || die "benchmark runner pod not found — run ./30-deploy-documentdb.sh"

log "matrix: methods=[$IO_METHODS] workloads=[$WORKLOADS] selectivities=[$SELECTIVITIES] repeats=$REPEATS run-time=$RUN_TIME"

for method in $IO_METHODS; do
  switch_io_method "$method"
  for wl in $WORKLOADS; do
    for sel in $SELECTIVITIES; do
      for rep in $(seq 1 "$REPEATS"); do
        cell="${wl}_sel${sel}_r${rep}"
        log ">>> ${method} / ${cell}"
        reset_io_stats
        skip_index=""; [ "$rep" -gt 1 ] && skip_index="--skip-index-setup"
        kubectl exec -n "$NS" "$POD" -- bash -lc '
          set -euo pipefail
          cd /opt/micro-benchmarks
          OUT="/results/'"$method"'"; mkdir -p "$OUT"
          URI="mongodb://${MONGO_USERNAME}:${MONGO_PASSWORD}@${GATEWAY_HOST}:${GATEWAY_PORT}/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true"
          bash test/run_locust.sh \
            --locustfile workloads/read_queries/'"$wl"'.py \
            --uri "$URI" \
            --users '"$QUERY_USERS"' --run-time '"$RUN_TIME"' \
            --document-count '"$DOCUMENT_COUNT"' \
            --selectivity '"$sel"' \
            --skip-data-load '"$skip_index"' \
            --csv "$OUT/'"$cell"'"
        ' || warn "cell ${method}/${cell} failed (continuing)"

        # Capture the I/O accounting for this cell from PG18 pg_stat_io.
        dump_io_stats > /tmp/iostat.csv 2>/dev/null || true
        kubectl cp /tmp/iostat.csv "$NS/$POD:/results/$method/${cell}_pg_stat_io.csv" >/dev/null 2>&1 || true
      done
    done
  done
done

log "matrix complete. Next: ./60-collect.sh"
