#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: documentdb-provided-mode-setup.sh [options]

Creates or updates the resources required to run the DocumentDB gateway in
Provided TLS mode, assuming the Azure Key Vault and certificate already exist.
The script wires up the Secrets Store CSI plumbing, manages the DocumentDB
manifest, and performs an optional mongosh connectivity check.

Options:
  -g, --resource-group <name>     Azure resource group with the Key Vault and AKS cluster (required)
      --aks-name <name>           Azure Kubernetes Service cluster name (required)
  --location <name>           Azure region (retained for backward compatibility)
      --keyvault <name>           Azure Key Vault name (required)
      --cert-name <name>          Azure Key Vault certificate name (default: documentdb-gateway)
      --sni-host <host>           Hostname embedded in the certificate and used for TLS/SNI (required)
      --namespace <name>          DocumentDB namespace (default: documentdb-preview-ns)
      --docdb-name <name>         DocumentDB resource name (default: documentdb-preview)
      --docdb-version <ver>       DocumentDB version (default: 16)
      --secret-name <name>        K8s secret with gateway credentials (default: documentdb-credentials)
      --username <value>          Gateway username (default: docdbuser)
      --password <value>          Gateway password (default: P@ssw0rd123)
      --provided-secret <name>    K8s TLS secret synced from Key Vault (default: documentdb-provided-tls)
      --spc-name <name>           SecretProviderClass name (default: documentdb-azure-tls)
      --pvc-size <size>           Volume size for DocumentDB (default: 10Gi)
      --storage-class <name>      StorageClass for DocumentDB PVCs (optional)
      --user-assigned-client <id> Kubelet user-assigned managed identity clientId (optional)
      --skip-cert-manager         Skip cert-manager install/upgrade
      --skip-csi-install          Skip installing the CSI driver/provider (assume already present)
      --timeout <seconds>         Timeout for TLS readiness (default: 900)
      --skip-mongosh              Skip mongosh connectivity test
  -h, --help                      Show this help text
EOF
}

RESOURCE_GROUP=""
AKS_NAME=""
LOCATION=""
KEYVAULT_NAME=""
CERT_NAME="documentdb-gateway"
SNI_HOST=""
NAMESPACE="documentdb-preview-ns"
DOCDB_NAME="documentdb-preview"
SECRET_NAME="documentdb-credentials"
SECRET_USER="docdbuser"
SECRET_PASS="P@ssw0rd123"
PROVIDED_SECRET="documentdb-provided-tls"
SPC_NAME="documentdb-azure-tls"
PVC_SIZE="10Gi"
STORAGE_CLASS=""
USER_ASSIGNED_CLIENT=""
INSTALL_CERT_MANAGER=1
INSTALL_CSI=1
TIMEOUT=900
RUN_MONGOSH=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    -g|--resource-group)
      RESOURCE_GROUP="$2"; shift 2 ;;
    --aks-name)
      AKS_NAME="$2"; shift 2 ;;
    --location)
      LOCATION="$2"; shift 2 ;;
    --keyvault)
      KEYVAULT_NAME="$2"; shift 2 ;;
    --cert-name)
      CERT_NAME="$2"; shift 2 ;;
    --sni-host)
      SNI_HOST="$2"; shift 2 ;;
    --namespace)
      NAMESPACE="$2"; shift 2 ;;
    --docdb-name)
      DOCDB_NAME="$2"; shift 2 ;;
    --secret-name)
      SECRET_NAME="$2"; shift 2 ;;
    --username)
      SECRET_USER="$2"; shift 2 ;;
    --password)
      SECRET_PASS="$2"; shift 2 ;;
    --provided-secret)
      PROVIDED_SECRET="$2"; shift 2 ;;
    --spc-name)
      SPC_NAME="$2"; shift 2 ;;
    --pvc-size)
      PVC_SIZE="$2"; shift 2 ;;
    --storage-class)
      STORAGE_CLASS="$2"; shift 2 ;;
    --user-assigned-client)
      USER_ASSIGNED_CLIENT="$2"; shift 2 ;;
    --skip-cert-manager)
      INSTALL_CERT_MANAGER=0; shift ;;
    --skip-csi-install)
      INSTALL_CSI=0; shift ;;
    --timeout)
      TIMEOUT="$2"; shift 2 ;;
    --skip-mongosh)
      RUN_MONGOSH=0; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "Unknown argument: $1" >&2
      usage; exit 1 ;;
  esac
done

if [[ -z "$RESOURCE_GROUP" || -z "$AKS_NAME" || -z "$KEYVAULT_NAME" || -z "$SNI_HOST" ]]; then
  echo "--resource-group, --aks-name, --keyvault, and --sni-host are required" >&2
  usage
  exit 1
fi

sanitize_id() {
  printf '%s' "$1" | tr -d '\r\n'
}

for bin in az kubectl helm jq mongosh; do
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
    echo "Install the operator per docs/gateway-tls-validation.md step 1.11 before running this script." >&2
    exit 1
  fi

  if ! kubectl -n "$operator_namespace" rollout status deployment "$operator_deployment" --timeout=300s >/dev/null 2>&1; then
    echo "DocumentDB operator deployment is not ready. Wait for the operator pods and retry." >&2
    exit 1
  fi
}

if ! az account show >/dev/null 2>&1; then
  echo "Azure CLI not logged in. Run 'az login' first." >&2
  exit 1
fi

verify_keyvault_assets() {
  if ! az keyvault show -n "$KEYVAULT_NAME" -g "$RESOURCE_GROUP" >/dev/null 2>&1; then
    echo "Key Vault $KEYVAULT_NAME not found in resource group $RESOURCE_GROUP" >&2
    exit 1
  fi
  if ! az keyvault certificate show --vault-name "$KEYVAULT_NAME" -n "$CERT_NAME" >/dev/null 2>&1; then
    echo "Certificate $CERT_NAME not found in Key Vault $KEYVAULT_NAME" >&2
    exit 1
  fi
}

ensure_csi_driver() {
  if [[ "$INSTALL_CSI" -eq 0 ]]; then
    echo "Skipping CSI driver installation"
    return
  fi
  if kubectl -n kube-system get ds secrets-store-csi-driver >/dev/null 2>&1; then
    echo "Secrets Store CSI driver already installed"
    return
  fi
  echo "Installing Secrets Store CSI driver + Azure provider"
  if ! helm repo list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "csi-azure"; then
    helm repo add csi-azure https://azure.github.io/secrets-store-csi-driver-provider-azure/charts
  fi
  helm repo update >/dev/null
  helm upgrade --install csi-azure-provider csi-azure/csi-secrets-store-provider-azure -n kube-system \
    --set "secrets-store-csi-driver.syncSecret.enabled=true" >/dev/null
  kubectl -n kube-system wait --for=condition=Ready pod -l app=secrets-store-csi-driver --timeout=180s >/dev/null
  kubectl -n kube-system wait --for=condition=Ready pod -l app=csi-secrets-store-provider-azure --timeout=180s >/dev/null
}

ensure_cert_manager() {
  if [[ "$INSTALL_CERT_MANAGER" -eq 0 ]]; then
    echo "Skipping cert-manager install"
    return
  fi
  if ! helm repo list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "jetstack"; then
    helm repo add jetstack https://charts.jetstack.io
  fi
  helm repo update >/dev/null
  helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --set installCRDs=true >/dev/null
  for deploy in cert-manager cert-manager-cainjector cert-manager-webhook; do
    kubectl -n cert-manager rollout status deployment "$deploy" --timeout=180s >/dev/null
  done
}

ensure_namespace_and_secret() {
  kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  if kubectl -n "$NAMESPACE" get secret "$SECRET_NAME" >/dev/null 2>&1; then
    echo "Credentials secret $SECRET_NAME already exists"
  else
    kubectl -n "$NAMESPACE" create secret generic "$SECRET_NAME" \
      --from-literal=username="$SECRET_USER" \
      --from-literal=password="$SECRET_PASS"
  fi
}

apply_secret_provider_class() {
  TENANT_ID=$(sanitize_id "$(az account show --query tenantId -o tsv)")
  {
    cat <<EOF
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: ${SPC_NAME}
  namespace: ${NAMESPACE}
spec:
  provider: azure
  secretObjects:
  - secretName: ${PROVIDED_SECRET}
    type: kubernetes.io/tls
    data:
    - objectName: "tls.crt"
      key: tls.crt
    - objectName: "tls.key"
      key: tls.key
  parameters:
    usePodIdentity: "false"
    useVMManagedIdentity: "true"
    keyvaultName: "${KEYVAULT_NAME}"
    tenantId: "${TENANT_ID}"
    cloudName: "AzurePublicCloud"
    syncSecret: "true"
    objects: |
      array:
        - |
          objectName: "${CERT_NAME}"
          objectType: "secret"
          objectAlias: "tls.crt"
          objectVersion: ""
        - |
          objectName: "${CERT_NAME}"
          objectType: "secret"
          objectAlias: "tls.key"
          objectVersion: ""
EOF
    if [[ -n "$USER_ASSIGNED_CLIENT" ]]; then
      cat <<EOF
    userAssignedIdentityID: "${USER_ASSIGNED_CLIENT}"
EOF
    fi
  } | kubectl apply -f -
}

ensure_cert_puller() {
  kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cert-puller
  namespace: ${NAMESPACE}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cert-puller
  template:
    metadata:
      labels:
        app: cert-puller
    spec:
      containers:
      - name: bb
        image: busybox
        command: ["sh", "-c", "sleep 3600"]
        volumeMounts:
        - name: secrets-store-inline
          mountPath: "/mnt/secrets-store"
          readOnly: true
      volumes:
      - name: secrets-store-inline
        csi:
          driver: secrets-store.csi.k8s.io
          readOnly: true
          volumeAttributes:
            secretProviderClass: "${SPC_NAME}"
EOF
}

wait_for_tls_secret() {
  echo "Waiting for synced TLS secret ${PROVIDED_SECRET}"
  deadline=$((SECONDS + TIMEOUT))
  while (( SECONDS < deadline )); do
    if kubectl -n "$NAMESPACE" get secret "$PROVIDED_SECRET" >/dev/null 2>&1; then
      if kubectl -n "$NAMESPACE" get secret "$PROVIDED_SECRET" -o jsonpath='{.data.tls\.crt}' >/dev/null 2>&1 && \
         kubectl -n "$NAMESPACE" get secret "$PROVIDED_SECRET" -o jsonpath='{.data.tls\.key}' >/dev/null 2>&1; then
        echo "TLS secret ${PROVIDED_SECRET} ready"
        return
      fi
    fi
    sleep 5
  done
  echo "Timed out waiting for TLS secret ${PROVIDED_SECRET}" >&2
  exit 1
}

ensure_documentdb_resource() {
  if kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" >/dev/null 2>&1; then
    echo "Patching DocumentDB ${DOCDB_NAME} into Provided mode"
    kubectl -n "$NAMESPACE" patch documentdb "$DOCDB_NAME" --type merge -p "$(cat <<JSON
{
  "spec": {
    "documentDbCredentialSecret": "${SECRET_NAME}",
    "tls": {
      "gateway": {
        "mode": "Provided",
        "provided": { "secretName": "${PROVIDED_SECRET}" }
      }
    }
  }
}
JSON
)"
  else
    echo "Creating DocumentDB ${DOCDB_NAME} in Provided mode"
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
  documentDbCredentialSecret: "${SECRET_NAME}"
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
    } | kubectl apply -f -
  fi
}

wait_for_documentdb_tls() {
  echo "Waiting for DocumentDB TLS readiness"
  deadline=$((SECONDS + TIMEOUT))
  while (( SECONDS < deadline )); do
    if ! kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" >/dev/null 2>&1; then
      sleep 5
      continue
    fi
    status_json=$(kubectl -n "$NAMESPACE" get documentdb "$DOCDB_NAME" -o json)
    tls_ready=$(echo "$status_json" | jq -r '.status.tls.ready // ""' | tr '[:upper:]' '[:lower:]')
    tls_message=$(echo "$status_json" | jq -r '.status.tls.message // ""')
    tls_secret=$(echo "$status_json" | jq -r '.status.tls.secretName // ""')
    if [[ "$tls_ready" == "true" ]]; then
      echo "DocumentDB reports TLS ready using secret ${tls_secret}"
      return
    fi
    echo "TLS status: ${tls_ready:-<pending>} ${tls_message}"
    sleep 10
  done
  echo "Timed out waiting for DocumentDB TLS readiness" >&2
  exit 1
}

### Execution flow
verify_keyvault_assets
ensure_cert_manager
ensure_csi_driver
ensure_namespace_and_secret
ensure_operator_ready

if [[ -z "$USER_ASSIGNED_CLIENT" ]]; then
  UAI_CLIENT=$(sanitize_id "$(az aks show -g \"$RESOURCE_GROUP\" -n \"$AKS_NAME\" --query identityProfile.kubeletidentity.clientId -o tsv)")
  USER_ASSIGNED_CLIENT="$UAI_CLIENT"
else
  USER_ASSIGNED_CLIENT=$(sanitize_id "$USER_ASSIGNED_CLIENT")
fi

apply_secret_provider_class
ensure_cert_puller
wait_for_tls_secret
ensure_documentdb_resource
wait_for_documentdb_tls

echo "DocumentDB provided TLS setup complete."
