# Long Haul Test Design

**Issue:** [#220](https://github.com/documentdb/documentdb-kubernetes-operator/issues/220)

---

## Terminology

- **DocumentDB cluster** — the database cluster managed by the operator (the `DocumentDB` CR and its pods).
- **Kubernetes cluster** — the infrastructure cluster where the operator and DocumentDB run.

When unqualified, "cluster" refers to the **DocumentDB cluster**.

---

## Problem Statement

E2E tests run for 15–60 minutes from a clean state. They cannot detect bugs whose **accumulation rate is tied to real operations** — memory leaks, lock-table bloat, CR-history drift, upgrade-under-state failures. These bugs surface only after days of continuous operation.

You can't speed up "memory leaked per reconciliation cycle" — you need many real reconciliation cycles. The long-haul infrastructure (persistent cluster, event journal, alerting) exists because these tests can't be attended, can't be reset between runs, and need accumulation that no existing test type provides.

---

## Architecture

### Components

The driver is a single Go binary that runs five long-lived components against an externally-provisioned DocumentDB cluster. Boxes are components; arrows are events written to the journal.

```mermaid
flowchart LR
    Sched[Operation Scheduler]
    WV[Writer / Verifier]
    Mon[Monitor]
    J[(Journal)]
    R[Report]

    Sched -- op events --> J
    WV -- write/verify events --> J
    Mon -- pod metrics + readiness --> J
    J --> R
```

| Component | Role | Output |
|---|---|---|
| **Writer/Verifier** | Data-plane workload. Connects via `mongodb://` only — no k8s imports. Writers insert monotonic sequences with checksums under majority write concern; verifiers scan for gaps and bad checksums. | Counters (acked, failed, verify passes, gaps, checksum errors); errors to journal. |
| **Operation Scheduler** | Control plane. Applies weighted-random ops (scale, kill, failover, backup, upgrade) with preconditions and cooldowns. | Operation start/end events to journal. |
| **Monitor** | Polls pod RSS/CPU and checks readiness of operator + DB pods. | Periodic samples + readiness events to journal. |
| **Journal** | In-process append-only event log shared by all components. | Reproducible event stream for the report. |
| **Report** | Aggregates the journal into a markdown summary at a configurable interval; raises alerts on threshold breaches. | Markdown report; alert lines. |

### Cluster Topology

The driver supports a **two-cluster** topology so signals become attributable: a **Primary** cluster the scheduler operates on, and a **Baseline** cluster the scheduler leaves alone. Both clusters receive the same data-plane traffic so they form a fair comparison.

| Observation | Diagnosis |
|---|---|
| Primary degrades, Baseline stable | Per-cluster bug — caused by operations on Primary |
| Both degrade | Operator-level bug — leak in the shared operator |
| Baseline degrades, Primary stable | Infrastructure noise — dismiss |

---

## Lifecycle

The test runs **continuously** — no cycles, no resets. Workload, metrics, operations, and health monitoring all run as long-lived processes. The system accumulates real state (PVC growth, CR history, operator memory) exactly as it would in production.

**Workload runs through upgrades.** No drain, no quiesce. Draining before upgrade hides exactly the upgrade-under-state bugs we're testing.

**Baseline gate before upgrades.** Upgrades are triggered by the operator release workflow, but don't fire immediately. The harness enforces a minimum accumulation period (default 48h) since the last upgrade — ensuring we always test "upgrade after accumulated state". If multiple versions arrive while the gate is closed, only the latest executes.

---

## Operations

The scheduler picks operations from these categories with weighted randomization:

| Category | Examples |
|---|---|
| **Topology** | scale up, scale down (within CRD bounds for `spec.instancesPerNode`) |
| **Lifecycle** | DocumentDB version upgrade, operator upgrade |
| **HA** | controlled failover |
| **Chaos** | kill primary pod, drain node |
| **Data protection** | trigger backup, verify backup |

**Sequencing invariants** (enforced by the scheduler — exact values live in code):

- One disruptive op at a time. Overlapping disruptions are non-diagnosable.
- Per-category cooldown between ops. Lets the cluster stabilize.
- Steady-state gate — health check must pass before the next op fires.
- Backup isolation — no topology changes during backup.

Each operation declares an **outage policy**: tolerated write failures during its disruption window and a max recovery time. Breaching the policy is recorded as a Tier-1 failure (see Failure Tiers).

---

## Data Plane Workload

The workload provides a **durability oracle** — every acknowledged write must be readable, in order, with the correct checksum, until the end of the run.

Key invariants:

- **Multiple writer goroutines**, each with a unique `writer_id`, write monotonic `seq` values with payload checksums.
- **Majority write concern.** A write is only counted as acknowledged after replication to a majority of replicas.
- **Verifier scans** the collection periodically and flags any gap in `seq` per writer or any checksum mismatch on read-back.
- **Majority read concern** to avoid false negatives from replica lag.
- **Deployment-blind.** The workload imports no Kubernetes libraries, so the same binary runs against any cluster (AKS, EKS, GKE, kind).

Losing an acknowledged write or observing a checksum mismatch is a Tier-1 failure regardless of what else is happening.

---

## Observability

**Per-component attribution.** Metrics are tagged by component (operator pod RSS, DB pod RSS, goroutine count, reconcile rate, API-call rate). Without separate series, a memory climb at hour 30 is undiagnosable.

**Human-in-the-loop alerts.** The hourly monitor posts a summary to the workflow run and, when configured, to a chat channel. A maintainer reviews the evidence and manually creates a GitHub issue. No auto-filed issues — alert fatigue from transient or infrastructure failures would erode trust in the canary.

### Failure Tiers

| Tier | Example | Action |
|---|---|---|
| **Fatal** | Acknowledged write lost, checksum mismatch, cluster unrecoverable past budget | Preserve cluster + exit non-zero |
| **Degraded** | Operator pod restart, write timeout inside an expected disruption window | Log and continue if recovery within budget |
| **Warning** | Memory trending up, reconcile latency rising | Log only |

---

## Learnings from Other Projects

| Project | Pattern We Adopt | Pattern We Skip |
|---|---|---|
| **Strimzi** | Run-until-failure loops; metrics collection | JUnit (we run a standalone Go binary, not a test framework) |
| **CloudNative-PG** | Failover via pod delete + SIGSTOP; pod-level resource sampling | Ginkgo framework (we use a long-lived `Deployment` instead) |
| **CockroachDB** | Chaos runner; separate workload from disruption; roachstress | Custom roachtest framework (too heavy) |
| **Vitess** | Background stress goroutine; per-query tracking | No fault injection (we need disruptive ops) |

**Universal pattern:** Separate workload from disruptions, run concurrently, verify against an acknowledged-write oracle, use per-operation disruption budgets.

---

## Open Questions

1. Multi-region canary scope — AKS Fleet integration?
