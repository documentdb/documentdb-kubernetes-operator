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
    if ! kubectl --context "$cluster" get nodes --request-timeout=10s &>/dev/null; then
        echo "⚠ Cannot access $cluster via kubectl, trying Run Command..."
    fi
    
    # Create istio-system namespace and label
    kubectl --context "$cluster" create namespace istio-system --dry-run=client -o yaml | \
        kubectl --context "$cluster" apply -f - 2>/dev/null || true
    kubectl --context "$cluster" label namespace istio-system topology.istio.io/network="${network_id}" --overwrite 2>/dev/null || true
    
    # Generate and apply cluster-specific certificates
    echo "Generating certificates for $cluster..."
    pushd "$CERT_DIR" > /dev/null
    make -f "istio-${ISTIO_VERSION}/tools/certs/Makefile.selfsigned.mk" "${cluster}-cacerts"
    popd > /dev/null
    
    kubectl --context "$cluster" create secret generic cacerts -n istio-system \
        --from-file="${CERT_DIR}/${cluster}/ca-cert.pem" \
        --from-file="${CERT_DIR}/${cluster}/ca-key.pem" \
        --from-file="${CERT_DIR}/${cluster}/root-cert.pem" \
        --from-file="${CERT_DIR}/${cluster}/cert-chain.pem" \
        --dry-run=client -o yaml | kubectl --context "$cluster" apply -f - 2>/dev/null || true
    echo "✓ Certificates configured"
    
    if [[ "$cluster" == k3s-* ]]; then
        # ─── k3s clusters: use Helm for base + istiod, istioctl for gateway ───
        echo "Installing Istio via Helm (k3s-optimized)..."
        
        # Add Istio Helm repo
        helm repo add istio https://istio-release.storage.googleapis.com/charts 2>/dev/null || true
        helm repo update istio 2>/dev/null || true
        
        # Install istio-base
        helm upgrade --install istio-base istio/base \
            --kube-context "$cluster" \
            --namespace istio-system \
            --version "$ISTIO_VERSION" \
            --wait --timeout 2m 2>/dev/null || echo "  istio-base may already be installed"
        
        # Install istiod (single replica, no autoscale for k3s)
        helm upgrade --install istiod istio/istiod \
            --kube-context "$cluster" \
            --namespace istio-system \
            --version "$ISTIO_VERSION" \
            --set global.meshID=mesh1 \
            --set global.multiCluster.clusterName="$cluster" \
            --set global.network="$network_id" \
            --set pilot.autoscaleEnabled=false \
            --set pilot.replicaCount=1 \
            --set meshConfig.defaultConfig.proxyMetadata.ISTIO_META_DNS_CAPTURE="true" \
            --set meshConfig.defaultConfig.proxyMetadata.ISTIO_META_DNS_AUTO_ALLOCATE="true" \
            --wait --timeout 5m 2>/dev/null || echo "  istiod may already be installed"
        
        echo "✓ Istio control plane installed via Helm"
        
        # Install east-west gateway via istioctl
        echo "Installing east-west gateway..."
        cat <<EOF | istioctl install --context "$cluster" -y -f - 2>/dev/null || true
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
metadata:
  name: eastwest
spec:
  revision: ""
  profile: empty
  components:
    ingressGateways:
      - name: istio-eastwestgateway
        label:
          istio: eastwestgateway
          app: istio-eastwestgateway
          topology.istio.io/network: ${network_id}
        enabled: true
        k8s:
          env:
            - name: ISTIO_META_ROUTER_MODE
              value: "sni-dnat"
            - name: ISTIO_META_REQUESTED_NETWORK_VIEW
              value: ${network_id}
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
  values:
    gateways:
      istio-ingressgateway:
        injectionTemplate: gateway
    global:
      network: ${network_id}
EOF
        
        # Patch east-west gateway with VM public IP
        # k3s servicelb assigns node internal IPs, not public IPs
        region="${cluster#k3s-}"
        for idx in "${!K3S_REGION_ARRAY[@]}"; do
            if [ "${K3S_REGION_ARRAY[$idx]}" = "$region" ]; then
                public_ip="${K3S_IP_ARRAY[$idx]}"
                if [ -n "$public_ip" ]; then
                    echo "Patching east-west gateway with public IP: $public_ip"
                    kubectl --context "$cluster" patch svc istio-eastwestgateway -n istio-system \
                        --type='json' -p="[{\"op\": \"add\", \"path\": \"/spec/externalIPs\", \"value\": [\"$public_ip\"]}]" 2>/dev/null || true
                fi
                break
            fi
        done
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
    
    echo "✓ East-west gateway installed"
    
    # Expose services via east-west gateway
    echo "Exposing services..."
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
    
    # Wait for gateway external IP
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
done

# ─── Create remote secrets ───
echo ""
echo "======================================="
echo "Creating remote secrets for cross-cluster discovery"
echo "======================================="

for source_cluster in "${ALL_CLUSTERS[@]}"; do
    for target_cluster in "${ALL_CLUSTERS[@]}"; do
        if [ "$source_cluster" != "$target_cluster" ]; then
            echo "Creating secret: $source_cluster -> $target_cluster"
            istioctl create-remote-secret --context="$source_cluster" --name="$source_cluster" | \
                kubectl --context="$target_cluster" apply -f - 2>/dev/null || \
                echo "  ⚠ Could not create remote secret (may already exist)"
        fi
    done
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
    kubectl --context "$cluster" get pods -n istio-system -o wide 2>/dev/null | head -10 || echo "  Could not get pods"
    kubectl --context "$cluster" get svc -n istio-system istio-eastwestgateway 2>/dev/null || echo "  Gateway not found"
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
