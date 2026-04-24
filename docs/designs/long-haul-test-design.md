# Long Haul Test Design — DocumentDB Kubernetes Operator

**Issue:** [#220](https://github.com/documentdb/documentdb-kubernetes-operator/issues/220)  
**Status:** In progress (Phase 1a complete)

## Terminology

This document refers to two kinds of cluster:

- **DocumentDB cluster** — the database cluster managed by the operator (the `DocumentDB` Custom Resource and its pods).
- **Kubernetes cluster** (or **AKS cluster**, **Kind cluster**) — the infrastructure cluster where the operator and DocumentDB run.

When unqualified, "cluster" in the context of operations, health, and state refers to the **DocumentDB cluster**. Infrastructure clusters are always qualified (AKS, Kind, etc.).

## Problem Statement

The operator lacks continuous, long-running test coverage. Issue #220 requires:
1. Constant writes/reads — ensure no data is lost
2. Constant management operations (add/remove region, HA toggle, scale, backup/restore)
3. Operator and DocumentDB cluster updates under load

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

We adopt the **run-until-failure (canary)** model inspired by Strimzi: the DocumentDB cluster runs indefinitely with continuous workload and operations. When something breaks — data loss, unrecoverable state, resource exhaustion — the test captures the failure, collects artifacts, and alerts the team. This answers the real question: **"what breaks first, and after how long?"**

### Portability

The long-haul test is designed to be **cloud-agnostic and portable**. Anyone with access to a Kubernetes cluster can run it:

- **No cloud-provider dependencies in test code.** The test binary uses only the Kubernetes API and MongoDB wire protocol. It runs on AKS, EKS, GKE, Kind, Minikube, or any conformant Kubernetes cluster.
- **Configuration via environment variables only.** No hard-coded cluster names, Azure subscriptions, or cloud-specific endpoints.
- **Standard deployment artifacts.** The Job manifest and RBAC resources use only core Kubernetes APIs — no cloud-specific annotations required.
- **Optional automation layers.** The AKS-specific deployment, alerting (GitHub Actions cron), and auto-upgrade workflows described in this document are optional CI/CD layers maintained by the core team. They are not required to run the tests.

Contributors can deploy the long-haul canary on their own Kubernetes cluster, observe results locally, and file bugs if issues are found. This keeps the barrier to running long-haul tests low and avoids coupling the test framework to any specific infrastructure.

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
| Restore to new DocumentDB cluster | Backup | N/A (new cluster) | Restored data matches backup watermark |
| Scheduled backup verify | Backup | None | Backups created on schedule |
| Operator upgrade | Update | None (DB pods should NOT restart) | Operator pod rolls, DocumentDB cluster unaffected |
| DocumentDB binary upgrade | Update | Rolling restart | Pods restart one-by-one, workload continues |
| Schema upgrade | Update | Varies | Pre-backup, post-upgrade reads/writes OK |
| Operator restart/leader failover | Chaos | Brief reconcile gap | Reconciliation resumes |
| Pod eviction (simulating node drain) | Chaos | Brief | Pod rescheduled, workload resumes |

**Sequencing Constraints:**

The operation scheduler enforces explicit constraints to prevent invalid or conflicting operations:

| Constraint | Rule | Rationale |
|-----------|------|-----------|
| **Minimum topology** | Cannot scale below `minNodeCount` (default 1) | Prevents loss of quorum / empty DocumentDB cluster |
| **Region cardinality** | Cannot remove region if only 1 region exists | At least one region must remain |
| **Replication prerequisite** | Cannot disable replication while multi-region is active | Regions depend on replication |
| **Concurrent disruptive ops** | At most 1 disruptive operation at a time | Overlapping disruptions make failures non-diagnosable |
| **Cooldown** | Minimum interval between disruptive ops (configurable, default 5 min) | Let the DocumentDB cluster stabilize before next disruption |
| **Steady-state gate** | Must reach steady state before scheduling next operation | Ensures previous operation completed successfully |
| **Backup isolation** | Backup/restore runs as a separate flow (restore creates a NEW DocumentDB cluster, verifies, then cleans up) | Avoids disrupting the primary canary DocumentDB cluster |

Additional rules:
- Operations are NOT fully random — the scheduler uses **preconditions and cooldowns**
- Each operation declares its preconditions (checked before execution) and postconditions (verified after)
- If a precondition fails, the scheduler picks a different operation (no error)

**Per-Operation Outage Policy:**
```go
type OutagePolicy struct {
    AllowedDowntime     time.Duration  // e.g., 60s for failover
    AllowedWriteFailures int           // tolerated write errors during window
    MustRecoverWithin   time.Duration  // e.g., 5min to return to steady state
}
```

### Component 3: Health Monitor & Metrics

**Purpose:** Continuous DocumentDB cluster health observation + resource leak detection.

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
- Cluster state snapshot (DocumentDB topology, pod count, primary node)
- Associated errors (if any)

**Key use case:** When a write failure occurs, the journal shows whether it happened during an expected disruption window (tolerable) or during steady state (bug).

---

## Canary Model

The long-haul test is a **single persistent canary** running on any Kubernetes cluster. Existing Kind-based integration tests (45-60 min, PR-gated) already cover short-lived validation — there is no need for a separate smoke mode. The core team runs the canary on a managed Kubernetes cluster with automated alerting (see [Deployment](#deployment)), but any contributor can run it on their own cluster.

**Canary Configuration:**
- 5 writers, 2 verifiers
- Full operation cycle (scale, HA, replication, backup/restore, upgrades, chaos)
- Runs indefinitely until a fatal failure occurs
- On failure: collect artifacts, preserve DocumentDB cluster state for investigation
- Key output: **MTTF** (mean time to failure) and failure classification
- During development: test locally with `--max-duration=30m` against Kind

### Failure Tiers

| Tier | Example | Action |
|------|---------|--------|
| **Fatal** (stop test) | Acknowledged write lost, checksum mismatch, DocumentDB cluster unrecoverable >10min | Artifact dump + preserve cluster + exit non-zero |
| **Degraded** (log + continue) | Operator pod restarted, brief write timeout during expected disruption | Log to journal, continue if recovery within budget |
| **Warning** (monitor) | Memory trending up, reconcile latency increasing | Log warning, no stop |

### Auto-Recovery Before Fatal Declaration
- Operator crash → wait for K8s restart → continue if healthy within 5 min
- Pod eviction → wait for reschedule → continue
- Data loss or corruption → **immediate stop**, preserve DocumentDB cluster state for investigation

### Future: Multi-Region Canary
- Add/remove region operations, cross-region replication verification
- AKS Fleet integration
- Separate canary Kubernetes cluster or extension of single-cluster canary

---

## Directory Structure

The test infrastructure follows a **three-directory layout** at the repo root:

```
test/
├── utils/                     # Shared test utilities (used by BOTH e2e and longhaul)
│   ├── go.mod                 # Separate module: github.com/.../test/utils
│   ├── mongo/                 # Mongo client, Seed, Count, Ping, Handle
│   ├── assertions/            # Gomega-compatible checkers (DocumentDBReady, InstanceCount, …)
│   ├── documentdb/            # DocumentDB CR CRUD (Create, WaitHealthy, Delete, PatchSpec, …)
│   ├── operatorhealth/        # Operator-churn gate (pod UID/restart tracking)
│   ├── portforward/           # Gateway port-forward (wraps CNPG forwardconnection)
│   ├── fixtures/              # Namespace/secret/label helpers, teardown-by-label
│   ├── timeouts/              # Centralised Eventually durations (reuses CNPG timeouts)
│   ├── clusterprobe/          # Runtime capability checks (VolumeSnapshot CRD, StorageClass)
│   ├── seed/                  # Deterministic datasets (SmallDataset, MediumDataset, …)
│   └── testenv/               # Shared environment config (kubeconfig, client setup)
│
├── e2e/                       # E2E test suite (PR #346)
│   ├── go.mod                 # Imports test/utils + operator API types
│   ├── tests/
│   │   ├── lifecycle/         # Deploy, delete, image update, log level
│   │   ├── scale/             # Instance scaling
│   │   ├── data/              # CRUD, aggregation, sort/limit
│   │   ├── backup/            # Backup & restore
│   │   ├── tls/               # TLS certificate modes
│   │   ├── upgrade/           # Operator & binary upgrades
│   │   └── ...
│   └── README.md
│
└── longhaul/                  # Long-haul canary test suite
    ├── go.mod                 # Imports test/utils + operator API types
    ├── README.md              # Usage guide (running locally, CI safety, configuration)
    ├── suite_test.go          # Ginkgo suite entry point for the canary
    ├── longhaul_test.go       # BeforeSuite (skip gate + config) + long-running test specs
    ├── config/
    │   ├── config.go          # Config struct, env var loading, validation, IsEnabled gate
    │   ├── suite_test.go      # Ginkgo suite entry for config unit tests
    │   └── config_test.go     # Config unit tests (23 specs, fast, no Kubernetes cluster needed)
    ├── workload/              # (Phase 1b)
    │   ├── writer.go          # Multi-writer with durability tracking
    │   ├── reader.go          # Reader + verifier (reuses test/utils/mongo)
    │   └── oracle.go          # Data integrity oracle (acknowledged write tracking)
    ├── operations/            # (Phase 1d-2d)
    │   ├── scheduler.go       # Operation sequencer with preconditions/cooldowns
    │   ├── scale.go           # Scale (reuses test/utils/documentdb.PatchInstances)
    │   ├── replication.go     # Replication enable/disable, add/remove region
    │   ├── backup.go          # Backup create + restore (reuses test/utils/clusterprobe)
    │   ├── upgrade.go         # Operator, DocumentDB binary, schema upgrades
    │   └── chaos.go           # Pod eviction, operator restart
    ├── monitor/               # (Phase 1d)
    │   ├── health.go          # Reuses test/utils/assertions + test/utils/operatorhealth
    │   ├── metrics.go         # OTel/Prometheus metric collection
    │   └── leakdetect.go      # Resource trend analysis
    ├── journal/               # (Phase 1c)
    │   ├── journal.go         # Event journal with disruption window tracking
    │   └── policy.go          # Per-operation outage policies
    └── report/                # (Phase 1f)
        ├── report.go          # Summary report generation
        └── templates/         # Report templates (markdown/HTML)
```

### Shared Utilities: `test/utils/`

The `test/utils/` module provides reusable test infrastructure for **both** E2E and long-haul tests. This avoids duplicating ~2000 lines of proven utilities. The packages originate from PR #346's `test/e2e/pkg/e2eutils/` and are promoted to the shared location.

**Key packages and how long-haul uses them:**

| Package | What it provides | Long-haul use |
|---------|-----------------|---------------|
| `mongo/` | Client, Seed, Count, Ping, Handle, port-forward connect | Writers + Verifiers connect to DocumentDB gateway |
| `assertions/` | AssertDocumentDBReady, AssertInstanceCount, AssertPrimaryUnchanged | Health monitor polls cluster health continuously |
| `documentdb/` | Create, WaitHealthy, Delete, PatchInstances, PatchSpec | Operation executor (scale, upgrade, backup/restore) |
| `operatorhealth/` | Gate (pod UID/restart tracking), Check, MarkChurned | Health monitor detects operator churn under load |
| `portforward/` | OpenWithErr for gateway service | Writers open port-forward to DocumentDB gateway |
| `timeouts/` | For(op), PollInterval(op) — standardised wait durations | All waiters use consistent, CNPG-aligned timeouts |
| `fixtures/` | ensureNamespace, ensureCredentialSecret, ownershipLabels, teardownByLabels | Canary setup creates namespace + credentials; teardown by label on abort |
| `clusterprobe/` | HasVolumeSnapshotCRD, StorageClassAllowsExpansion | Backup operations skip when CSI snapshots unavailable |
| `seed/` | SmallDataset, MediumDataset (deterministic bson.M generators) | Writer seed data for baseline verification |

**Module structure:**

```
test/utils/go.mod    → github.com/documentdb/documentdb-operator/test/utils
test/e2e/go.mod      → github.com/documentdb/documentdb-operator/test/e2e
test/longhaul/go.mod → github.com/documentdb/documentdb-operator/test/longhaul
operator/src/go.mod  → github.com/documentdb/documentdb-operator (unchanged)
```

Each test module uses a `replace` directive to point at the local operator source and `test/utils`:

```go
// test/longhaul/go.mod
module github.com/documentdb/documentdb-operator/test/longhaul

require (
    github.com/documentdb/documentdb-operator/test/utils v0.0.0
    github.com/documentdb/documentdb-operator              v0.0.0
)

replace (
    github.com/documentdb/documentdb-operator/test/utils => ../utils
    github.com/documentdb/documentdb-operator              => ../../operator/src
)
```

> **Migration note:** PR #346 currently has utilities under `test/e2e/pkg/e2eutils/`. Extracting them to
> `test/utils/` is a follow-up task that should be coordinated with xgerman. Until extraction happens,
> long-haul tests can vendor the needed types locally and swap to imports once `test/utils/` exists.

---

## Configuration

All configuration is via environment variables. Tests are **gated** behind `LONGHAUL_ENABLED` — they are safely skipped in regular CI runs (`go test ./...`).

**Current (Phase 1a):**

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGHAUL_ENABLED` | Yes | — | Must be `true`, `1`, or `yes` to run. Otherwise all tests skip. |
| `LONGHAUL_CLUSTER_NAME` | Yes | — | Name of the target DocumentDB cluster CR. |
| `LONGHAUL_NAMESPACE` | No | `default` | Kubernetes namespace of the target DocumentDB cluster. |
| `LONGHAUL_MAX_DURATION` | No | `30m` | Max test duration (`0s` = run until failure). |

> **Note:** The default 30m timeout is a safety net for local development. The persistent canary
> Job manifest explicitly sets `LONGHAUL_MAX_DURATION=0s` to enable run-until-failure mode.

**Planned (future phases):**

| Variable | Default | Phase | Description |
|----------|---------|-------|-------------|
| `LONGHAUL_NUM_WRITERS` | `5` | 1b | Number of concurrent writer goroutines |
| `LONGHAUL_NUM_VERIFIERS` | `2` | 1b | Number of concurrent verifier goroutines |
| `LONGHAUL_OP_COOLDOWN` | `5m` | 1e | Min interval between disruptive operations |
| `LONGHAUL_OP_ENABLED` | all | 1e | Comma-separated list of enabled operations |
| `LONGHAUL_RECOVERY_TIMEOUT` | `5m` | 2e | Max time to wait for auto-recovery before fatal |

---

## Deployment & Visibility

### Approach

The long haul test code is fully open source in the repository — anyone can run it. There is no requirement for a public-facing dashboard or scheduled CI workflow for the canary. This matches the pattern of most early-stage OSS projects; public dashboards (like Strimzi's Jenkins or CockroachDB's TeamCity) can be added later as the project matures.

### Running the Canary

**Local development (anyone):**
```bash
cd test/longhaul

# Run config unit tests (fast, no Kubernetes cluster needed)
go test ./config/ -v

# Run the canary against a local Kind cluster
LONGHAUL_ENABLED=true \
LONGHAUL_CLUSTER_NAME=documentdb-sample \
LONGHAUL_NAMESPACE=default \
LONGHAUL_MAX_DURATION=10m \
go test ./... -v -timeout 0

# Or build a standalone binary
go test -c -o longhaul.test ./
LONGHAUL_ENABLED=true \
LONGHAUL_CLUSTER_NAME=documentdb-sample \
./longhaul.test -test.v -test.timeout 0
```
Runs against whatever Kubernetes cluster your kubeconfig points to (Kind, Minikube, etc.).

**Persistent canary (core team):**
- Managed Kubernetes cluster provisioned once (manually or via IaC)
- Long haul test deployed as a Kubernetes Job on the same cluster (separate `longhaul` namespace)
- On new operator release: re-deploy operator via Helm + restart longhaul Job
- Grafana/OTel dashboard for monitoring (optional)
- DocumentDB cluster preserved on failure for investigation

> **Note:** The canary Job manifest, RBAC, and test binary are fully portable — they use only core Kubernetes
> APIs and work on any conformant cluster. The core team runs an automated instance with GitHub Actions-based
> alerting and auto-upgrade (see below), but any contributor can deploy the same artifacts on their own cluster,
> observe results locally (pod logs, ConfigMap status), and file bugs if issues are found.

### Alerting (Optional — Core Team Automation)

The alerting system uses a **two-layer architecture** to avoid managing long-lived tokens on the Kubernetes cluster. This is optional CI/CD automation maintained by the core team — contributors running the canary on their own cluster can monitor results via pod logs and the `longhaul-status` ConfigMap.

**Layer 1: Kubernetes cluster (always running)**
- Long-haul canary runs as a Kubernetes Job — continuous workload
- Writes status to a well-known ConfigMap (`longhaul-status` in `longhaul` namespace)
- Updates include: current state (running/failed/passed), last heartbeat, failure details, journal excerpt
- No GitHub token needed on the Kubernetes cluster

**Layer 2: GitHub Actions (periodic health check)**
- Scheduled workflow runs every hour (`cron: '0 * * * *'`)
- Connects to Kubernetes cluster via cloud-provider auth (e.g., Azure OIDC for AKS)
- Checks canary health: pod status, status ConfigMap, recent pod logs
- If failure detected → posts a failure report to the workflow summary and optionally notifies
  the team via Slack/email. A maintainer reviews the evidence and **manually triggers** issue
  creation using `workflow_dispatch` (human-in-the-loop — no auto-created issues).
  - Failure report includes: DocumentDB cluster name, uptime, error details, journal excerpt, pod logs
  - Label for tracking: `long-haul-failure`
- Uses `GITHUB_TOKEN` (auto-managed by GitHub Actions, no expiry, no rotation)
- Maintainers receive notification through Slack webhook and/or GitHub Actions summary
- Deduplication: the issue-creation workflow skips if an open `long-haul-failure` issue already exists

**Workflow 1: Canary Health Check** (scheduled — detects failures, notifies team)

```yaml
# .github/workflows/longhaul-health-check.yml
name: Long Haul Health Check
on:
  schedule:
    - cron: '0 * * * *'       # every hour
  workflow_dispatch:            # manual trigger

jobs:
  check-canary:
    runs-on: ubuntu-latest
    permissions:
      id-token: write           # cloud-provider OIDC (e.g., Azure)
    steps:
      - uses: actions/checkout@v4
      - uses: azure/login@v2    # adapt for your cloud provider
        with:
          client-id: ${{ secrets.AZURE_CLIENT_ID }}
          tenant-id: ${{ secrets.AZURE_TENANT_ID }}
          subscription-id: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
      - run: az aks get-credentials --resource-group $RG --name $CLUSTER
      - name: Check canary status
        id: status
        run: |
          POD_STATUS=$(kubectl get pods -l job-name=longhaul -n longhaul -o jsonpath='{.items[0].status.phase}')
          CANARY_STATUS=$(kubectl get configmap longhaul-status -n longhaul -o jsonpath='{.data.status}')
          FAILURE_DETAILS=$(kubectl get configmap longhaul-status -n longhaul -o jsonpath='{.data.details}' 2>/dev/null || echo "N/A")
          echo "pod_status=$POD_STATUS" >> $GITHUB_OUTPUT
          echo "canary_status=$CANARY_STATUS" >> $GITHUB_OUTPUT
          echo "failure_details=$FAILURE_DETAILS" >> $GITHUB_OUTPUT
      - name: Report failure in workflow summary
        if: steps.status.outputs.canary_status == 'failed' || steps.status.outputs.pod_status != 'Running'
        run: |
          echo "## 🔴 Long Haul Canary Failure Detected" >> $GITHUB_STEP_SUMMARY
          echo "" >> $GITHUB_STEP_SUMMARY
          echo "- **Pod status:** ${{ steps.status.outputs.pod_status }}" >> $GITHUB_STEP_SUMMARY
          echo "- **Canary status:** ${{ steps.status.outputs.canary_status }}" >> $GITHUB_STEP_SUMMARY
          echo "- **Details:** ${{ steps.status.outputs.failure_details }}" >> $GITHUB_STEP_SUMMARY
          echo "" >> $GITHUB_STEP_SUMMARY
          echo "**Action required:** Review the failure data and run the" >> $GITHUB_STEP_SUMMARY
          echo "[Create Long Haul Issue](../../actions/workflows/longhaul-create-issue.yml)" >> $GITHUB_STEP_SUMMARY
          echo "workflow to file a bug if this is a real failure." >> $GITHUB_STEP_SUMMARY
      # Optional: Slack notification
      # - name: Notify Slack
      #   if: steps.status.outputs.canary_status == 'failed'
      #   run: |
      #     curl -X POST -H 'Content-type: application/json' \
      #       --data '{"text":"🔴 Long haul canary failure detected. Review: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}"}' \
      #       ${{ secrets.SLACK_WEBHOOK_URL }}
```

**Workflow 2: Create Long Haul Issue** (manual trigger — human-in-the-loop)

```yaml
# .github/workflows/longhaul-create-issue.yml
name: Create Long Haul Issue
on:
  workflow_dispatch:
    inputs:
      failure_summary:
        description: 'Brief description of the failure (from health check workflow summary)'
        required: true
        type: string

jobs:
  create-issue:
    runs-on: ubuntu-latest
    permissions:
      issues: write
    steps:
      - uses: actions/github-script@v7
        with:
          script: |
            // Deduplicate: skip if open issue exists
            const { data: issues } = await github.rest.issues.listForRepo({
              owner: context.repo.owner, repo: context.repo.repo,
              labels: 'long-haul-failure', state: 'open'
            });
            if (issues.length > 0) {
              core.info(`Skipping — open issue already exists: #${issues[0].number}`);
              return;
            }
            await github.rest.issues.create({
              owner: context.repo.owner, repo: context.repo.repo,
              title: `[Long Haul Failure] ${new Date().toISOString().split('T')[0]}`,
              body: `## Long Haul Canary Failure\n\n${{ github.event.inputs.failure_summary }}\n\n_Created manually after human review of health check workflow._`,
              labels: ['long-haul-failure']
            });
```

**Benefits:**
- **Human-in-the-loop**: Failures are reported but issues are only created after a maintainer reviews the evidence — reduces noise from transient/infra failures
- No long-lived GitHub tokens on the Kubernetes cluster
- `GITHUB_TOKEN` in Actions is auto-managed — no expiry, no rotation
- Failure reports are visible in GitHub Actions workflow summaries
- All filed issues are publicly visible as GitHub Issues — contributors can see and comment
- Easy to extend: add Slack webhook, Teams notification, or status badge in future

### Auto-Upgrade (Optional — Core Team Automation)

A GitHub Actions workflow handles upgrading the canary Kubernetes cluster automatically. It triggers on new releases and can also be triggered manually. The example below uses Azure AKS — adapt the auth and credential steps for other cloud providers.

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
- **Cloud auth**: Uses cloud-provider identity federation (e.g., Azure OIDC for AKS) — no stored secrets
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

### Phase 1a: Project Skeleton + Config ✅
- `test/longhaul/` directory with Ginkgo suite, BeforeSuite skip gate, placeholder test
- `test/longhaul/config/` sub-package with Config struct, env var loading, validation, IsEnabled
- Config unit tests (23 specs) in separate suite — fast, no Kubernetes cluster needed
- README with usage guide, config reference, CI safety explanation
- CI-safe: `LONGHAUL_ENABLED` gate skips tests in `go test ./...`

### Phase 1b: Data Plane Workload
- Multi-writer goroutines with durability oracle
- Reader/verifier with gap, duplicate, and checksum detection
- Reuses `test/utils/mongo` for gateway connections and `test/utils/seed` patterns for data generation
- Metrics counters (writes attempted/acknowledged/failed, reads, verification failures)

### Phase 1c: Event Journal
- Central event log (op_start, op_end, health_change, workload_error, etc.)
- Disruption window tracking (expected vs unexpected errors)
- In-memory + file-backed for post-mortem

### Phase 1d: Health Monitor
- Pod readiness, restart counts, OOMKills
- DocumentDB CR status conditions
- Reuses `test/utils/assertions` (AssertDocumentDBReady) and `test/utils/operatorhealth` (Gate)
- Steady-state detection (all healthy, no recent restarts, workload success rate OK)

### Phase 1e: Scale Operations
- Scale up/down with precondition checks (reuses `test/utils/documentdb.PatchInstances`)
- Per-operation outage policy enforcement
- First control plane operation — validates the operation scheduler pattern

### Phase 1f: Summary Report
- Markdown report on exit (pass/fail, duration, stats, operation timeline)
- Event journal dump
- Testable locally: `cd test/longhaul && LONGHAUL_MAX_DURATION=30m go test ./... -v -timeout 0` against Kind

### Phase 2a: Backup & Restore Operations
- On-demand backup creation + wait for completion
- Restore to new DocumentDB cluster + data verification against backup watermark
- Cleanup of restored DocumentDB cluster

### Phase 2b: HA & Replication Operations
- Toggle HA (localHA)
- Enable/disable replication
- Precondition checks (e.g., cannot disable if already standalone)

### Phase 2c: Upgrade Operations
- Operator upgrade (Helm)
- DocumentDB binary upgrade (documentDBVersion)
- Schema upgrade (schemaVersion)
- Each tested separately with outage policy

### Phase 2d: Chaos Operations
- Pod eviction (simulating node drain)
- Operator restart / leader failover

### Phase 2e: Failure Tiers + Auto-Recovery
- Fatal / degraded / warning classification
- Auto-recovery logic (wait for K8s restart before declaring fatal)
- DocumentDB cluster state preservation on fatal failure

### Phase 2f: Kubernetes Deployment
- Dockerfile for longhaul test image
- Kubernetes Job manifest, RBAC (ServiceAccount, ClusterRole, Binding)
- ConfigMap for tuning parameters
- Deploy script / instructions (portable — works on any Kubernetes cluster)

### Phase 2g: Auto-Upgrade Workflow (Optional)
- GitHub Actions workflow (triggered on release + manual dispatch)
- Cloud-provider auth (e.g., Azure OIDC), Helm upgrade, Job restart

### Phase 2h: Alerting Workflow (Optional)
- GitHub Actions scheduled workflow (hourly cron) detects failures
- Posts failure report to workflow summary + optional Slack notification
- **Human-in-the-loop**: Maintainer reviews evidence, then manually triggers issue creation via `workflow_dispatch`
- Labels: `long-haul-failure`
- Deduplication: skips issue creation if an open `long-haul-failure` issue already exists

### Phase 3: Multi-Region Canary
- Add/remove region operations
- Cross-region replication verification
- AKS Fleet integration

---

## Open Questions
1. What Kubernetes cluster should the core team use for the persistent canary? (Any conformant cluster works — AKS, EKS, GKE, etc.)
2. Desired SLO targets (e.g., 99.9% write success during steady state)?
3. **Module placement:** Long-haul tests live in `test/longhaul/` as a separate Go module (`test/longhaul/go.mod`). Shared test infrastructure lives in `test/utils/` and is imported by both `test/e2e/` and `test/longhaul/` via `replace` directives. This keeps test dependencies (Ginkgo, mongo-driver, CNPG test utils) out of the operator's runtime `go.mod`.
4. **Shared utility extraction:** PR #346 currently places reusable utilities under `test/e2e/pkg/e2eutils/`. A follow-up task will extract them to `test/utils/` so long-haul tests can import without depending on the E2E module. Until extraction, long-haul can vendor needed helpers locally.

## Design Decisions (Provisional)

The following decisions shape future Phase interfaces. They are provisional — details will be refined when each Phase begins, but the approach is locked.

### Journal Durability (Phase 1c)
The event journal will use a PVC-backed file for persistence across pod restarts. The journal appends structured JSON lines (`{timestamp, event_type, op_id, cluster_state, error}`). On startup, the journal reader scans the existing file to reconstruct in-memory state. The PVC is mounted at `/data/journal/` in the canary Job manifest.

### Writer Sequence Resumption (Phase 1b)
On restart, each writer bootstraps its sequence number from `max(seq)` for its `writer_id` in the database. The oracle tolerates gaps between a crash and resume — gaps are logged as expected (crash-recovery gap) rather than flagged as data loss. The `(writer_id, seq)` unique index guarantees no duplicate sequence numbers.

### Teardown on Abort (Phase 1b)
The harness registers a signal handler for SIGTERM and SIGINT. On signal: (1) cancel all writer/reader contexts, (2) flush journal to disk, (3) write final status to ConfigMap, (4) exit with appropriate code. On startup, the harness checks for a leftover run (stale ConfigMap with state=running but no matching pod) and logs a warning before proceeding.

### Latency-Regression Baseline (Phase 1d)
During the first 30 minutes of a canary run, the monitor establishes P50/P99 write and read latency baselines. After warmup, sustained P99 regression >2× baseline for >5 minutes triggers a warning-level alert. The exact thresholds are configurable via environment variables (`LONGHAUL_LATENCY_P99_MULTIPLIER`, `LONGHAUL_LATENCY_WINDOW`).
