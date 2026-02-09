#!/bin/bash
set -e

# ================================
# k3s + AKS Infrastructure Deployment with Istio
# ================================
# Deploys:
# - 1 AKS cluster (hub) in westus3
# - 2 k3s VMs in eastus2 and uksouth
# - No VNet peering (Istio handles cross-cluster traffic)
# - Uses Azure VM Run Command (no SSH required)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Configuration
RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-k3s-fleet-rg}"
HUB_REGION="${HUB_REGION:-westus3}"
K3S_REGIONS="${K3S_REGIONS_CSV:-eastus2,uksouth}"

# Convert comma-separated to array
IFS=',' read -ra K3S_REGION_ARRAY <<< "$K3S_REGIONS"

echo "======================================="
echo "k3s + AKS Infrastructure Deployment"
echo "======================================="
echo "Resource Group: $RESOURCE_GROUP"
echo "Hub Region: $HUB_REGION"
echo "k3s Regions: ${K3S_REGION_ARRAY[*]}"
echo ""
echo "Networking: Istio service mesh (no VNet peering)"
echo "VM Access: Azure VM Run Command (no SSH required)"
echo "======================================="
echo ""

# Use a stable SSH key path (Azure requires SSH key for VMs, but we use Run Command instead)
SSH_KEY_PATH="${SCRIPT_DIR}/.ssh-key"
if [ ! -f "$SSH_KEY_PATH" ]; then
    echo "Generating SSH key (required by Azure, but we use Run Command instead)..."
    ssh-keygen -t rsa -b 2048 -f "$SSH_KEY_PATH" -N "" -C "k3s-azure-fleet" -q
fi
SSH_PUBLIC_KEY=$(cat "${SSH_KEY_PATH}.pub")

# Create resource group
echo "Creating/verifying resource group..."
if az group show --name "$RESOURCE_GROUP" &>/dev/null; then
    RG_STATE=$(az group show --name "$RESOURCE_GROUP" --query "properties.provisioningState" -o tsv 2>/dev/null || echo "Unknown")
    if [ "$RG_STATE" = "Deleting" ]; then
        echo "Resource group is being deleted. Waiting..."
        while az group show --name "$RESOURCE_GROUP" &>/dev/null; do
            sleep 10
        done
        echo "Creating resource group '$RESOURCE_GROUP' in '$HUB_REGION'"
        az group create --name "$RESOURCE_GROUP" --location "$HUB_REGION" --output none
    else
        echo "Using existing resource group '$RESOURCE_GROUP'"
    fi
else
    echo "Creating resource group '$RESOURCE_GROUP' in '$HUB_REGION'"
    az group create --name "$RESOURCE_GROUP" --location "$HUB_REGION" --output none
fi

# Check if VMs already exist (to skip Bicep if just re-running for kubeconfig)
EXISTING_VMS=$(az vm list -g "$RESOURCE_GROUP" --query "[?contains(name,'k3s')].name" -o tsv 2>/dev/null | wc -l | tr -d ' ')
SKIP_BICEP=false

if [ "$EXISTING_VMS" -gt 0 ]; then
    echo ""
    echo "Found $EXISTING_VMS existing k3s VM(s). Skipping Bicep deployment."
    echo "(Delete VMs or resource group to force re-deployment)"
    SKIP_BICEP=true
fi

if [ "$SKIP_BICEP" = "false" ]; then
    # Deploy Bicep template
    echo ""
    echo "Deploying Azure infrastructure with Bicep..."
    echo "(This includes AKS hub and k3s VMs - typically takes 5-10 minutes)"
    
    # Build k3s regions array for Bicep
    K3S_REGIONS_JSON=$(printf '%s\n' "${K3S_REGION_ARRAY[@]}" | jq -R . | jq -s .)
    
    az deployment group create \
        --resource-group "$RESOURCE_GROUP" \
        --template-file "${SCRIPT_DIR}/main.bicep" \
        --parameters hubLocation="$HUB_REGION" \
        --parameters k3sRegions="$K3S_REGIONS_JSON" \
        --parameters sshPublicKey="$SSH_PUBLIC_KEY" \
        --output none
    
    echo "✓ Infrastructure deployed"
fi

# Get deployment outputs
echo ""
echo "Retrieving deployment outputs..."

DEPLOYMENT_OUTPUT=$(az deployment group show \
    --resource-group "$RESOURCE_GROUP" \
    --name main \
    --query "properties.outputs" \
    -o json 2>/dev/null || echo "{}")

AKS_CLUSTER_NAME=$(echo "$DEPLOYMENT_OUTPUT" | jq -r '.aksClusterName.value // empty')
K3S_VM_NAMES=$(echo "$DEPLOYMENT_OUTPUT" | jq -r '.k3sVmNames.value // [] | @csv' | tr -d '"')
K3S_PUBLIC_IPS=$(echo "$DEPLOYMENT_OUTPUT" | jq -r '.k3sVmPublicIps.value // [] | @csv' | tr -d '"')

# Fallback if outputs not available yet
if [ -z "$AKS_CLUSTER_NAME" ]; then
    AKS_CLUSTER_NAME="hub-${HUB_REGION}"
fi

echo "AKS Cluster: $AKS_CLUSTER_NAME"
echo "k3s VMs: $K3S_VM_NAMES"
echo "k3s IPs: $K3S_PUBLIC_IPS"

# Configure kubectl for AKS
echo ""
echo "Configuring kubectl for AKS hub cluster..."
az aks get-credentials \
    --resource-group "$RESOURCE_GROUP" \
    --name "$AKS_CLUSTER_NAME" \
    --overwrite-existing \
    --admin \
    --context "hub-${HUB_REGION}" \
    2>/dev/null || \
az aks get-credentials \
    --resource-group "$RESOURCE_GROUP" \
    --name "$AKS_CLUSTER_NAME" \
    --overwrite-existing \
    --context "hub-${HUB_REGION}"

echo "✓ AKS kubectl context: hub-${HUB_REGION}"

# Wait for k3s VMs to be ready and get kubeconfig via Run Command
echo ""
echo "Waiting for k3s clusters to be ready (using Azure VM Run Command)..."
echo "This avoids SSH and works through corporate firewalls."

IFS=',' read -ra K3S_IP_ARRAY <<< "$K3S_PUBLIC_IPS"

for i in "${!K3S_REGION_ARRAY[@]}"; do
    region="${K3S_REGION_ARRAY[$i]}"
    vm_name="k3s-${region}"
    
    echo ""
    echo "Configuring k3s-${region}..."
    
    # Get public IP for kubeconfig
    public_ip=$(az vm show -g "$RESOURCE_GROUP" -n "$vm_name" -d --query publicIps -o tsv 2>/dev/null || echo "")
    K3S_IP_ARRAY[$i]="$public_ip"
    
    if [ -z "$public_ip" ]; then
        echo "⚠ Could not get IP for $vm_name, skipping..."
        continue
    fi
    
    echo "  VM Public IP: $public_ip"
    
    # Wait for k3s to be ready using Run Command
    echo "  Waiting for k3s to be ready..."
    k3s_ready=false
    for attempt in {1..30}; do
        result=$(az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "sudo k3s kubectl get nodes 2>/dev/null && echo K3S_READY" \
            --query 'value[0].message' -o tsv 2>/dev/null || echo "")
        
        if echo "$result" | grep -q "K3S_READY"; then
            echo "  ✓ k3s ready"
            k3s_ready=true
            break
        fi
        echo "  Waiting for k3s... (attempt $attempt/30)"
        sleep 10
    done
    
    if [ "$k3s_ready" = "false" ]; then
        echo "  ✗ ERROR: k3s failed to become ready on $vm_name after 5 minutes"
        echo "  Check VM status: az vm run-command invoke -g $RESOURCE_GROUP -n $vm_name --command-id RunShellScript --scripts 'systemctl status k3s'"
        continue
    fi
    
    # Get kubeconfig via Run Command
    echo "  Retrieving kubeconfig via Run Command..."
    RAW_OUTPUT=$(az vm run-command invoke \
        --resource-group "$RESOURCE_GROUP" \
        --name "$vm_name" \
        --command-id RunShellScript \
        --scripts "sudo cat /etc/rancher/k3s/k3s.yaml" \
        --query 'value[0].message' -o tsv 2>/dev/null || echo "")
    
    # Extract the YAML from the Run Command output
    # The output format is: [stdout]\n<content>\n[stderr]\n<errors>
    # We need to extract just the content between [stdout] and [stderr]
    KUBECONFIG_CONTENT=$(echo "$RAW_OUTPUT" | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag')
    
    # Fallback: try to find apiVersion line and extract from there
    if [ -z "$KUBECONFIG_CONTENT" ] || ! echo "$KUBECONFIG_CONTENT" | grep -q "apiVersion"; then
        KUBECONFIG_CONTENT=$(echo "$RAW_OUTPUT" | sed -n '/^apiVersion:/,/^current-context:/p')
        # Add the current-context line if we have it
        CURRENT_CTX=$(echo "$RAW_OUTPUT" | grep "^current-context:" | head -1)
        if [ -n "$CURRENT_CTX" ]; then
            KUBECONFIG_CONTENT="$KUBECONFIG_CONTENT"$'\n'"$CURRENT_CTX"
        fi
    fi
    
    if [ -n "$KUBECONFIG_CONTENT" ] && echo "$KUBECONFIG_CONTENT" | grep -q "apiVersion"; then
        # Replace localhost/127.0.0.1 with public IP and set context name
        KUBECONFIG_FILE="$HOME/.kube/k3s-${region}.yaml"
        echo "$KUBECONFIG_CONTENT" | \
            sed "s|127.0.0.1|${public_ip}|g" | \
            sed "s|server: https://[^:]*:|server: https://${public_ip}:|g" | \
            sed "s|name: default|name: k3s-${region}|g" | \
            sed "s|cluster: default|cluster: k3s-${region}|g" | \
            sed "s|user: default|user: k3s-${region}|g" | \
            sed "s|current-context: default|current-context: k3s-${region}|g" \
            > "$KUBECONFIG_FILE"
        
        chmod 600 "$KUBECONFIG_FILE"
        
        # Delete existing context if present (avoids merge conflicts)
        kubectl config delete-context "k3s-${region}" 2>/dev/null || true
        kubectl config delete-cluster "k3s-${region}" 2>/dev/null || true
        kubectl config delete-user "k3s-${region}" 2>/dev/null || true
        
        # Merge into main kubeconfig
        export KUBECONFIG="$HOME/.kube/config:$KUBECONFIG_FILE"
        kubectl config view --flatten > "$HOME/.kube/config.new"
        mv "$HOME/.kube/config.new" "$HOME/.kube/config"
        chmod 600 "$HOME/.kube/config"
        unset KUBECONFIG
        
        echo "  ✓ Context added: k3s-${region}"
    else
        echo "  ⚠ Could not retrieve kubeconfig for k3s-${region}"
        echo "  Debug: Run Command output was:"
        echo "$KUBECONFIG_CONTENT" | head -5
    fi
done

# Create kubectl aliases
echo ""
echo "Setting up kubectl aliases..."

ALIAS_FILE="$HOME/.bashrc"
if [[ "$OSTYPE" == "darwin"* ]]; then
    ALIAS_FILE="$HOME/.zshrc"
fi

# Remove old aliases (use markers for clean removal)
if [ -f "$ALIAS_FILE" ]; then
    awk '/# BEGIN k3s-fleet aliases/,/# END k3s-fleet aliases/ {next} {print}' "$ALIAS_FILE" > "$ALIAS_FILE.tmp" 2>/dev/null || true
    mv "$ALIAS_FILE.tmp" "$ALIAS_FILE" 2>/dev/null || true
fi

# Add new aliases with markers
{
    echo ""
    echo "# BEGIN k3s-fleet aliases"
    echo "alias k-hub='kubectl --context hub-${HUB_REGION}'"
    echo "alias k-${HUB_REGION}='kubectl --context hub-${HUB_REGION}'"
    for region in "${K3S_REGION_ARRAY[@]}"; do
        echo "alias k-${region}='kubectl --context k3s-${region}'"
    done
    echo "# END k3s-fleet aliases"
} >> "$ALIAS_FILE"

echo "✓ Aliases added to $ALIAS_FILE"

# Save deployment info (quote values with spaces)
DEPLOYMENT_INFO_FILE="${SCRIPT_DIR}/.deployment-info"
{
    echo "RESOURCE_GROUP=\"$RESOURCE_GROUP\""
    echo "HUB_REGION=\"$HUB_REGION\""
    echo "HUB_CLUSTER_NAME=\"hub-${HUB_REGION}\""
    echo "AKS_CLUSTER_NAME=\"$AKS_CLUSTER_NAME\""
    echo "K3S_REGIONS=\"${K3S_REGION_ARRAY[*]}\""
    echo "K3S_PUBLIC_IPS=\"${K3S_IP_ARRAY[*]}\""
} > "$DEPLOYMENT_INFO_FILE"

echo ""
echo "======================================="
echo "Infrastructure Deployment Complete!"
echo "======================================="
echo ""
echo "Clusters:"
echo "  - hub-${HUB_REGION} (AKS)"
for i in "${!K3S_REGION_ARRAY[@]}"; do
    echo "  - k3s-${K3S_REGION_ARRAY[$i]} (VM: ${K3S_IP_ARRAY[$i]})"
done
echo ""
echo "Next steps:"
echo "  1. Source your shell config: source $ALIAS_FILE"
echo "  2. Install Istio: ./install-istio.sh"
echo "  3. Setup Fleet: ./setup-fleet.sh"
echo "  4. Install cert-manager: ./install-cert-manager.sh"
echo "  5. Install DocumentDB operator: ./install-documentdb-operator.sh"
echo "  6. Deploy DocumentDB: ./deploy-documentdb.sh"
echo ""
echo "Quick test:"
echo "  kubectl --context hub-${HUB_REGION} get nodes"
for region in "${K3S_REGION_ARRAY[@]}"; do
    echo "  kubectl --context k3s-${region} get nodes"
done
echo ""
