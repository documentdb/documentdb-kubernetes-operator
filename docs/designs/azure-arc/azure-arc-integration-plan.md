# Azure Arc Integration Plan for DocumentDB Kubernetes Operator

## Overview

This document outlines a two-phase plan to integrate DocumentDB Kubernetes Operator with Azure Kubernetes extensions for deployment tracking and billing across **all** Kubernetes environments.

**Goal:** Enable customers to install DocumentDB on any Kubernetes cluster (AKS, EKS, GKE, on-premises) while providing visibility in Azure Portal and usage-based billing.

**Key Insight:** Azure knows a cluster exists, but NOT what's installed on it. Kubernetes extensions solve this tracking gap.

> **Note:** "Kubernetes extensions" (via `az k8s-extension`) are specific to Kubernetes clusters. Azure has separate extension mechanisms for VMs, Arc servers, etc.

> **Interim Solution:** For immediate portal visibility without extension registration or billing integration, see [arc-hybrid-setup-with-fleet](../../../documentdb-playground/arc-hybrid-setup-with-fleet/) which uses Azure Fleet Manager + direct Helm deployment. This provides cluster tracking today while the full integration is being developed.

### Cluster Type Support

| Cluster Type | Extension Cluster Type | Arc Agent Required? |
|--------------|----------------------|--------------------|
| **AKS** (Azure) | `managedClusters` | ❌ No - AKS native |
| **EKS** (AWS) | `connectedClusters` | ✅ Yes |
| **GKE** (GCP) | `connectedClusters` | ✅ Yes |
| **On-premises** | `connectedClusters` | ✅ Yes |

**Same Kubernetes extension type (`Microsoft.DocumentDB.Operator`) works for all cluster types.**

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                   Azure Resource Manager                         │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  Extension Inventory (tracks ALL installations)           │  │
│  │  └── Microsoft.DocumentDB.Operator instances              │  │
│  │      ├── AKS clusters (managedClusters)                   │  │
│  │      └── Arc clusters (connectedClusters)                 │  │
│  ├── Billing Service (Phase 2)                               │  │
│  └── Azure Portal visibility                                 │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                    ▲                           ▲
                    │                           │
         ┌──────────┴──────────┐    ┌──────────┴──────────┐
         │        AKS          │    │  Arc-enabled        │
         │  (managedClusters)  │    │  (connectedClusters)│
         │                     │    │                     │
         │  No Arc agent       │    │  Arc agent required │
         │  Extension: ✅      │    │  Extension: ✅      │
         │                     │    │  (EKS, GKE, on-prem)│
         └─────────────────────┘    └─────────────────────┘
```

---

## Deployment Options

Two approaches are available for deploying DocumentDB via Azure:

### Option A: Kubernetes Extension (Full Registration)

Deploy as an official Azure Kubernetes extension type via `az k8s-extension`. **Works for both AKS and Arc-enabled clusters.**

```bash
# For AKS clusters (no Arc agent needed)
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-aks-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters    # <-- AKS native

# For Arc-enabled clusters (EKS, GKE, on-prem)
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-arc-cluster \
  --resource-group my-rg \
  --cluster-type connectedClusters  # <-- Arc-enabled
```

> **Note:** Same extension type, same billing, unified tracking across all cluster types.

### Option B: Flux GitOps (No Registration)

Deploy via Flux GitOps configuration. **No Azure extension registration required. Works for both AKS and Arc-enabled clusters.**

```bash
# For AKS clusters
az k8s-configuration flux create \
  --name documentdb-operator \
  --cluster-name my-aks-cluster \
  --resource-group my-rg \
  --cluster-type managedClusters \
  --namespace documentdb-operator \
  --scope cluster \
  --source-kind HelmRepository \
  --helm-repo-url "oci://ghcr.io/documentdb" \
  --helm-chart documentdb-operator \
  --helm-chart-version 0.1.3

# For Arc-enabled clusters (EKS, GKE, on-prem)
az k8s-configuration flux create \
  --name documentdb-operator \
  --cluster-name my-arc-cluster \
  --resource-group my-rg \
  --cluster-type connectedClusters \
  --namespace documentdb-operator \
  --scope cluster \
  --source-kind HelmRepository \
  --helm-repo-url "oci://ghcr.io/documentdb" \
  --helm-chart documentdb-operator \
  --helm-chart-version 0.1.3
```

> **Note:** Flux is an open-source CNCF GitOps project (not Microsoft-owned). Azure integrates Flux natively for GitOps deployments.

### Comparison

| Feature | Option A: K8s Extension | Option B: Flux GitOps |
|---------|------------------------|----------------------|
| **AKS support** | ✅ Yes (managedClusters) | ✅ Yes (managedClusters) |
| **Arc cluster support** | ✅ Yes (connectedClusters) | ✅ Yes (connectedClusters) |
| **Azure registration required** | ✅ Yes (approval process) | ❌ No |
| **Time to deploy** | 3-4 weeks (registration wait) | Same day |
| **Cluster visible in Portal** | ✅ Yes | ✅ Yes |
| **Deploy via Azure CLI** | ✅ Yes | ✅ Yes |
| **Shows as "Extension" in Portal** | ✅ Yes | ❌ Shows as Flux config |
| **Azure Marketplace listing** | ✅ Yes | ❌ No |
| **Built-in Arc metering** | ✅ Yes | ❌ No (custom required) |
| **Health monitoring in Portal** | ✅ Automatic | ❌ Manual setup |
| **Upgrade via Azure CLI** | ✅ `az k8s-extension update` | ✅ `az k8s-configuration flux update` |
| **Enterprise support** | ✅ Microsoft support | ⚠️ Community + custom |

### Pros & Cons

#### Option A: Kubernetes Extension

**Pros:**
- Official Azure Marketplace presence
- Built-in health monitoring and status reporting
- Native Azure metering for billing (Phase 2)
- Enterprise support from Microsoft
- Consistent experience with other K8s extensions (Defender, Policy, etc.)

**Cons:**
- Requires Azure extension type registration (approval process)
- 2-4 week wait for registration approval
- More complex initial setup

#### Option B: Flux GitOps

**Pros:**
- No Azure registration/approval required
- Can deploy immediately
- Uses open-source CNCF standard (Flux)
- Full GitOps workflow with version control
- Works with any Git provider (GitHub, GitLab, Azure Repos)

**Cons:**
- No Azure Marketplace listing
- Must implement custom metering for billing
- Shows as "Flux configuration" not "Extension" in Portal
- Less integrated Azure experience

### Recommendation

| Scenario | Recommended Option |
|----------|-------------------|
| Need to deploy immediately | **Option B: Flux GitOps** |
| Want Azure Marketplace presence | **Option A: K8s Extension** |
| Require built-in Azure billing | **Option A: K8s Extension** |
| Already using GitOps workflow | **Option B: Flux GitOps** |
| Enterprise customers expecting official extension | **Option A: K8s Extension** |

---

## Phase 1: K8s Extension + ARM Visibility

**Duration:** 3-4 weeks  
**Goal:** Install DocumentDB via `az k8s-extension`, view in Azure Portal

> **Note:** This phase covers Option A (K8s Extension). For Option B (Flux GitOps), skip to the [Flux GitOps Setup](#flux-gitops-setup-option-b) section.

### What Gets Deployed

| Component | Deployed By | Location |
|-----------|-------------|----------|
| Azure Arc Agent | Customer (one-time, non-AKS only) | `azure-arc` namespace |
| DocumentDB Operator | K8s Extension Manager | `documentdb-operator` namespace |
| CloudNative-PG Operator | Helm dependency | `cnpg-operator` namespace |

### Task Breakdown

#### Task 1.1: Create Extension Manifest (Week 1)

Create `extension.yaml` that tells Arc how to deploy the operator.

**Files to create:**
```
operator/
└── arc-extension/
    ├── extension.yaml       # K8s extension manifest
    ├── values-arc.yaml      # Arc-specific Helm overrides
    └── README.md            # Installation guide
```

**Deliverable:** Working extension manifest pointing to ghcr.io

---

#### Task 1.2: Set Up Local Test Environment (Week 1-2)

Set up Kind cluster with Arc agent for local testing.

**Steps:**
```bash
# Create Kind cluster
kind create cluster --name arc-test

# Install Arc agent (requires Azure subscription)
az connectedk8s connect --name arc-test --resource-group dev-rg

# Verify Arc agent running
kubectl get pods -n azure-arc
```

**Deliverable:** Reproducible local test environment

---

#### Task 1.3: Test Extension Install Locally (Week 2)

Test extension deployment before Azure registration.

**Steps:**
```bash
# Manually deploy extension (simulates Arc behavior)
helm install documentdb-operator \
  oci://ghcr.io/documentdb/documentdb-operator \
  --version 0.1.3 \
  --namespace documentdb-operator \
  --create-namespace

# Verify deployment
kubectl get pods -n documentdb-operator
kubectl get deployment documentdb-operator -n documentdb-operator
```

**Deliverable:** Confirmed Helm chart works via Arc-connected cluster

---

#### Task 1.4: Register Extension Type with Azure (Week 3)

Register `Microsoft.DocumentDB.Operator` as valid K8s extension type.

**Steps:**
1. **Contact Azure Arc team** via one of:
   - Internal: [Extension Registration (eng.ms)](https://eng.ms/docs/cloud-ai-platform/azure-edge-platform-aep/aep-arc-for-kubernetes/arc-for-k8s-developer-docs/extension-registration)
   - External: [Arc K8s Extensions Feedback](https://aka.ms/arc-k8s-extensions-feedback)
2. **Provide extension manifest** including:
   - Extension type name: `Microsoft.DocumentDB.Operator`
   - Helm chart OCI URL: `oci://ghcr.io/documentdb/documentdb-operator`
   - Chart version(s): `0.1.3`
   - Configuration settings schema
   - Health check definitions
3. **Configure release trains** (preview, stable)
4. **Test in staging environment** (Azure provides test tenant)
5. **Promote to production** after validation

> **Note:** There's no self-service portal for extension registration. This requires manual coordination with the Azure Arc team.

**Deliverable:** Registered extension type in Azure

---

#### Task 1.5: E2E Testing & Documentation (Week 3-4)

Full end-to-end testing of customer experience + documentation.

**Test scenarios:**
```bash
# Install extension via CLI
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name test-cluster \
  --resource-group test-rg \
  --cluster-type connectedClusters

# Verify in Azure Portal
az k8s-extension show --name documentdb-operator ...

# Test upgrade
az k8s-extension update --version 0.1.4 ...

# Test uninstall
az k8s-extension delete --name documentdb-operator ...
```

**Deliverables:**
- All test scenarios passing
- Installation guide in README.md
- Troubleshooting guide

---

### Customer Experience

```bash
# 1. Connect cluster to Azure Arc (one-time per cluster)
az connectedk8s connect --name my-cluster --resource-group my-rg

# 2. Install DocumentDB extension
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --cluster-name my-cluster \
  --resource-group my-rg \
  --cluster-type connectedClusters

# 3. Verify in Azure Portal or CLI
az k8s-extension show --name documentdb-operator \
  --cluster-name my-cluster --resource-group my-rg \
  --cluster-type connectedClusters
```

### Chart Source: Use Existing ghcr.io Registry

**No repackaging required.** Arc can pull directly from the existing OCI registry.

| Source Option | Supported | Recommendation |
|---------------|-----------|----------------|
| OCI Registry (ghcr.io) | ✅ | **Use this** - already have it |
| Azure Container Registry | ✅ | Alternative if needed |
| Public Helm repo | ✅ | Alternative option |

Existing chart location:
```
oci://ghcr.io/documentdb/documentdb-operator:0.1.1
```

### Extension Manifest Example

```yaml
# operator/arc-extension/extension.yaml
extensionType: Microsoft.DocumentDB.Operator
version: 0.1.3

helm:
  # Point directly to existing ghcr.io chart (no repackaging)
  registryUrl: oci://ghcr.io/documentdb
  chartName: documentdb-operator
  chartVersion: 0.1.3
  
  releaseName: documentdb-operator
  releaseNamespace: documentdb-operator

# User-configurable settings via az k8s-extension create
configurationSettings:
  - name: documentDbVersion
    description: "DocumentDB version"
    defaultValue: "0.1.3"

# Health monitoring
healthChecks:
  - kind: Deployment
    name: documentdb-operator
    namespace: documentdb-operator
```

### Registry Authentication

If ghcr.io requires authentication:

```bash
# Option 1: Make chart public (simplest)
# Option 2: Configure Arc with registry credentials
az k8s-extension create \
  --name documentdb-operator \
  --extension-type Microsoft.DocumentDB.Operator \
  --configuration-protected-settings "registry.username=xxx" \
  --configuration-protected-settings "registry.password=xxx" \
  ...
```

### Testing Checklist

- [ ] Extension installs via `az k8s-extension create`
- [ ] Operators running in cluster
- [ ] Extension visible in Azure Portal
- [ ] Health status reported correctly
- [ ] Upgrade and uninstall work

---

## Phase 2: Metering & Billing

**Duration:** 3-5 weeks (after Phase 1)  
**Goal:** Track usage and generate customer invoices

### Billing Metrics

| Meter ID | Description | Unit |
|----------|-------------|------|
| `documentdb-instance-hours` | Running DocumentDB instances | instance-hour |
| `documentdb-storage-gb` | Provisioned storage | GB-month |
| `documentdb-vcpu-hours` | Allocated vCPUs | vCPU-hour |

### Task Breakdown

#### Task 2.1: Define Billing Model with Azure Commerce (Week 1-2)

Work with Azure Commerce team to register meter IDs.

**Steps:**
1. Define pricing tiers and SKUs
2. Register meter IDs with Azure Commerce
3. Set up billing sandbox for testing
4. Define billing frequency (hourly, daily)

**Deliverable:** Registered meter IDs, billing sandbox access

---

#### Task 2.2: Implement Usage Collection (Week 2-3)

Add metering code to operator to collect usage data.

**Files to create:**
```
operator/src/internal/
└── metering/
    ├── reporter.go      # Usage collection & reporting
    ├── reporter_test.go # Unit tests
    ├── metrics.go       # Meter definitions
    └── types.go         # Data structures
```

**Sample code:**
```go
// internal/metering/reporter.go
type UsageRecord struct {
    Timestamp      time.Time
    InstanceCount  int
    TotalStorageGB float64
    TotalVCPUs     int
    ClusterID      string
}

func (r *Reporter) CollectUsage(ctx context.Context) (*UsageRecord, error) {
    var docdbList documentdbv1.DocumentDBList
    if err := r.client.List(ctx, &docdbList); err != nil {
        return nil, err
    }
    
    record := &UsageRecord{Timestamp: time.Now().UTC()}
    for _, db := range docdbList.Items {
        record.InstanceCount++
        record.TotalStorageGB += parseStorageGB(db.Spec.Resource.Storage.PvcSize)
        record.TotalVCPUs += db.Spec.NodeCount * db.Spec.InstancesPerNode
    }
    return record, nil
}
```

**Deliverable:** Working usage collection with unit tests

---

#### Task 2.3: Integrate Arc Metering SDK (Week 3-4)

Send collected usage to Azure via Arc metering API.

**Files to create:**
```
operator/src/internal/
└── metering/
    └── arc_client.go    # Arc metering API client
```

**Sample code:**
```go
// internal/metering/arc_client.go
func (c *ArcClient) SubmitUsage(ctx context.Context, records []MeterRecord) error {
    // POST to Arc metering endpoint
    // https://management.azure.com/.../extensions/.../usage
    return c.httpClient.Post(ctx, c.meteringEndpoint, records)
}
```

**Deliverable:** Usage data flowing to Azure

---

#### Task 2.4: Add Metering to Controller (Week 4)

Integrate metering reporter into main reconciliation loop.

**Modify:** `operator/src/internal/controller/documentdb_controller.go`

```go
func (r *DocumentDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Existing reconciliation logic...
    
    // Report usage (runs periodically, not on every reconcile)
    if r.metering != nil && r.shouldReportUsage() {
        if err := r.metering.ReportUsage(ctx); err != nil {
            log.Error(err, "failed to report usage")
            // Don't fail reconcile for metering errors
        }
    }
    
    return ctrl.Result{}, nil
}
```

**Deliverable:** Operator reports usage automatically

---

#### Task 2.5: E2E Billing Validation (Week 5)

Test full billing flow in Azure sandbox.

**Test scenarios:**
1. Deploy DocumentDB instance → verify usage recorded
2. Scale up instances → verify usage increases
3. Delete instance → verify usage stops
4. Check Azure invoice → verify charges appear

**Deliverable:** Validated billing accuracy

---

#### Task 2.6: Production Rollout (Week 5)

Release metering feature to production.

**Steps:**
1. Feature flag for metering (opt-in initially)
2. Update Helm chart with metering config
3. Update extension manifest
4. Release new version via Arc
5. Monitor billing data flow

**Deliverable:** GA release with billing

---

## Flux GitOps Setup (Option B)

Alternative deployment using Flux GitOps. **No Azure extension registration required.**

### Duration: 1-2 days

### Prerequisites

- Azure subscription
- Kubernetes cluster (v1.26+)
- Azure CLI with `connectedk8s` extension

### Setup Steps

#### Step 1: Connect Cluster to Azure Arc

```bash
# Login to Azure
az login
az account set --subscription <subscription-id>

# Create resource group
az group create --name my-arc-rg --location eastus

# Connect cluster to Arc
az connectedk8s connect --name my-cluster --resource-group my-arc-rg

# Verify connection
kubectl get pods -n azure-arc
```

#### Step 2: Deploy via Flux GitOps

```bash
# Create Flux configuration for DocumentDB operator
az k8s-configuration flux create \
  --name documentdb-operator \
  --cluster-name my-cluster \
  --resource-group my-arc-rg \
  --cluster-type connectedClusters \
  --namespace documentdb-operator \
  --scope cluster \
  --source-kind HelmRepository \
  --source-url "https://ghcr.io/documentdb" \
  --helm-chart documentdb-operator \
  --helm-chart-version 0.1.3 \
  --helm-release-name documentdb-operator \
  --helm-release-namespace documentdb-operator

# Verify deployment
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
```

#### Step 3: Verify in Azure Portal

1. Navigate to **Azure Arc** > **Kubernetes clusters**
2. Select your cluster
3. Go to **GitOps** > **Configurations**
4. Verify `documentdb-operator` configuration status

### Upgrade via Flux

```bash
az k8s-configuration flux update \
  --name documentdb-operator \
  --cluster-name my-cluster \
  --resource-group my-arc-rg \
  --cluster-type connectedClusters \
  --helm-chart-version 0.1.4
```

### Uninstall

```bash
az k8s-configuration flux delete \
  --name documentdb-operator \
  --cluster-name my-cluster \
  --resource-group my-arc-rg \
  --cluster-type connectedClusters \
  --yes
```

### Billing with Flux (Custom Metering)

Since Flux doesn't support built-in Arc metering, implement custom metering:

1. **Option 1:** Push metrics to Azure Monitor
   - Operator collects usage → sends to Azure Monitor workspace
   - Build billing reports from Azure Monitor data

2. **Option 2:** Push metrics to custom backend
   - Operator sends usage to your billing service
   - Full control over billing logic

```go
// Example: Push to Azure Monitor instead of Arc metering
func (r *Reporter) ReportToAzureMonitor(ctx context.Context, record *UsageRecord) error {
    // POST to Azure Monitor ingestion endpoint
    return r.azureMonitorClient.IngestMetrics(ctx, []Metric{
        {Name: "documentdb_instance_count", Value: float64(record.InstanceCount)},
        {Name: "documentdb_storage_gb", Value: record.TotalStorageGB},
    })
}
```

---

## Prerequisites

**For AKS clusters:**
- Azure subscription
- AKS cluster (v1.26+)
- Azure CLI with `aks` extension

**For non-AKS clusters (EKS, GKE, on-prem):**
- Azure subscription
- Kubernetes cluster (v1.26+)
- Azure CLI with `connectedk8s` extension
- Arc agent installed on cluster (`az connectedk8s connect`)

**Additional for Option A (K8s Extension):**
- Extension type registration (requires Microsoft approval)
- Azure Commerce onboarding (Phase 2 billing)

**Additional for Option B (Flux GitOps):**
- None - can start immediately

### Querying All DocumentDB Installations

```bash
# Query all DocumentDB extensions across ALL clusters (AKS + Arc)
az graph query -q "
  resources
  | where type == 'microsoft.kubernetesconfiguration/extensions'
  | where properties.extensionType == 'Microsoft.DocumentDB.Operator'
  | extend clusterType = case(
      id contains 'managedClusters', 'AKS',
      id contains 'connectedClusters', 'Arc',
      'Unknown')
  | project subscriptionId, resourceGroup, clusterName=split(id,'/')[8], clusterType, version=properties.version
"
```

---

## References

### Extension Development & Registration

- [Cluster Extensions Conceptual Overview](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/conceptual-extensions) - How extensions work
- [Extensions Release & Publishing](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/extensions-release) - Partner extension guide
- [Extension Type Registration (Internal)](https://eng.ms/docs/cloud-ai-platform/azure-edge-platform-aep/aep-arc-for-kubernetes/arc-for-k8s-developer-docs/extension-registration) - Microsoft internal docs (requires corp access)
- [Arc K8s Extensions Feedback](https://aka.ms/arc-k8s-extensions-feedback) - Request new extension registration

### General Documentation

- [AKS Cluster Extensions](https://learn.microsoft.com/en-us/azure/aks/cluster-extensions)
- [Azure Arc-enabled Kubernetes](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/)
- [Create Arc Extensions](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/extensions)
- [Arc Metering](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/conceptual-usage-metering)
- [Flux GitOps with Azure Arc](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/tutorial-use-gitops-flux2)
- [Flux Documentation](https://fluxcd.io/flux/) (CNCF project)
- [Azure Kubernetes Fleet Manager](https://learn.microsoft.com/en-us/azure/kubernetes-fleet/)
- [Azure Resource Graph](https://learn.microsoft.com/en-us/azure/governance/resource-graph/)

### Example Extensions (Reference Implementations)

| Extension | Type | Docs |
|-----------|------|------|
| Azure Monitor | `Microsoft.AzureMonitor.Containers` | [Container Insights](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/container-insights-enable-arc-enabled-clusters) |
| Azure Defender | `Microsoft.AzureDefender.Kubernetes` | [Defender for Containers](https://learn.microsoft.com/en-us/azure/defender-for-cloud/defender-for-kubernetes-azure-arc) |
| Azure ML | `Microsoft.AzureML.Kubernetes` | [ML on Arc](https://learn.microsoft.com/en-us/azure/machine-learning/how-to-attach-kubernetes-anywhere) |
| Flux | `Microsoft.Flux` | [GitOps with Flux](https://learn.microsoft.com/en-us/azure/azure-arc/kubernetes/conceptual-gitops-flux2) |
| Dapr | `Microsoft.Dapr` | [Dapr extension](https://learn.microsoft.com/en-us/azure/aks/dapr) |

---

## Appendix A: Azure Fleet Manager and Arc

### Overview

Azure Kubernetes Fleet Manager is a multi-cluster orchestration service that can manage both AKS and non-Azure Kubernetes clusters. This section clarifies how Fleet relates to Arc and when each should be used.

### How Fleet Uses Arc

```
┌────────────────────────────────────────────────────────────────────┐
│                         Azure Control Plane                         │
├────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Fleet Hub ◄──────── needs a way to talk to clusters ────────►    │
│                                                                     │
└────────────────────────────────────────────────────────────────────┘
                    │                              │
                    ▼                              ▼
         ┌──────────────────┐           ┌──────────────────┐
         │      AKS         │           │   EKS / GKE /    │
         │                  │           │   On-prem        │
         │  Already in Azure│           │                  │
         │  (native API)    │           │  NOT in Azure    │
         │                  │           │  (no API access) │
         └──────────────────┘           └──────────────────┘
                    │                              │
            Fleet talks via              Fleet needs Arc
            Azure Resource Manager       as a "bridge"
                    │                              │
                    ▼                              ▼
              No Arc needed              Arc agent required
```

**Key insight:** Arc is Azure's universal "reach into any K8s cluster" mechanism. Fleet reuses Arc's secure tunnel rather than implementing its own connectivity solution.

### Arc Agent Capabilities

```
Arc Agent on cluster:
├── Secure tunnel to Azure (outbound HTTPS only)
├── Identity (managed identity for the cluster)
├── Extension framework (install add-ons)
└── Configuration sync (GitOps, policies)

Fleet uses:
└── Just the secure tunnel (to push Work objects)
```

### When Arc Agent is Required

| Cluster Type | Arc Agent Needed? | Reason |
|--------------|-------------------|--------|
| **AKS** (Azure) | ❌ No | AKS is Azure-native, direct API access |
| **EKS** (AWS) | ✅ Yes | Arc provides Azure connectivity |
| **GKE** (GCP) | ✅ Yes | Arc provides Azure connectivity |
| **On-premises** | ✅ Yes | Arc provides Azure connectivity |

### Fleet vs K8s Extension: Different Use Cases

| Aspect | K8s Extension | Fleet Manager |
|--------|---------------|---------------|
| **Primary purpose** | Per-cluster app lifecycle | Multi-cluster orchestration |
| **Installation trigger** | `az k8s-extension create` | ClusterResourcePlacement (CRP) |
| **Tracking mechanism** | ARM extension inventory | CRP status in Fleet hub |
| **Best for** | Individual cluster billing | Enterprise multi-cluster management |
| **Per-app Azure registration** | ✅ Required | ❌ Not required |
| **Built-in metering** | ✅ Yes | ❌ Custom required |

### Fleet's Work Object

When Fleet deploys workloads, it creates "Work" objects on member clusters:

```yaml
apiVersion: placement.kubernetes-fleet.io/v1
kind: Work
metadata:
  name: documentdb-operator-work
  namespace: fleet-member-<cluster-id>
spec:
  workload:
    manifests:
      - # Helm release or raw manifests pushed by Fleet
```

### Querying Installations via Fleet

```bash
# List all Fleet member clusters
az fleet member list --fleet-name my-fleet -o table

# Check DocumentDB deployment status across all clusters
kubectl get clusterresourceplacement documentdb-operator \
  -o jsonpath='{.status.placementStatuses[*].clusterName}'
```

### Recommendation Matrix

| Customer Profile | Recommended Approach |
|------------------|---------------------|
| Single cluster | K8s Extension (Option A or B) |
| Multiple AKS clusters | Fleet Manager (no Arc agents needed) |
| Multi-cloud (AKS + EKS/GKE) | Fleet + Arc agents on non-AKS clusters |
| Needs Azure Marketplace billing | K8s Extension (Option A) |
| Enterprise with existing Fleet | Fleet CRPs for deployment, custom metering for billing |

### Existing Fleet Implementation

This repository includes Fleet deployment examples in `documentdb-playground/aks-fleet-deployment/`:

- `documentdb-operator-crp.yaml` - ClusterResourcePlacement for operator
- `cert-manager-crp.yaml` - ClusterResourcePlacement for cert-manager
- `deploy-multi-region.sh` - Multi-region deployment script

These examples demonstrate AKS-only Fleet deployment (no Arc agents required).
