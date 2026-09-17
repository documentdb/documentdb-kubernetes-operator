#!/usr/bin/env bash
set -euo pipefail

# Delete all resources created by this playground

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load deployment info if available
if [ -f "$SCRIPT_DIR/.deployment-info" ]; then
  source "$SCRIPT_DIR/.deployment-info"
fi

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-k3s-fleet-rg}"
FORCE="${FORCE:-false}"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --force|-f)
      FORCE="true"
      shift
      ;;
    --resource-group|-g)
      RESOURCE_GROUP="$2"
      shift 2
      ;;
    --vms-only)
      VMS_ONLY="true"
      shift
      ;;
    --aks-only)
      AKS_ONLY="true"
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --force, -f           Skip confirmation prompts"
      echo "  --resource-group, -g  Resource group name (default: documentdb-k3s-fleet-rg)"
      echo "  --vms-only            Delete only k3s VMs"
      echo "  --aks-only            Delete only AKS clusters"
      echo "  -h, --help            Show this help"
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

echo "======================================="
echo "Resource Cleanup"
echo "======================================="
echo "Resource Group: $RESOURCE_GROUP"
echo "======================================="

# Check if resource group exists
if ! az group show --name "$RESOURCE_GROUP" &>/dev/null; then
  echo "Resource group '$RESOURCE_GROUP' does not exist. Nothing to delete."
  exit 0
fi

# Confirmation
if [ "$FORCE" != "true" ]; then
  echo ""
  echo "⚠️  WARNING: This will delete all resources in '$RESOURCE_GROUP'"
  echo ""
  read -p "Are you sure? (yes/no): " CONFIRM
  if [ "$CONFIRM" != "yes" ]; then
    echo "Cancelled."
    exit 0
  fi
fi

# Delete specific resources if requested
if [ "${VMS_ONLY:-false}" = "true" ]; then
  echo ""
  echo "Deleting k3s VMs only..."
  
  VMS=$(az vm list -g "$RESOURCE_GROUP" --query "[?contains(name,'k3s')].name" -o tsv)
  for vm in $VMS; do
    echo "  Deleting VM: $vm"
    az vm delete -g "$RESOURCE_GROUP" -n "$vm" --yes --no-wait
  done
  
  echo "✓ VM deletion initiated"
  exit 0
fi

if [ "${AKS_ONLY:-false}" = "true" ]; then
  echo ""
  echo "Deleting AKS clusters only..."
  
  CLUSTERS=$(az aks list -g "$RESOURCE_GROUP" --query "[].name" -o tsv)
  for cluster in $CLUSTERS; do
    echo "  Deleting AKS cluster: $cluster"
    az aks delete -g "$RESOURCE_GROUP" -n "$cluster" --yes --no-wait
  done
  
  echo "✓ AKS deletion initiated"
  exit 0
fi

# Delete DocumentDB resources first (if clusters still exist)
echo ""
echo "Cleaning up Kubernetes resources..."

# Try to delete DocumentDB resources from hub
if [ -n "${HUB_CLUSTER_NAME:-}" ]; then
  if kubectl config get-contexts "$HUB_CLUSTER_NAME" &>/dev/null 2>&1; then
    echo "  Deleting DocumentDB ClusterResourcePlacement..."
    kubectl --context "$HUB_CLUSTER_NAME" delete clusterresourceplacement documentdb-namespace-crp --ignore-not-found=true 2>/dev/null || true
    kubectl --context "$HUB_CLUSTER_NAME" delete clusterresourceplacement documentdb-operator-crp --ignore-not-found=true 2>/dev/null || true
    kubectl --context "$HUB_CLUSTER_NAME" delete clusterresourceplacement cert-manager-crp --ignore-not-found=true 2>/dev/null || true
    
    echo "  Deleting DocumentDB namespace..."
    kubectl --context "$HUB_CLUSTER_NAME" delete namespace documentdb-preview-ns --ignore-not-found=true 2>/dev/null || true
  fi
fi

# Delete entire resource group
echo ""
echo "Deleting resource group '$RESOURCE_GROUP'..."
echo "This will delete all VMs, AKS clusters, VNets, and associated resources."
az group delete --name "$RESOURCE_GROUP" --yes --no-wait

echo ""
echo "✓ Resource group deletion initiated"

# Clean up local files
echo ""
echo "Cleaning up local files..."
rm -f "$SCRIPT_DIR/.deployment-info"
rm -f "$SCRIPT_DIR/documentdb-operator-*.tgz"
rm -rf "$SCRIPT_DIR/.istio-certs"

# Clean up kubeconfig contexts
echo "Cleaning up kubectl contexts..."
for ctx in $(kubectl config get-contexts -o name 2>/dev/null | grep -E "(hub-|member-|k3s-)" || true); do
  kubectl config delete-context "$ctx" 2>/dev/null || true
done

# Remove aliases from shell config files
for SHELL_RC in "$HOME/.bashrc" "$HOME/.zshrc"; do
  if [ -f "$SHELL_RC" ]; then
    if grep -q "# BEGIN k3s-fleet aliases" "$SHELL_RC" 2>/dev/null; then
      echo "Removing kubectl aliases from $SHELL_RC..."
      awk '/# BEGIN k3s-fleet aliases/,/# END k3s-fleet aliases/ {next} {print}' "$SHELL_RC" > "$SHELL_RC.tmp"
      mv "$SHELL_RC.tmp" "$SHELL_RC"
    fi
  fi
done

echo ""
echo "======================================="
echo "✅ Cleanup Complete!"
echo "======================================="
echo ""
echo "Resource group deletion is running in the background."
echo "Run 'az group show -n $RESOURCE_GROUP' to check status."
echo ""
echo "To verify deletion is complete:"
echo "  az group list --query \"[?name=='$RESOURCE_GROUP']\" -o table"
echo "======================================="
