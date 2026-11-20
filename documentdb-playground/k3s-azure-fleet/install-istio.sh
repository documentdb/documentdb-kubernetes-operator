#!/bin/bash
set -e

# ================================
# Install Istio Service Mesh across all clusters
# ================================
# - AKS hub: installed via istioctl (standard approach)
# - k3s VMs: installed via Helm + istioctl (for east-west gateway)
#
# Uses multi-primary, multi-network mesh configuration
# with shared root CA for cross-cluster mTLS trust.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ISTIO_VERSION="${ISTIO_VERSION:-1.24.0}"

# Load deployment info
if [ -f "${SCRIPT_DIR}/.deployment-info" ]; then
    source "${SCRIPT_DIR}/.deployment-info"
else
    echo "Error: .deployment-info not found. Run deploy-infrastructure.sh first."
    exit 1
fi

# Build cluster list
ALL_CLUSTERS=("hub-${HUB_REGION}")
IFS=' ' read -ra K3S_REGION_ARRAY <<< "$K3S_REGIONS"
IFS=' ' read -ra K3S_IP_ARRAY <<< "$K3S_PUBLIC_IPS"
for region in "${K3S_REGION_ARRAY[@]}"; do
    ALL_CLUSTERS+=("k3s-${region}")
done

echo "======================================="
echo "Istio Service Mesh Installation"
echo "======================================="
echo "Version: $ISTIO_VERSION"
echo "Clusters: ${ALL_CLUSTERS[*]}"
echo "======================================="
echo ""

# Check prerequisites
for cmd in kubectl helm make openssl curl; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Error: Required command '$cmd' not found."
        exit 1
    fi
done

# Download istioctl if not present
if ! command -v istioctl &> /dev/null; then
    echo "Installing istioctl..."
    curl -L https://istio.io/downloadIstio | ISTIO_VERSION=${ISTIO_VERSION} sh -
    export PATH="$PWD/istio-${ISTIO_VERSION}/bin:$PATH"
    echo "✓ istioctl installed"
fi

ISTIO_INSTALLED_VERSION=$(istioctl version --remote=false 2>/dev/null | head -1 || echo "unknown")
echo "Using istioctl: $ISTIO_INSTALLED_VERSION"

# ─── Generate shared root CA ───
CERT_DIR="${SCRIPT_DIR}/.istio-certs"
mkdir -p "$CERT_DIR"

if [ ! -f "$CERT_DIR/root-cert.pem" ]; then
    echo ""
    echo "Generating shared root CA..."
    pushd "$CERT_DIR" > /dev/null
    if [ ! -d "istio-${ISTIO_VERSION}" ]; then
        curl -sL "https://github.com/istio/istio/archive/refs/tags/${ISTIO_VERSION}.tar.gz" | tar xz
    fi
    make -f "istio-${ISTIO_VERSION}/tools/certs/Makefile.selfsigned.mk" root-ca
    echo "✓ Root CA generated"
    popd > /dev/null
fi

# ─── Install Istio on each cluster ───
for i in "${!ALL_CLUSTERS[@]}"; do
    cluster="${ALL_CLUSTERS[$i]}"
    network_id="network$((i + 1))"
    
    echo ""
    echo "======================================="
    echo "Installing Istio on $cluster (${network_id})"
    echo "======================================="
    
    # Verify cluster access
    if [[ "$cluster" != k3s-* ]]; then
        if ! kubectl --context "$cluster" get nodes --request-timeout=10s &>/dev/null; then
            echo "⚠ Cannot access $cluster via kubectl"
            continue
        fi
    fi
    
    if [[ "$cluster" == k3s-* ]]; then
        # ─── k3s clusters: namespace label + certs via Run Command ───
        region="${cluster#k3s-}"
        vm_name="k3s-${region}"
        
        echo "Labeling istio-system namespace on $vm_name..."
        az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "k3s kubectl create namespace istio-system --dry-run=client -o yaml | k3s kubectl apply -f - && k3s kubectl label namespace istio-system topology.istio.io/network=${network_id} --overwrite && echo NS_LABELED" \
            --query 'value[0].message' -o tsv 2>/dev/null | tail -1
    else
        # AKS: direct kubectl
        kubectl --context "$cluster" create namespace istio-system --dry-run=client -o yaml | \
            kubectl --context "$cluster" apply -f - 2>/dev/null || true
        kubectl --context "$cluster" label namespace istio-system topology.istio.io/network="${network_id}" --overwrite 2>/dev/null || true
    fi
    
    # Generate and apply cluster-specific certificates
    echo "Generating certificates for $cluster..."
    pushd "$CERT_DIR" > /dev/null
    make -f "istio-${ISTIO_VERSION}/tools/certs/Makefile.selfsigned.mk" "${cluster}-cacerts"
    popd > /dev/null
    
    if [[ "$cluster" == k3s-* ]]; then
        # k3s certs are pre-injected via cloud-init during VM deployment.
        # Verify the cacerts secret exists on the VM.
        region="${cluster#k3s-}"
        vm_name="k3s-${region}"
        echo "Verifying pre-injected cacerts secret on $vm_name..."
        result=$(az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts 'k3s kubectl get secret cacerts -n istio-system -o name 2>/dev/null && echo CERTS_OK || echo CERTS_MISSING' \
            --query 'value[0].message' -o tsv 2>/dev/null || echo "")
        
        if echo "$result" | grep -q "CERTS_OK"; then
            echo "✓ Cacerts secret verified (pre-injected via cloud-init)"
        else
            echo "⚠ Cacerts secret not found — applying via Run Command..."
            # Fallback: create from locally-generated certs via Run Command
            ROOT_CERT_CONTENT=$(cat "${CERT_DIR}/${cluster}/root-cert.pem")
            CA_CERT_CONTENT=$(cat "${CERT_DIR}/${cluster}/ca-cert.pem")
            CA_KEY_CONTENT=$(cat "${CERT_DIR}/${cluster}/ca-key.pem")
            CERT_CHAIN_CONTENT=$(cat "${CERT_DIR}/${cluster}/cert-chain.pem")
            az vm run-command invoke \
                --resource-group "$RESOURCE_GROUP" \
                --name "$vm_name" \
                --command-id RunShellScript \
                --scripts "
k3s kubectl create namespace istio-system --dry-run=client -o yaml | k3s kubectl apply -f -
cat > /tmp/root-cert.pem <<'CERTEOF'
${ROOT_CERT_CONTENT}
CERTEOF
cat > /tmp/ca-cert.pem <<'CERTEOF'
${CA_CERT_CONTENT}
CERTEOF
cat > /tmp/ca-key.pem <<'CERTEOF'
${CA_KEY_CONTENT}
CERTEOF
cat > /tmp/cert-chain.pem <<'CERTEOF'
${CERT_CHAIN_CONTENT}
CERTEOF
k3s kubectl create secret generic cacerts -n istio-system \
  --from-file=ca-cert.pem=/tmp/ca-cert.pem \
  --from-file=ca-key.pem=/tmp/ca-key.pem \
  --from-file=root-cert.pem=/tmp/root-cert.pem \
  --from-file=cert-chain.pem=/tmp/cert-chain.pem \
  --dry-run=client -o yaml | k3s kubectl apply -f -
echo CERTS_APPLIED" \
                --query 'value[0].message' -o tsv 2>/dev/null || echo "  ⚠ Failed to apply certs via Run Command"
        fi
    else
        # AKS: apply certs via kubectl (direct access works)
        kubectl --context "$cluster" create secret generic cacerts -n istio-system \
            --from-file="${CERT_DIR}/${cluster}/ca-cert.pem" \
            --from-file="${CERT_DIR}/${cluster}/ca-key.pem" \
            --from-file="${CERT_DIR}/${cluster}/root-cert.pem" \
            --from-file="${CERT_DIR}/${cluster}/cert-chain.pem" \
            --dry-run=client -o yaml | kubectl --context "$cluster" apply -f - 2>/dev/null || true
    fi
    echo "✓ Certificates configured"
    
    if [[ "$cluster" == k3s-* ]]; then
        # ─── k3s clusters: install via Helm through Run Command ───
        region="${cluster#k3s-}"
        vm_name="k3s-${region}"
        
        # Look up public IP for this region
        public_ip=""
        for idx in "${!K3S_REGION_ARRAY[@]}"; do
            if [ "${K3S_REGION_ARRAY[$idx]}" = "$region" ]; then
                public_ip="${K3S_IP_ARRAY[$idx]}"
                break
            fi
        done
        
        echo "Installing Istio via Helm on $vm_name (Run Command)..."
        
        # Step 1: Install istio-base via Helm
        echo "  Installing istio-base..."
        az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
helm repo add istio https://istio-release.storage.googleapis.com/charts
helm repo update istio
helm upgrade --install istio-base istio/base \
  --namespace istio-system \
  --version ${ISTIO_VERSION} \
  --skip-schema-validation \
  --wait --timeout 2m && echo ISTIO_BASE_OK || echo ISTIO_BASE_FAIL" \
            --query 'value[0].message' -o tsv 2>/dev/null | tail -3
        
        # Step 2: Install istiod via Helm
        echo "  Installing istiod..."
        az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
helm repo add istio https://istio-release.storage.googleapis.com/charts
helm upgrade --install istiod istio/istiod \
  --namespace istio-system \
  --version ${ISTIO_VERSION} \
  --set global.meshID=mesh1 \
  --set global.multiCluster.clusterName=${cluster} \
  --set global.network=${network_id} \
  --set pilot.autoscaleEnabled=false \
  --set pilot.replicaCount=1 \
  --set meshConfig.defaultConfig.proxyMetadata.ISTIO_META_DNS_CAPTURE=true \
  --set meshConfig.defaultConfig.proxyMetadata.ISTIO_META_DNS_AUTO_ALLOCATE=true \
  --wait --timeout 5m && echo ISTIOD_OK || echo ISTIOD_FAIL" \
            --query 'value[0].message' -o tsv 2>/dev/null | tail -3
        
        echo "✓ Istio control plane installed via Helm"
        
        # Step 3: Install east-west gateway via Helm (use values file for dot-containing labels)
        echo "  Installing east-west gateway..."
        az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
cat > /tmp/eastwest-values.yaml <<'VALEOF'
labels:
  istio: eastwestgateway
  app: istio-eastwestgateway
  topology.istio.io/network: ${network_id}
env:
  ISTIO_META_ROUTER_MODE: sni-dnat
  ISTIO_META_REQUESTED_NETWORK_VIEW: ${network_id}
service:
  ports:
    - name: status-port
      port: 15021
      targetPort: 15021
    - name: tls
      port: 15443
      targetPort: 15443
    - name: tls-istiod
      port: 15012
      targetPort: 15012
    - name: tls-webhook
      port: 15017
      targetPort: 15017
VALEOF
helm repo add istio https://istio-release.storage.googleapis.com/charts
helm upgrade --install istio-eastwestgateway istio/gateway \
  -n istio-system \
  --version ${ISTIO_VERSION} \
  -f /tmp/eastwest-values.yaml \
  --skip-schema-validation \
  --wait --timeout 5m && echo EW_GW_OK || echo EW_GW_FAIL" \
            --query 'value[0].message' -o tsv 2>/dev/null | tail -3
        
        # Step 4: Patch east-west gateway with public IP + apply Gateway resource
        if [ -n "$public_ip" ]; then
            echo "  Patching east-west gateway with public IP: $public_ip"
            az vm run-command invoke \
                --resource-group "$RESOURCE_GROUP" \
                --name "$vm_name" \
                --command-id RunShellScript \
                --scripts "
k3s kubectl patch svc istio-eastwestgateway -n istio-system \
  --type=json -p='[{\"op\": \"add\", \"path\": \"/spec/externalIPs\", \"value\": [\"${public_ip}\"]}]'
cat <<'GWEOF' | k3s kubectl apply -n istio-system -f -
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: cross-network-gateway
spec:
  selector:
    istio: eastwestgateway
  servers:
    - port:
        number: 15443
        name: tls
        protocol: TLS
      tls:
        mode: AUTO_PASSTHROUGH
      hosts:
        - '*.local'
GWEOF
echo GW_PATCHED" \
                --query 'value[0].message' -o tsv 2>/dev/null | tail -3
        fi
        
        echo "✓ East-west gateway installed on $vm_name"
    else
        # ─── AKS hub: use istioctl (standard approach) ───
        echo "Installing Istio via istioctl..."
        cat <<EOF | istioctl install --context "$cluster" -y -f -
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
spec:
  values:
    global:
      meshID: mesh1
      multiCluster:
        clusterName: ${cluster}
      network: ${network_id}
  meshConfig:
    defaultConfig:
      proxyMetadata:
        ISTIO_META_DNS_CAPTURE: "true"
        ISTIO_META_DNS_AUTO_ALLOCATE: "true"
EOF
        
        echo "✓ Istio control plane installed"
        
        # Install east-west gateway
        echo "Installing east-west gateway..."
        ISTIO_DIR="${CERT_DIR}/istio-${ISTIO_VERSION}"
        if [ -f "${ISTIO_DIR}/samples/multicluster/gen-eastwest-gateway.sh" ]; then
            "${ISTIO_DIR}/samples/multicluster/gen-eastwest-gateway.sh" --network "${network_id}" | \
                istioctl install --context "$cluster" -y -f -
        fi
    fi
    
    if [[ "$cluster" != k3s-* ]]; then
        # AKS only: expose services + wait for gateway IP (k3s already did this above)
        echo "Exposing services via east-west gateway..."
        cat <<EOF | kubectl --context "$cluster" apply -n istio-system -f -
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: cross-network-gateway
spec:
  selector:
    istio: eastwestgateway
  servers:
    - port:
        number: 15443
        name: tls
        protocol: TLS
      tls:
        mode: AUTO_PASSTHROUGH
      hosts:
        - "*.local"
EOF
        echo "✓ Services exposed"
        
        echo "Waiting for east-west gateway external IP..."
        GATEWAY_IP=""
        for attempt in {1..30}; do
            GATEWAY_IP=$(kubectl --context "$cluster" get svc istio-eastwestgateway -n istio-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
            if [ -n "$GATEWAY_IP" ]; then
                echo "✓ Gateway IP: $GATEWAY_IP"
                break
            fi
            sleep 10
        done
        [ -z "$GATEWAY_IP" ] && echo "⚠ Gateway IP not yet assigned"
    fi
done

# ─── Create remote secrets ───
# Remote secrets allow each cluster's Istio to discover services on other clusters.
# For k3s clusters, we use Run Command since direct kubectl may not work.
echo ""
echo "======================================="
echo "Creating remote secrets for cross-cluster discovery"
echo "======================================="

# Helper: apply a secret YAML to a target cluster (handles k3s via Run Command)
apply_secret_to_target() {
    local target="$1"
    local secret_yaml="$2"
    
    if [[ "$target" == k3s-* ]]; then
        local region="${target#k3s-}"
        local vm_name="k3s-${region}"
        # Escape the YAML for embedding in Run Command script
        az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "cat <<'SECRETEOF' | k3s kubectl apply -f -
${secret_yaml}
SECRETEOF
echo SECRET_APPLIED" \
            --query 'value[0].message' -o tsv 2>/dev/null | tail -1
    else
        echo "$secret_yaml" | kubectl --context "$target" apply -f - 2>/dev/null
    fi
}

for source_cluster in "${ALL_CLUSTERS[@]}"; do
    if [[ "$source_cluster" == k3s-* ]]; then
        # k3s source: read the pre-built remote secret from the VM
        # (auto-generated during cloud-init — see main.bicep runcmd)
        source_region="${source_cluster#k3s-}"
        source_vm="k3s-${source_region}"
        
        echo "Reading pre-built remote secret from $source_vm..."
        RAW_OUTPUT=$(az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$source_vm" \
            --command-id RunShellScript \
            --scripts "cat /etc/istio-remote/remote-secret.yaml 2>/dev/null || echo REMOTE_SECRET_NOT_FOUND" \
            --query 'value[0].message' -o tsv 2>/dev/null || echo "")
        SECRET_YAML=$(echo "$RAW_OUTPUT" | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag')
        
        if [ -z "$SECRET_YAML" ] || ! echo "$SECRET_YAML" | grep -q "apiVersion"; then
            echo "  ⚠ Remote secret not ready on $source_vm, skipping"
            echo "  (Cloud-init may still be running. Re-run this script to retry.)"
            continue
        fi
        
        for target_cluster in "${ALL_CLUSTERS[@]}"; do
            if [ "$source_cluster" != "$target_cluster" ]; then
                echo "  Applying: $source_cluster -> $target_cluster"
                apply_secret_to_target "$target_cluster" "$SECRET_YAML"
            fi
        done
    else
        # AKS source: use istioctl (direct access works)
        for target_cluster in "${ALL_CLUSTERS[@]}"; do
            if [ "$source_cluster" != "$target_cluster" ]; then
                echo "Creating secret: $source_cluster -> $target_cluster"
                SECRET_YAML=$(istioctl create-remote-secret --context="$source_cluster" --name="$source_cluster" 2>/dev/null || echo "")
                if [ -n "$SECRET_YAML" ]; then
                    apply_secret_to_target "$target_cluster" "$SECRET_YAML"
                else
                    echo "  ⚠ Could not create remote secret for $source_cluster"
                fi
            fi
        done
    fi
done

echo "✓ Remote secrets configured"

# ─── Verify ───
echo ""
echo "======================================="
echo "Verifying Istio Installation"
echo "======================================="

for cluster in "${ALL_CLUSTERS[@]}"; do
    echo ""
    echo "=== $cluster ==="
    if [[ "$cluster" == k3s-* ]]; then
        region="${cluster#k3s-}"
        vm_name="k3s-${region}"
        az vm run-command invoke \
            --resource-group "$RESOURCE_GROUP" \
            --name "$vm_name" \
            --command-id RunShellScript \
            --scripts "k3s kubectl get pods -n istio-system -o wide 2>/dev/null | head -10; echo '---'; k3s kubectl get svc -n istio-system istio-eastwestgateway 2>/dev/null || echo 'Gateway not found'" \
            --query 'value[0].message' -o tsv 2>/dev/null | awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag'
    else
        kubectl --context "$cluster" get pods -n istio-system -o wide 2>/dev/null | head -10 || echo "  Could not get pods"
        kubectl --context "$cluster" get svc -n istio-system istio-eastwestgateway 2>/dev/null || echo "  Gateway not found"
    fi
done

echo ""
echo "======================================="
echo "✅ Istio Installation Complete!"
echo "======================================="
echo ""
echo "Mesh: mesh1"
echo "Networks:"
for i in "${!ALL_CLUSTERS[@]}"; do
    echo "  - ${ALL_CLUSTERS[$i]}: network$((i + 1))"
done
echo ""
echo "Next steps:"
echo "  1. Setup Fleet: ./setup-fleet.sh"
echo "  2. Install cert-manager: ./install-cert-manager.sh"
echo "  3. Install DocumentDB operator: ./install-documentdb-operator.sh"
echo ""
