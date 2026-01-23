#!/usr/bin/env bash

set -eu

# Define variables (allow env overrides)
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
# Resource Group location (does not have to match cluster regions)
RG_LOCATION="${RG_LOCATION:-eastus2}"
# Hub region
HUB_REGION="${HUB_REGION:-westus3}"
SCRIPT_DIR="$(dirname "$0")"

# Regions for member clusters (keep in sync with parameters.bicepparam if you change it)
if [ -n "${MEMBER_REGIONS_CSV:-}" ]; then
  IFS=',' read -r -a MEMBER_REGIONS <<< "$MEMBER_REGIONS_CSV"
else
  MEMBER_REGIONS=("westus3" "uksouth" "eastus2")
fi

# Optional: explicitly override the VM size used by the template param vmSize.
# If left empty, the template's default (currently Standard_DS2_v2) will be used.
KUBE_VM_SIZE="${KUBE_VM_SIZE:-}"

# Build JSON arrays for parameters (after any fallbacks)
MEMBER_REGIONS_JSON=$(printf '%s\n' "${MEMBER_REGIONS[@]}" | jq -R . | jq -s .)

# Wait for any in-progress AKS operations in this resource group to finish
wait_for_no_inprogress() {
  local rg="$1"
  echo "Checking for in-progress AKS operations in resource group '$rg'..."
  # Use az aks list for reliable provisioningState at top-level
  local inprogress
  inprogress=$(az aks list -g "$rg" -o json \
    | jq -r '.[] | select(.provisioningState != "Succeeded" and .provisioningState != null) | [.name, .provisioningState] | @tsv')

  if [ -z "$inprogress" ]; then
    echo "No in-progress AKS operations detected."
    return 0
  fi

  echo "Found clusters still provisioning:" 
  echo "$inprogress" | while IFS=$'\t' read -r name state; do echo "  - $name: $state"; done
  echo "Please re-run this script after the above operations complete. To abort a stuck operation, use: az aks operation-abort --resource-group <rg> --name <cluster> --operation-id <id>" >&2
  return 1
}

echo "Creating or using resource group..."
EXISTING_RG_LOCATION=$(az group show --name "$RESOURCE_GROUP" --query location -o tsv 2>/dev/null || true)
if [ -n "$EXISTING_RG_LOCATION" ]; then
  echo "Using existing resource group '$RESOURCE_GROUP' in location '$EXISTING_RG_LOCATION'"
  RG_LOCATION="$EXISTING_RG_LOCATION"
else
  az group create --name "$RESOURCE_GROUP" --location "$RG_LOCATION"
fi

echo "Deploying AKS Clusters with Bicep..."
# Ensure we don't kick off another deployment while clusters are still provisioning
if ! wait_for_no_inprogress "$RESOURCE_GROUP"; then
  echo "Exiting without changes due to in-progress operations. Re-run when provisioning completes." >&2
  exit 1
fi
# Build parameter overrides
PARAMS=(
  --parameters "$SCRIPT_DIR/parameters.bicepparam"
  --parameters memberRegions="$MEMBER_REGIONS_JSON"
)
if [ -n "$KUBE_VM_SIZE" ]; then
  echo "Overriding kubernetes VM size with: $KUBE_VM_SIZE"
  PARAMS+=( --parameters vmSize="$KUBE_VM_SIZE" )
fi

DEPLOYMENT_NAME=${DEPLOYMENT_NAME:-"aks-deployment-$(date +%s)"}
az deployment group create \
  --name "$DEPLOYMENT_NAME" \
  --resource-group $RESOURCE_GROUP \
  --template-file "$SCRIPT_DIR/main.bicep" \
  "${PARAMS[@]}" >/dev/null

# Retrieve outputs
DEPLOYMENT_OUTPUT=$(az deployment group show \
  --resource-group $RESOURCE_GROUP \
  --name "$DEPLOYMENT_NAME" \
  --query "properties.outputs" -o json)

# Extract outputs
MEMBER_CLUSTER_NAMES=$(echo $DEPLOYMENT_OUTPUT | jq -r '.memberClusterNames.value[]')

HUB_CLUSTER=""
while read -r cluster; do
  [ -z "$cluster" ] && continue
  az aks get-credentials --resource-group "$RESOURCE_GROUP" --name "$cluster" --overwrite-existing
  if [[ "$cluster" == *"$HUB_REGION"* ]]; then HUB_CLUSTER="$cluster"; fi
done <<< "$MEMBER_CLUSTER_NAMES"

kubeDir=$(mktemp -d)
git clone https://github.com/kubefleet-dev/kubefleet.git $kubeDir
pushd $kubeDir
# Set up HUB_CLUSTER as the hub
kubectl config use-context $HUB_CLUSTER
export REGISTRY="ghcr.io/kubefleet-dev/kubefleet"
export TAG=$(curl "https://api.github.com/repos/kubefleet-dev/kubefleet/tags" | jq -r '.[0].name') # Gets latest tag
# Install the helm chart for running Fleet agents on the hub cluster.
helm upgrade --install hub-agent ./charts/hub-agent/ \
        --set image.pullPolicy=Always \
        --set image.repository=$REGISTRY/hub-agent \
        --set image.tag=$TAG \
        --set logVerbosity=5 \
        --set enableGuardRail=false \
        --set forceDeleteWaitTime="3m0s" \
        --set clusterUnhealthyThreshold="5m0s" \
        --set logFileMaxSize=100000 \
        --set MaxConcurrentClusterPlacement=200 \
        --set namespace=fleet-system-hub \
        --set enableWorkload=true

# Run the script.
chmod +x ./hack/membership/joinMC.sh
./hack/membership/joinMC.sh  $TAG $HUB_CLUSTER $MEMBER_CLUSTER_NAMES
popd

fleetNetworkingDir=$(mktemp -d)
git clone https://github.com/Azure/fleet-networking.git $fleetNetworkingDir
pushd $fleetNetworkingDir
# Set up HUB_CLUSTER as the hub
NETWORKING_TAG=$(curl "https://api.github.com/repos/Azure/fleet-networking/tags" | jq -r '.[0].name') # Gets latest tag

# Install the helm chart for running Fleet agents on the hub cluster.
kubectl config use-context $HUB_CLUSTER
helm install hub-net-controller-manager ./charts/hub-net-controller-manager/ \
  --set fleetSystemNamespace=fleet-system-hub \
  --set leaderElectionNamespace=fleet-system-hub \
  --set image.tag=$NETWORKING_TAG 

HUB_CLUSTER_ADDRESS=$(kubectl config view -o jsonpath="{.clusters[?(@.name==\"$HUB_CLUSTER\")].cluster.server}")

while read -r MEMBER_CLUSTER; do
  kubectl config use-context $MEMBER_CLUSTER

  kubectl apply -f config/crd/*

  echo "Installing mcs-controller-manager..."
  helm install mcs-controller-manager ./charts/mcs-controller-manager/ \
    --set refreshtoken.repository=$REGISTRY/refresh-token \
    --set refreshtoken.tag=$TAG \
    --set image.tag=$NETWORKING_TAG \
    --set image.pullPolicy=Always \
    --set refreshtoken.pullPolicy=Always \
    --set config.hubURL=$HUB_CLUSTER_ADDRESS \
    --set config.memberClusterName=$MEMBER_CLUSTER \
    --set enableV1Beta1APIs=true \
    --set logVerbosity=8

  echo "Installing member-net-controller-manager..."
  helm install member-net-controller-manager ./charts/member-net-controller-manager/ \
    --set refreshtoken.repository=$REGISTRY/refresh-token \
    --set refreshtoken.tag=$TAG \
    --set image.tag=$NETWORKING_TAG \
    --set image.pullPolicy=Always \
    --set refreshtoken.pullPolicy=Always \
    --set config.hubURL=$HUB_CLUSTER_ADDRESS \
    --set config.memberClusterName=$MEMBER_CLUSTER \
    --set enableV1Beta1APIs=true \
    --set logVerbosity=8

done <<< "$MEMBER_CLUSTER_NAMES"

popd

# Create kubectl aliases and export FLEET_ID (k-hub and k-<region>) persisted in ~/.bashrc
ALIASES_BLOCK_START="# BEGIN aks aliases"
ALIASES_BLOCK_END="# END aks aliases"
ALIASES_TMP=$(mktemp)
{
  echo "$ALIASES_BLOCK_START"
  # For each member cluster, derive region from name pattern 'member-<region>-<suffix>' and create k-<region>
  while read -r cluster; do
    [ -z "$cluster" ] && continue
    region=$(echo "$cluster" | awk -F- '{print $2}')
    # Fallback if pattern unexpected
    [ -z "$region" ] && region="$cluster"
    echo "alias k-$region=\"kubectl --context '$cluster'\""
  done <<< "$MEMBER_CLUSTER_NAMES"
  echo "$ALIASES_BLOCK_END"
} > "$ALIASES_TMP"

BASHRC="$HOME/.bashrc"
# Create or replace block in ~/.bashrc
if [ -f "$BASHRC" ]; then
  # Remove existing block if present
  awk -v start="$ALIASES_BLOCK_START" -v end="$ALIASES_BLOCK_END" '
    $0==start {inblock=1; next}
    $0==end {inblock=0; next}
    !inblock {print}
  ' "$BASHRC" > "$BASHRC.tmp"
  cat "$ALIASES_TMP" >> "$BASHRC.tmp"
  mv "$BASHRC.tmp" "$BASHRC"
else
  cp "$ALIASES_TMP" "$BASHRC"
fi
rm -f "$ALIASES_TMP"

echo ""
echo "✅ Deployment completed successfully!"
echo ""
echo "Hub cluster: $HUB_CLUSTER"
echo "Member Clusters:"
echo "$MEMBER_CLUSTER_NAMES" | while read cluster; do
  echo "  - $cluster"
done
