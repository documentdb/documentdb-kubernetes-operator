# Setup Azure Fleet Manager hub + AKS member cluster
# Creates Fleet hub and AKS cluster, then joins AKS to Fleet
# PowerShell version for Windows

param(
    [string]$ResourceGroup = "documentdb-fleet-rg",
    [string]$Location = "eastus",
    [string]$FleetName = "documentdb-fleet",
    [string]$AksCluster = "documentdb-aks",
    [int]$NodeCount = 2,
    [string]$NodeSize = "Standard_D4s_v3",
    [switch]$Help
)

# Colors
function Write-Log { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] $Message" -ForegroundColor Cyan }
function Write-Success { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] ✅ $Message" -ForegroundColor Green }
function Write-Warn { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] ⚠️  $Message" -ForegroundColor Yellow }
function Write-Err { param([string]$Message) Write-Host "[$(Get-Date -Format 'HH:mm:ss')] ❌ $Message" -ForegroundColor Red; exit 1 }

if ($Help) {
    Write-Host @"
Usage: .\setup-fleet-hub.ps1 [OPTIONS]

Creates Azure Fleet Manager hub and AKS member cluster

Parameters:
  -ResourceGroup   Azure resource group (default: documentdb-fleet-rg)
  -Location        Azure region (default: eastus)
  -FleetName       Fleet hub name (default: documentdb-fleet)
  -AksCluster      AKS cluster name (default: documentdb-aks)
  -NodeCount       Number of nodes (default: 2)
  -NodeSize        VM size (default: Standard_D4s_v3)
  -Help            Show this help message
"@
    exit 0
}

# Check prerequisites
Write-Log "Checking prerequisites..."
if (-not (Get-Command az -ErrorAction SilentlyContinue)) { Write-Err "Azure CLI not found" }
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) { Write-Err "kubectl not found" }
if (-not (Get-Command helm -ErrorAction SilentlyContinue)) { Write-Err "Helm not found" }

# Check Azure login
try {
    $null = az account show 2>$null
    if ($LASTEXITCODE -ne 0) { throw }
} catch {
    Write-Err "Not logged into Azure. Run 'az login' first."
}

# Install Fleet extension
Write-Log "Checking Azure CLI Fleet extension..."
az extension add --name fleet --upgrade --yes 2>$null

$Subscription = az account show --query name -o tsv
Write-Log "Using Azure subscription: $Subscription"

# Create resource group
Write-Log "Creating resource group: $ResourceGroup in $Location..."
az group create --name $ResourceGroup --location $Location --output none
if ($LASTEXITCODE -ne 0) { Write-Err "Failed to create resource group" }
Write-Success "Resource group created"

# Create Fleet hub
Write-Log "Creating Azure Fleet Manager hub: $FleetName..."
az fleet create --resource-group $ResourceGroup --name $FleetName --location $Location --output none
if ($LASTEXITCODE -ne 0) { Write-Err "Failed to create Fleet hub" }
Write-Success "Fleet hub created"

# Create AKS cluster
Write-Log "Creating AKS cluster: $AksCluster (this takes ~5-10 minutes)..."
az aks create `
    --resource-group $ResourceGroup `
    --name $AksCluster `
    --node-count $NodeCount `
    --node-vm-size $NodeSize `
    --enable-managed-identity `
    --generate-ssh-keys `
    --tags purpose=documentdb environment=aks fleet=$FleetName `
    --output none
if ($LASTEXITCODE -ne 0) { Write-Err "Failed to create AKS cluster" }
Write-Success "AKS cluster created"

# Wait for AKS to be fully ready
Write-Log "Waiting for AKS cluster to be ready..."
do {
    Start-Sleep -Seconds 10
    $state = az aks show -g $ResourceGroup -n $AksCluster --query provisioningState -o tsv
    Write-Log "AKS state: $state"
} while ($state -eq "Updating")

# Join AKS to Fleet
Write-Log "Joining AKS cluster to Fleet..."
$AksId = az aks show -g $ResourceGroup -n $AksCluster --query id -o tsv
az fleet member create `
    --resource-group $ResourceGroup `
    --fleet-name $FleetName `
    --name $AksCluster `
    --member-cluster-id $AksId `
    --output none
if ($LASTEXITCODE -ne 0) { Write-Err "Failed to join AKS to Fleet" }
Write-Success "AKS joined to Fleet"

# Get AKS credentials
Write-Log "Getting AKS cluster credentials..."
az aks get-credentials --resource-group $ResourceGroup --name $AksCluster --overwrite-existing

# Verify connectivity
Write-Log "Verifying AKS cluster connectivity..."
kubectl cluster-info
kubectl get nodes

# Install cert-manager on AKS
Write-Log "Installing cert-manager on AKS..."
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.2/cert-manager.yaml
Write-Log "Waiting for cert-manager to be ready..."
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s
Write-Success "cert-manager installed on AKS"

# Summary
Write-Host ""
Write-Host "=============================================="
Write-Success "Fleet Hub + AKS Member Setup Complete!"
Write-Host "=============================================="
Write-Host ""
Write-Host "Fleet Details:"
Write-Host "  Resource Group:  $ResourceGroup"
Write-Host "  Fleet Hub:       $FleetName"
Write-Host "  AKS Member:      $AksCluster"
Write-Host "  Location:        $Location"
Write-Host ""
Write-Host "Azure Portal Links:"
$SubscriptionId = az account show --query id -o tsv
Write-Host "  Fleet Hub:  https://portal.azure.com/#@/resource/subscriptions/$SubscriptionId/resourceGroups/$ResourceGroup/providers/Microsoft.ContainerService/fleets/$FleetName/overview"
Write-Host "  AKS:        https://portal.azure.com/#@/resource/subscriptions/$SubscriptionId/resourceGroups/$ResourceGroup/providers/Microsoft.ContainerService/managedClusters/$AksCluster/overview"
Write-Host ""
Write-Host "Next step: Run .\setup-arc-member.ps1 to add Arc-enabled on-prem cluster"
Write-Host ""

# Export variables for next script
$envContent = @"
`$env:RESOURCE_GROUP = "$ResourceGroup"
`$env:LOCATION = "$Location"
`$env:FLEET_NAME = "$FleetName"
`$env:AKS_CLUSTER = "$AksCluster"
"@
$envContent | Out-File -FilePath ".fleet-env.ps1" -Encoding UTF8
Write-Log "Variables saved to .fleet-env.ps1 (dot-source it for next scripts: . .\.fleet-env.ps1)"
