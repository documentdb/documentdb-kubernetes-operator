#!/usr/bin/env bash
# Deploy ExtendDB (DynamoDB-compatible API, MongoDB storage backend) wired up
# to an existing DocumentDB instance in the cluster.
#
# Prerequisites: kubectl, envsubst (gettext), a running cluster with the
# DocumentDB operator installed and a DocumentDB resource already deployed
# (see ../documentdb.yaml), and the extenddb-mongo:playground image built
# and loaded/pushed (see build-image.sh).
set -euo pipefail

command -v kubectl >/dev/null || { echo "kubectl is required" >&2; exit 1; }
command -v envsubst >/dev/null || { echo "envsubst (gettext) is required" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MANIFEST_DIR="$SCRIPT_DIR/../manifests"

export EXTENDDB_NAMESPACE="${EXTENDDB_NAMESPACE:-extenddb}"
export EXTENDDB_IMAGE="${EXTENDDB_IMAGE:-extenddb-mongo:playground}"
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-test}"
DOCUMENTDB_CLUSTER="${DOCUMENTDB_CLUSTER:-documentdb-cluster}"

echo "=== ExtendDB + DocumentDB Deployment ==="

# 1. Namespace + PVC
echo ""
echo "--- Step 1: Namespace and persistent state volume ---"
envsubst '${EXTENDDB_NAMESPACE}' < "$MANIFEST_DIR/namespace.yaml" | kubectl apply -f -

# 2. Resolve the DocumentDB connection string
echo ""
echo "--- Step 2: DocumentDB connection ---"
RAW_CONN=$(kubectl get documentdb "$DOCUMENTDB_CLUSTER" -n "$DOCUMENTDB_NAMESPACE" \
    -o jsonpath='{.status.connectionString}' 2>/dev/null) || true

if [ -z "$RAW_CONN" ]; then
    echo "Could not read status.connectionString from DocumentDB resource" >&2
    echo "'$DOCUMENTDB_CLUSTER' in namespace '$DOCUMENTDB_NAMESPACE'." >&2
    echo "Set MONGO_URI yourself and re-run, or check DOCUMENTDB_NAMESPACE/DOCUMENTDB_CLUSTER." >&2
    exit 1
fi

# The operator embeds shell substitutions for the credentials in the printed
# connection string. Resolve the credentials directly instead of eval: a
# password containing shell metacharacters (for example '!') must remain
# literal and must never be interpreted by a shell.
DOCDB_USER=$(kubectl get secret docdb-credentials -n "$DOCUMENTDB_NAMESPACE" \
    -o jsonpath='{.data.username}' | base64 -d)
DOCDB_PASSWORD=$(kubectl get secret docdb-credentials -n "$DOCUMENTDB_NAMESPACE" \
    -o jsonpath='{.data.password}' | base64 -d)
case "$RAW_CONN" in
    mongodb://*@*) ;;
    *)
        echo "DocumentDB status.connectionString has an unexpected format" >&2
        exit 1
        ;;
esac
MONGO_URI="mongodb://${DOCDB_USER}:${DOCDB_PASSWORD}@${RAW_CONN#*@}"

# Swap the ClusterIP for the in-cluster DNS name so the URI keeps working
# after the DocumentDB service's IP changes.
SVC_IP=$(kubectl get svc "documentdb-service-${DOCUMENTDB_CLUSTER}" -n "$DOCUMENTDB_NAMESPACE" \
    -o jsonpath='{.spec.clusterIP}' 2>/dev/null) || true
if [ -n "$SVC_IP" ]; then
    SVC_DNS="documentdb-service-${DOCUMENTDB_CLUSTER}.${DOCUMENTDB_NAMESPACE}.svc.cluster.local"
    MONGO_URI=$(echo "$MONGO_URI" | sed "s/$SVC_IP/$SVC_DNS/g")
fi

# ExtendDB's MongoDB backend accepts (and its docs recommend) `replicaSet=rs0`
# for standalone MongoDB deployments, since real single-node MongoDB needs a
# replica set for transactions/Change Streams. DocumentDB's gateway doesn't
# need this from the client -- it identifies as a mongos ("isdbgrid") with no
# `setName` at all, regardless of what the client asks for -- and the Rust
# `mongodb` v3 driver ExtendDB links strictly validates any requested
# `replicaSet` name against the server's actual one even when
# `directConnection=true` is also set, unlike some other drivers (Node's
# mongosh) which skip that validation under direct connection. Empirically
# confirmed with the real `extenddb init --backend mongodb` binary against
# this operator's DocumentDB gateway (v0.110.0):
#   directConnection=true & replicaSet=rs0 -> fails: "Connection string
#     replicaSet name \"rs0\" does not match actual name <none>"
#   directConnection=true, no replicaSet   -> succeeds
# So -- same as the lightrag and keda-autoscaling playgrounds in this repo,
# and for the same underlying reason -- strip replicaSet=rs0 here too.
# See ../README.md's Troubleshooting section for the full writeup.
MONGO_URI=$(echo "$MONGO_URI" | sed -E 's/[?&]replicaSet=[^&]*//g')

# TLS: DocumentDB's gateway uses a self-signed certificate by default (see
# ../tls/), which the Rust `mongodb` driver's default certificate verifier
# rejects with "invalid peer certificate: UnknownIssuer". This is a
# playground/demo (not a production TLS trust setup), so append
# tlsAllowInvalidCertificates=true, matching the same pattern already used
# by the lightrag and keda-autoscaling playgrounds in this repo.
case "$MONGO_URI" in
    *tlsAllowInvalidCertificates=*) ;; # already present, leave as-is
    *\?*) MONGO_URI="${MONGO_URI}&tlsAllowInvalidCertificates=true" ;;
    *) MONGO_URI="${MONGO_URI}?tlsAllowInvalidCertificates=true" ;;
esac
echo "Connection string resolved (replicaSet=rs0 stripped, tlsAllowInvalidCertificates=true added for the self-signed gateway cert)."

kubectl create secret generic extenddb-mongo-uri \
    -n "$EXTENDDB_NAMESPACE" \
    --from-literal=connection_string="$MONGO_URI" \
    --dry-run=client -o yaml | kubectl apply -f -

# 3. Run the init Job and wait for it to complete before starting the server,
#    since both mount the same ReadWriteOnce PVC.
echo ""
echo "--- Step 3: Initialize ExtendDB (extenddb init --backend mongodb) ---"
kubectl delete job extenddb-init -n "$EXTENDDB_NAMESPACE" --ignore-not-found
envsubst '${EXTENDDB_NAMESPACE} ${EXTENDDB_IMAGE}' < "$MANIFEST_DIR/init-job.yaml" | kubectl apply -f -
kubectl wait --for=condition=Complete job/extenddb-init -n "$EXTENDDB_NAMESPACE" --timeout=180s

echo ""
echo "Admin credentials (printed once by 'extenddb init' -- save them now):"
echo "----------------------------------------------------------------------"
INIT_LOGS=$(kubectl logs job/extenddb-init -n "$EXTENDDB_NAMESPACE")
CRED_LINES=$(echo "$INIT_LOGS" | grep -iE "admin|access|secret|account" || true)
if [ -n "$CRED_LINES" ]; then
    echo "$CRED_LINES"
else
    echo "(Could not identify credential lines by keyword match -- showing full Job logs"
    echo " so nothing is lost; init prints credentials only once.)"
    echo "$INIT_LOGS"
fi
echo "----------------------------------------------------------------------"

# 4. Deploy the long-running server
echo ""
echo "--- Step 4: Deploy ExtendDB server ---"
envsubst '${EXTENDDB_NAMESPACE} ${EXTENDDB_IMAGE}' < "$MANIFEST_DIR/serve.yaml" | kubectl apply -f -
echo "Waiting for ExtendDB pod to be ready..."
kubectl wait --for=condition=Ready pod -l app=extenddb -n "$EXTENDDB_NAMESPACE" --timeout=300s

echo ""
echo "✓ ExtendDB deployed in namespace '$EXTENDDB_NAMESPACE', backed by DocumentDB"
echo "  ('$DOCUMENTDB_CLUSTER' in '$DOCUMENTDB_NAMESPACE')."
echo ""
echo "Next: port-forward and test with:"
echo "  kubectl port-forward svc/extenddb 18443:18443 -n $EXTENDDB_NAMESPACE"
echo "  ./scripts/test-connection.sh"
