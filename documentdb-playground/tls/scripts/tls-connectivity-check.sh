#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: documentdb-gateway-check.sh [options]

Automates validation of DocumentDB gateway TLS setup. Currently supports the
SelfSigned flow end-to-end and can reuse the same structure for Provided mode.

Steps executed:
  1. Optionally install cert-manager
  2. Create namespace + credentials secret if missing
  3. Apply a DocumentDB manifest for gateway TLS (SelfSigned today)
  4. Wait for TLS readiness and capture service endpoint
  5. Run mongosh ping against the gateway

Options:
  -n, --namespace <name>      DocumentDB namespace (default: documentdb-preview-ns)
      --docdb-name <name>     DocumentDB resource name (default: documentdb-preview)
      --docdb-version <ver>   DocumentDB version (default: 16)
      --secret-name <name>    Credentials secret name (default: documentdb-credentials)
      --username <value>      DocumentDB username (default: docdbuser)
      --password <value>      DocumentDB password (default: P@ssw0rd123)
      --pvc-size <size>       Volume size (default: 10Gi)
      --storage-class <name>  StorageClass to use (optional)
  --mode <mode>           TLS mode: selfsigned|provided (default: selfsigned)
  --provided-secret <name>Secret with tls.crt/tls.key (required for provided if --keyvault not set)
  --keyvault <name>       Azure Key Vault name to download the gateway certificate (optional)
  --keyvault-cert <name>  Azure Key Vault certificate name (default: documentdb-gateway)
      --sni-host <host>       Hostname used for TLS verification (recommended for provided mode)
      --skip-cert-manager     Skip cert-manager install/upgrade
      --timeout <seconds>     Timeout for TLS readiness (default: 900)
      --skip-wait             Skip waiting for TLS readiness/mongosh
  -h, --help                  Show this message
EOF
}

NAMESPACE="documentdb-preview-ns"
DOCDB_NAME="documentdb-preview"
SECRET_NAME="documentdb-credentials"
SECRET_USER="docdbuser"
SECRET_PASS="P@ssw0rd123"
PVC_SIZE="10Gi"
STORAGE_CLASS=""
MODE="selfsigned"
PROVIDED_SECRET=""
KEYVAULT_NAME=""
KEYVAULT_CERT_NAME="documentdb-gateway"
SNI_HOST=""
INSTALL_CERT_MANAGER=1
CERT_MANAGER_RELEASE="cert-manager"
CERT_MANAGER_NAMESPACE="cert-manager"
TIMEOUT=900
WAIT_FOR_READY=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--namespace)
      NAMESPACE="$2"; shift 2 ;;
    --docdb-name)
      DOCDB_NAME="$2"; shift 2 ;;
    --secret-name)
      SECRET_NAME="$2"; shift 2 ;;
    --username)
      SECRET_USER="$2"; shift 2 ;;
    --password)
      SECRET_PASS="$2"; shift 2 ;;
    --pvc-size)
      PVC_SIZE="$2"; shift 2 ;;
    --storage-class)
      STORAGE_CLASS="$2"; shift 2 ;;
    --mode)
      MODE="$2"; shift 2 ;;
    --provided-secret)
      PROVIDED_SECRET="$2"; shift 2 ;;
    --keyvault)
      KEYVAULT_NAME="$2"; shift 2 ;;
    --keyvault-cert)
      KEYVAULT_CERT_NAME="$2"; shift 2 ;;
    --sni-host)
      SNI_HOST="$2"; shift 2 ;;
    --skip-cert-manager)
      INSTALL_CERT_MANAGER=0; shift ;;
    --timeout)
      TIMEOUT="$2"; shift 2 ;;
    --skip-wait)
      WAIT_FOR_READY=0; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage; exit 1 ;;
  esac
done

MODE=${MODE,,}
if [[ "$MODE" != "selfsigned" && "$MODE" != "provided" ]]; then
  echo "Invalid --mode '$MODE'" >&2
  usage; exit 1
fi

for bin in kubectl helm mongosh jq openssl; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "Required command '$bin' not found" >&2
    exit 1
  fi
done

ensure_operator_ready() {
  local operator_namespace="documentdb-operator"
  local operator_deployment="documentdb-operator"

  if ! kubectl get deployment -n "$operator_namespace" "$operator_deployment" >/dev/null 2>&1; then
    echo "DocumentDB operator deployment not found in namespace '$operator_namespace'." >&2
    echo "Follow docs/gateway-tls-validation.md step 1.11 to install the operator before running this script." >&2
    exit 1
  fi

  if ! kubectl -n "$operator_namespace" rollout status deployment "$operator_deployment" --timeout=300s >/dev/null 2>&1; then
    echo "DocumentDB operator deployment is not ready. Wait for the operator pods to become ready and retry." >&2
    exit 1
  fi
}

if [[ -n "$KEYVAULT_NAME" ]]; then
  if ! command -v az >/dev/null 2>&1; then
    echo "Required command 'az' not found for Key Vault access" >&2
    exit 1
  fi
  if ! az account show >/dev/null 2>&1; then
    echo "Azure CLI not logged in. Run 'az login' first." >&2
    exit 1
  fi
fi

if [[ "$INSTALL_CERT_MANAGER" -eq 1 ]]; then
  if ! helm repo list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "jetstack"; then
    helm repo add jetstack https://charts.jetstack.io
  fi
  helm repo update >/dev/null
  helm upgrade --install "$CERT_MANAGER_RELEASE" jetstack/cert-manager \
    --namespace "$CERT_MANAGER_NAMESPACE" \
    --create-namespace \
    --set installCRDs=true
  for deploy in cert-manager cert-manager-cainjector cert-manager-webhook; do
    kubectl -n "$CERT_MANAGER_NAMESPACE" rollout status deployment "$deploy" --timeout=180s
  done
fi

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NAMESPACE" create secret generic "$SECRET_NAME" \
  --from-literal=username="$SECRET_USER" \
  --from-literal=password="$SECRET_PASS" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

ensure_operator_ready

apply_documentdb_manifest() {
  if [[ "$MODE" == "selfsigned" ]]; then
    {
      cat <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: ${DOCDB_NAME}
  namespace: ${NAMESPACE}
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: ${PVC_SIZE}
EOF
      if [[ -n "$STORAGE_CLASS" ]]; then
        printf '      storageClass: %s\n' "$STORAGE_CLASS"
      fi
      cat <<'EOF'
  exposeViaService:
    serviceType: LoadBalancer
  tls:
    gateway:
      mode: SelfSigned
EOF
    }
  else
    if [[ -z "$PROVIDED_SECRET" ]]; then
      echo "--provided-secret is required for provided mode" >&2
      exit 1
    fi
    cat <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: ${DOCDB_NAME}
  namespace: ${NAMESPACE}
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: ${PVC_SIZE}
EOF
    if [[ -n "$STORAGE_CLASS" ]]; then
      printf '      storageClass: %s\n' "$STORAGE_CLASS"
    fi
    cat <<EOF
  exposeViaService:
    serviceType: LoadBalancer
  tls:
    gateway:
      mode: Provided
      provided:
        secretName: ${PROVIDED_SECRET}
EOF
  fi
}

apply_documentdb_manifest | kubectl apply -f -

if [[ "$WAIT_FOR_READY" -eq 1 ]]; then
  echo "Waiting for DocumentDB TLS readiness (timeout ${TIMEOUT}s)..."
  deadline=$((SECONDS + TIMEOUT))
  while (( SECONDS < deadline )); do
    if ! kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" >/dev/null 2>&1; then
      echo "DocumentDB resource not ready, retrying..."
      sleep 5
      continue
    fi
    status_json=$(kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" -o json)
    tls_ready=$(echo "$status_json" | jq -r '.status.tls.ready // ""' | tr '[:upper:]' '[:lower:]')
    tls_message=$(echo "$status_json" | jq -r '.status.tls.message // ""')
    tls_secret=$(echo "$status_json" | jq -r '.status.tls.secretName // ""')

    if [[ "$tls_ready" == "true" ]]; then
      echo "Gateway TLS ready. Secret: ${tls_secret}"
      break
    fi
    echo "TLS status: ${tls_ready:-<pending>} ${tls_message}"
    sleep 10
  done

  if (( SECONDS >= deadline )); then
    echo "Timed out waiting for TLS readiness" >&2
    exit 1
  fi

  svc_name="documentdb-service-${DOCDB_NAME}"
  echo "Waiting for service ${svc_name} (timeout ${TIMEOUT}s)..."
  while (( SECONDS < deadline )); do
    if kubectl -n "$NAMESPACE" get svc "$svc_name" >/dev/null 2>&1; then
      break
    fi
    echo "Service ${svc_name} not created yet, retrying..."
    sleep 5
  done

  if (( SECONDS >= deadline )); then
    echo "Timed out waiting for service ${svc_name}" >&2
    exit 1
  fi

  svc_ip=""
  while (( SECONDS < deadline )); do
    svc_ip=$(kubectl -n "$NAMESPACE" get svc "$svc_name" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
    if [[ -n "$svc_ip" ]]; then
      break
    fi
    echo "Service ${svc_name} pending LoadBalancer IP, retrying..."
    sleep 5
  done

  if [[ -z "$svc_ip" ]]; then
    echo "Service LoadBalancer IP not assigned yet" >&2
    exit 1
  fi
  echo "LoadBalancer IP: ${svc_ip}"
  default_sni_host="${svc_ip}.sslip.io"
  echo "Suggested SNI hostname: ${default_sni_host}"
  if [[ -z "$SNI_HOST" ]]; then
    SNI_HOST="$default_sni_host"
  fi
  host_for_uri="$svc_ip"
  extra_query='&tlsAllowInvalidCertificates=true&tlsAllowInvalidHostnames=true'
  tmp_cert=""
  if [[ "$MODE" == "provided" ]]; then
    extra_query='&tlsAllowInvalidHostnames=true'
    if [[ -n "$PROVIDED_SECRET" ]]; then
      echo "Provided TLS secret in use: $PROVIDED_SECRET"
    fi
    if [[ -n "$SNI_HOST" ]]; then
      if getent hosts "$SNI_HOST" >/dev/null 2>&1; then
        host_for_uri="$SNI_HOST"
      else
        echo "Warning: --sni-host $SNI_HOST did not resolve; using LoadBalancer IP for connection" >&2
      fi
    else
      echo "Warning: --sni-host not supplied; TLS hostname verification may require relaxation" >&2
    fi
    tmp_cert=$(mktemp)
    if [[ -n "$KEYVAULT_NAME" ]]; then
      cert_payload=$(az keyvault certificate show --vault-name "$KEYVAULT_NAME" -n "$KEYVAULT_CERT_NAME" --query cer -o tsv 2>/dev/null || true)
      if [[ -z "$cert_payload" ]]; then
        echo "Failed to fetch certificate $KEYVAULT_CERT_NAME from Key Vault $KEYVAULT_NAME" >&2
        rm -f "$tmp_cert"
        exit 1
      fi
      if ! printf '%s' "$cert_payload" | tr -d '\r\n ' | base64 -d 2>/dev/null | openssl x509 -inform der -out "$tmp_cert" >/dev/null 2>&1; then
        echo "Failed to convert Key Vault certificate $KEYVAULT_CERT_NAME to PEM" >&2
        rm -f "$tmp_cert"
        exit 1
      fi
      echo "Using certificate from Key Vault $KEYVAULT_NAME/$KEYVAULT_CERT_NAME"
    else
      if [[ -z "$PROVIDED_SECRET" ]]; then
        echo "--provided-secret is required when --keyvault is not specified" >&2
        rm -f "$tmp_cert"
        exit 1
      fi
      if ! kubectl -n "$NAMESPACE" get secret "$PROVIDED_SECRET" -o jsonpath='{.data.tls\.crt}' | base64 -d >"$tmp_cert" 2>/dev/null; then
        echo "Failed to extract tls.crt from secret $PROVIDED_SECRET" >&2
        rm -f "$tmp_cert"
        exit 1
      fi
      echo "Using certificate from Kubernetes secret $PROVIDED_SECRET"
    fi
  fi

  if command -v mongosh >/dev/null 2>&1; then
    mongo_user=$(kubectl -n "$NAMESPACE" get secret "$SECRET_NAME" -o jsonpath='{.data.username}' | base64 -d)
    mongo_pass=$(kubectl -n "$NAMESPACE" get secret "$SECRET_NAME" -o jsonpath='{.data.password}' | base64 -d)
    conn_uri="mongodb://${mongo_user}:${mongo_pass}@${host_for_uri}:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0${extra_query}"
    echo "Running mongosh ping..."
    mongosh_args=($conn_uri "--eval" "db.runCommand({ ping: 1 })")
    if [[ "$MODE" == "provided" ]]; then
      mongosh_args=($conn_uri "--tlsCAFile" "$tmp_cert" "--eval" "db.runCommand({ ping: 1 })")
      if [[ -n "$SNI_HOST" && $(mongosh --help 2>&1 | grep -c -- '--tlsHostname') -gt 0 ]]; then
        mongosh_args+=("--tlsHostname" "$SNI_HOST")
      fi
      mongosh_args+=("--tlsAllowInvalidHostnames")
    fi
    mongosh_log=$(mktemp)
    if mongosh "${mongosh_args[@]}" >"$mongosh_log" 2>&1; then
      cat "$mongosh_log"
      echo "mongosh connectivity OK"
    else
      if [[ "$MODE" == "provided" ]] && grep -qi 'self-signed certificate' "$mongosh_log"; then
        echo "mongosh encountered a self-signed certificate; retrying with --tlsAllowInvalidCertificates" >&2
        if [[ " ${mongosh_args[*]} " != *" --tlsAllowInvalidCertificates "* ]]; then
          mongosh_args+=("--tlsAllowInvalidCertificates")
        fi
        if mongosh "${mongosh_args[@]}" >"$mongosh_log" 2>&1; then
          cat "$mongosh_log"
          echo "mongosh connectivity OK (certificate relaxed)"
        else
          cat "$mongosh_log" >&2
          echo "mongosh connectivity failed" >&2
          rm -f "$mongosh_log"
          if [[ -n "$tmp_cert" ]]; then rm -f "$tmp_cert"; fi
          exit 1
        fi
      elif [[ "$MODE" == "provided" ]] && grep -qi 'hostname' "$mongosh_log"; then
        echo "mongosh encountered a hostname mismatch; retrying with --tlsAllowInvalidHostnames" >&2
        if [[ " ${mongosh_args[*]} " != *" --tlsAllowInvalidHostnames "* ]]; then
          mongosh_args+=("--tlsAllowInvalidHostnames")
        fi
        if mongosh "${mongosh_args[@]}" >"$mongosh_log" 2>&1; then
          cat "$mongosh_log"
          echo "mongosh connectivity OK (hostname relaxed)"
        else
          cat "$mongosh_log" >&2
          echo "mongosh connectivity failed" >&2
          rm -f "$mongosh_log"
          if [[ -n "$tmp_cert" ]]; then rm -f "$tmp_cert"; fi
          exit 1
        fi
      else
        cat "$mongosh_log" >&2
        echo "mongosh connectivity failed" >&2
        rm -f "$mongosh_log"
        if [[ -n "$tmp_cert" ]]; then rm -f "$tmp_cert"; fi
        exit 1
      fi
    fi
    rm -f "$mongosh_log"
    if [[ -n "$tmp_cert" ]]; then rm -f "$tmp_cert"; fi
  else
    echo "mongosh not found; skipping connectivity test" >&2
    if [[ -n "$tmp_cert" ]]; then rm -f "$tmp_cert"; fi
  fi
fi

echo "DocumentDB gateway validation complete."
