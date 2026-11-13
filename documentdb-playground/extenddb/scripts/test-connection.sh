#!/usr/bin/env bash
# Smoke test: port-forward to the ExtendDB service and run
# basic CRUD plus TransactWriteItems/TransactGetItems through the AWS CLI to
# confirm the DynamoDB API round-trips through ExtendDB into DocumentDB.
#
# Requires a SigV4 access key/secret pair -- NOT the admin username/password
# printed by scripts/deploy.sh, which authenticates the management API only.
# See ../README.md's "Creating a DynamoDB API access key" section for the
# extenddb manage create-account/create-user/put-user-policy/
# create-access-key steps needed to obtain one. Pass the resulting keys via
# EXTENDDB_ACCESS_KEY_ID / EXTENDDB_SECRET_ACCESS_KEY, or export them as
# AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY yourself before running this
# script.
set -euo pipefail

command -v kubectl >/dev/null || { echo "kubectl is required" >&2; exit 1; }
command -v aws >/dev/null || { echo "aws CLI is required" >&2; exit 1; }

EXTENDDB_NAMESPACE="${EXTENDDB_NAMESPACE:-extenddb}"
LOCAL_PORT="${LOCAL_PORT:-18443}"
ENDPOINT="https://127.0.0.1:${LOCAL_PORT}"
TABLE_NAME="${TABLE_NAME:-extenddb-playground-smoke-test}"

export AWS_ACCESS_KEY_ID="${EXTENDDB_ACCESS_KEY_ID:-${AWS_ACCESS_KEY_ID:-}}"
export AWS_SECRET_ACCESS_KEY="${EXTENDDB_SECRET_ACCESS_KEY:-${AWS_SECRET_ACCESS_KEY:-}}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
export AWS_PAGER=""

if [ -z "$AWS_ACCESS_KEY_ID" ] || [ -z "$AWS_SECRET_ACCESS_KEY" ]; then
    echo "Set EXTENDDB_ACCESS_KEY_ID / EXTENDDB_SECRET_ACCESS_KEY (created" >&2
    echo "through extenddb manage), or AWS_ACCESS_KEY_ID /" >&2
    echo "AWS_SECRET_ACCESS_KEY directly." >&2
    exit 1
fi

echo "=== ExtendDB Connection Smoke Test ==="

# ExtendDB uses a self-signed TLS certificate by default; skip verification
# for this local playground test rather than extracting the generated cert.
AWS_CLI_OPTS=(--endpoint-url "$ENDPOINT" --no-verify-ssl)
PF_PID=""
TABLE_CREATED=false

cleanup() {
    status=$?
    trap - EXIT
    if [ "$TABLE_CREATED" = true ]; then
        echo ""
        echo "--- DeleteTable (failure cleanup) ---"
        aws dynamodb delete-table "${AWS_CLI_OPTS[@]}" \
            --table-name "$TABLE_NAME" >/dev/null 2>&1 || true
    fi
    if [ -n "$PF_PID" ]; then
        kill "$PF_PID" 2>/dev/null || true
    fi
    exit "$status"
}
trap cleanup EXIT

echo ""
echo "--- Port-forwarding svc/extenddb (namespace: $EXTENDDB_NAMESPACE) ---"
kubectl port-forward svc/extenddb "${LOCAL_PORT}:18443" \
    -n "$EXTENDDB_NAMESPACE" >/dev/null 2>&1 &
PF_PID=$!
sleep 3

echo ""
echo "--- CreateTable ---"
aws dynamodb create-table "${AWS_CLI_OPTS[@]}" \
    --table-name "$TABLE_NAME" \
    --attribute-definitions AttributeName=id,AttributeType=S \
    --key-schema AttributeName=id,KeyType=HASH \
    --billing-mode PAY_PER_REQUEST >/dev/null
TABLE_CREATED=true
aws dynamodb wait table-exists "${AWS_CLI_OPTS[@]}" --table-name "$TABLE_NAME"
echo "Table '$TABLE_NAME' created."

echo ""
echo "--- PutItem ---"
aws dynamodb put-item "${AWS_CLI_OPTS[@]}" \
    --table-name "$TABLE_NAME" \
    --item '{"id": {"S": "smoke-test-1"}, "message": {"S": "hello from ExtendDB via DocumentDB"}}'
echo "Item written."

echo ""
echo "--- GetItem ---"
aws dynamodb get-item "${AWS_CLI_OPTS[@]}" \
    --table-name "$TABLE_NAME" \
    --key '{"id": {"S": "smoke-test-1"}}'

echo ""
echo "--- UpdateItem ---"
aws dynamodb update-item "${AWS_CLI_OPTS[@]}" \
    --table-name "$TABLE_NAME" \
    --key '{"id": {"S": "smoke-test-1"}}' \
    --update-expression 'SET #message = :message' \
    --expression-attribute-names '{"#message": "message"}' \
    --expression-attribute-values '{":message": {"S": "updated through ExtendDB"}}'
echo "Item updated."

echo ""
echo "--- DeleteItem ---"
aws dynamodb delete-item "${AWS_CLI_OPTS[@]}" \
    --table-name "$TABLE_NAME" \
    --key '{"id": {"S": "smoke-test-1"}}'
echo "Item deleted."

echo ""
echo "--- TransactWriteItems ---"
TRANSACT_WRITE_ITEMS=$(printf \
    '[{"Put":{"TableName":"%s","Item":{"id":{"S":"transaction-test-1"},"message":{"S":"written transactionally"}}}}]' \
    "$TABLE_NAME")
aws dynamodb transact-write-items "${AWS_CLI_OPTS[@]}" \
    --transact-items "$TRANSACT_WRITE_ITEMS"
echo "Transactional item written."

echo ""
echo "--- TransactGetItems ---"
TRANSACT_GET_ITEMS=$(printf \
    '[{"Get":{"TableName":"%s","Key":{"id":{"S":"transaction-test-1"}}}}]' \
    "$TABLE_NAME")
TRANSACTION_MESSAGE=$(aws dynamodb transact-get-items "${AWS_CLI_OPTS[@]}" \
    --transact-items "$TRANSACT_GET_ITEMS" \
    --query 'Responses[0].Item.message.S' \
    --output text)
if [ "$TRANSACTION_MESSAGE" != "written transactionally" ]; then
    echo "TransactGetItems returned an unexpected item" >&2
    exit 1
fi
echo "Transactional item read."

echo ""
echo "--- DeleteTable (cleanup) ---"
aws dynamodb delete-table "${AWS_CLI_OPTS[@]}" --table-name "$TABLE_NAME" >/dev/null
aws dynamodb wait table-not-exists "${AWS_CLI_OPTS[@]}" --table-name "$TABLE_NAME"
TABLE_CREATED=false
echo "Table '$TABLE_NAME' deleted."

echo ""
echo "✓ ExtendDB is serving the DynamoDB API on top of DocumentDB."
