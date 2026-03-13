#!/bin/bash
# Lean gateway entrypoint for CNPG sidecar mode.
# Handles args passed by the sidecar-injector plugin:
#   --create-user, --start-pg, --pg-port, --cert-path, --key-file
set -e

CREATE_USER="true"
PG_PORT="5432"
CERT_PATH=""
KEY_FILE=""
# In CNPG clusters the superuser is always "postgres"
OWNER="${OWNER:-postgres}"
USERNAME="${USERNAME:-}"
PASSWORD="${PASSWORD:-}"

while [[ $# -gt 0 ]]; do
  case $1 in
    --create-user) shift; CREATE_USER="$1"; shift;;
    --start-pg)    shift; shift;;  # ignored — PG managed by CNPG
    --pg-port)     shift; PG_PORT="$1"; shift;;
    --cert-path)   shift; CERT_PATH="$1"; shift;;
    --key-file)    shift; KEY_FILE="$1"; shift;;
    --owner)       shift; OWNER="$1"; shift;;
    --username)    shift; USERNAME="$1"; shift;;
    --password)    shift; PASSWORD="$1"; shift;;
    *) echo "Unknown option: $1" >&2; shift;;
  esac
done

# PG is in a separate container; force TCP connection via localhost
export PGHOST=localhost

# Set up gateway configuration
CONFIG="/home/documentdb/gateway/pg_documentdb_gw/target/SetupConfiguration_temp.json"
cp /home/documentdb/gateway/pg_documentdb_gw/SetupConfiguration.json "$CONFIG"

if ! [[ "$PG_PORT" =~ ^[0-9]+$ ]]; then
  echo "ERROR: PG_PORT must be a number, got: $PG_PORT" >&2
  exit 1
fi
jq --argjson port "$PG_PORT" '.PostgresPort = $port' "$CONFIG" > "$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"

if [ -n "$CERT_PATH" ] && [ -n "$KEY_FILE" ]; then
  jq --arg c "$CERT_PATH" --arg k "$KEY_FILE" \
    '.CertificateOptions = {"CertType":"PemFile","FilePath":$c,"KeyFilePath":$k}' \
    "$CONFIG" > "$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
fi

# Wait for PostgreSQL (TCP readiness check)
echo "Waiting for PostgreSQL on localhost:$PG_PORT..."
timeout=600; elapsed=0
while ! pg_isready -h localhost -p "$PG_PORT" -q 2>/dev/null; do
  if [ "$elapsed" -ge "$timeout" ]; then
    echo "PostgreSQL did not become ready within ${timeout}s"; exit 1
  fi
  sleep 2; elapsed=$((elapsed + 2))
done
echo "PostgreSQL is ready."

# Create admin user if requested
if [ "$CREATE_USER" = "true" ] && [ -n "$USERNAME" ] && [ -n "$PASSWORD" ]; then
  echo "Creating admin user $USERNAME..."
  source /home/documentdb/gateway/scripts/utils.sh
  SetupCustomAdminUser "$USERNAME" "$PASSWORD" "$PG_PORT" "$OWNER"
fi

# Start gateway
exec /usr/bin/documentdb_gateway "$CONFIG"
