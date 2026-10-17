#!/usr/bin/env bash
set -uo pipefail

: "${E2E_PRIMARY_KUBECONFIG:?}"
: "${E2E_REPLICA_KUBECONFIG:?}"
: "${E2E_ARTIFACTS_DIR:?}"
for role in primary replica; do
  config="$E2E_PRIMARY_KUBECONFIG"
  if [[ "$role" == replica ]]; then config="$E2E_REPLICA_KUBECONFIG"; fi
  out="${E2E_ARTIFACTS_DIR}/diagnostics/$(date -u +%Y%m%dT%H%M%S%N)/${role}"
  mkdir -p "$out"
  k=(kubectl --kubeconfig "$config" --request-timeout=15s)
  # Intentionally whitelist resources: never export Secrets or kubeconfigs.
  for resource in documentdbs.documentdb.io clusters.postgresql.cnpg.io pods services endpointslices events; do
    "${k[@]}" get "$resource" -A -o yaml > "$out/${resource}.yaml" 2> "$out/${resource}.errors" || true
  done
  while read -r ns pod; do
    [[ -n "$pod" ]] || continue
    "${k[@]}" logs -n "$ns" "$pod" --all-containers --tail=2000 \
      > "$out/${ns}-${pod}.log" 2>&1 || true
  done < <("${k[@]}" get pods -A -o json |
    jq -r '.items[] | select(.metadata.namespace == "documentdb-operator" or
      .metadata.namespace == "cnpg-system" or (.metadata.namespace | startswith("e2e-pg-tls-"))) |
      [.metadata.namespace, .metadata.name] | @tsv')
  while read -r ns; do
    for secret in postgres-server postgres-replication; do
      # Decode ONLY the public certificate, streaming directly to openssl.
      "${k[@]}" get secret "$secret" -n "$ns" -o 'jsonpath={.data.tls\.crt}' |
        base64 --decode | openssl x509 -noout -subject -issuer -serial -dates -ext subjectAltName \
        > "$out/${ns}-${secret}-public.txt" 2>&1 || true
    done
  done < <("${k[@]}" get namespaces -o json |
    jq -r '.items[].metadata.name | select(startswith("e2e-pg-tls-"))')
done
