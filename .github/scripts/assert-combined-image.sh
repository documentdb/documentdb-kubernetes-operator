#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
#
# Asserts that a DocumentDB cluster runs in combined image mode with the
# published combined image for the node's architecture, then checks the
# extension version and Mongo wire-protocol reads/writes.
#
# Required env:
#   DB_NS, DB_NAME            DocumentDB namespace and name
#   EXPECTED_IMAGE            e.g. ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb-combined:18-1.0.0
#   EXPECTED_ARCH             amd64 | arm64
#   EXPECTED_EXT_VERSION      pg_extension version, e.g. 1.0-0
#   KIND_CLUSTER              kind cluster name (to inspect the pulled image on the node)
#   DB_USERNAME, DB_PASSWORD  DocumentDB credentials
# Optional env:
#   DB_PORT (default 10260)
set -euo pipefail

: "${DB_NS:?}" "${DB_NAME:?}" "${EXPECTED_IMAGE:?}" "${EXPECTED_ARCH:?}" "${EXPECTED_EXT_VERSION:?}"
: "${KIND_CLUSTER:?}" "${DB_USERNAME:?}" "${DB_PASSWORD:?}"
DB_PORT=${DB_PORT:-10260}
POD="${DB_NAME}-1"
FAILED=0

check() {
  local what=$1 got=$2 want=$3
  if [[ "$got" == "$want" ]]; then
    echo "✓ $what: $got"
  else
    echo "❌ $what: got '$got', want '$want'"
    FAILED=1
  fi
}

cnpg() { kubectl -n "$DB_NS" get clusters.postgresql.cnpg.io "$DB_NAME" -o jsonpath="$1"; }

# Fail fast: the "no image volumes" checks compare against "" and would pass
# vacuously if the objects were missing.
if ! kubectl -n "$DB_NS" get clusters.postgresql.cnpg.io "$DB_NAME" >/dev/null || \
   ! kubectl -n "$DB_NS" get pod "$POD" >/dev/null; then
  echo "❌ CNPG Cluster $DB_NAME or pod $POD not found in $DB_NS"
  exit 1
fi

echo "== Image mode selected by the operator"
kubectl -n "$DB_NS" get events --field-selector reason=ImageModeSelected \
  -o jsonpath='{range .items[*]}{.message}{"\n"}{end}'
check "CNPG image-mode annotation" "$(cnpg '{.metadata.annotations.documentdb\.io/image-mode}')" "combined"
check "CNPG spec.imageName" "$(cnpg '{.spec.imageName}')" "$EXPECTED_IMAGE"
check "CNPG extension image volumes" "$(cnpg '{.spec.postgresql.extensions[*].name}')" ""
check "postgres container image" \
  "$(kubectl -n "$DB_NS" get pod "$POD" -o jsonpath='{.spec.containers[?(@.name=="postgres")].image}')" "$EXPECTED_IMAGE"
check "pod image volumes" "$(kubectl -n "$DB_NS" get pod "$POD" -o jsonpath='{.spec.volumes[*].image.reference}')" ""
check "status.documentDBImage" "$(kubectl -n "$DB_NS" get documentdb "$DB_NAME" -o jsonpath='{.status.documentDBImage}')" "$EXPECTED_IMAGE"

echo "== Architecture of the pulled image"
NODE=$(kubectl -n "$DB_NS" get pod "$POD" -o jsonpath='{.spec.nodeName}')
check "node architecture" "$(kubectl get node "$NODE" -o jsonpath='{.status.nodeInfo.architecture}')" "$EXPECTED_ARCH"
if ! kind get nodes --name "$KIND_CLUSTER" | grep -qx "$NODE"; then
  echo "❌ pod node $NODE is not a node of kind cluster $KIND_CLUSTER"
  FAILED=1
else
  # The image index is multi-arch; the runtime must have pulled the manifest
  # for this node's platform.
  check "pulled image architecture (crictl)" \
    "$(docker exec "$NODE" crictl inspecti -o json "$EXPECTED_IMAGE" | jq -r '.info.imageSpec.architecture')" "$EXPECTED_ARCH"
fi
case "$EXPECTED_ARCH" in
  amd64) WANT_MACHINE=x86_64 ;;
  arm64) WANT_MACHINE=aarch64 ;;
  *) WANT_MACHINE=$EXPECTED_ARCH ;;
esac
check "postgres container machine" "$(kubectl -n "$DB_NS" exec "$POD" -c postgres -- uname -m | tr -d '[:space:]')" "$WANT_MACHINE"

echo "== Extension"
check "documentdb extension version" \
  "$(kubectl -n "$DB_NS" exec "$POD" -c postgres -- psql -U postgres -tAc \
    "select extversion from pg_extension where extname='documentdb'" | tr -d '[:space:]')" "$EXPECTED_EXT_VERSION"

echo "== Mongo wire protocol"
python3 -m pip install --quiet --user pymongo
PF_LOG=$(mktemp)
kubectl -n "$DB_NS" port-forward "svc/documentdb-service-${DB_NAME}" "0:${DB_PORT}" >"$PF_LOG" 2>&1 &
PF_PID=$!
trap 'kill "$PF_PID" 2>/dev/null || true' EXIT
LOCAL_PORT=""
for _ in $(seq 1 30); do
  LOCAL_PORT=$(grep -o '127.0.0.1:[0-9]*' "$PF_LOG" | head -1 | cut -d: -f2 || true)
  [[ -n "$LOCAL_PORT" ]] && break
  sleep 1
done
if [[ -z "$LOCAL_PORT" ]]; then
  echo "❌ port-forward did not start"; cat "$PF_LOG"; exit 1
fi
if DB_USERNAME="$DB_USERNAME" DB_PASSWORD="$DB_PASSWORD" LOCAL_PORT="$LOCAL_PORT" python3 - <<'EOF'
import os
from urllib.parse import quote_plus
from pymongo import MongoClient

uri = (f"mongodb://{quote_plus(os.environ['DB_USERNAME'])}:{quote_plus(os.environ['DB_PASSWORD'])}"
       f"@127.0.0.1:{os.environ['LOCAL_PORT']}/?tls=true&tlsAllowInvalidCertificates=true"
       "&directConnection=true&authMechanism=SCRAM-SHA-256")
col = MongoClient(uri, serverSelectionTimeoutMS=30000).combined_e2e.items
col.drop()
col.insert_many([{"_id": i, "v": i * i} for i in range(100)])
col.create_index("v")
assert col.count_documents({}) == 100
assert col.find_one({"_id": 7})["v"] == 49
print("✓ mongo: 100 documents written, indexed and read back")
EOF
then :; else echo "❌ mongo wire-protocol check failed"; FAILED=1; fi

if [[ $FAILED -ne 0 ]]; then
  echo "Combined image assertions FAILED"
  exit 1
fi
echo "All combined image assertions passed"
