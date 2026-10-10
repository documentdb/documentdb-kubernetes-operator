#!/usr/bin/env bash
set -euo pipefail

# CI and local runs use the same two-cluster provisioning entry point.
: "${E2E_CLUSTER_PREFIX:?set a unique prefix for the two Kind clusters}"
: "${E2E_KUBECONFIG_DIR:?set a private directory outside the report directory}"
: "${E2E_CHART:?set the built chart path}"
: "${OPERATOR_IMAGE:?set the built-from-branch operator image}"
: "${SIDECAR_IMAGE:?set the built-from-branch sidecar image}"
: "${DOCUMENTDB_IMAGE:?set the database image loaded into both clusters}"
: "${GATEWAY_IMAGE:?set the gateway image loaded into both clusters}"
mkdir -p "$E2E_KUBECONFIG_DIR"
chmod 700 "$E2E_KUBECONFIG_DIR"

for role in primary replica; do
  name="${E2E_CLUSTER_PREFIX}-${role}"
  config="${E2E_KUBECONFIG_DIR}/${role}.yaml"
  kind create cluster --name "$name" --image kindest/node:v1.35.0 --kubeconfig "$config" --wait 5m
  chmod 600 "$config"
  kind load docker-image --name "$name" "$OPERATOR_IMAGE" "$SIDECAR_IMAGE" "$DOCUMENTDB_IMAGE" "$GATEWAY_IMAGE"
  kubectl --kubeconfig "$config" wait --for=condition=Ready nodes --all --timeout=300s
  helm --kubeconfig "$config" upgrade --install cert-manager \
    oci://quay.io/jetstack/charts/cert-manager --version v1.19.2 \
    --namespace cert-manager --create-namespace --set crds.enabled=true --wait --timeout=10m
  helm --kubeconfig "$config" upgrade --install documentdb-operator "$E2E_CHART" \
    --namespace documentdb-operator --create-namespace \
    --set-string image.documentdbk8soperator.repository="${OPERATOR_IMAGE%:*}" \
    --set-string image.documentdbk8soperator.tag="${OPERATOR_IMAGE##*:}" \
    --set-string image.sidecarinjector.repository="${SIDECAR_IMAGE%:*}" \
    --set-string image.sidecarinjector.tag="${SIDECAR_IMAGE##*:}" \
    --set documentDbImagePullPolicy=IfNotPresent \
    --set gatewayImagePullPolicy=IfNotPresent \
    --wait --timeout=15m
  kubectl --kubeconfig "$config" wait --for=condition=Available \
    deployment/documentdb-operator -n documentdb-operator --timeout=300s
  kubectl --kubeconfig "$config" wait --for=condition=Available \
    deployment --all -n cnpg-system --timeout=300s
done
