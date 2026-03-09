#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: setup-selfsigned-gateway-tls.sh [options]

Installs cert-manager (unless skipped) and configures a DocumentDB cluster to use
SelfSigned gateway TLS as documented in docs/gateway-tls-validation.md.

Options:
  -n, --namespace <name>          Kubernetes namespace for the DocumentDB resource (default: documentdb-preview-ns)
      --name <name>               DocumentDB resource name (default: documentdb-preview)
      --pvc-size <size>           Persistent volume claim size (default: 10Gi)
      --storage-class <name>      StorageClass to use for PVCs (optional)
      --secret-name <name>        Credentials secret name (default: documentdb-credentials)
      --username <value>          DocumentDB username (default: docdbuser)
      --password <value>          DocumentDB password (default: P@ssw0rd123)
      --skip-cert-manager         Skip cert-manager install/upgrade
      --cert-manager-version <v>  Helm chart version for cert-manager (optional)
      --timeout <seconds>         Wait timeout for TLS readiness (default: 900)
      --skip-wait                 Do not wait for TLS readiness
  -h, --help                      Show this help text
EOF
}

NAMESPACE="documentdb-preview-ns"
DOCDB_NAME="documentdb-preview"
PVC_SIZE="10Gi"
STORAGE_CLASS=""
SECRET_NAME="documentdb-credentials"
SECRET_USER="docdbuser"
SECRET_PASS="P@ssw0rd123"
INSTALL_CERT_MANAGER=1
CERT_MANAGER_VERSION=""
CERT_MANAGER_RELEASE="cert-manager"
CERT_MANAGER_NAMESPACE="cert-manager"
TIMEOUT=900
WAIT_FOR_READY=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    --name)
      DOCDB_NAME="$2"
      shift 2
      ;;
    --pvc-size)
      PVC_SIZE="$2"
      shift 2
      ;;
    --storage-class)
      STORAGE_CLASS="$2"
      shift 2
      ;;
    --secret-name)
      SECRET_NAME="$2"
      shift 2
      ;;
    --username)
      SECRET_USER="$2"
      shift 2
      ;;
    --password)
      SECRET_PASS="$2"
      shift 2
      ;;
    --skip-cert-manager)
      INSTALL_CERT_MANAGER=0
      shift
      ;;
    --cert-manager-version)
      CERT_MANAGER_VERSION="$2"
      shift 2
      ;;
    --timeout)
      TIMEOUT="$2"
      shift 2
      ;;
    --skip-wait)
      WAIT_FOR_READY=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

for bin in kubectl helm; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "Required command '$bin' not found on PATH" >&2
    exit 1
  fi
done

if [[ "$INSTALL_CERT_MANAGER" -eq 1 ]]; then
  if ! helm repo list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "jetstack"; then
    helm repo add jetstack https://charts.jetstack.io
  fi
  helm repo update >/dev/null
  cm_args=(upgrade --install "$CERT_MANAGER_RELEASE" jetstack/cert-manager --namespace "$CERT_MANAGER_NAMESPACE" --create-namespace --set installCRDs=true)
  if [[ -n "$CERT_MANAGER_VERSION" ]]; then
    cm_args+=(--version "$CERT_MANAGER_VERSION")
  fi
  helm "${cm_args[@]}"
  for deploy in cert-manager cert-manager-cainjector cert-manager-webhook; do
    # Wait for cert-manager control plane pods to be ready before requesting certificates
    kubectl -n "$CERT_MANAGER_NAMESPACE" rollout status deployment "$deploy" --timeout=180s
  done
fi

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

kubectl -n "$NAMESPACE" create secret generic "$SECRET_NAME" \
  --from-literal=username="$SECRET_USER" \
  --from-literal=password="$SECRET_PASS" \
  --dry-run=client -o yaml | kubectl apply -f -

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
  if [[ -n "${STORAGE_CLASS}" ]]; then
    printf '      storageClass: %s\n' "${STORAGE_CLASS}"
  fi
  cat <<'EOF'
  exposeViaService:
    serviceType: LoadBalancer
  tls:
    gateway:
      mode: SelfSigned
EOF
} | kubectl apply -f -

if [[ "$WAIT_FOR_READY" -eq 1 ]]; then
  echo "Waiting for gateway TLS to become ready (timeout: ${TIMEOUT}s)..."
  deadline=$((SECONDS + TIMEOUT))
  while (( SECONDS < deadline )); do
    if ! kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" >/dev/null 2>&1; then
      echo "DocumentDB resource not yet available; retrying..."
      sleep 5
      continue
    fi

    tls_ready=$(kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" -o jsonpath='{.status.tls.ready}' 2>/dev/null || echo "")
    tls_message=$(kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" -o jsonpath='{.status.tls.message}' 2>/dev/null || echo "")
    tls_secret=$(kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" -o jsonpath='{.status.tls.secretName}' 2>/dev/null || echo "")

    tls_ready=${tls_ready,,}
    if [[ "$tls_ready" == "<no value>" ]]; then
      tls_ready=""
    fi

    if [[ "$tls_message" == "<no value>" ]]; then
      tls_message=""
    fi
    if [[ "$tls_secret" == "<no value>" ]]; then
      tls_secret=""
    fi

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

  if svc_ip=$(kubectl -n "$NAMESPACE" get svc documentdb-service-"$DOCDB_NAME" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null); then
    if [[ -n "$svc_ip" ]]; then
      echo "LoadBalancer IP: ${svc_ip}"
      echo "Suggested SNI hostname: ${svc_ip}.sslip.io"
    fi
  fi
fi

echo "SelfSigned gateway TLS setup complete."
