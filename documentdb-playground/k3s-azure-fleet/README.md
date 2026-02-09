# k3s on Azure VMs with KubeFleet and Istio Multi-Cluster Management

This playground demonstrates deploying DocumentDB on **k3s clusters running on Azure VMs**, integrated with **KubeFleet** for cluster membership and **Istio** for cross-cluster networking. This hybrid architecture showcases:

- **Lightweight Kubernetes**: k3s on Azure VMs for edge/resource-constrained scenarios
- **Cluster Membership**: KubeFleet hub for fleet-wide resource propagation (e.g., DocumentDB CRDs)
- **Istio Service Mesh**: Cross-cluster networking without complex VNet peering
- **Multi-Region**: AKS + k3s clusters across multiple Azure regions
- **DocumentDB**: Multi-region database deployment with Istio-based replication

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        Istio Service Mesh (mesh1)                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌────────────────────┐         ┌────────────────────┐                      │
│  │   AKS Hub Cluster  │         │   k3s Cluster      │                      │
│  │   (westus3)        │         │   (eastus2)        │                      │
│  │                    │         │                    │                      │
│  │  ┌──────────────┐  │         │  ┌──────────────┐  │                      │
│  │  │ KubeFleet    │  │         │  │ Fleet Member │  │                      │
│  │  │ Hub Agent    │  │         │  │ Agent        │  │                      │
│  │  └──────────────┘  │         │  └──────────────┘  │                      │
│  │  ┌──────────────┐  │         │  ┌──────────────┐  │                      │
│  │  │ Istio       ─┼──┼─────────┼──┼─ Istio       │  │                      │
│  │  │ East-West GW │  │         │  │ East-West GW │  │                      │
│  │  └──────────────┘  │         │  └──────────────┘  │                      │
│  │  ┌──────────────┐  │         │  ┌──────────────┐  │                      │
│  │  │ DocumentDB   │  │◄───────►│  │ DocumentDB   │  │                      │
│  │  │ (Primary)    │  │  Istio  │  │ (Replica)    │  │                      │
│  │  └──────────────┘  │         │  └──────────────┘  │                      │
│  └────────────────────┘         └────────────────────┘                      │
│            │                              │                                  │
│            │      Remote Secrets          │                                  │
│            └──────────────┬───────────────┘                                  │
│                           │                                                  │
│  ┌────────────────────────┴─────────────────────────┐                       │
│  │                k3s Cluster (uksouth)              │                       │
│  │  ┌──────────────┐  ┌──────────────┐              │                       │
│  │  │ Fleet Member │  │ Istio        │              │                       │
│  │  │ Agent        │  │ East-West GW │              │                       │
│  │  └──────────────┘  └──────────────┘              │                       │
│  │  ┌──────────────────────────────────┐            │                       │
│  │  │ DocumentDB (Replica)             │            │                       │
│  │  └──────────────────────────────────┘            │                       │
│  └──────────────────────────────────────────────────┘                       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Networking Design

- **Istio Service Mesh** handles all cross-cluster communication
- **East-West Gateways** expose services between clusters via LoadBalancer
- **Remote Secrets** enable service discovery across cluster boundaries
- **No VNet Peering Required** - Istio routes traffic over public LoadBalancers with mTLS
- **Shared Root CA** ensures all clusters trust each other

## Network Requirements

> **Important**: The k3s VMs require the following network access:
> 
> | Port | Protocol | Direction | Purpose |
> |------|----------|-----------|---------|
> | 6443 | TCP | Inbound | Kubernetes API server (kubectl access) |
> | 15443 | TCP | Inbound | Istio east-west gateway |
> | 80, 443 | TCP | Inbound | HTTP/HTTPS traffic |
>
> **Corporate Environment Considerations**:
> - This playground uses **Azure VM Run Command** for VM operations (no SSH/port 22 needed)
> - However, **kubectl access to k3s clusters** requires port 6443 to be reachable from your client
> - Corporate firewalls may block port 6443 even when NSG rules allow it
> - **If you cannot reach k3s API**: Use Azure VPN Gateway or deploy from within the Azure network
> - The AKS hub cluster uses Azure AD authentication and works through corporate firewalls

## Prerequisites

- Azure CLI installed and logged in (`az login`)
- Sufficient quota in target regions for VMs and AKS clusters
- Contributor access to the subscription
- kubelogin for Azure AD authentication: `az aks install-cli`
- Helm 3.x installed
- jq for JSON processing
- istioctl (auto-downloaded if not present)
- **Network access to port 6443 on k3s VM public IPs** (see Network Requirements)

## Quick Start

```bash
# Set your resource group (optional, defaults to documentdb-k3s-fleet-rg)
export RESOURCE_GROUP=my-documentdb-fleet

# 1. Deploy all infrastructure (AKS hub, k3s VMs)
./deploy-infrastructure.sh

# 2. Install Istio service mesh across all clusters
./install-istio.sh

# 3. Setup KubeFleet hub and join all members
./setup-fleet.sh

# 4. Install cert-manager across all clusters
./install-cert-manager.sh

# 5. Install DocumentDB operator on all clusters
./install-documentdb-operator.sh

# 6. Deploy multi-region DocumentDB
./deploy-documentdb.sh

# Test connection
./test-connection.sh
```

## Deployment Scripts

### 1. `deploy-infrastructure.sh`

Deploys Azure infrastructure:
- AKS hub cluster in westus3 (also serves as a member)
- Azure VMs with k3s in eastus2 and uksouth
- Each cluster in its own VNet (no peering required - Istio handles connectivity)

```bash
# With defaults
./deploy-infrastructure.sh

# With custom resource group
RESOURCE_GROUP=my-rg ./deploy-infrastructure.sh

# With custom regions
export K3S_REGIONS_CSV="eastus2,uksouth,northeurope"
./deploy-infrastructure.sh
```

### 2. `install-istio.sh`

Installs Istio service mesh on all clusters:
- Generates shared root CA for cross-cluster trust
- AKS hub: installs via `istioctl` (standard approach)
- k3s VMs: installs via **Helm** (`istio-base` + `istiod`) to avoid ownership conflicts, plus `istioctl` for east-west gateway only
- Patches k3s east-west gateways with VM public IPs (k3s `servicelb` only assigns internal IPs)
- Creates remote secrets for cross-cluster service discovery

```bash
./install-istio.sh
```

### 3. `setup-fleet.sh`

Sets up KubeFleet for multi-cluster management:
- Installs KubeFleet hub-agent on the hub cluster
- Joins all clusters (AKS and k3s) as fleet members
- **Known issue**: `joinMC.sh` has a context-switching bug; if a member fails to join, see Troubleshooting
- Fleet is used for cluster membership; Istio handles data traffic

```bash
./setup-fleet.sh
```

### 4. `install-cert-manager.sh`

Installs cert-manager on all clusters:
- Applies CRDs explicitly before Helm install (avoids silent failures)
- Installs via Helm with `startupapicheck.enabled=false` (avoids timeouts on k3s)
- Applies ClusterResourcePlacement for future cluster propagation

```bash
./install-cert-manager.sh
```

### 5. `install-documentdb-operator.sh`

Deploys DocumentDB operator on all clusters:
- Packages and installs the operator Helm chart on the AKS hub
- Installs CNPG from upstream release + DocumentDB manifests on k3s via Run Command
- Verifies deployment across all clusters

```bash
# Build from local chart (default)
./install-documentdb-operator.sh

# With custom values file
VALUES_FILE=custom-values.yaml ./install-documentdb-operator.sh
```

### 6. `deploy-documentdb.sh`

Deploys multi-region DocumentDB with Istio networking:
- Creates namespace with istio-injection label
- Deploys DocumentDB with crossCloudNetworkingStrategy: Istio
- Configures primary and replicas across all regions

```bash
# With auto-generated password
./deploy-documentdb.sh

# With custom password
./deploy-documentdb.sh "MySecurePassword123!"
```

## Configuration

### Default Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `RESOURCE_GROUP` | `documentdb-k3s-fleet-rg` | Azure resource group |
| `HUB_REGION` | `westus3` | KubeFleet hub region (AKS) |
| `K3S_REGIONS` | `eastus2,uksouth` | k3s VM regions |
| `VM_SIZE` | `Standard_D2s_v3` | Azure VM size for k3s |
| `AKS_VM_SIZE` | `Standard_DS2_v2` | AKS node VM size |
| `K3S_VERSION` | `v1.30.4+k3s1` | k3s version |
| `ISTIO_VERSION` | `1.24.0` | Istio version |

### Network Configuration (Istio)

Each cluster has its own isolated VNet - Istio east-west gateways handle all cross-cluster traffic:

| Cluster | Region | Network ID | VNet CIDR |
|---------|--------|------------|-----------|
| hub-westus3 (AKS) | westus3 | network1 | 10.1.0.0/16 |
| k3s-eastus2 | eastus2 | network2 | 10.2.0.0/16 |
| k3s-uksouth | uksouth | network3 | 10.3.0.0/16 |

## kubectl Aliases

After deployment, these aliases are configured in `~/.bashrc`:

```bash
source ~/.bashrc

# AKS hub cluster
k-westus3 get nodes
k-hub get nodes

# k3s clusters
k-eastus2 get nodes
k-uksouth get nodes
```

## Istio Management

```bash
# Check Istio installation on each cluster
for ctx in hub-westus3 k3s-eastus2 k3s-uksouth; do
  echo "=== $ctx ==="
  kubectl --context $ctx get pods -n istio-system
done

# Check east-west gateway services
k-hub get svc -n istio-system istio-eastwestgateway

# Verify remote secrets (for service discovery)
k-hub get secrets -n istio-system -l istio/multiCluster=true
```

## Fleet Management

```bash
# List all member clusters
k-hub get membercluster

# Check ClusterResourcePlacement status
k-hub get clusterresourceplacement

# View fleet hub agent logs
k-hub logs -n fleet-system-hub -l app=hub-agent

# Check member agent on k3s cluster
k-uksouth logs -n fleet-system -l app=member-agent
```

## DocumentDB Management

### Check Status

```bash
# Check operator on all clusters
for ctx in hub-westus3 k3s-eastus2 k3s-uksouth; do
  echo "=== $ctx ==="
  kubectl --context $ctx get pods -n documentdb-operator
done

# Check DocumentDB instances
for ctx in hub-westus3 k3s-eastus2 k3s-uksouth; do
  echo "=== $ctx ==="
  kubectl --context $ctx get documentdb -n documentdb-preview-ns
done
```

### Connect to Database

```bash
# Port forward to primary
k-westus3 port-forward -n documentdb-preview-ns svc/documentdb-preview 10260:10260

# Connection string
mongodb://default_user:<password>@localhost:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true
```

### Failover

```bash
# Failover to k3s cluster in UK South
k-hub patch documentdb documentdb-preview -n documentdb-preview-ns \
  --type='merge' -p '{"spec":{"clusterReplication":{"primary":"k3s-uksouth"}}}'
```

## Use Cases

### Edge Computing
k3s on Azure VMs simulates edge locations where full AKS might be too heavy. DocumentDB replication ensures data availability at the edge while maintaining consistency with central clusters.

### Hybrid Cloud
Mix AKS managed clusters with self-managed k3s for:
- Cost optimization (k3s on cheaper VMs)
- Specific compliance requirements
- Testing/development environments

### Disaster Recovery
Multi-region deployment with automatic failover capabilities:
- Primary in AKS (production-grade)
- Replicas in k3s (cost-effective DR)

## Troubleshooting

### k3s VM Issues

```bash
# Check k3s status via Run Command (no SSH needed)
az vm run-command invoke \
  --resource-group $RESOURCE_GROUP \
  --name k3s-uksouth \
  --command-id RunShellScript \
  --scripts "sudo systemctl status k3s; sudo k3s kubectl get nodes"

# View k3s logs via Run Command
az vm run-command invoke \
  --resource-group $RESOURCE_GROUP \
  --name k3s-uksouth \
  --command-id RunShellScript \
  --scripts "sudo journalctl -u k3s --no-pager -n 50"
```

### Istio Issues

```bash
# Check Istio pods
k-uksouth get pods -n istio-system

# Check east-west gateway external IP
k-uksouth get svc -n istio-system istio-eastwestgateway

# Verify remote secrets exist
k-hub get secrets -n istio-system -l istio/multiCluster=true

# Check Istio proxy status in DocumentDB namespace
k-uksouth get pods -n documentdb-preview-ns -o jsonpath='{.items[*].spec.containers[*].name}' | tr ' ' '\n' | grep istio
```

### Fleet Member Not Joining

```bash
# Check member agent logs on k3s
k-uksouth logs -n fleet-system deployment/member-agent

# Verify hub API server is reachable (via Istio)
k-uksouth run test --rm -it --image=curlimages/curl -- curl -k https://hub-westus3-api:443/healthz
```

### DocumentDB Not Propagating

```bash
# Check ClusterResourcePlacement
k-hub describe clusterresourceplacement documentdb-namespace-crp

# Verify namespace exists on member
k-uksouth get namespace documentdb-preview-ns
```

### Cross-Cluster Connectivity (Istio)

```bash
# Test Istio mesh connectivity
kubectl --context k3s-uksouth run test --rm -it --image=nicolaka/netshoot -- \
  curl -k https://documentdb-preview.documentdb-preview-ns.svc:10260/health

# Check Istio eastwest gateway is exposed
k-uksouth get svc -n istio-system istio-eastwestgateway -o wide
```

## Cleanup

```bash
# Delete everything
./delete-resources.sh

# Force delete without confirmation
./delete-resources.sh --force

# Delete specific resources only
./delete-resources.sh --vms-only  # Only k3s VMs
./delete-resources.sh --aks-only  # Only AKS clusters
```

## Cost Estimates

| Resource | Configuration | Estimated Monthly Cost |
|----------|---------------|----------------------|
| AKS Hub (westus3) | 2x Standard_DS2_v2 | ~$140 |
| k3s VM (eastus2) | 1x Standard_D2s_v3 | ~$70 |
| k3s VM (uksouth) | 1x Standard_D2s_v3 | ~$70 |
| Storage (3x 10GB) | Premium SSD | ~$6 |
| Load Balancers | 3x Standard (Istio) | ~$54 |
| **Total** | | **~$340/month** |

> **Tip**: Use `./delete-resources.sh` when not in use to avoid charges.

## Files Reference

| File | Description |
|------|-------------|
| `main.bicep` | Bicep template for Azure infrastructure |
| `parameters.bicepparam` | Bicep parameters file |
| `deploy-infrastructure.sh` | Deploy VMs, VNets, AKS cluster |
| `install-istio.sh` | Install Istio service mesh |
| `setup-fleet.sh` | Configure KubeFleet hub and members |
| `install-cert-manager.sh` | Install cert-manager |
| `install-documentdb-operator.sh` | Deploy DocumentDB operator |
| `deploy-documentdb.sh` | Deploy multi-region DocumentDB |
| `delete-resources.sh` | Cleanup all resources |
| `test-connection.sh` | Test DocumentDB connectivity |
| `documentdb-operator-crp.yaml` | Operator CRP (reference only — not applied) |
| `cert-manager-crp.yaml` | cert-manager CRP (for future cluster propagation) |
| `documentdb-resource-crp.yaml` | DocumentDB ClusterResourcePlacement |

## Known Issues & Lessons Learned

### Azure VM Run Command
This playground uses Azure VM Run Command instead of SSH for all VM operations:
- **Benefits**: Works through corporate firewalls, no SSH keys to manage, no port 22 required
- **Limitations**: ~30-60 seconds per invocation, output format requires parsing
- **Output parsing**: Results come as `[stdout]\n...\n[stderr]\n...` — extract with:
  ```bash
  az vm run-command invoke ... --query 'value[0].message' -o tsv | \
    awk '/^\[stdout\]/{flag=1; next} /^\[stderr\]/{flag=0} flag'
  ```

### k3s TLS SANs and API Server (Critical)
- k3s generates certificates with `127.0.0.1` only — external access requires adding the public IP as a TLS SAN
- The cloud-init uses Azure Instance Metadata Service (IMDS) to get the public IP before k3s install:
  ```bash
  curl -s -H Metadata:true "http://169.254.169.254/metadata/instance/network/interface/0/ipv4/ipAddress/0/publicIpAddress?api-version=2021-02-01&format=text"
  ```
- **`advertise-address`**: Must be set to the private IP, otherwise `kubernetes` endpoint uses the public IP which breaks internal pod→API server connectivity via ClusterIP (10.43.0.1)
- **`node-external-ip`**: Set to public IP so LoadBalancer services get the public IP

### k3s kubeconfig Management
- k3s generates kubeconfig with `127.0.0.1` — scripts automatically update to public IP
- When redeploying, old kubeconfigs have stale IPs/certs — scripts delete old contexts first
- Use `kubectl config delete-context <name>` to clean up manually if needed

### Istio on k3s
- **Use Helm**, not `istioctl install`, for k3s clusters — `istioctl` creates resources without Helm annotations, causing ownership conflicts if you later use Helm
- k3s uses `servicelb` (klipper) for LoadBalancer services which assigns node IPs, not public IPs
- Patch east-west gateway services with `externalIPs` pointing to the node's public IP:
  ```bash
  kubectl patch svc istio-eastwestgateway -n istio-system \
    --type='json' -p='[{"op": "add", "path": "/spec/externalIPs", "value": ["<PUBLIC_IP>"]}]'
  ```
- Set `pilot.autoscaleEnabled=false` and `pilot.replicaCount=1` for single-node k3s clusters

### DocumentDB on k3s
- The `environment` field only supports `aks`, `eks`, `gke` — **use `aks` for k3s clusters**
- DocumentDB operator is installed on k3s via Run Command (base64-encoded manifests + CNPG upstream release)
- CNPG must be installed separately on k3s since the Helm chart can't be transferred easily

### cert-manager on k3s
- Set `startupapicheck.enabled=false` to avoid timeouts on resource-constrained k3s
- Apply CRDs explicitly with `kubectl apply -f` before Helm install (the `crds.enabled=true` flag can silently fail)

### Corporate Network (NRMS)
- Azure NRMS policies auto-add deny rules at priority 105-109 on NSGs
- Port 22 is denied by NRMS-Rule-106; to enable SSH, add allow rule at priority 100
- Port 6443 is not in NRMS deny lists but corporate VPN/firewall may block it
- NSG minimum priority is 100 (cannot go lower)

### Bicep Deployment Tips
- Use `resourceId()` function for subnet references to avoid race conditions
- Add explicit `dependsOn` for AKS clusters referencing VNets
- Check AKS supported Kubernetes versions: `az aks get-versions --location <region>`
- Azure VMs require SSH key even when not using SSH; changing key on existing VM causes "PropertyChangeNotAllowed" error

## Related Playgrounds

- [aks-fleet-deployment](../aks-fleet-deployment/) - Pure AKS multi-region with KubeFleet
- [aks-setup](../aks-setup/) - Single AKS cluster setup
- [multi-cloud-deployment](../multi-cloud-deployment/) - Cross-cloud (AKS + GKE + EKS) with Istio

## Additional Resources

- [k3s Documentation](https://docs.k3s.io/)
- [KubeFleet Documentation](https://kubefleet.dev/docs/)
- [Istio Multi-Cluster](https://istio.io/latest/docs/setup/install/multicluster/)
- [Azure VMs Documentation](https://docs.microsoft.com/en-us/azure/virtual-machines/)
- [DocumentDB Kubernetes Operator](../../README.md)
