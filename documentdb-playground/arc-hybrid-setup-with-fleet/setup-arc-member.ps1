# Setup Arc-enabled on-prem cluster and join to Fleet
# Creates Kind cluster (in WSL), Arc-enables it, and joins to existing Fleet
# PowerShell version for Azure CLI commands

param(
    [string]$ResourceGroup = "documentdb-fleet-rg",
    [string]$Location = "westus2",
    [string]$FleetName = "documentdb-fleet",
    [string]$ArcCluster = "documentdb-onprem",
    [switch]$Help
)

# Colors
function Write-Log { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] $Message" -ForegroundColor Cyan }
function Write-Success { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] ✅ $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] ⚠️  $Message" -ForegroundColor Yellow }
function Write-Err { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] ❌ $Message" -ForegroundColor Red; exit 1 }

if ($Help) {
    Write-Host @"
Usage: .\setup-arc-member.ps1 [OPTIONS]

Arc-enables a Kind cluster and joins it to Fleet.

IMPORTANT: Run Kind cluster creation in WSL first:
  kind create cluster --name documentdb-onprem

Parameters:
  -ResourceGroup   Azure resource group (default: documentdb-fleet-rg)
  -Location        Azure region (default: westus2)
  -FleetName       Fleet hub name (default: documentdb-fleet)
  -ArcCluster      Arc cluster name (default: documentdb-onprem)
  -Help            Show this help message
"@
    exit 0
}

Write-Host ""
Write-Host "=============================================="
Write-Host "Arc-Enabled Cluster Setup (PowerShell)"
Write-Host "=============================================="
Write-Host ""

# Set KUBECONFIG to WSL path
$WslUser = $env:USERNAME
$env:KUBECONFIG = "\\wsl.localhost\Ubuntu\home\$WslUser\.kube\config"
Write-Log "Using WSL kubeconfig: $env:KUBECONFIG"

# Check prerequisites
Write-Log "Checking prerequisites..."
if (-not (Get-Command az -ErrorAction SilentlyContinue)) { Write-Err "Azure CLI not found" }
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) { Write-Err "kubectl not found" }

# Check Azure login
try {
    $null = az account show 2>$null
    if ($LASTEXITCODE -ne 0) { throw }
} catch {
    Write-Err "Not logged into Azure. Run 'az login' first."
}

# Install connectedk8s extension
Write-Log "Checking Azure CLI extensions..."
az extension add --name connectedk8s --upgrade --yes 2>$null
az extension add --name fleet --upgrade --yes 2>$null

# Verify kubectl context
Write-Log "Verifying kubeconfig context..."
$currentContext = kubectl config current-context 2>$null
Write-Log "Current context: $currentContext"

# Check if Kind cluster exists and we can connect
$nodes = kubectl get nodes -o name 2>$null
if (-not $nodes) {
    Write-Host ""
    Write-Warn "Cannot connect to Kubernetes cluster!"
    Write-Host ""
    Write-Host "Please run this in WSL first:" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "  kind create cluster --name $ArcCluster --config - <<EOF"
    Write-Host "kind: Cluster"
    Write-Host "apiVersion: kind.x-k8s.io/v1alpha4"
    Write-Host "nodes:"
    Write-Host "- role: control-plane"
    Write-Host "- role: worker"
    Write-Host "- role: worker"
    Write-Host "EOF"
    Write-Host ""
    exit 1
}

Write-Log "Connected to cluster with nodes:"
kubectl get nodes

# Switch to Kind context if needed
if ($currentContext -ne "kind-$ArcCluster") {
    Write-Log "Switching to kind-$ArcCluster context..."
    kubectl config use-context "kind-$ArcCluster" 2>$null
    if ($LASTEXITCODE -ne 0) {
        Write-Warn "Could not switch context. Proceeding with current context: $currentContext"
    }
}

# Verify Fleet exists
Write-Log "Verifying Fleet hub exists..."
$fleetCheck = az fleet show --resource-group $ResourceGroup --name $FleetName 2>$null
if ($LASTEXITCODE -ne 0) {
    Write-Err "Fleet hub '$FleetName' not found. Run setup-fleet-hub.ps1 first."
}
Write-Success "Fleet hub found"

# Check if Arc cluster already exists
Write-Log "Checking for existing Arc cluster..."
$existingArc = az connectedk8s show -g $ResourceGroup -n $ArcCluster 2>$null
if ($LASTEXITCODE -eq 0) {
    Write-Warn "Arc cluster '$ArcCluster' already exists. Delete it first if you want to recreate."
    Write-Host "To delete: az connectedk8s delete --name $ArcCluster --resource-group $ResourceGroup --yes"
    
    $connectivityStatus = az connectedk8s show -g $ResourceGroup -n $ArcCluster --query connectivityStatus -o tsv
    if ($connectivityStatus -eq "Connected") {
        Write-Success "Arc cluster is connected. Skipping Arc-enable step."
    } else {
        Write-Err "Arc cluster exists but is not connected. Delete and recreate."
    }
} else {
    # Arc-enable the cluster
    Write-Log "Arc-enabling cluster (this takes 2-3 minutes)..."
    az connectedk8s connect `
        --name $ArcCluster `
        --resource-group $ResourceGroup `
        --location $Location `
        --tags "environment=onprem" "purpose=documentdb" "fleet=$FleetName" "cluster-type=kind"
    
    if ($LASTEXITCODE -ne 0) { Write-Err "Failed to Arc-enable cluster" }
    Write-Success "Cluster Arc-enabled"
}

# Verify Arc connection
Write-Log "Verifying Arc connection..."
az connectedk8s show --name $ArcCluster --resource-group $ResourceGroup `
    --query "{name:name, connectivityStatus:connectivityStatus, kubernetesVersion:kubernetesVersion}" -o table

# Check if already a Fleet member
Write-Log "Checking Fleet membership..."
$existingMember = az fleet member show --resource-group $ResourceGroup --fleet-name $FleetName --name $ArcCluster 2>$null
if ($LASTEXITCODE -eq 0) {
    Write-Success "Already a Fleet member"
} else {
    # Join Arc cluster to Fleet
    Write-Log "Joining Arc cluster to Fleet..."
    $ArcId = az connectedk8s show -g $ResourceGroup -n $ArcCluster --query id -o tsv
    az fleet member create `
        --resource-group $ResourceGroup `
        --fleet-name $FleetName `
        --name $ArcCluster `
        --member-cluster-id $ArcId
    
    if ($LASTEXITCODE -ne 0) { Write-Err "Failed to join Arc cluster to Fleet" }
    Write-Success "Arc cluster joined to Fleet"
}

# Show Arc agent pods
Write-Log "Arc agent pods:"
kubectl get pods -n azure-arc

# Create service account for portal viewing
Write-Log "Creating service account for Azure Portal access..."
kubectl create serviceaccount arc-portal-viewer -n default 2>$null
kubectl create clusterrolebinding arc-portal-viewer-binding `
    --clusterrole=cluster-admin `
    --serviceaccount=default:arc-portal-viewer 2>$null

Write-Log "Generating bearer token for Azure Portal..."
$BearerToken = kubectl create token arc-portal-viewer -n default --duration=8760h

# Summary
Write-Host ""
Write-Host "=============================================="
Write-Success "Arc-Enabled Member Setup Complete!"
Write-Host "=============================================="
Write-Host ""
Write-Host "Cluster Details:"
Write-Host "  Resource Group:     $ResourceGroup"
Write-Host "  Fleet Hub:          $FleetName"
Write-Host "  Arc Cluster Name:   $ArcCluster"
Write-Host "  Azure Location:     $Location"
Write-Host ""
Write-Host "Fleet Members:"
az fleet member list --resource-group $ResourceGroup --fleet-name $FleetName -o table
Write-Host ""
$SubscriptionId = az account show --query id -o tsv
Write-Host "Azure Portal Links:"
Write-Host "  Arc Cluster:  https://portal.azure.com/#@/resource/subscriptions/$SubscriptionId/resourceGroups/$ResourceGroup/providers/Microsoft.Kubernetes/connectedClusters/$ArcCluster/overview"
Write-Host ""
Write-Host "=============================================="
Write-Host "BEARER TOKEN FOR AZURE PORTAL" -ForegroundColor Yellow
Write-Host "=============================================="
Write-Host "Use this token in Azure Portal to view Kubernetes resources:"
Write-Host "1. Go to Arc cluster -> Kubernetes resources"
Write-Host "2. Click 'Sign in with service account token'"
Write-Host "3. Paste this token:"
Write-Host ""
Write-Host $BearerToken -ForegroundColor Green
Write-Host ""
Write-Host "=============================================="
Write-Host ""
Write-Host "Next steps (run in WSL):"
Write-Host "1. Install cert-manager: kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml"
Write-Host "2. Deploy DocumentDB: helm install documentdb-operator ./operator/documentdb-helm-chart --namespace documentdb-operator --create-namespace"
Write-Host ""

# Save token to file
$BearerToken | Out-File -FilePath ".arc-portal-token.txt" -Encoding UTF8
Write-Log "Token saved to .arc-portal-token.txt"
