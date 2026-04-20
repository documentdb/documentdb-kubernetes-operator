# Long Haul Test Design — DocumentDB Kubernetes Operator

**Issue:** [#220](https://github.com/documentdb/documentdb-kubernetes-operator/issues/220)  
**Status:** Design phase

## Problem Statement

The operator lacks continuous, long-running test coverage. Issue #220 requires:
1. Constant writes/reads — ensure no data is lost
2. Constant management operations (add/remove region, HA toggle, scale, backup/restore)
3. Operator and cluster updates under load

## Why Long Haul Testing?

Problems that only surface over extended continuous operation:
- **Memory/resource leaks** — need hours of reconciliation loops to see growth trends
- **WAL accumulation / disk fill** — cleanup bugs take time to manifest
- **Connection pool exhaustion** — gradual leak over many connect/disconnect cycles
- **Reconciliation drift** — operator state slowly diverges after many operations
- **Certificate rotation** — certs don't expire during 60-min CI runs
- **Backup retention cleanup** — need to exceed retention period to verify pruning
- **Pod restart cascades** — subtle race conditions under repeated scale/failover cycles
- **Upgrade correctness under load** — data corruption from rolling restarts

Existing 60-min E2E tests verify correctness of individual operations. Long haul tests verify **sustained reliability** — that the operator doesn't degrade over time.

## Design Overview

The design is based on research of Strimzi, CloudNative-PG, CockroachDB (roachtest), and Vitess soak test patterns. The common architecture across all projects: **separate workload generation from disruption injection, run them concurrently, verify correctness post-hoc**.

We adopt the **run-until-failure (canary)** model inspired by Strimzi: the cluster runs indefinitely with continuous workload and operations. When something breaks — data loss, unrecoverable state, resource exhaustion — the test captures the failure, collects artifacts, and alerts the team. This answers the real question: **"what breaks first, and after how long?"**

---

## Architecture: 4 Components

```
┌─────────────────────────────────────────────────────────┐
│                    Long Haul Test (Go/Ginkgo)             │
│                                                          │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────┐  │
│  │ Data Plane   │  │ Control Plane│  │ Health Monitor │  │
│  │ Workload     │  │ Operations   │  │ & Metrics      │  │
│  │              │  │              │  │                │  │
│  │ • Writers    │  │ • Scale      │  │ • Pod status   │  │
│  │ • Readers    │  │ • Replication│  │ • CR conditions│  │
│  │ • Verifiers  │  │ • Backup     │  │ • OTel metrics │  │
│  │              │  │ • Upgrade    │  │ • Leak detect  │  │
│  └──────┬───────┘  └──────┬───────┘  └───────┬───────┘  │
│         │                 │                   │          │
│         └─────────┬───────┴───────────────────┘          │
│                   ▼                                      │
│          ┌────────────────┐                              │
│          │ Event Journal  │                              │
│          │                │                              │
│          │ • Op start/end │                              │
│          │ • State changes│                              │
│          │ • Error windows│                              │
│          │ • Disruption   │                              │
│          │   budgets      │                              │
│          └────────────────┘                              │
└─────────────────────────────────────────────────────────┘
```

### Component 1: Data Plane Workload

**Purpose:** Continuous read/write traffic to detect data loss, corruption, and availability gaps.

**Implementation:** Go with the official MongoDB driver (`go.mongodb.org/mongo-driver`), NOT shelling out to mongosh. This gives better cancellation/retry/context control over 24h+ runs.

**Writer Model (Durability Oracle):**
- Multiple writer goroutines, each with a unique `writer_id`
- Each write: `{writer_id, seq, payload, checksum(payload), timestamp}`
- Unique index on `(writer_id, seq)` to detect duplicates
- Track three states per write: **attempted**, **acknowledged**, **verified**
- Use `writeConcern: majority` for durability claims
- Small percentage of **upserts/updates** (not just inserts) for broader coverage

**Reader/Verifier Model:**
- Periodic full-scan verification: no gaps in acknowledged sequences per writer
- Checksum validation on read-back
- Separate counters for: missing acknowledged writes, duplicates, stale reads, checksum mismatches
- Use `readConcern: majority` to avoid false negatives from replica lag
- Lag-aware: don't flag replication delay as data loss

**Metrics Emitted:**
- `longhaul_writes_attempted`, `longhaul_writes_acknowledged`, `longhaul_writes_failed`
- `longhaul_reads_total`, `longhaul_reads_stale`, `longhaul_verification_failures`
- `longhaul_write_latency_ms`, `longhaul_read_latency_ms`

### Component 2: Control Plane Operations

**Purpose:** Exercise management operations under continuous load.

**Operation Categories:**

| Operation | Type | Expected Disruption | Validation |
|-----------|------|-------------------|------------|
| Scale up (nodeCount++) | Topology | None | New pods ready, data accessible |
| Scale down (nodeCount--) | Topology | Brief write pause | Remaining pods healthy, no data loss |
| Enable replication | Replication | None | Replicas created, WAL streaming |
| Disable replication | Replication | Brief | Standalone healthy |
| Add region | Multi-region | None | New region catches up, data synced |
| Remove region | Multi-region | Brief | Remaining regions healthy |
| Toggle HA (localHA) | HA | Brief failover | Primary switches, writes resume |
| On-demand backup | Backup | None | Backup CR reaches Completed |
| Restore to new cluster | Backup | N/A (new cluster) | Restored data matches backup watermark |
| Scheduled backup verify | Backup | None | Backups created on schedule |
| Operator upgrade | Update | None (DB pods should NOT restart) | Operator pod rolls, cluster unaffected |
| Cluster binary upgrade | Update | Rolling restart | Pods restart one-by-one, workload continues |
| Schema upgrade | Update | Varies | Pre-backup, post-upgrade reads/writes OK |
| Operator restart/leader failover | Chaos | Brief reconcile gap | Reconciliation resumes |
| Pod eviction (simulating node drain) | Chaos | Brief | Pod rescheduled, workload resumes |

**Sequencing Rules:**
- Operations are NOT fully random — use **preconditions and cooldowns**
- Cannot remove region if only 1 region exists
- Cannot scale below minimum node count
- Cooldown between disruptive ops (configurable, default 5 min)
- Must reach steady state before next operation
- Backup/restore is a **separate flow** (restore creates a NEW cluster, verifies, then cleans up)

**Per-Operation Outage Policy:**
```go
type OutagePolicy struct {
    AllowedDowntime     time.Duration  // e.g., 60s for failover
    AllowedWriteFailures int           // tolerated write errors during window
    MustRecoverWithin   time.Duration  // e.g., 5min to return to steady state
}
```

### Component 3: Health Monitor & Metrics

**Purpose:** Continuous cluster health observation + resource leak detection.

**What to Monitor:**
- **Kubernetes layer:** Pod readiness, restart counts, OOMKills, events
- **CR layer:** DocumentDB status conditions, backup phase transitions
- **Operator layer:** Operator logs/errors, reconciliation count, reconcile duration
- **Database layer:** Connection count, WAL lag, replication status
- **Resource layer:** Memory/CPU usage trends (via OTel/cAdvisor), PVC usage

**Leak Detection:**
- Sample memory/CPU at fixed intervals
- Linear regression over last N samples
- Alert if slope exceeds threshold (configurable)
- 48-72h runs recommended for reliable leak detection

**Steady State Definition:**
```
- All pods in Ready state
- DocumentDB CR conditions: all True
- Replication lag < threshold (if replicated)
- No new pod restarts in last 5 min
- Workload success rate > 99.9%
- No unresolved backup failures
```

### Component 4: Event Journal

**Purpose:** Central log correlating operations, disruptions, and errors for post-mortem analysis.

**Every entry records:**
- Timestamp
- Event type (op_start, op_end, disruption_window_open, disruption_window_close, health_change, workload_error, verification_failure)
- Operation ID
- Cluster state snapshot (topology, pod count, primary node)
- Associated errors (if any)

**Key use case:** When a write failure occurs, the journal shows whether it happened during an expected disruption window (tolerable) or during steady state (bug).

---

## Canary Model

The long-haul test is a **single persistent canary** running on a dedicated AKS cluster. Existing Kind-based integration tests (45-60 min, PR-gated) already cover short-lived validation — there is no need for a separate smoke mode.

**Canary Cluster:**
- 5 writers, 2 verifiers
- Full operation cycle (scale, HA, replication, backup/restore, upgrades, chaos)
- Runs indefinitely until a fatal failure occurs
- On failure: collect artifacts, preserve cluster state for investigation
- Key output: **MTTF** (mean time to failure) and failure classification
- During development: test locally with `--max-duration=30m` against Kind

### Failure Tiers

| Tier | Example | Action |
|------|---------|--------|
| **Fatal** (stop test) | Acknowledged write lost, checksum mismatch, cluster unrecoverable >10min | Artifact dump + preserve cluster + exit non-zero |
| **Degraded** (log + continue) | Operator pod restarted, brief write timeout during expected disruption | Log to journal, continue if recovery within budget |
| **Warning** (monitor) | Memory trending up, reconcile latency increasing | Log warning, no stop |

### Auto-Recovery Before Fatal Declaration
- Operator crash → wait for K8s restart → continue if healthy within 5 min
- Pod eviction → wait for reschedule → continue
- Data loss or corruption → **immediate stop**, preserve cluster state for investigation

### Future: Multi-Region Canary
- Add/remove region operations, cross-region replication verification
- AKS Fleet integration
- Separate canary cluster or extension of single-cluster canary

---

## Directory Structure

```
operator/src/test/longhaul/
├── main_test.go           # Ginkgo suite entry, profile selection
├── config.go              # Configuration (duration, intervals, cluster, profile)
├── workload/
│   ├── writer.go          # Multi-writer with durability tracking
│   ├── reader.go          # Reader + verifier
│   └── oracle.go          # Data integrity oracle (acknowledged write tracking)
├── operations/
│   ├── scheduler.go       # Operation sequencer with preconditions/cooldowns
│   ├── scale.go           # Scale up/down operations
│   ├── replication.go     # Replication enable/disable, add/remove region
│   ├── backup.go          # Backup create + restore-to-new-cluster verification
│   ├── upgrade.go         # Operator, cluster binary, schema upgrades
│   └── chaos.go           # Pod eviction, operator restart
├── monitor/
│   ├── health.go          # Cluster health checks
│   ├── metrics.go         # OTel/Prometheus metric collection
│   └── leakdetect.go      # Resource trend analysis
├── journal/
│   ├── journal.go         # Event journal with disruption window tracking
│   └── policy.go          # Per-operation outage policies
└── report/
    ├── report.go          # Summary report generation
    └── templates/         # Report templates (markdown/HTML)
```

---

## Configuration

```go
type Config struct {
    // Canary runs until failure; MaxDuration=0 means infinite.
    // Use --max-duration=30m for local dev testing against Kind.
    MaxDuration time.Duration

    // Workload tuning
    NumWriters   int           // default: 5
    NumVerifiers int           // default: 2

    // Operation scheduling
    OpCooldown  time.Duration // min interval between disruptive ops
    OpEnabled   []string      // which operations to enable

    // Failure handling
    RecoveryTimeout time.Duration // max time to wait for auto-recovery before fatal
}
```

---

## Deployment & Visibility

### Approach

The long haul test code is fully open source in the repository — anyone can run it. There is no requirement for a public-facing dashboard or scheduled CI workflow for the canary. This matches the pattern of most early-stage OSS projects; public dashboards (like Strimzi's Jenkins or CockroachDB's TeamCity) can be added later as the project matures.

### Running the Canary

**Local development (anyone):**
```bash
cd operator/src
go test ./test/longhaul/ -v --max-duration=30m
```
Runs against whatever cluster your kubeconfig points to (Kind, Minikube, etc.).

**Persistent canary (internal):**
- Dedicated AKS cluster provisioned once (manually or via IaC)
- Long haul test deployed as a Kubernetes Job on the same cluster (separate `longhaul` namespace)
- On new operator release: re-deploy operator via Helm + restart longhaul Job
- Internal Grafana/OTel dashboard for monitoring (optional)
- Cluster preserved on failure for investigation

### When Bugs Are Found

Bugs discovered by the canary are filed as regular GitHub issues — no special process needed. The long haul test collects enough context (event journal, cluster state snapshot, failure details) to make issues actionable.

### Auto-Upgrade

A GitHub Actions workflow handles upgrading the canary cluster automatically. It triggers on new releases and can also be triggered manually.

```yaml
on:
  workflow_dispatch:        # manual trigger
  release:
    types: [published]      # auto-trigger on new operator release

jobs:
  upgrade-canary:
    runs-on: ubuntu-latest
    permissions:
      id-token: write       # for Azure federated identity (OIDC)
    steps:
      - uses: actions/checkout@v4
      - uses: azure/login@v2
        with:
          client-id: ${{ secrets.AZURE_CLIENT_ID }}
          tenant-id: ${{ secrets.AZURE_TENANT_ID }}
          subscription-id: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
      - run: az aks get-credentials --resource-group $RG --name $CLUSTER
      - run: helm upgrade documentdb-operator ./operator/documentdb-helm-chart
      - run: |
          kubectl delete job longhaul -n longhaul --ignore-not-found
          kubectl apply -f test/longhaul/deploy/job.yaml
          kubectl wait --for=condition=ready pod -l job-name=longhaul -n longhaul --timeout=120s
```

**Key points:**
- **AKS auth**: Azure federated identity (OIDC) — no stored secrets, just a trust relationship between GitHub and Azure
- **Operator release** → workflow auto-triggers → Helm upgrade → restart longhaul Job
- **Test code change** → rebuild longhaul image, trigger workflow manually via `workflow_dispatch`
- **Audit trail**: Every upgrade is visible in GitHub Actions history

---

## Learnings from Other Projects

| Project | Key Pattern We Adopt | Key Pattern We Skip |
|---------|---------------------|-------------------|
| **Strimzi** | Run-until-failure loops; metrics collection; CI profiles | JUnit (we use Ginkgo) |
| **CloudNative-PG** | Ginkgo framework; failover via pod delete + SIGSTOP; LSN verification | Single-sequence failover (we need continuous concurrent workload) |
| **CockroachDB** | Chaos runner (periodic kill/restart); separate workload from disruption; roachstress repeated runs | Custom roachtest framework (too heavy for our needs) |
| **Vitess** | Background stress goroutine; per-query tracking; Go native driver | No fault injection (we need disruptive ops) |

**Universal pattern adopted:** Separate workload generators from disruption injectors, run concurrently, verify correctness against an acknowledged-write oracle, use per-operation disruption budgets. Run-until-failure (Strimzi model) rather than time-bounded.

---

## Implementation Phases

Each phase is a self-contained, demoable increment (~1-2 PRs each).

### Phase 1a: Project Skeleton + Config
- `test/longhaul/` directory structure, Ginkgo suite entry point
- Config loading (`--max-duration`, writer count, cooldowns, operation list)
- Can run against a cluster (does nothing yet)

### Phase 1b: Data Plane Workload
- Multi-writer goroutines with durability oracle
- Reader/verifier with gap, duplicate, and checksum detection
- Metrics counters (writes attempted/acknowledged/failed, reads, verification failures)

### Phase 1c: Event Journal
- Central event log (op_start, op_end, health_change, workload_error, etc.)
- Disruption window tracking (expected vs unexpected errors)
- In-memory + file-backed for post-mortem

### Phase 1d: Health Monitor
- Pod readiness, restart counts, OOMKills
- DocumentDB CR status conditions
- Steady-state detection (all healthy, no recent restarts, workload success rate OK)

### Phase 1e: Scale Operations
- Scale up/down with precondition checks
- Per-operation outage policy enforcement
- First control plane operation — validates the operation scheduler pattern

### Phase 1f: Summary Report
- Markdown report on exit (pass/fail, duration, stats, operation timeline)
- Event journal dump
- Testable locally: `go test ./test/longhaul/ -v --max-duration=30m` against Kind

### Phase 2a: Backup & Restore Operations
- On-demand backup creation + wait for completion
- Restore to new cluster + data verification against backup watermark
- Cleanup of restored cluster

### Phase 2b: HA & Replication Operations
- Toggle HA (localHA)
- Enable/disable replication
- Precondition checks (e.g., cannot disable if already standalone)

### Phase 2c: Upgrade Operations
- Operator upgrade (Helm)
- Cluster binary upgrade (documentDBVersion)
- Schema upgrade (schemaVersion)
- Each tested separately with outage policy

### Phase 2d: Chaos Operations
- Pod eviction (simulating node drain)
- Operator restart / leader failover

### Phase 2e: Failure Tiers + Auto-Recovery
- Fatal / degraded / warning classification
- Auto-recovery logic (wait for K8s restart before declaring fatal)
- Cluster state preservation on fatal failure

### Phase 2f: AKS Deployment
- Dockerfile for longhaul test image
- Kubernetes Job manifest, RBAC (ServiceAccount, ClusterRole, Binding)
- ConfigMap for tuning parameters
- Deploy script / instructions

### Phase 2g: Auto-Upgrade Workflow
- GitHub Actions workflow (triggered on release + manual dispatch)
- Azure OIDC auth, Helm upgrade, Job restart

### Phase 3: Multi-Region Canary
- Add/remove region operations
- Cross-region replication verification
- AKS Fleet integration

---

## Open Questions
1. What AKS cluster/subscription should be used for the dedicated canary cluster?
2. Desired SLO targets (e.g., 99.9% write success during steady state)?
