#!/usr/bin/env bash
# 40-load-data.sh — load the dataset ONCE via the in-cluster runner.
#
# Runs documentdb/micro-benchmarks find_read_loader, which drops the collection, inserts
# DOCUMENT_COUNT docs, then auto-quits. The io_method in effect during load does not
# matter — the query phase is where io_uring is measured. Subsequent query runs auto-skip
# loading when the collection already holds >= 95% of DOCUMENT_COUNT.
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl
POD="$(bench_pod)"; [ -n "$POD" ] || die "benchmark runner pod not found — run ./30-deploy-documentdb.sh"

log "loading $DOCUMENT_COUNT documents (users=$LOAD_USERS, batch=$LOAD_BATCH_SIZE) — this can take a while..."
kubectl exec -n "$NS" "$POD" -- bash -lc '
set -euo pipefail
cd /opt/micro-benchmarks
URI="mongodb://${MONGO_USERNAME}:${MONGO_PASSWORD}@${GATEWAY_HOST}:${GATEWAY_PORT}/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true"
bash test/run_locust.sh \
  --locustfile workloads/read_queries/find_read_loader.py \
  --uri "$URI" \
  --users '"$LOAD_USERS"' --run-time 9999s \
  --document-count '"$DOCUMENT_COUNT"' --load-batch-size '"$LOAD_BATCH_SIZE"'
'
log "data load complete. Next: ./50-run-matrix.sh"
