#!/usr/bin/env bash
set -euo pipefail

# Validates DocumentDB deployment state across fleet member clusters by comparing
# the configured replication topology with the actual CNPG resources.
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
DOCUMENTDB_NAME="${DOCUMENTDB_NAME:-documentdb-preview}"
DOCUMENTDB_NAMESPACE="${DOCUMENTDB_NAMESPACE:-documentdb-preview-ns}"
DOCUMENTDB_APP_LABEL="${DOCUMENTDB_APP_LABEL:-$DOCUMENTDB_NAME}"
CLUSTER_SELECTOR_PREFIX="${CLUSTER_SELECTOR_PREFIX:-member-}"

declare -i OVERALL_STATUS=0
FAILURE_MESSAGES=()

declare -a CLUSTER_ARRAY=()
declare -a DOCUMENTDB_CLUSTER_NAMES=()
declare -A DOCUMENTDB_CLUSTER_SET=()
EXPECTED_CLUSTER_NAMES_JSON="[]"
EXPECTED_CNPG_NAMES_JSON="[]"
declare -i EXPECTED_CLUSTER_COUNT=0

log() {
  echo "[$(date '+%Y-%m-%dT%H:%M:%S%z')] $*"
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Error: required command '$1' not found in PATH" >&2
    exit 1
  fi
}

record_success() {
  local cluster="$1"; shift
  echo "[$cluster] ✔ $*"
}

record_failure() {
  local cluster="$1"; shift
  echo "[$cluster] ✖ $*"
  OVERALL_STATUS=1
  FAILURE_MESSAGES+=("[$cluster] $*")
}

get_cnpg_name_for_cluster() {
  local cluster="$1"

  if ! kubectl --context "$cluster" get namespace "$DOCUMENTDB_NAMESPACE" >/dev/null 2>&1; then
    return 1
  fi

  local cnpg_list
  if ! cnpg_list=$(kubectl --context "$cluster" get clusters.postgresql.cnpg.io -n "$DOCUMENTDB_NAMESPACE" -o json 2>/dev/null); then
    return 1
  fi

  local doc_owned_clusters
  doc_owned_clusters=$(echo "$cnpg_list" | jq --arg doc "$DOCUMENTDB_NAME" '[.items[] | select(any(.metadata.ownerReferences[]?; .kind=="DocumentDB" and .name==$doc))]')
  local doc_owned_count
  doc_owned_count=$(echo "$doc_owned_clusters" | jq 'length')
  if (( doc_owned_count == 0 )); then
    return 1
  fi

  echo "$doc_owned_clusters" | jq -r '.[0].metadata.name'
}

check_member_cluster() {
  local cluster="$1"

  if ! kubectl --context "$cluster" get namespace "$DOCUMENTDB_NAMESPACE" >/dev/null 2>&1; then
    record_failure "$cluster" "Namespace $DOCUMENTDB_NAMESPACE is missing"
    return
  fi

  local cnpg_list
  if ! cnpg_list=$(kubectl --context "$cluster" get clusters.postgresql.cnpg.io -n "$DOCUMENTDB_NAMESPACE" -o json 2>&1); then
    record_failure "$cluster" "Unable to list CNPG clusters: $cnpg_list"
    return
  fi

  local doc_owned_clusters
  doc_owned_clusters=$(echo "$cnpg_list" | jq --arg doc "$DOCUMENTDB_NAME" '[.items[] | select(any(.metadata.ownerReferences[]?; .kind=="DocumentDB" and .name==$doc))]')
  local doc_owned_count
  doc_owned_count=$(echo "$doc_owned_clusters" | jq 'length')

  if (( doc_owned_count == 0 )); then
    record_failure "$cluster" "No CNPG Cluster owned by DocumentDB $DOCUMENTDB_NAME"
    return
  fi

  if (( doc_owned_count > 1 )); then
    record_failure "$cluster" "Found $doc_owned_count CNPG Clusters owned by DocumentDB (expected 1)"
  fi

  local cnpg_obj
  cnpg_obj=$(echo "$doc_owned_clusters" | jq '.[0]')
  local cnpg_name
  cnpg_name=$(echo "$cnpg_obj" | jq -r '.metadata.name')

  local external_count
  local expected_external_names_json
  expected_external_names_json="$EXPECTED_CNPG_NAMES_JSON"
  local expected_external_count
  expected_external_count=$(echo "$expected_external_names_json" | jq 'length')
  external_count=$(echo "$cnpg_obj" | jq '.spec.externalClusters // [] | length')
  if (( external_count == expected_external_count )); then
    record_success "$cluster" "Cluster $cnpg_name externalClusters count matches ($external_count)"
  else
    record_failure "$cluster" "Cluster $cnpg_name has $external_count externalClusters (expected $expected_external_count)"
  fi

  local external_names_json
  external_names_json=$(echo "$cnpg_obj" | jq '[.spec.externalClusters // [] | .[]? | .name] | map(select(. != null))')
  local missing_names
  missing_names=$(jq --argjson expected "$expected_external_names_json" --argjson actual "$external_names_json" -n '[ $expected[] | select(. as $item | ($actual | index($item)) | not) ]')
  local missing_count
  missing_count=$(echo "$missing_names" | jq 'length')
  if (( missing_count > 0 )); then
    local missing_list
    missing_list=$(echo "$missing_names" | jq -r 'join(", ")')
    record_failure "$cluster" "Cluster $cnpg_name missing externalClusters for: $missing_list"
  else
    record_success "$cluster" "Cluster $cnpg_name exposes all expected externalClusters"
  fi

  local extra_names
  extra_names=$(jq --argjson expected "$expected_external_names_json" --argjson actual "$external_names_json" -n '[ $actual[] | select(. as $item | ($expected | index($item)) | not) ]')
  local extra_count
  extra_count=$(echo "$extra_names" | jq 'length')
  if (( extra_count > 0 )); then
    local extra_list
    extra_list=$(echo "$extra_names" | jq -r 'join(", ")')
    record_failure "$cluster" "Cluster $cnpg_name has unexpected externalClusters: $extra_list"
  fi

  local expected_instances
  expected_instances=$(echo "$cnpg_obj" | jq '.spec.instances // 0')
  local pods_json
  if ! pods_json=$(kubectl --context "$cluster" get pods -n "$DOCUMENTDB_NAMESPACE" -l "cnpg.io/cluster=$cnpg_name" -o json 2>&1); then
    record_failure "$cluster" "Unable to list pods for cluster $cnpg_name: $pods_json"
    return
  fi
  local actual_pods
  actual_pods=$(echo "$pods_json" | jq '.items | length')
  if (( actual_pods == expected_instances )); then
    record_success "$cluster" "Cluster $cnpg_name has $actual_pods pods (matches spec.instances)"
  else
    record_failure "$cluster" "Cluster $cnpg_name has $actual_pods pods (expected $expected_instances)"
  fi

  local additional_service_count
  additional_service_count=$(echo "$cnpg_obj" | jq '.spec.managed.services.additional // [] | length')
  local expected_service_count=$((3 + additional_service_count))
  local services_json
  if ! services_json=$(kubectl --context "$cluster" get svc -n "$DOCUMENTDB_NAMESPACE" -o json 2>&1); then
    record_failure "$cluster" "Unable to list services in namespace $DOCUMENTDB_NAMESPACE: $services_json"
    return
  fi
  local services_for_cluster
  services_for_cluster=$(echo "$services_json" | jq --arg name "$cnpg_name" '[.items[] | select(any(.metadata.ownerReferences[]?; .kind=="Cluster" and .name==$name))]')
  local actual_service_count
  actual_service_count=$(echo "$services_for_cluster" | jq 'length')
  if (( actual_service_count == expected_service_count )); then
    record_success "$cluster" "Cluster $cnpg_name has $actual_service_count services (expected $expected_service_count)"
  else
    record_failure "$cluster" "Cluster $cnpg_name has $actual_service_count services (expected $expected_service_count)"
  fi
}

check_non_member_cluster() {
  local cluster="$1"

  local cnpg_list
  if cnpg_list=$(kubectl --context "$cluster" get clusters.postgresql.cnpg.io -n "$DOCUMENTDB_NAMESPACE" -o json 2>/dev/null); then
    local doc_owned_clusters
    doc_owned_clusters=$(echo "$cnpg_list" | jq --arg doc "$DOCUMENTDB_NAME" '[.items[] | select(any(.metadata.ownerReferences[]?; .kind=="DocumentDB" and .name==$doc))]')
    local doc_owned_count
    doc_owned_count=$(echo "$doc_owned_clusters" | jq 'length')
    if (( doc_owned_count == 0 )); then
      record_success "$cluster" "No DocumentDB CNPG clusters present"
    else
      record_failure "$cluster" "Found $doc_owned_count DocumentDB CNPG cluster(s) but region is not in clusterList"
    fi
  else
    record_success "$cluster" "CNPG CRD unavailable; treated as no DocumentDB clusters"
  fi

  local pods_json
  if pods_json=$(kubectl --context "$cluster" get pods -n "$DOCUMENTDB_NAMESPACE" -l "app=$DOCUMENTDB_APP_LABEL" -o json 2>/dev/null); then
    local pod_count
    pod_count=$(echo "$pods_json" | jq '.items | length')
    if (( pod_count == 0 )); then
      record_success "$cluster" "No DocumentDB pods present"
    else
      record_failure "$cluster" "Found $pod_count DocumentDB pods but region is not in clusterList"
    fi
  else
    record_success "$cluster" "Namespace $DOCUMENTDB_NAMESPACE absent; no DocumentDB pods present"
  fi

  local services_json
  if services_json=$(kubectl --context "$cluster" get svc -n "$DOCUMENTDB_NAMESPACE" -l "app=$DOCUMENTDB_APP_LABEL" -o json 2>/dev/null); then
    local service_count
    service_count=$(echo "$services_json" | jq '.items | length')
    if (( service_count == 0 )); then
      record_success "$cluster" "No DocumentDB services present"
    else
      record_failure "$cluster" "Found $service_count DocumentDB services but region is not in clusterList"
    fi
  else
    record_success "$cluster" "Namespace $DOCUMENTDB_NAMESPACE absent; no DocumentDB services present"
  fi
}

main() {
  require_command az
  require_command jq
  require_command kubectl

  log "Discovering fleet member clusters in resource group $RESOURCE_GROUP"
  local discovery_output
  if ! discovery_output=$(az aks list -g "$RESOURCE_GROUP" -o json 2>&1); then
    echo "Error: unable to list AKS clusters - $discovery_output" >&2
    exit 1
  fi

  readarray -t CLUSTER_ARRAY < <(echo "$discovery_output" | jq -r --arg prefix "$CLUSTER_SELECTOR_PREFIX" '.[] | select(.name | startswith($prefix)) | .name' | sort -u)
  if (( ${#CLUSTER_ARRAY[@]} == 0 )); then
    echo "Error: no member clusters found with prefix '$CLUSTER_SELECTOR_PREFIX' in resource group $RESOURCE_GROUP" >&2
    exit 1
  fi

  log "Found ${#CLUSTER_ARRAY[@]} member cluster(s):"
  for cluster in "${CLUSTER_ARRAY[@]}"; do
    echo "  - $cluster"
  done

  local hub_cluster=""
  for cluster in "${CLUSTER_ARRAY[@]}"; do
    if [[ "$cluster" == *"$HUB_REGION"* ]]; then
      hub_cluster="$cluster"
      break
    fi
  done

  if [[ -z "$hub_cluster" ]]; then
    echo "Error: unable to find hub cluster matching region substring '$HUB_REGION'" >&2
    exit 1
  fi

  log "Using hub cluster context: $hub_cluster"

  local documentdb_json
  if ! documentdb_json=$(kubectl --context "$hub_cluster" get documentdb "$DOCUMENTDB_NAME" -n "$DOCUMENTDB_NAMESPACE" -o json 2>&1); then
    echo "Error: unable to fetch DocumentDB $DOCUMENTDB_NAME from hub cluster: $documentdb_json" >&2
    exit 1
  fi

  EXPECTED_CLUSTER_NAMES_JSON=$(echo "$documentdb_json" | jq '[.spec.clusterReplication.clusterList[]? | .name] | map(select(. != null))')
  EXPECTED_CLUSTER_COUNT=$(echo "$EXPECTED_CLUSTER_NAMES_JSON" | jq 'length')
  readarray -t DOCUMENTDB_CLUSTER_NAMES < <(echo "$EXPECTED_CLUSTER_NAMES_JSON" | jq -r '.[]')

  if (( EXPECTED_CLUSTER_COUNT == 0 )); then
    echo "Error: DocumentDB $DOCUMENTDB_NAME has an empty clusterReplication.clusterList" >&2
    exit 1
  fi

  for name in "${DOCUMENTDB_CLUSTER_NAMES[@]}"; do
    DOCUMENTDB_CLUSTER_SET["$name"]=1
  done

  EXPECTED_CNPG_NAMES_JSON="[]"
  for cluster in "${DOCUMENTDB_CLUSTER_NAMES[@]}"; do
    local cnpg_name
    if ! cnpg_name=$(get_cnpg_name_for_cluster "$cluster"); then
      record_failure "$cluster" "Unable to determine CNPG cluster name for DocumentDB $DOCUMENTDB_NAME"
      continue
    fi
    EXPECTED_CNPG_NAMES_JSON=$(jq --arg name "$cnpg_name" '. + [$name]' <<<"$EXPECTED_CNPG_NAMES_JSON")
  done

  log "DocumentDB $DOCUMENTDB_NAME expects $EXPECTED_CLUSTER_COUNT cluster(s):"
  for name in "${DOCUMENTDB_CLUSTER_NAMES[@]}"; do
    echo "  - $name"
  done

  log "Resolved CNPG cluster names:"
  echo "$EXPECTED_CNPG_NAMES_JSON" | jq -r '.[]' | sed 's/^/  - /'

  for name in "${DOCUMENTDB_CLUSTER_NAMES[@]}"; do
    local match_found="false"
    for cluster in "${CLUSTER_ARRAY[@]}"; do
      if [[ "$cluster" == "$name" ]]; then
        match_found="true"
        break
      fi
    done
    if [[ "$match_found" == "false" ]]; then
      record_failure "$hub_cluster" "DocumentDB references cluster '$name' that was not discovered in resource group $RESOURCE_GROUP"
    fi
  done

  for cluster in "${CLUSTER_ARRAY[@]}"; do
    echo
    if [[ -n "${DOCUMENTDB_CLUSTER_SET[$cluster]:-}" ]]; then
      log "Checking DocumentDB member cluster: $cluster"
      check_member_cluster "$cluster"
    else
      log "Checking non-member cluster: $cluster"
      check_non_member_cluster "$cluster"
    fi
  done

  echo
  if (( OVERALL_STATUS == 0 )); then
    log "All checks passed across ${#CLUSTER_ARRAY[@]} cluster(s)."
  else
    log "Completed with ${#FAILURE_MESSAGES[@]} issue(s):"
    for msg in "${FAILURE_MESSAGES[@]}"; do
      echo "  - $msg"
    done
  fi

  exit $OVERALL_STATUS
}

main "$@"
