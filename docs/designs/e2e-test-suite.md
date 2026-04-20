# Plan: Unified Go/Ginkgo E2E Suite for DocumentDB Operator

## Problem

Four independent black-box test workflows exercise overlapping parts of the operator, each
with its own bash glue, port-forward logic, and inline mongosh/Python assertions:

| Workflow | What it covers today |
|---|---|
| `test-integration.yml` | Port-forward + mongosh comprehensive JS + pymongo heredoc |
| `test-E2E.yml` | Port-forward + `comprehensive_mongosh_tests.js` + `performance_test.js` + status/PV/mount checks |
| `test-backup-and-restore.yml` | Seed data → ScheduledBackup → wait → delete data → restore CR → validate |
| `test-upgrade-and-rollback.yml` | Install released operator → seed → Helm upgrade to built → verify → recreate → verify again |

Pain points: port-forward lifecycle re-implemented everywhere; assertion logic is JS
(`throw new Error`) or homegrown Python — no JUnit; no coverage for update/scale/delete-reclaim/
TLS modes/ChangeStreams/service exposure/PV recovery; heavy operations (cluster creation
~60–120 s) are repeated per workflow and per test; two toolchains (bash + JS + Python) for
contributors to navigate on top of the Go operator code.

## Proposed Approach

Build **one unified Go + Ginkgo v2 + Gomega E2E suite** that drives the operator
end-to-end, reusing CNPG's `tests/utils/` Go packages wherever possible. Tests are grouped
by CRD operation in per-area Go packages; the data plane is validated via
`go.mongodb.org/mongo-driver/v2`. The suite fully replaces the four workflows.

### Why Go over Python

Spike result (see `Spike Findings` below): ~20 CNPG util packages are directly reusable
because DocumentDB wraps the same `apiv1.Cluster` / `apiv1.Backup` CRs CNPG defines.
Reusing them deletes a large fraction of the infrastructure we were about to rebuild
(MinIO deploy, namespace management, envsubst, stern log streaming, CNPG Cluster
introspection, backup CR helpers, timeouts map).

### Design principles

1. **Amortize heavy lifting.** Cluster creation (~60–120 s per 1-instance cluster) is the
   single biggest cost. Classify every spec as *read-only* or *mutating*. Read-only specs
   share a session-scoped cluster and isolate via per-spec Mongo database names; only
   mutating specs pay for a fresh cluster.
2. **Small, single-purpose tests.** Each `It(...)` asserts one behavior. Porting
   `comprehensive_mongosh_tests.js` produces ~10 small specs, not one monolith.
3. **Parallelize safely.** Ginkgo `-p` (process-per-package) + worker-aware namespace
   naming. Marker/label-grouped CI jobs add a second parallelism layer.
4. **Structure for growth.** Per-area Go packages + shared `pkg/e2eutils/` + composable
   manifest fragments. Adding a new CRD field = one new package, not sprawl.

### Stack

- **Ginkgo v2 + Gomega** — BDD runner + matchers. Same framework the operator already
  uses for `envtest`, so contributors share patterns and caches.
- **`sigs.k8s.io/controller-runtime/pkg/client`** — typed CR access via our `api/preview`
  types (no dynamic client / unstructured dicts).
- **`go.mongodb.org/mongo-driver/v2`** — data-plane assertions.
- **CNPG `tests/utils/`** — imported as a library (Apache-2.0, compatible with our MIT).
  Pin version in `go.mod`.
- **`github.com/cloudnative-pg/cloudnative-pg/tests/labels`** + `tests/levels` — import the
  depth/label plumbing rather than re-implementing.

### Layout

```
test/e2e/                                       # new top-level Go test tree
├── go.mod                                      # separate module; pins CNPG utils version
├── README.md                                   # local run instructions
├── suite_test.go                               # SynchronizedBeforeSuite, global fixtures
├── labels.go                                   # our label taxonomy (wraps CNPG's)
├── levels.go                                   # thin re-export of CNPG's levels
├── pkg/e2eutils/                               # our helpers; each file <300 LOC
│   ├── testenv/
│   │   └── env.go                              # wraps CNPG's TestingEnvironment with dummy PG values
│   ├── documentdb/
│   │   └── documentdb.go                       # CR verbs: Create, PatchSpec, WaitHealthy, Delete
│   ├── mongo/
│   │   └── client.go                           # MongoClient builder, Seed, Probe, Count
│   ├── assertions/
│   │   └── assertions.go                       # AssertDocumentDBReady, AssertPrimaryUnchanged, AssertWalLevel, …
│   ├── timeouts/
│   │   └── timeouts.go                         # DocumentDB-specific overrides atop CNPG's map
│   ├── seed/
│   │   └── datasets.go                         # canonical datasets (Small, Medium, Sort, Agg)
│   ├── portforward/
│   │   └── portforward.go                      # wraps CNPG's forwardconnection for Mongo port
│   ├── operatorhealth/
│   │   └── gate.go                             # adapted from CNPG's operator/ for documentdb-operator ns
│   └── fixtures/                               # shared cluster fixtures (session scope)
│       ├── shared_ro.go                        # 1-instance cluster, per-spec DB names
│       ├── shared_scale.go                     # 2-instance cluster; tests reset to 2 on teardown
│       └── minio.go                            # lazy, label-gated (wraps CNPG minio.Deploy)
├── manifests/                                  # .yaml.template files; CNPG envsubst expands
│   ├── base/
│   │   └── documentdb.yaml.template
│   ├── mixins/                                 # concatenated into base; simple sh envsubst pipeline
│   │   ├── tls_disabled.yaml.template
│   │   ├── tls_selfsigned.yaml.template
│   │   ├── tls_certmanager.yaml.template
│   │   ├── tls_provided.yaml.template
│   │   ├── feature_changestreams.yaml.template
│   │   ├── exposure_loadbalancer.yaml.template
│   │   ├── exposure_clusterip.yaml.template
│   │   ├── storage_custom.yaml.template
│   │   └── reclaim_retain.yaml.template
│   └── backup/
│       ├── backup.yaml.template
│       ├── scheduled_backup.yaml.template
│       ├── recovery_from_backup.yaml.template
│       └── recovery_from_pv.yaml.template
└── tests/                                      # per-area Go packages; Ginkgo `-p` = 1 proc/pkg
    ├── lifecycle/
    │   ├── lifecycle_suite_test.go
    │   ├── deploy_test.go
    │   ├── update_image_test.go
    │   ├── update_loglevel_test.go
    │   ├── update_storage_test.go
    │   └── delete_reclaim_test.go
    ├── scale/
    │   ├── scale_suite_test.go                 # spins up shared_scale_cluster
    │   ├── scale_up_test.go                    # 1→2, 2→3
    │   └── scale_down_test.go                  # 3→2, 2→1; primary re-election
    ├── data/                                   # all read-only; shares ro cluster
    │   ├── data_suite_test.go                  # spins up shared_ro_cluster
    │   ├── crud_test.go
    │   ├── query_test.go
    │   ├── aggregation_test.go
    │   ├── sort_limit_skip_test.go
    │   ├── update_ops_test.go
    │   ├── delete_ops_test.go
    │   └── pipeline_test.go
    ├── performance/                            # read-only; shares ro cluster; serial (-procs=1)
    │   ├── performance_suite_test.go
    │   ├── perf_insert_test.go
    │   ├── perf_count_range_test.go
    │   ├── perf_aggregation_test.go
    │   ├── perf_sort_test.go
    │   ├── perf_update_test.go
    │   └── perf_delete_drop_test.go
    ├── backup/
    │   ├── backup_suite_test.go                # spins up minio
    │   ├── backup_ondemand_test.go
    │   ├── backup_scheduled_test.go
    │   ├── restore_from_backup_test.go
    │   └── restore_from_pv_test.go
    ├── tls/
    │   ├── tls_suite_test.go
    │   ├── tls_disabled_test.go
    │   ├── tls_selfsigned_test.go
    │   ├── tls_certmanager_test.go             # skipped via Label("needs-certmanager")
    │   └── tls_provided_test.go
    ├── feature_gates/
    │   ├── feature_gates_suite_test.go
    │   └── changestreams_test.go               # table-driven over (enabled/disabled)
    ├── exposure/
    │   ├── exposure_suite_test.go
    │   ├── clusterip_test.go
    │   └── loadbalancer_test.go                # Label("needs-metallb")
    ├── status/
    │   ├── status_suite_test.go                # shared_ro_cluster
    │   ├── connection_string_test.go
    │   ├── pv_name_test.go
    │   └── mount_options_test.go
    └── upgrade/
        ├── upgrade_suite_test.go               # owns its own operator install; Label("disruptive")
        ├── upgrade_control_plane_test.go       # released chart → built chart, verify data
        ├── upgrade_images_test.go              # extension + gateway image bump
        └── rollback_test.go                    # optional — if rollback is supported
```

### Fixture tiers (dedup heavy lifting)

All fixtures read config from env + CLI options (`--kube-context`, `--operator-ns`,
`--image-tag`, `--chart-version`, `--test-depth`, `--keep-clusters`). Ginkgo uses `flag`
registration; env vars mirror flags.

**Session-scoped (most expensive, created once per `go test` invocation of a package):**

- `Env` — wraps `environment.TestingEnvironment` from CNPG, constructed in
  `SynchronizedBeforeSuite`. `POSTGRES_IMG` set to a dummy value because we don't use
  the `postgres/` helpers that read it.
- `OperatorReady` — one-time check the documentdb-operator Deployment is Available +
  CRDs installed.
- `SharedROCluster` — 1-instance DocumentDB, created once per package that imports it.
  Consumed by `data/`, `performance/`, `status/`. **Read-only-by-convention**: each spec
  uses its own Mongo database `db_<hash(ginkgo.CurrentSpec().Text)>`. The fixture wraps
  the CR handle in a read-only proxy that panics on `PatchSpec`/`Delete`.
- `SharedScaleCluster` — 2-instance cluster used as starting state for `scale/`. Tests
  reset instance count to 2 in `AfterEach` so the cluster is reusable.
- `Minio` — lazy session fixture in `backup/backup_suite_test.go`; calls
  `cnpgminio.Deploy` only if the package is selected.

**Per-spec (cheap or mutating), constructed in `BeforeEach`:**

- `FreshDocumentDB(spec *apiv1preview.DocumentDB)` — factory used by lifecycle/tls/
  feature/exposure/backup/upgrade. Unique namespace, wait healthy, register cleanup via
  `DeferCleanup`.
- `MongoClient(documentdb)` — mongo-driver client bound to the CR's service via a
  Ginkgo-owned port-forward.
- `TmpNamespace()` — `e2e-<procId>-<hash>`, auto-deleted.

**Auto-applied:**

- `operatorhealth.Gate` — invoked from `BeforeEach` and `AfterEach` of a top-level
  `Describe` in `suite_test.go`. Snapshots operator pod UID + restart count; if it
  churned, all subsequent non-`disruptive`/`upgrade` specs are **skipped** via a
  package-global sentinel. Adapted from CNPG's `operator/` package, retargeted to our
  `documentdb-operator` namespace and image.

**Dedup summary:**

| Test area | Cluster source | Wall-time saving vs all-fresh |
|---|---|---|
| `data/` (7 specs) | `SharedROCluster` | ~10 min |
| `performance/` (6 specs) | `SharedROCluster` | ~9 min |
| `status/` (3 specs) | `SharedROCluster` | ~5 min |
| `scale/` (4 specs) | `SharedScaleCluster` | ~5 min |
| `lifecycle/`, `tls/`, `feature_gates/`, `exposure/`, `backup/`, `upgrade/` | `FreshDocumentDB` | N/A (need isolation) |

### Parallelism

- `ginkgo -p ./tests/...` — one process per package. `SharedROCluster` is created once
  per `data/` / `performance/` / `status/` process (acceptable, Ginkgo cannot share
  across processes without external coordination).
- Within a package: Ginkgo defaults to serial within a process. For `data/` we enable
  `--procs=N` and use `BeforeAll` (ordered container) so the cluster is created once per
  process while specs run in parallel against their own DBs.
- Per-process naming: namespaces `e2e-<GINKGO_PARALLEL_PROCESS>-<hash>`, DBs
  `db_<proc>_<hash>`, cluster names `ro-<proc>`.
- CI: marker-grouped GitHub Actions jobs run in parallel; within each job, Ginkgo
  parallelizes at the process level.
- Performance job forces `--procs=1` so timing thresholds aren't noisy.
- Upgrade job forces `--procs=1` (disruptive; owns its own operator install).

### Level/depth control

- Import CNPG's `tests/levels` package. Every top-level `Describe`/`Context` adds a
  level tag via `Label(levels.Medium.String())` (or Highest/High/Low/Lowest).
- `TEST_DEPTH=N` env var — reused as-is from CNPG's plumbing.
- Default depth = Medium. Smoke CI job uses Highest; nightly uses Lowest.

### Labels (replaces "markers")

Ginkgo labels, applied via `Label("…")` on `Describe`/`Context`/`It` and filtered via
`--label-filter`. We wrap CNPG's `tests/labels.go` and add DocumentDB-specific ones:

```go
// labels.go
const (
    // Functional area (one per package via suite_test Describe label)
    LifecycleLabel     = "lifecycle"
    ScaleLabel         = "scale"
    DataLabel          = "data"
    PerformanceLabel   = "performance"
    BackupLabel        = "backup"
    RecoveryLabel      = "recovery"
    TLSLabel           = "tls"
    FeatureLabel       = "feature"
    ExposureLabel      = "exposure"
    StatusLabel        = "status"
    UpgradeLabel       = "upgrade"

    // Cross-cutting
    SmokeLabel       = "smoke"
    BasicLabel       = "basic"
    DestructiveLabel = "destructive"  // mutates cluster data
    DisruptiveLabel  = "disruptive"   // may break operator; exempt from health gate
    SlowLabel        = "slow"         // >5 min

    // Prereqs — tests with these labels Skip() if env missing
    NeedsMinioLabel       = "needs-minio"
    NeedsCertManagerLabel = "needs-certmanager"
    NeedsMetalLBLabel     = "needs-metallb"
)
```

### Manifests — base + mixin templates

Plain text files expanded by `cnpgenvsubst.Envsubst` (from `tests/utils/envsubst`).
Composition is done in Go:

```go
// pkg/e2eutils/documentdb/documentdb.go
func RenderCR(name, ns string, mixins []string, vars map[string]string) ([]byte, error) {
    parts := []string{"manifests/base/documentdb.yaml.template"}
    for _, m := range mixins {
        parts = append(parts, "manifests/mixins/"+m+".yaml.template")
    }
    return envsubst.Expand(concatFiles(parts), vars)
}
```

No Jinja2; `envsubst` is enough for our CRs, and it matches what CNPG uses so mental
model is shared.

### Assertions & timeouts

- `pkg/e2eutils/assertions/assertions.go` — Gomega-wrapped verbs:
  `AssertDocumentDBReady`, `AssertInstanceCount`, `AssertPrimaryUnchanged`,
  `AssertPVCCount`, `AssertTLSSecretReady`, `AssertWalLevel`, `AssertServiceType`,
  `AssertConnectionStringMatches`. Each returns `func()` suitable for
  `Eventually(...).Should(Succeed())`.
- `pkg/e2eutils/timeouts/timeouts.go` — starts from
  `cnpgtimeouts.Timeouts()`, overrides/adds DocumentDB-specific ops:
  ```go
  type Op string
  const (
      DocumentDBReady   Op = "documentdb-ready"
      DocumentDBUpgrade Op = "documentdb-upgrade"
      InstanceScale     Op = "instance-scale"
      PVCResize         Op = "pvc-resize"
  )
  func For(op Op) time.Duration { … }
  ```

### CI Workflow

One workflow `test-e2e.yml` with amd64+arm64 matrix. Within each matrix row, marker-grouped
jobs in parallel:

| CI job | `--label-filter` | `ginkgo --procs` | Runner |
|---|---|---|---|
| `smoke` | `smoke` | auto | ubuntu-latest |
| `lifecycle` | `lifecycle` | auto | ubuntu-latest |
| `scale` | `scale` | 2 | ubuntu-latest |
| `data` | `data` | auto | ubuntu-latest |
| `performance` | `performance` | 1 | ubuntu-latest (dedicated) |
| `backup` | `backup` | 2 | ubuntu-latest |
| `tls` | `tls` | auto | ubuntu-latest |
| `feature` | `feature \|\| exposure \|\| status` | auto | ubuntu-latest |
| `upgrade` | `upgrade` | 1 | ubuntu-latest |

Each job: setup kind → install operator (existing `setup-test-environment` action) →
`ginkgo -r --label-filter="…" --procs=N --junit-report=junit.xml ./tests/...` → upload
JUnit + logs. `workflow_dispatch` inputs: `label`, `depth`, `keep_clusters`.

### Fate of Existing Artifacts

**Delete** after the new suite is green in CI for one full run:
- `.github/workflows/{test-integration,test-E2E,test-backup-and-restore,test-upgrade-and-rollback}.yml`
- `.github/actions/setup-port-forwarding/`
- `operator/src/scripts/test-scripts/{test-mongodb-connection.sh,test-python-pymongo.sh,mongo-python-data-pusher.py,comprehensive_mongosh_tests.js,performance_test.js}`

**Keep:**
- `.github/actions/setup-test-environment/`, `.github/actions/collect-logs/`
- `operator/src/scripts/test-scripts/deploy-csi-driver.sh` (infra prep)
- Go unit/envtest suite — out of scope.

### Scope Boundaries

- In scope: single-cluster operations on kind; all CRD spec fields + CRs.
- Out of scope: cross-cluster replication, multi-cloud, AKS/EKS-specific LB annotations,
  Azure Fleet — stays in `documentdb-playground/`.
- Operator install/uninstall is in `setup-test-environment`; the suite assumes a running
  operator. `tests/upgrade/` owns its two-phase install.

### Module layout (go.mod placement)

`test/e2e/` is a **separate Go module** (own `go.mod`). Reasons:
- Pulls in CNPG test utils + Ginkgo + mongo-driver without polluting the operator's
  runtime dependencies.
- Lets us iterate on test deps without triggering operator builds.
- Matches how CNPG itself is organized (`tests/e2e/`).

## Spike findings

**Repo investigated:** `github.com/cloudnative-pg/cloudnative-pg` @ main, `tests/utils/`.
**License:** Apache-2.0 (compatible with our MIT; no NOTICE file).
**API stability:** `tests/utils/*` is public (not `internal/`) but has no stability
contract — expect occasional churn at CNPG version bumps; pin version in `go.mod`.

Reusability tally of the 29 `tests/utils/*` packages:

| Status | Packages | Count |
|---|---|---|
| ✅ Direct reuse | `clusterutils`, `minio`, `backups`, `timeouts`, `namespaces`, `pods`, `services`, `storage`, `secrets`, `yaml`, `envsubst`, `exec`, `run`, `logs`, `objects`, `sternmultitailer`, `forwardconnection`, `nodes`, `endpoints`, `deployments` | ~20 |
| ⚠️ Adapt | `environment.TestingEnvironment` (PG-coupled; construct with dummy POSTGRES_IMG), `operator` (retarget to `documentdb-operator` namespace) | 2 |
| ❌ Skip | `postgres`, `replicationslot`, `fencing`, `importdb`, `cloudvendors`, `openshift`, `proxy`, `azurite` | ~7 |

Key enabling fact: DocumentDB's operator **wraps CNPG's `apiv1.Cluster` and
`apiv1.Backup`** — so `clusterutils.GetPrimary`, `clusterutils.GetReplicas`,
`backups.Create`, `backups.AssertBackupConditionInClusterStatus` work on our resources as-is.

## Todos

### Phase 0 — Spike verification (new)

1. `cnpg-utils-probe` — Write 30-line `cmd/probe/main.go` that constructs
   `environment.TestingEnvironment` with dummy PG env vars, calls
   `clusterutils.GetPrimary` on a live DocumentDB cluster in kind, confirms compile + run.
   Gate for the rest of Phase 1.

### Phase 1 — Scaffolding & helpers

2. `scaffold` — `test/e2e/` tree, separate `go.mod` (pinning CNPG utils version), Ginkgo
   suite boilerplate, `labels.go`, re-export of CNPG `levels.go`, CLI flag plumbing,
   area-package skeleton with empty `*_suite_test.go` in each.
3. `testenv` — `pkg/e2eutils/testenv/env.go`: constructor that wraps
   `environment.NewTestingEnvironment()` with dummy `POSTGRES_IMG`; exposes our typed
   `client.Client` with `api/preview` scheme registered.
4. `helpers-documentdb` — `documentdb.go`: `Create`, `PatchSpec`, `WaitHealthy`,
   `Delete`, `List`, `RenderCR` (base+mixin envsubst pipeline).
5. `helpers-mongo` — `mongo/client.go`: `NewClient(host, port, user, pw, tls)`,
   `Seed(ctx, db, n)`, `Ping`, `Count`.
6. `helpers-portforward` — `portforward.go`: thin wrapper over CNPG's
   `forwardconnection` targeting the DocumentDB gateway port.
7. `helpers-assertions` — `assertions.go`: `AssertDocumentDBReady`,
   `AssertInstanceCount`, `AssertPrimaryUnchanged`, `AssertPVCCount`,
   `AssertTLSSecretReady`, `AssertWalLevel`, `AssertServiceType`,
   `AssertConnectionStringMatches`. Each returns `func() error` for `Eventually`.
8. `helpers-timeouts` — `timeouts.go`: extends CNPG's map with DocumentDB ops.
9. `helpers-seed` — `seed/datasets.go`: `SmallDataset(10)`, `MediumDataset(1000)`,
   `SortDataset`, `AggDataset` — reused by data/performance/backup/upgrade.
10. `operator-health-gate` — `operatorhealth/gate.go`: adapted from CNPG's `operator/`
    package for `documentdb-operator` ns; `BeforeEach`/`AfterEach` hooks + package
    sentinel to skip subsequent specs on churn.
11. `shared-fixtures` — `pkg/e2eutils/fixtures/`: `shared_ro.go`, `shared_scale.go`,
    `minio.go` (wraps CNPG `minio.Deploy`, lazy-constructed).
12. `manifests-base` — `manifests/base/documentdb.yaml.template` + all mixins under
    `manifests/mixins/` and `manifests/backup/`.
13. `suite-root` — `suite_test.go`: `SynchronizedBeforeSuite` builds `Env`, installs
    lazy MinIO hook, starts stern log tailer, registers operator-health gate.

### Phase 2 — Test packages (one per area)

14. `tests-data` — `data_suite_test.go` spins up `SharedROCluster`; port
    `comprehensive_mongosh_tests.js` + pymongo heredoc, **split** into 7 spec files.
    Package label `DataLabel`.
15. `tests-performance` — 6 spec files, one per timed op; shares `SharedROCluster`;
    forced serial in CI. Thresholds preserved.
16. `tests-status` — 3 spec files; shares `SharedROCluster`.
17. `tests-lifecycle` — 5 spec files; each owns its own `FreshDocumentDB`.
18. `tests-scale` — `scale_suite_test.go` with `SharedScaleCluster`; up + down spec
    files; each `AfterEach` resets to 2 instances.
19. `tests-backup` — `backup_suite_test.go` owns `Minio`; 4 spec files.
20. `tests-tls` — 4 spec files, one per mode. CertManager file uses
    `NeedsCertManagerLabel`.
21. `tests-feature-gates` — `changestreams_test.go` table-driven over (enabled, disabled).
22. `tests-exposure` — ClusterIP + LoadBalancer spec files; LB uses `NeedsMetalLBLabel`.
23. `tests-upgrade` — `upgrade_suite_test.go` with multi-phase install helpers; **split**
    into 2–3 spec files so failures pinpoint the phase.

### Phase 3 — Integration

24. `local-run` — Full suite green locally on kind at `TEST_DEPTH=Medium` with `ginkgo -p`.
25. `ci-workflow` — `.github/workflows/test-e2e.yml`: amd64+arm64 matrix, label-grouped
    jobs per table above, `workflow_dispatch` inputs.
26. `cleanup-workflows` — Delete the 4 old workflows + `setup-port-forwarding` composite.
27. `cleanup-scripts` — Delete old bash/JS/Python test scripts.
28. `docs` — Update `docs/developer-guides/` + AGENTS.md: tree, local run (`ginkgo -p
    ./tests/...`), labels, levels, how to add a new area / mixin / assertion; CHANGELOG
    migration note; document the CNPG utils dependency + pin policy.

## Comparison: Our Plan vs CloudNative-PG E2E Suite

| Aspect | CNPG | Our plan | Decision |
|---|---|---|---|
| Language | Go (Ginkgo+Gomega) | Go (Ginkgo+Gomega) | **Aligned.** |
| Test selection | 28 labels + TEST_DEPTH | Our labels + **imported** `tests/levels` | Aligned; we re-export CNPG's levels. |
| Matrix (K8s×PG×engine) | full 3-D | amd64/arm64 only | Defer to GA. |
| Cluster bring-up | `hack/setup-cluster.sh` | existing `setup-test-environment` action | Keep ours. |
| Session-scoped MinIO | yes (`minio.Deploy`) | **imported as-is** from CNPG | Adopted verbatim. |
| Operator health gate | yes (`BeforeEach` pod check) | `operatorhealth/gate.go` — adapted from CNPG `operator/` | Adapted (ns retargeted). |
| Shared cluster for read-only | implicit per-namespace | explicit `SharedROCluster` + read-only proxy | **We go further.** |
| Assertion composables | `AssertClusterIsReady`, etc. | `pkg/e2eutils/assertions` | Aligned. |
| Manifest templating | `envsubst` over `.yaml.template` | `envsubst` over `.yaml.template` | **Imported.** |
| Per-op timeouts | `Timeouts()` map | extends CNPG's map | **Imported + extended.** |
| Parallelism | `ginkgo -p` + within-pkg procs | `ginkgo -p` + within-pkg procs + label-grouped CI | Two-layer. |
| Stern log streaming | yes | **imported** (`sternmultitailer`) | Adopted. |
| Label filter (`/test` comment) | yes | `workflow_dispatch` inputs | Defer. |

### Not copying, with rationale

- **Multi-engine** (k3d/EKS/AKS/GKE/OpenShift) — defer to GA.
- **Branch-snapshot operator install** from artifacts repo — we build in the same workflow.
- **postgres/** helpers — we speak Mongo, not libpq.

## Open Questions / Risks

- **CNPG utils API churn**: pinned version mitigates but doesn't eliminate. Budget ~½ day
  per CNPG bump for test-util compat fixes. Document in contribute guide.
- **Dummy `POSTGRES_IMG`** in `testenv.Env` feels brittle; if CNPG starts *eagerly*
  validating the image in `NewTestingEnvironment`, we'd need to fork. Check on first
  probe; fallback plan is to copy the constructor (~100 LOC).
- **Read-only proxy enforcement**: making sure tests can't accidentally call
  `PatchSpec` on `SharedROCluster`. The proxy panics at runtime — acceptable; maybe add
  a linter later.
- **Backup object store**: confirm `test-backup-and-restore.yml` uses MinIO (likely) so
  CNPG's `minio.Deploy` is a drop-in. Verify during Phase 0 probe.
- **MetalLB / SC expansion / cert-manager**: label-gated skips; document the env
  contract in README.
- **Ginkgo parallelism across processes** can't share `SharedROCluster`; acceptable
  cost (we pay for one cluster per Go process in `data/`+`performance/`+`status/` =
  3 clusters max per CI job instead of 1). Lower than the N-per-spec baseline we're
  replacing.
- **Total CI wallclock**: budget review after first full run.
- **Rubber-duck review**: after Phase 0 (probe) + Phase 1 (scaffold + helpers +
  suite_test) + one populated area (e.g. `tests/data/`), review shape before building
  the rest.
