#!/usr/bin/env bash
# Consolidated AKS Fleet and DocumentDB Deployment Script
# This script combines all deployment steps into a single workflow

set -euo pipefail

# ============================================================================
# Configuration
# ============================================================================

RESOURCE_GROUP="${RESOURCE_GROUP:-documentdb-aks-fleet-rg}"
RG_LOCATION="${RG_LOCATION:-eastus2}"
HUB_REGION="${HUB_REGION:-$RG_LOCATION}"
TEMPLATE_DIR="$(dirname "$0")"
HUB_VM_SIZE="${HUB_VM_SIZE:-}"
VERSION="${VERSION:-200}"
VALUES_FILE="${VALUES_FILE:-}"
ISTIO_DIR="${ISTIO_DIR:-}"
AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME:-azure-documentdb}"
AKS_REGION="${AKS_REGION:-eastus2}"
HUB_CONTEXT="${HUB_CONTEXT:-hub}"

PROJECT_ID="${PROJECT_ID:-sanguine-office-475117-s6}"
GCP_USER="${GCP_USER:-alexanderlaye59@gmail.com}"
ZONE="${ZONE:-us-central1-a}"
GKE_CLUSTER_NAME="${GKE_CLUSTER_NAME:-gcp-documentdb}"

EKS_CLUSTER_NAME="${EKS_CLUSTER_NAME:-aws-documentdb}"
EKS_REGION="${EKS_REGION:-us-west-2}"

# ============================================================================
# Helper Functions
# ============================================================================

run() { echo "+ $*"; "$@"; }

check_prerequisites() {
  echo "Checking prerequisites..."

  # Check Azure CLI
  if ! command -v az &> /dev/null; then
    echo "ERROR: Azure CLI not found. Please install Azure CLI first." >&2
    exit 1
  fi

  # Check kubectl
  if ! command -v kubectl &> /dev/null; then
    echo "ERROR: kubectl not found. Please install kubectl first." >&2
    exit 1
  fi

  # Check Helm
  if ! command -v helm &> /dev/null; then
    echo "ERROR: Helm not found. Please install Helm first." >&2
    exit 1
  fi

  # Check gcloud CLI
  if ! command -v gcloud &> /dev/null; then
    echo "ERROR: gcloud CLI not found. Please install Google Cloud SDK first." >&2
    exit 1
  fi

  # Check AWS CLI
  if ! command -v aws &> /dev/null; then
    echo "ERROR: AWS CLI not found. Please install AWS CLI first." >&2
    exit 1
  fi

  # Check eksctl
  if ! command -v eksctl &> /dev/null; then
    echo "ERROR: eksctl not found. Please install eksctl first." >&2
    exit 1
  fi

  # Check jq
  if ! command -v jq &> /dev/null; then
    echo "ERROR: jq not found. Please install jq first." >&2
    exit 1
  fi

  # Check Azure login
  if ! az account show &> /dev/null; then
    echo "ERROR: Not logged into Azure. Please run 'az login' first." >&2
    exit 1
  fi

  # Check gcloud login
  gcloud config set account $GCP_USER
  if ! gcloud auth list --filter=status:ACTIVE --format="value(account)" 2>/dev/null | grep -q .; then
    echo "ERROR: Not logged into Google Cloud. Please run 'gcloud auth login' first." >&2
    exit 1
  fi

  # Check AWS credentials
  if ! aws sts get-caller-identity &> /dev/null; then
    echo "ERROR: AWS credentials not configured. Please run 'aws configure' first." >&2
    exit 1
  fi

  echo "✅ All prerequisites met"
}

wait_for_no_inprogress() {
  local rg="$1"
  echo "Checking for in-progress AKS operations in resource group '$rg'..."
  local inprogress
  inprogress=$(az aks list -g "$rg" -o json \
    | jq -r '.[] | select(.provisioningState != "Succeeded" and .provisioningState != null) | [.name, .provisioningState] | @tsv')

  if [ -z "$inprogress" ]; then
    echo "No in-progress AKS operations detected."
    return 0
  fi

  echo "Found clusters still provisioning:" 
  echo "$inprogress" | while IFS=$'\t' read -r name state; do echo "  - $name: $state"; done
  echo "Please re-run this script after the above operations complete." >&2
  return 1
}

# ============================================================================
# Step 1: Deploy AKS Fleet Infrastructure
# ============================================================================

aks_fleet_deploy() {
  echo "Creating or using resource group..."
  EXISTING_RG_LOCATION=$(az group show --name "$RESOURCE_GROUP" --query location -o tsv 2>/dev/null || true)
  if [ -n "$EXISTING_RG_LOCATION" ]; then
    echo "Using existing resource group '$RESOURCE_GROUP' in location '$EXISTING_RG_LOCATION'"
    RG_LOCATION="$EXISTING_RG_LOCATION"
  else
    az group create --name "$RESOURCE_GROUP" --location "$RG_LOCATION"
  fi

  echo "Deploying AKS Fleet with Bicep..."
  if ! wait_for_no_inprogress "$RESOURCE_GROUP"; then
    echo "Exiting without changes due to in-progress operations." >&2
    exit 1
  fi

  PARAMS=(
    --parameters "$TEMPLATE_DIR/parameters.bicepparam"
    --parameters hubRegion="$HUB_REGION"
    --parameters memberRegion="$AKS_REGION"
    --parameters memberName="$AKS_CLUSTER_NAME"
  )

  if [ -n "$HUB_VM_SIZE" ]; then
    echo "Overriding hubVmSize with: $HUB_VM_SIZE"
    PARAMS+=( --parameters hubVmSize="$HUB_VM_SIZE" )
  fi

  DEPLOYMENT_NAME="aks-fleet-$(date +%s)"
  az deployment group create \
    --name "$DEPLOYMENT_NAME" \
    --resource-group $RESOURCE_GROUP \
    --template-file "$TEMPLATE_DIR/main.bicep" \
    "${PARAMS[@]}" >/dev/null

  # Retrieve outputs
  DEPLOYMENT_OUTPUT=$(az deployment group show \
    --resource-group $RESOURCE_GROUP \
    --name "$DEPLOYMENT_NAME" \
    --query "properties.outputs" -o json)

  FLEET_NAME=$(echo $DEPLOYMENT_OUTPUT | jq -r '.fleetName.value')
  FLEET_ID_FROM_OUTPUT=$(echo $DEPLOYMENT_OUTPUT | jq -r '.fleetId.value')
  AKS_CLUSTER_NAME=$(echo $DEPLOYMENT_OUTPUT | jq -r '.memberClusterName.value')

  SUBSCRIPTION_ID=$(az account show --query id -o tsv)
  export FLEET_ID="/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${RESOURCE_GROUP}/providers/Microsoft.ContainerService/fleets/${FLEET_NAME}"

  # Set up RBAC
  echo "Setting up RBAC access for Fleet..."
  export IDENTITY=$(az ad signed-in-user show --query "id" --output tsv)
  export ROLE="Azure Kubernetes Fleet Manager RBAC Cluster Admin"
  echo "Assigning role '$ROLE' to user '$IDENTITY'..."
  az role assignment create --role "${ROLE}" --assignee ${IDENTITY} --scope ${FLEET_ID} >/dev/null 2>&1 || true

  # Fetch kubeconfig contexts
  echo "Fetching kubeconfig contexts..."
  az fleet get-credentials --resource-group "$RESOURCE_GROUP" --name "$FLEET_NAME" --overwrite-existing

  az aks get-credentials --resource-group "$RESOURCE_GROUP" --name "$AKS_CLUSTER_NAME" --overwrite-existing 
}

# ============================================================================
# Step 1.2: Deploy GKE Infrastructure
# ============================================================================

# TODO move this to a check at the top
# sudo apt-get install google-cloud-cli-gke-gcloud-auth-plugin

# Create project if it doesn't exist
gke_deploy() {
  if ! gcloud projects describe $PROJECT_ID &>/dev/null; then
    gcloud projects create $PROJECT_ID
  fi

  gcloud config set project $PROJECT_ID

  gcloud services enable container.googleapis.com
  gcloud projects add-iam-policy-binding $PROJECT_ID --member="user:$GCP_USER" --role="roles/container.admin"
  gcloud projects add-iam-policy-binding $PROJECT_ID --member="user:$GCP_USER" --role="roles/compute.networkAdmin"
  gcloud projects add-iam-policy-binding $PROJECT_ID --member="user:$GCP_USER" --role="roles/iam.serviceAccountUser"

  # Delete cluster if it exists
  if gcloud container clusters describe "$GKE_CLUSTER_NAME" --zone "$ZONE" --project $PROJECT_ID &>/dev/null; then
    gcloud container clusters delete "$GKE_CLUSTER_NAME" \
      --zone "$ZONE" \
      --project $PROJECT_ID  \
      --quiet
  fi

  gcloud container clusters create "$GKE_CLUSTER_NAME" \
    --zone "$ZONE" \
    --num-nodes "2" \
    --machine-type "e2-standard-4" \
    --enable-ip-access \
    --project $PROJECT_ID

  kubectl config delete-context "$GKE_CLUSTER_NAME" || true
  kubectl config delete-cluster "$GKE_CLUSTER_NAME" || true
  kubectl config delete-user "$GKE_CLUSTER_NAME" || true
  gcloud container clusters get-credentials "$GKE_CLUSTER_NAME" \
      --location="$ZONE"
  fullName="gke_${PROJECT_ID}_${ZONE}_${GKE_CLUSTER_NAME}"
  # Replace all occurrences of the generated name with GKE_CLUSTER_NAME in kubeconfig
  sed -i "s|$fullName|$GKE_CLUSTER_NAME|g" ~/.kube/config
}


# ============================================================================
# Step 1.3: Deploy EKS Infrastructure
# ============================================================================

eks_deploy() {
  NODE_TYPE="m5.large"

  if eksctl get cluster --name $EKS_CLUSTER_NAME --region $EKS_REGION &> /dev/null; then
    echo "Cluster $EKS_CLUSTER_NAME already exists."
  else
    eksctl create cluster \
      --name $EKS_CLUSTER_NAME \
      --region $EKS_REGION \
      --node-type $NODE_TYPE \
      --nodes 2 \
      --nodes-min 2 \
      --nodes-max 2 \
      --managed \
      --with-oidc
  fi

  eksctl create iamserviceaccount \
    --cluster $EKS_CLUSTER_NAME \
    --namespace kube-system \
    --name ebs-csi-controller-sa \
    --attach-policy-arn arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy \
    --override-existing-serviceaccounts \
    --approve \
    --region $EKS_REGION

  # Install EBS CSI driver addon
  eksctl create addon \
      --name aws-ebs-csi-driver \
      --cluster $EKS_CLUSTER_NAME \
      --region $EKS_REGION \
      --force
      
  # Wait for EBS CSI driver to be ready
  echo "Waiting for EBS CSI driver to be ready..."
  sleep 5
  kubectl wait --for=condition=ready pod -l app=ebs-csi-controller -n kube-system --timeout=300s || echo "EBS CSI driver pods may still be starting"

  echo "Installing AWS Load Balancer Controller..."

    # Check if already installed
  if helm list -n kube-system | grep -q aws-load-balancer-controller; then
    echo "AWS Load Balancer Controller already installed. Skipping installation."
  else
    # Get VPC ID for the cluster
    VPC_ID=$(aws eks describe-cluster --name $EKS_CLUSTER_NAME --region $EKS_REGION --query 'cluster.resourcesVpcConfig.vpcId' --output text)
    echo "Using VPC ID: $VPC_ID"
      
    # Verify subnet tags for Load Balancer Controller
    echo "Verifying subnet tags for Load Balancer Controller..."
    PUBLIC_SUBNETS=$(aws ec2 describe-subnets \
      --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=true" \
      --query 'Subnets[].SubnetId' --output text --region $EKS_REGION)
      
    PRIVATE_SUBNETS=$(aws ec2 describe-subnets \
      --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=false" \
      --query 'Subnets[].SubnetId' --output text --region $EKS_REGION)

    # Tag public subnets for internet-facing load balancers
    if [ -n "$PUBLIC_SUBNETS" ]; then
        echo "Tagging public subnets for internet-facing load balancers..."
        for subnet in $PUBLIC_SUBNETS; do
            aws ec2 create-tags --resources "$subnet" --tags Key=kubernetes.io/role/elb,Value=1 --region $EKS_REGION 2>/dev/null || true
            echo "Tagged public subnet: $subnet"
        done
    fi

    # Tag private subnets for internal load balancers
    if [ -n "$PRIVATE_SUBNETS" ]; then
        echo "Tagging private subnets for internal load balancers..."
        for subnet in $PRIVATE_SUBNETS; do
            aws ec2 create-tags --resources "$subnet" --tags Key=kubernetes.io/role/internal-elb,Value=1 --region $EKS_REGION 2>/dev/null || true
            echo "Tagged private subnet: $subnet"
        done
    fi

    # Download the official IAM policy (latest version)
    echo "Downloading AWS Load Balancer Controller IAM policy (latest version)..."
    curl -o /tmp/iam_policy.json https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/main/docs/install/iam_policy.json

    # Get account ID
    ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

    # Check if policy exists and create/update as needed
    if aws iam get-policy --policy-arn arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy &>/dev/null; then
      echo "IAM policy already exists, updating to latest version..."
      # Delete and recreate to ensure we have the latest version
      aws iam delete-policy --policy-arn arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy 2>/dev/null || true
      sleep 5  # Wait for deletion to propagate
    fi

    # Create IAM policy with latest permissions
    echo "Creating IAM policy with latest permissions..."
    aws iam create-policy \
      --policy-name AWSLoadBalancerControllerIAMPolicy \
      --policy-document file:///tmp/iam_policy.json 2>/dev/null || \
      echo "IAM policy already exists or was just created"
    # Wait a moment for policy to be available
    sleep 5

    # Create IAM service account with proper permissions using eksctl
    echo "Creating IAM service account with proper permissions..."
    eksctl create iamserviceaccount \
      --cluster=$EKS_CLUSTER_NAME \
      --namespace=kube-system \
      --name=aws-load-balancer-controller \
      --role-name "AmazonEKSLoadBalancerControllerRole-$EKS_CLUSTER_NAME" \
      --attach-policy-arn=arn:aws:iam::$ACCOUNT_ID:policy/AWSLoadBalancerControllerIAMPolicy \
      --approve \
      --override-existing-serviceaccounts \
      --region=$EKS_REGION

    # Add EKS Helm repository
    helm repo add eks https://aws.github.io/eks-charts
    helm repo update eks

    # Install Load Balancer Controller using the existing service account
    helm install aws-load-balancer-controller eks/aws-load-balancer-controller \
      -n kube-system \
      --set clusterName=$EKS_CLUSTER_NAME \
      --set serviceAccount.create=false \
      --set serviceAccount.name=aws-load-balancer-controller \
      --set region=$EKS_REGION \
      --set vpcId=$VPC_ID

    # Wait for Load Balancer Controller to be ready
    echo "Waiting for Load Balancer Controller to be ready..."
    sleep 5
    kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=aws-load-balancer-controller -n kube-system --timeout=300s || echo "Load Balancer Controller pods may still be starting"

    # Clean up temp file
    rm -f /tmp/iam_policy.json

    echo "AWS Load Balancer Controller installed"
  fi

  if kubectl get storageclass documentdb-storage &> /dev/null; then
    echo "DocumentDB storage class already exists. Skipping creation."
  else
      kubectl apply -f - <<EOF
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: documentdb-storage
  annotations:
    storageclass.kubernetes.io/is-default-class: "false"
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
  iops: "3000"
  throughput: "125"
  fsType: ext4
  encrypted: "true"
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Retain
EOF
  fi


  kubectl config delete-context "$EKS_CLUSTER_NAME" || true
  kubectl config delete-cluster "$EKS_CLUSTER_NAME" || true
  kubectl config delete-user "$EKS_CLUSTER_NAME" || true
  clusterName="$EKS_CLUSTER_NAME.$EKS_REGION.eksctl.io"
  fullName=documentdb-admin@$clusterName
  # Replace all occurrences of the generated name with EKS_CLUSTER_NAME in kubeconfig
  sed -i "s|$fullName|$EKS_CLUSTER_NAME|g" ~/.kube/config
  sed -i "s|$clusterName|$EKS_CLUSTER_NAME|g" ~/.kube/config
}


# ============================================================================
# Step 2: Collect Names
# ============================================================================
check_prerequisites
aks_fleet_deploy &
aks_pid=$!
gke_deploy &
gke_pid=$!
eks_deploy
wait $aks_pid
wait $gke_pid

MEMBER_CLUSTER_NAMES=("$AKS_CLUSTER_NAME" "$GKE_CLUSTER_NAME" "$EKS_CLUSTER_NAME")

echo "✅ Fleet infrastructure deployed successfully"
echo "Member Clusters:"
echo "$AKS_CLUSTER_NAME"
echo "$GKE_CLUSTER_NAME"
echo "$EKS_CLUSTER_NAME"

# ============================================================================
# Step 3: Join member clusters to fleet
# ============================================================================

temp_dir=$(mktemp -d)
echo "Temporary directory created at: $temp_dir"
pushd $temp_dir
git clone https://github.com/kubefleet-dev/kubefleet.git
git clone https://github.com/Azure/fleet-networking.git
pushd $temp_dir/kubefleet
git checkout d3f42486fa78874e33ba8e6e5e34636767f77b8f
chmod +x hack/membership/joinMC.sh
hack/membership/joinMC.sh "v0.16.9" "$HUB_CONTEXT" "$GKE_CLUSTER_NAME" "$EKS_CLUSTER_NAME"
popd

# TODO clean this up a bit
echo "Waiting for $GKE_CLUSTER_NAME to join fleet..."
kubectl --context $HUB_CONTEXT wait --for=jsonpath='{.status.resourceUsage.observationTime}' membercluster/$GKE_CLUSTER_NAME
echo "Waiting for $EKS_CLUSTER_NAME to join fleet..."
kubectl --context $HUB_CONTEXT wait --for=jsonpath='{.status.resourceUsage.observationTime}' membercluster/$EKS_CLUSTER_NAME

pushd $temp_dir/fleet-networking
chmod +x hack/membership/joinMC.sh 
hack/membership/joinMC.sh "v0.16.5" "v0.3.24" $HUB_CONTEXT $GKE_CLUSTER_NAME $EKS_CLUSTER_NAME
popd

# TODO fix this
# kubectl --context $HUB_CONTEXT wait --for=jsonpath='{.status.agentStatus[?(@.conditions[?(@.reason=="AgentJoined" && @.status=="True")])].type}' membercluster/$GKE_CLUSTER_NAME

popd

# ============================================================================
# Step 4: Install cert-manager on all member clusters
# ============================================================================

helm repo add jetstack https://charts.jetstack.io 2>/dev/null || true
helm repo update >/dev/null 2>&1

for cluster in ${MEMBER_CLUSTER_NAMES[@]}; do
  echo "Installing cert-manager on $cluster..."
  kubectl config use-context "$cluster" 2>/dev/null
  helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --set installCRDs=true \
    --wait --timeout=5m >/dev/null 2>&1 || echo "  Warning: cert-manager installation issue on $cluster"
  echo "✓ cert-manager installed on $cluster"
done

echo "✅ cert-manager installed on all clusters"

# ============================================================================
# Step 5: Install Istio and setup mesh
# ============================================================================

# Create an issuer in istio-system namespace on hub
temp_dir=$(mktemp -d)
echo "Temporary directory created at: $temp_dir"

# Check if istioctl is installed, if not install it to temp_dir
if ! command -v istioctl &> /dev/null; then
  echo "istioctl not found, installing to $temp_dir..."
  ISTIO_VERSION="1.24.0"
  curl -L https://istio.io/downloadIstio | ISTIO_VERSION=$ISTIO_VERSION TARGET_ARCH=x86_64 sh - -d "$temp_dir" >/dev/null 2>&1
  export PATH="$temp_dir/istio-$ISTIO_VERSION/bin:$PATH"
  echo "✓ istioctl installed to $temp_dir/istio-$ISTIO_VERSION/bin"
else
  echo "✓ istioctl already installed: $(which istioctl)"
fi

if [ -z "$ISTIO_DIR" ]; then
  git clone https://github.com/istio/istio.git "$temp_dir/istio"
  export ISTIO_DIR="$temp_dir/istio"
fi
rm -rf "$TEMPLATE_DIR/certs"
mkdir $TEMPLATE_DIR/certs
pushd $TEMPLATE_DIR/certs
make -f "$ISTIO_DIR/tools/certs/Makefile.selfsigned.mk" root-ca
index=1
for cluster in ${MEMBER_CLUSTER_NAMES[@]}; do
  make -f "$ISTIO_DIR/tools/certs/Makefile.selfsigned.mk" "${cluster}-cacerts"
  kubectl --context "$cluster" delete namespace/istio-system --wait=true --ignore-not-found=true
  kubectl --context "$cluster" create namespace istio-system
  kubectl --context "$cluster" wait --for=jsonpath='{.status.phase}'=Active namespace/istio-system --timeout=60s
  # create certs
  kubectl --context "$cluster" create secret generic cacerts -n istio-system \
        --from-file="${cluster}/ca-cert.pem" \
        --from-file="${cluster}/ca-key.pem" \
        --from-file="${cluster}/root-cert.pem" \
        --from-file="${cluster}/cert-chain.pem"

  kubectl --context="${cluster}" label namespace istio-system topology.istio.io/network=network${index}

  #install istio on each cluster
  cat <<EOF | istioctl --context "$cluster" apply -y -f -
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
spec:
  values:
    global:
      meshID: mesh1
      multiCluster:
        clusterName: ${cluster}
      network: network${index}
EOF

  $ISTIO_DIR/samples/multicluster/gen-eastwest-gateway.sh \
    --network network${index} | \
    istioctl --context="${cluster}" install -y -f -

  kubectl --context="${cluster}" apply -n istio-system -f \
    $ISTIO_DIR/samples/multicluster/expose-services.yaml

  index=$((index + 1))
done

# only after everything else is done, make the secrets
for cluster in ${MEMBER_CLUSTER_NAMES[@]}; do
  remoteSecretFile=$temp_dir/${cluster}-remote-secret.yaml
  istioctl create-remote-secret \
      --context="${cluster}" \
      --name="${cluster}" > $remoteSecretFile
  for other_cluster in ${MEMBER_CLUSTER_NAMES[@]}; do
    if [ "$cluster" = "$other_cluster" ]; then
      continue
    fi
      kubectl apply -f $remoteSecretFile --context="${other_cluster}"
  done
done

popd

# 5.1 add lb tags to istio ew gateway on aws
kubectl --context "$EKS_CLUSTER_NAME" -n istio-system annotate service istio-eastwestgateway \
  service.beta.kubernetes.io/aws-load-balancer-type="nlb" \
  service.beta.kubernetes.io/aws-load-balancer-scheme="internet-facing" \
  service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled="true" \
  service.beta.kubernetes.io/aws-load-balancer-nlb-target-type="ip"

# ============================================================================
# Step 6: Install DocumentDB Operator
# ============================================================================

CHART_DIR="$(cd "$TEMPLATE_DIR/../../" && pwd)/operator/documentdb-helm-chart"
CHART_PKG="$TEMPLATE_DIR/documentdb-operator-0.0.${VERSION}.tgz"

# Apply cert-manager CRDs on hub
echo "Applying cert-manager CRDs on hub ($HUB_CONTEXT)..."
kubectl --context "$HUB_CONTEXT" apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.crds.yaml #>/dev/null 2>&1

# Create documentdb-operator namespace with Istio injection on hub
cat <<EOF | kubectl --context "$HUB_CONTEXT" apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-operator
  labels:
    istio-injection: enabled
EOF

# Build/package chart and remove previous
if [ -f "$CHART_PKG" ] && [ -d "$CHART_DIR" ]; then
  echo "Removing previous chart package $CHART_PKG..."
  rm -f "$CHART_PKG"
fi

echo "Packaging chart..."
helm dependency update "$CHART_DIR"
helm package "$CHART_DIR" --version 0.0."${VERSION}" --destination "$TEMPLATE_DIR"

# Install operator
echo "Installing DocumentDB operator on hub ($HUB_CONTEXT)..."
if [ -f "$CHART_PKG" ]; then
  if [ -n "$VALUES_FILE" ] && [ -f "$VALUES_FILE" ]; then
    helm upgrade --install documentdb-operator "$CHART_PKG" \
      --namespace documentdb-operator \
      --kube-context "$HUB_CONTEXT" \
      --values "$VALUES_FILE"
  else
    helm upgrade --install documentdb-operator "$CHART_PKG" \
      --namespace documentdb-operator \
      --kube-context "$HUB_CONTEXT"
  fi
fi

kubectl --context "$HUB_CONTEXT" apply -f "$TEMPLATE_DIR/documentdb-base.yaml" 

echo "✅ DocumentDB operator installed"

# Verify operator on member clusters
echo "Verifying operator deployment..."
for cluster in ${MEMBER_CLUSTER_NAMES[@]}; do
  READY=$(kubectl --context "$cluster" get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
  DESIRED=$(kubectl --context "$cluster" get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
  echo "  $cluster: $READY/$DESIRED replicas ready"
done

# ============================================================================
# Save environment variables and aliases
# ============================================================================
