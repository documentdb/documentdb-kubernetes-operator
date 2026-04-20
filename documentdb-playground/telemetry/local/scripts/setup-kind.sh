#!/bin/bash
set -euo pipefail

# Configuration
CLUSTER_NAME="${CLUSTER_NAME:-documentdb-telemetry}"
REG_NAME="kind-registry"
REG_PORT="5001"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOCAL_DIR="$(dirname "$SCRIPT_DIR")"
K8S_VERSION="${K8S_VERSION:-v1.35.0}"

echo "=== DocumentDB Telemetry - Kind Cluster Setup ==="

# 1. Create registry container unless it already exists
if [ "$(docker inspect -f '{{.State.Running}}' "${REG_NAME}" 2>/dev/null || true)" = 'true' ]; then
  echo "Registry '${REG_NAME}' already running"
elif docker inspect "${REG_NAME}" &>/dev/null; then
  echo "Starting existing registry '${REG_NAME}'..."
  docker start "${REG_NAME}"
else
  echo "Creating local registry on port ${REG_PORT}..."
  docker run -d --restart=always -p "127.0.0.1:${REG_PORT}:5000" --network bridge --name "${REG_NAME}" registry:2
fi

# 2. Create Kind cluster if it doesn't exist
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Kind cluster '${CLUSTER_NAME}' already exists"
else
  echo "Creating Kind cluster '${CLUSTER_NAME}' with 3 worker nodes..."
  cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --image "kindest/node:${K8S_VERSION}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
  - role: worker
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry]
      config_path = "/etc/containerd/certs.d"
EOF
fi

# 3. Configure registry on each node
REGISTRY_DIR="/etc/containerd/certs.d/localhost:${REG_PORT}"
for node in $(kind get nodes --name "${CLUSTER_NAME}"); do
  docker exec "${node}" mkdir -p "${REGISTRY_DIR}"
  cat <<EOF | docker exec -i "${node}" cp /dev/stdin "${REGISTRY_DIR}/hosts.toml"
[host."http://${REG_NAME}:5000"]
EOF
done

# 4. Connect registry to Kind network
if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${REG_NAME}")" = 'null' ]; then
  docker network connect "kind" "${REG_NAME}"
fi

# 5. Document the local registry
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:${REG_PORT}"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

echo ""
echo "✓ Kind cluster '${CLUSTER_NAME}' ready with 3 worker nodes"
echo "✓ Local registry available at localhost:${REG_PORT}"
echo ""
echo "Next: run ./scripts/deploy.sh to deploy the full stack"
