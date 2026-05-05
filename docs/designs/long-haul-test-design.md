# Long Haul Test Design — DocumentDB Kubernetes Operator

**Issue:** [#220](https://github.com/documentdb/documentdb-kubernetes-operator/issues/220)  
**Status:** In progress (Phase 1a complete)

---

## Terminology

- **DocumentDB cluster** — the database cluster managed by the operator (the `DocumentDB` CR and its pods).
- **Kubernetes cluster** — the infrastructure cluster where the operator and DocumentDB run.

When unqualified, "cluster" refers to the **DocumentDB cluster**.

---

## Problem Statement

E2E tests run for 15–60 minutes from a clean state. They cannot detect bugs whose **accumulation rate is tied to real operations** — memory leaks, lock-table bloat, CR-history drift, upgrade-under-state failures. These bugs surface only after days of continuous operation.

### The Core Insight

> Long-haul testing isn't a different testing *technique* — it's what happens when the failure modes you need to detect require more time and operations than fit in any bounded test run.

You can't speed up "memory leaked per reconciliation cycle" — you need many real reconciliation cycles. The long-haul infrastructure (persistent cluster, event journal, alerting) exists because these tests can't be attended, can't be reset between runs, and need accumulation that no existing test type provides.

### Eight Failure Modes

**Primary** (drive architecture decisions — without specific components, we'd miss these):

| # | Failure Mode | Why It Drives Architecture |
|---|---|---|
| 1 | **Operational Residue** | Resources leak proportional to operations → needs trend analysis on per-component metrics |
| 3 | **Concurrent Data + Control Plane** | Needs continuous data-plane traffic during control-plane ops |
| 7 | **Random Operation Sequences** | Needs weighted-random scheduler + journal for reproduction |
| 8 | **Performance Degradation** | Needs baseline metrics + continuous statistical comparison |

**Emergent** (free by running long — no special components needed):

| # | Failure Mode | Why It's Emergent |
|---|---|---|
| 2 | **Reconciliation Idempotency at Scale** | Millions of reconcile loops happen naturally over days |
| 4 | **Upgrade Under Accumulated State** | Upgrade after days of operations tests this naturally |
| 5 | **Repeated Crash-Recovery Cycles** | Chaos operations over time create compounding partial states |
| 6 | **Environmental Event Overlap** | Real infrastructure events just happen |

---

## Design Principles

These are **negative constraints** — each prevents accidentally hiding a failure mode:

| Principle | What It Prevents |
|---|---|
| **P1.** Bug Amplifier, Not Production Replica | Restarting pods (like production) would reset memory, hiding FM1 |
| **P2.** Don't Change the Measuring Instrument | Changing workload mid-run contaminates the signal — can't tell system bug from test change |
| **P3.** Workload Is Deployment-Blind | L2 workload must not import `client-go` — enables fair comparison across clusters |
| **P4.** Per-Component Attribution From Day One | Without separate series for operator/DB, a memory climb at hour 30 is undiagnosable |
| **P5.** Realistic Concurrency, Not Stress | 20–50 clients (production-like), not 1000+ (stress test is a different program) |
| **P6.** Forward-Only Upgrades; Workload Runs Through | Draining before upgrade hides exactly the upgrade bugs we're testing (FM4) |
| **P7.** Random With Journal, Not Scripted | Scripted sequences only find bugs humans imagined — journal enables reproduction |
| **P8.** Human in the Loop for Alerts | Auto-filed issues cause alert fatigue; humans review before creating issues |

---

## Architecture

### Layered Model

```
┌────────────────────────────────────────────────────────────────┐
│ L4: REPORTING / PASS-FAIL GATES                                │
│     RSS slope · error rate · p99 latency · recovery time       │
│     Trend: this period vs previous · Cluster A vs B delta      │
├────────────────────────────────────────────────────────────────┤
│ L3: METRICS COLLECTION                                         │
│     Prometheus: operator /metrics, postgres-exporter            │
│     cAdvisor / metrics-server for pod-level RSS, CPU           │
├────────────────────────────────────────────────────────────────┤
│ L2: DATA-PLANE WORKLOAD  (deployment-agnostic)                 │
│     Input: mongodb:// connection string + workload config      │
│     Output: structured events (start/end/error/latency)        │
│     MUST NOT import k8s libraries (P3)                         │
├────────────────────┬───────────────────────────────────────────┤
│ L1a: DEPLOYMENT    │ L1b: OPERATION SCHEDULER                  │
│      HARNESS       │      (control-plane workload)             │
│                    │                                           │
│ Provisions or      │  Weighted random k8s operations:          │
│ connects to k8s    │  scale, kill, failover, backup, upgrade   │
│ cluster.           │  Targets Cluster A only.                  │
│                    │  Preconditions + cooldowns + journaled.    │
│ Output:            │  Uses client-go.                          │
│ {conn_string,      │  Output: journal events                   │
│  metrics_targets}  │                                           │
└────────────────────┴───────────────────────────────────────────┘
```

### Two-Cluster Topology

The framework runs **two DocumentDB clusters** — a Primary (target of chaos) and a Baseline (control group). The Baseline makes signals attributable:

| Observation | Diagnosis |
|---|---|
| Cluster A degrades, B stable | Per-cluster bug — caused by operations on A |
| Both A and B degrade | Operator-level bug — leak in the shared operator |
| B degrades, A stable | Infrastructure noise — dismiss |

**Orchestration rules:**
- Operations target **Cluster A only** (Baseline stays stable)
- Data-plane traffic runs on **both** clusters (same load — fair comparison)
- Operator upgrades apply to both (single operator instance)
- DB upgrades can be staggered across clusters (tests mixed-version fleet)

### Failure Mode → Component Mapping

| Failure Mode | Architecture Component |
|---|---|
| FM1: Operational Residue | Health Monitor (per-component trend analysis) |
| FM3: Concurrent Data + Control | Data-Plane Workload + Operation Scheduler (simultaneous) |
| FM4: Upgrade Under Accumulated State | Lifecycle: upgrade after days of accumulated CRs |
| FM7: Random Operation Sequences | Operation Scheduler (weighted random + journal) |
| FM8: Performance Degradation | Health Monitor (baseline comparison: A vs B, this period vs last) |

---

## Lifecycle Model

### Continuous Operation

The test runs **continuously** — no cycles, no resets. Workload, metrics, operations, and health monitoring all run as long-lived processes. The system accumulates real state (PVC growth, CR history, operator memory) exactly as it would in production.

| Activity | Trigger | Disrupts system? |
|---|---|---|
| Workload traffic, metrics, operation scheduling | Always running | No |
| Report generation, journal rotation | Timer (default 48h) | No |
| Operator/DB upgrade | Release workflow | Briefly (pod restarts) |

### Upgrades Triggered From Release Workflow

Upgrades are triggered by the **operator release workflow** — when a new version is published, the release workflow updates the canary's target version (e.g., via ConfigMap patch or Helm upgrade). The harness detects `target ≠ current` and executes the upgrade.

**Baseline gate:** An upgrade doesn't fire immediately. The harness enforces a minimum accumulation period (default 48h) since the last upgrade — ensuring we always test "upgrade after accumulated state" (FM4).

**Collapse rule:** If multiple versions arrive while the gate is closed, only the latest executes. Long-haul is not a per-release regression suite — CI covers that.

**Workload runs through:** No drain, no quiesce. Traffic continues during upgrades. Draining before upgrade hides the bugs we're testing (P6).

### Cluster Retirement

Replace a cluster when: hardware EOL, Kubernetes version too old, or accumulated state exceeds practical limits (e.g., PVC full). Retirement = provision new cluster + start fresh accumulation.

---

## Operations Catalog

| Operation | Category | Target | Precondition |
|---|---|---|---|
| Scale Up | Topology | A | replicas < max |
| Scale Down | Topology | A | replicas > min(3) |
| Controlled Failover | HA | A | cluster healthy, 3+ replicas |
| Kill Primary Pod | Chaos | A | cluster healthy |
| Drain Node | Chaos | A | multi-node cluster |
| Trigger Backup | Data Protection | A | no backup running |
| Verify Backup | Data Protection | A | backup exists |
| Configuration Change | Config | A | cluster healthy |
| Operator Upgrade | Lifecycle | Both | target ≠ current + gate open |
| DB Upgrade | Lifecycle | A then B | target ≠ current + gate open |

### Sequencing Constraints

| Constraint | Rule | Rationale |
|---|---|---|
| Min Topology | Never scale below 3 replicas | Maintains HA |
| Concurrent Ops | Max 1 disruptive op at a time | Overlapping disruptions are non-diagnosable |
| Cooldown | Min gap between same-category ops (default 5 min) | Let cluster stabilize |
| Steady-State Gate | Health check must pass before next op | Ensures recovery from previous op |
| Backup Isolation | No topology changes during backup | Backup assumes stable topology |
| Region Cardinality | At most 2 region changes per reporting period | Avoids replication thrash |

### Per-Operation Outage Policy

Each operation declares expected disruption and recovery budget:

```go
type OutagePolicy struct {
    AllowedDowntime     time.Duration  // e.g., 60s for failover
    AllowedWriteFailures int           // tolerated errors during window
    MustRecoverWithin   time.Duration  // e.g., 5min to return to steady state
}
```

---

## Data Plane Workload

### Writer Model (Durability Oracle)

- Multiple writer goroutines, each with unique `writer_id`
- Each write: `{writer_id, seq, payload, checksum, timestamp}`
- Track states: **attempted** → **acknowledged** → **verified**
- `writeConcern: majority` for durability claims
- Unique index on `(writer_id, seq)` detects duplicates

### Reader/Verifier Model

- Periodic full-scan: no gaps in acknowledged sequences per writer
- Checksum validation on read-back
- `readConcern: majority` to avoid false negatives from replica lag
- Lag-aware: don't flag replication delay as data loss

### Metrics

- `longhaul_writes_{attempted,acknowledged,failed}`
- `longhaul_reads_total`, `longhaul_verification_failures`
- `longhaul_write_latency_ms`, `longhaul_read_latency_ms`

---

## Observability

### Per-Component Attribution (P4)

Separate metrics for: operator RSS (Go), DB pod RSS (postmaster + backends), goroutine count, reconcile rate, API call rate. Without this, a climb at hour 30 is undiagnosable.

### Leak Detection

- Sample memory/CPU at fixed intervals
- Linear regression over last N samples
- Alert if slope exceeds threshold (configurable)
- 48h+ runs recommended for reliable signal vs noise

### Alerting (Human-in-the-Loop — P8)

- Hourly health check detects failures → posts to workflow summary + optional Slack
- Maintainer reviews evidence → manually triggers issue creation via `workflow_dispatch`
- No auto-created issues — reduces noise from transient/infra failures
- Deduplication: skips if an open `long-haul-failure` issue already exists

---

## Deployment & Portability

### Cloud-Agnostic Design

The test binary uses only the Kubernetes API and MongoDB wire protocol — runs on AKS, EKS, GKE, Kind, or any conformant cluster. No cloud-provider dependencies in test code.

### Configuration

All config via environment variables. Tests gated behind `LONGHAUL_ENABLED` — safely skipped in `go test ./...`:

| Variable | Required | Default | Description |
|---|---|---|---|
| `LONGHAUL_ENABLED` | Yes | — | Must be `true`/`1`/`yes` to run |
| `LONGHAUL_CLUSTER_NAME` | Yes | — | Target DocumentDB cluster CR name |
| `LONGHAUL_NAMESPACE` | No | `default` | Kubernetes namespace |
| `LONGHAUL_MAX_DURATION` | No | `30m` | Max duration (`0s` = run until failure) |
| `LONGHAUL_NUM_WRITERS` | No | `5` | Concurrent writer goroutines |
| `LONGHAUL_OP_COOLDOWN` | No | `5m` | Min interval between disruptive ops |

### Running

**Local development (anyone):**
```bash
cd test/longhaul
LONGHAUL_ENABLED=true LONGHAUL_CLUSTER_NAME=documentdb-sample \
  LONGHAUL_MAX_DURATION=10m go test ./... -v -timeout 0
```

**Persistent canary (core team):**
- Kubernetes Job on managed cluster (separate `longhaul` namespace)
- On new operator release: release workflow upgrades canary via Helm + restarts Job
- Grafana/OTel dashboard for monitoring (optional)
- DocumentDB cluster preserved on failure for investigation

### Failure Tiers

| Tier | Example | Action |
|---|---|---|
| **Fatal** (stop) | Acknowledged write lost, checksum mismatch, cluster unrecoverable >10min | Artifact dump + preserve cluster + exit non-zero |
| **Degraded** (continue) | Operator pod restarted, write timeout during expected disruption | Log to journal, continue if recovery within budget |
| **Warning** (monitor) | Memory trending up, reconcile latency increasing | Log warning, no stop |

---

## Implementation Phases

| Phase | Scope | Status |
|---|---|---|
| **1a** | Project skeleton + config + CI safety gate | ✅ Complete |
| **1b** | Data-plane workload (writers, oracle, verifiers) | Next |
| **1c** | Event journal (disruption window tracking) | Planned |
| **1d** | Health monitor (steady-state detection, leak alerts) | Planned |
| **1e** | Scale operations + scheduler pattern | Planned |
| **1f** | Summary report (markdown on exit) | Planned |
| **2a** | Backup & restore operations | Planned |
| **2b** | HA & replication operations | Planned |
| **2c** | Upgrade operations (operator + DB + schema) | Planned |
| **2d** | Chaos operations (pod eviction, operator restart) | Planned |
| **2e** | Failure tiers + auto-recovery logic | Planned |
| **2f** | Kubernetes Job deployment (Dockerfile, RBAC) | Planned |
| **2g** | Auto-upgrade workflow (triggered from release workflow) | Planned |
| **2h** | Alerting workflow (hourly cron + human-in-the-loop) | Planned |
| **3** | Multi-region canary (add/remove region, cross-region verification) | Future |

Each phase is a self-contained, demoable increment (~1-2 PRs).

---

## Learnings from Other Projects

| Project | Pattern We Adopt | Pattern We Skip |
|---|---|---|
| **Strimzi** | Run-until-failure loops; metrics collection | JUnit (we use Ginkgo) |
| **CloudNative-PG** | Ginkgo framework; failover via pod delete + SIGSTOP | Single-sequence failover (we need continuous concurrent workload) |
| **CockroachDB** | Chaos runner; separate workload from disruption; roachstress | Custom roachtest framework (too heavy) |
| **Vitess** | Background stress goroutine; per-query tracking | No fault injection (we need disruptive ops) |

**Universal pattern:** Separate workload from disruptions, run concurrently, verify against acknowledged-write oracle, use per-operation disruption budgets.

---

## Open Questions

1. Which Kubernetes cluster for the persistent canary? (Any conformant cluster works)
2. Desired SLO targets (e.g., 99.9% write success during steady state)?
3. Multi-region canary scope (Phase 3) — AKS Fleet integration?


