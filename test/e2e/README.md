# DocumentDB Operator E2E Test Suite

## What this is

A unified Go / Ginkgo v2 / Gomega end-to-end test suite that drives the
DocumentDB Kubernetes Operator against a real Kubernetes cluster. It replaces the four
legacy GitHub Actions workflows (`test-integration.yml`, `test-E2E.yml`,
`test-backup-and-restore.yml`, `test-upgrade-and-rollback.yml`) and their
bash / JavaScript (mongosh) / Python (pymongo) glue with a single Go module
at `test/e2e/`. Specs are organised by CRD operation (lifecycle, scale, data,
performance, backup, tls, feature gates, exposure, status, upgrade), reuse
CloudNative-PG's `tests/utils` packages as a library, and speak the Mongo
wire protocol via `go.mongodb.org/mongo-driver/v2`. Design rationale and
scope: [`docs/designs/e2e-test-suite.md`](../../docs/designs/e2e-test-suite.md).

## Prereqs

| Tool | Version | Notes |
|---|---|---|
| Go | 1.26.x (match `test/e2e/go.mod` — currently `go 1.26.5`) | Separate module from the operator |
| Docker | any recent | Required for kind |
| kind | any recent | Local Kubernetes |
| kubectl | matching the target Kubernetes cluster | |
| helm | 3.x | Operator install |
| `ginkgo` CLI | v2 | `go install github.com/onsi/ginkgo/v2/ginkgo@latest` |

The suite itself installs no Kubernetes-cluster components — it expects an already-running
Kubernetes cluster with the operator deployed. Backup specs additionally need the CSI
snapshot CRDs; TLS cert-manager specs need cert-manager. Both gate with a
runtime probe and `Skip()` rather than fail when the dependency is missing.

The dedicated `tests/multicluster_postgres_tls` package is an exception to
skip-on-prerequisite handling: both clusters and all dependencies are required,
and all five acceptance specs must run regardless of `TEST_DEPTH`.

## Quick start

From the repository root:

```bash
# 1. Build images + bring up a kind cluster + install the operator + CRDs.
#    The script in scripts/development/deploy.sh drives `make deploy` and the
#    same composite action (.github/actions/setup-test-environment) CI uses.
cd operator/src
DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh
cd -

# 2. Run the smoke label against that Kubernetes cluster.
cd test/e2e
ginkgo -r --label-filter=smoke ./tests/...
```

Run a single area:

```bash
ginkgo -r --label-filter=lifecycle ./tests/...
ginkgo -r --label-filter='data && level:low' ./tests/data
```

## Multi-cluster PostgreSQL TLS

`tests/multicluster_postgres_tls` covers only `spec.tls.postgres`: the four
provided certificate Secret fields, external-cluster CA/client Secret selectors, verified
cross-cluster PostgreSQL connections, TLS replication identity/serials from
`pg_stat_ssl`, exact document replication, independent rejection of an
untrusted server CA, missing hostname SAN, and untrusted replication client,
and live server/client certificate rotation under unchanged CAs.
`tests/tls` separately covers `serverAltDNSNames` with CNPG-managed server
certificates: CNPG rejects that field when `serverTLSSecret` is provided.

The topology is two **independent one-node Kind Kubernetes clusters** on the
same Docker network. The supported `crossCloudNetworkingStrategy: None`
delegates networking to the caller: the tests create NodePort services on each
cluster and selectorless Services/EndpointSlices targeting the *other*
cluster's node IP and NodePort. Kubernetes provides DNS for the actual
external-cluster names, included in the server SANs. There are no
same-cluster ExternalName bridges, service mesh, TLS termination, or disabled
PostgreSQL certificate verification. A verify-full connection from a probe pod
in the replica cluster checks DNS, reachability, and certificate authentication
before replica bootstrap.

Build operator and sidecar images from the current branch and supply the same
images/chart to both clusters. `provision-postgres-tls.sh` installs Kind 1.35,
cert-manager 1.19.2, and the chart's CNPG dependency; it does not deploy a
DocumentDB fixture. Its required environment variables are:

| Variable | Purpose |
|---|---|
| `E2E_CLUSTER_PREFIX` | Unique names `<prefix>-primary` and `<prefix>-replica` |
| `E2E_KUBECONFIG_DIR` | Private directory **outside** the artifacts directory |
| `E2E_CHART` | Built Helm chart archive or local chart with dependencies restored |
| `OPERATOR_IMAGE`, `SIDECAR_IMAGE` | Locally available built-from-branch images |
| `DOCUMENTDB_IMAGE`, `GATEWAY_IMAGE` | Locally available database runtime images |

On hosts with a low inotify instance limit (often 128), two Kind clusters may
exhaust it and cause kubelet's `inotify_init: too many open files` failure.
Raise `fs.inotify.max_user_instances` to at least 1024 before provisioning,
with the host administrator's approval; restore the original value after
cleanup. The dedicated CI job handles this on its isolated runner.

From the repository root, after setting those variables:

```bash
export E2E_PRIMARY_KUBECONFIG="$E2E_KUBECONFIG_DIR/primary.yaml"
export E2E_REPLICA_KUBECONFIG="$E2E_KUBECONFIG_DIR/replica.yaml"
export E2E_ARTIFACTS_DIR="$PWD/test/e2e/artifacts"
bash test/e2e/scripts/provision-postgres-tls.sh
cd test/e2e
ginkgo --procs=1 --timeout=65m --label-filter='tls && multi-cluster-postgres-tls' \
  --junit-report=junit.xml --json-report=report.json --output-dir=artifacts \
  ./tests/multicluster_postgres_tls
python3 scripts/check-postgres-tls-report.py artifacts/report.json
```

On failure, `postgres-tls-diagnostics.sh` collects state, events, and logs from
both explicit kubeconfigs; failed specs also collect before deleting fixtures.
The collector whitelists resource types and exports only public certificate
metadata, never Secret contents, private keys, or kubeconfig credentials.
Ephemeral signing keys remain in process memory. After saving reports, always
delete **both** dedicated Kind clusters and their kubeconfig files, including
after a provisioning failure:

```bash
kind delete cluster --name "$E2E_CLUSTER_PREFIX-primary"
kind delete cluster --name "$E2E_CLUSTER_PREFIX-replica"
rm -f "$E2E_PRIMARY_KUBECONFIG" "$E2E_REPLICA_KUBECONFIG"
```

The `multicluster-postgres-tls` CI job runs all five specs serially at the
default Medium depth on amd64 and arm64 for PRs and pushes to `main`. It uses
the same branch-built image/chart artifacts in both clusters, rejects
missing/skipped/filtered specs via the JSON report checker, publishes JUnit and
safe diagnostics before cleanup, and always removes both clusters.

## Layout

```
test/e2e/
├── go.mod, go.sum            # separate module; pins CNPG test utils
├── suite.go                  # SetupSuite / TeardownSuite; env + run-id wiring
├── suite_test.go             # SynchronizedBeforeSuite entry point
├── labels.go                 # Ginkgo label constants (area + cross-cutting)
├── levels.go                 # TEST_DEPTH → Level gate (CurrentLevel, SkipUnlessLevel)
├── runid.go                  # E2E_RUN_ID resolver (stable per-process id)
├── manifests/
│   ├── base/                 # documentdb.yaml.template — the base CR
│   ├── mixins/               # composable overlays (tls_*, exposure_*, storage_*, …)
│   └── backup/               # backup / scheduled_backup / recovery CR templates
├── pkg/e2eutils/             # helper packages imported by every area suite
└── tests/                    # one Go package per functional area
    ├── lifecycle/  scale/  data/  performance/  status/
    ├── backup/  tls/  feature_gates/  exposure/  upgrade/
```

## Labels & depth

Labels live in [`labels.go`](labels.go) and are attached either to the area
suite's top-level `Describe` (area labels) or to individual specs (cross-cutting
and capability labels).

| Group | Labels |
|---|---|
| Area | `lifecycle`, `scale`, `data`, `performance`, `backup`, `recovery`, `tls`, `feature-gates`, `exposure`, `status`, `upgrade` |
| Cross-cutting | `smoke`, `basic`, `destructive`, `disruptive`, `slow` |
| Capability | `needs-cert-manager`, `needs-metallb`, `needs-csi-snapshots`, `needs-csi-resize` |
| Depth | `level:lowest`, `level:low`, `level:medium`, `level:high`, `level:highest` |

**Depth gate.** `TEST_DEPTH` accepts an integer `0`–`4` or a case-insensitive name
(`Highest`, `High`, `Medium`, `Low`, `Lowest`) mapping to `Highest` (0) … `Lowest` (4).
Default is `Medium` (2) — the authoritative gate is `e2e.SkipUnlessLevel(e2e.Medium)`,
which reads `TEST_DEPTH` at runtime and `Skip()`s when the configured depth is shallower.
Invalid values fall back to `Medium` and emit a one-time warning to GinkgoWriter.
The `level:*` labels are informational duplicates for Ginkgo's `--label-filter`.
The `Level` type and constants are re-exported from CNPG's
[`tests`](https://pkg.go.dev/github.com/cloudnative-pg/cloudnative-pg/tests) package
via type alias; only `SkipUnlessLevel` is local.

Examples:

```bash
# Fast smoke — typically Highest depth
TEST_DEPTH=0 ginkgo -r --label-filter=smoke ./tests/...

# Full backup area at default depth, skipping Kubernetes clusters without CSI snapshot support
ginkgo -r --label-filter='backup && !needs-csi-snapshots' ./tests/backup

# Nightly: everything
TEST_DEPTH=4 ginkgo -r --procs=4 ./tests/...

# Upgrade suite (disruptive — runs serial, owns its own operator install)
E2E_UPGRADE=1 E2E_UPGRADE_PREVIOUS_CHART=… \
  ginkgo --procs=1 --label-filter=upgrade ./tests/upgrade
```

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `TEST_DEPTH` | `Medium` (2) | Depth gate; accepts `0`–`4` or `Highest`/`High`/`Medium`/`Low`/`Lowest` (case-insensitive) |
| `E2E_RUN_ID` | auto-generated | Stable id stamped onto shared fixtures + cluster-scoped objects. Set this in CI so parallel Ginkgo binaries share fixtures; leave **unset locally** — an auto-generated id is safer for ad-hoc runs |
| `E2E_ARTIFACTS_DIR` | `./_artifacts/<RunID>/proc-<N>/` | Override the JUnit / log dump directory |
| `DOCUMENTDB_IMAGE` | chart default | Overrides the extension image used by fresh fixtures |
| `GATEWAY_IMAGE` | chart default | Overrides the gateway image used by fresh fixtures |
| `E2E_STORAGE_CLASS` | Kubernetes cluster default | StorageClass for fresh fixtures |
| `E2E_STORAGE_SIZE` | `1Gi` | PVC size for fresh fixtures |
| `GINKGO_PARALLEL_PROCESS` | set by Ginkgo | Consumed; do not set manually |
| `POSTGRES_IMG` | dummy stub | Set by `testenv` to satisfy CNPG's `TestingEnvironment`; do not override |

**Upgrade area (gated behind `E2E_UPGRADE=1`):**

| Variable | Purpose |
|---|---|
| `E2E_UPGRADE` | Must be `1` or every spec in `tests/upgrade/` Skips |
| `E2E_UPGRADE_PREVIOUS_CHART` | OCI or path ref for the "old" operator chart |
| `E2E_UPGRADE_PREVIOUS_VERSION` | Chart version string for the old chart |
| `E2E_UPGRADE_CURRENT_CHART` | Chart ref for the "new" (built-from-tree) chart |
| `E2E_UPGRADE_CURRENT_VERSION` | Optional — defaults to chart's own version |
| `E2E_UPGRADE_RELEASE` | Helm release name |
| `E2E_UPGRADE_OPERATOR_NS` | Operator namespace |
| `E2E_UPGRADE_OLD_DOCUMENTDB_IMAGE` | Extension image used before upgrade |
| `E2E_UPGRADE_NEW_DOCUMENTDB_IMAGE` | Extension image used after upgrade |
| `E2E_UPGRADE_OLD_DOCUMENTDB_VERSION` | Schema-upgrade spec: `spec.documentDBVersion` to start from (default `0.109.0`) |
| `E2E_UPGRADE_NEW_DOCUMENTDB_VERSION` | Schema-upgrade spec: `spec.documentDBVersion` to upgrade to (default `0.110.0`) |

> A note on `E2E_KEEP_CLUSTERS`: the design doc discusses a flag for keeping
> DocumentDB-cluster fixtures around after a failed spec, but no such knob is
> honored by the current suite code. Skip-on-prereq is the intended mechanism;
> to inspect a DocumentDB cluster (CR) after a failing spec, pass `--fail-fast`
> and manually defer DocumentDB-CR teardown outside the suite.

**Missing prereqs are `Skip()`, not `Fail()`.** Backup specs probe the
`VolumeSnapshot`/`VolumeSnapshotClass` CRDs at runtime (`Skip` when absent),
and `tls/tls_certmanager_test.go` probes the `cert-manager.io/v1` API group
the same way. The capability labels (`needs-csi-snapshots`, `needs-cert-manager`,
`needs-metallb`, `needs-csi-resize`) let you filter these out up front if
you already know your environment.

## Adding a new test

**Adding a spec to an existing area.** Create a new `*_test.go` in
`tests/<area>/`, import the area suite's label, attach the right depth
label, and use the suite's shared fixture rather than a fresh DocumentDB cluster (CR) when
the spec is read-only:

```go
var _ = Describe("my new behavior", Label(e2e.DataLabel), e2e.MediumLevelLabel, func() {
    It("does the thing", func(sctx SpecContext) {
        e2e.SkipUnlessLevel(e2e.Medium)
        // ... sharedROCluster is available via the area's BeforeAll
    })
})
```

**Adding a new area package.** Create `tests/<area>/`, add
`<area>_suite_test.go` that calls `e2e.SetupSuite` / `e2e.TeardownSuite`,
define an area label in `labels.go`, and attach it to the top-level
`Describe`. Mirror an existing area — `tests/status/` is the smallest
reference for read-only areas; `tests/lifecycle/` for mutating ones.

**Adding a new manifest mixin.** Drop a `.yaml.template` under
`manifests/mixins/` and pass its stem via `CreateOptions.Mixins` to
`documentdb.Create`. Note the merge semantics: `RenderCR` produces a
multi-document YAML stream (one doc per template) and `Create` deep-merges
them into a single DocumentDB object before applying — maps merge recursively,
**scalars and slices in later mixins overwrite earlier values**. The public
`RenderCR` still returns the raw multi-doc bytes (useful for artifact dumps
or manual `kubectl apply`).

**Adding a new assertion.** Put the reusable verb in
`pkg/e2eutils/assertions/assertions.go`. Assertions return `func() error`
so callers can wrap them in `Eventually(...).Should(Succeed())`.

## Helper packages (`pkg/e2eutils/`)

| Package | Role |
|---|---|
| `testenv/` | Wraps CNPG's `environment.TestingEnvironment` with dummy `POSTGRES_IMG`; registers our `api/preview` scheme on the typed `client.Client`. |
| `documentdb/` | DocumentDB CR verbs: `RenderCR` (base + mixin envsubst), `Create` (multi-doc merge), `PatchSpec`, `WaitHealthy`, `Delete`, `List`. |
| `mongo/` | `go.mongodb.org/mongo-driver/v2` client builder, seed/probe/count helpers; owns the post-port-forward ping retry budget (`connectRetryTimeout`). |
| `portforward/` | Thin wrapper over CNPG's `forwardconnection` for the DocumentDB gateway port. |
| `assertions/` | Composable Gomega verbs (`AssertDocumentDBReady`, `AssertInstanceCount`, `AssertPrimaryUnchanged`, `AssertPVCCount`, `AssertTLSSecretReady`, `AssertServiceType`, `AssertConnectionStringMatches`). |
| `timeouts/` | DocumentDB-specific overrides layered on top of CNPG's `timeouts` map (`DocumentDBReady`, `DocumentDBUpgrade`, `InstanceScale`, `PVCResize`). |
| `seed/` | Canonical datasets (`SmallDataset(10)`, `MediumDataset(1000)`, sort/agg fixtures) shared by data / performance / backup / upgrade specs. |
| `fixtures/` | Session-scoped shared DocumentDB clusters (`shared_ro.go`, `shared_scale.go`) and lazy MinIO (`minio.go`). Honors `E2E_RUN_ID`, `DOCUMENTDB_IMAGE`, `GATEWAY_IMAGE`, `E2E_STORAGE_CLASS`, `E2E_STORAGE_SIZE`. |
| `namespaces/` | Per-proc, run-id-scoped namespace naming (`e2e-<proc>-<hash>`). |
| `operatorhealth/` | Operator-pod UID + restart-count gate; flips a package sentinel on churn so subsequent non-`disruptive`/`upgrade` specs skip. |
| `clusterprobe/` | Capability probes (CSI snapshot CRDs, cert-manager, StorageClass resize support) used by area `Skip*` helpers. |
| `backup/` | Helpers for asserting `Backup` / `ScheduledBackup` CR state, snapshot readiness, and MinIO object inspection. |
| `tlscerts/` | Self-signed + provided-mode certificate material builders used by `tests/tls/`. |
| `helmop/` | Helm install/upgrade/uninstall for the upgrade suite (multi-phase operator lifecycle). |

## CI

The suite is driven by [`.github/workflows/test-e2e.yml`](../../.github/workflows/test-e2e.yml)
(owned by the CI workflow migration; the file may not yet be present in
every working tree — it is added as part of the Phase 3 rollout). The
workflow fans out into nine label-grouped jobs:

| Job | `--label-filter` | `--procs` |
|---|---|---|
| `smoke` | `smoke` | auto |
| `lifecycle` | `lifecycle` | auto |
| `scale` | `scale` | 2 |
| `data` | `data` | auto |
| `performance` | `performance` | 1 (dedicated runner) |
| `backup` | `backup` | 2 |
| `tls` | `tls` | auto |
| `feature` | `feature-gates \|\| exposure \|\| status` | auto |
| `upgrade` | `upgrade` | 1 |

Each job runs `setup-test-environment` → `ginkgo -r --label-filter=…
--junit-report=junit.xml ./tests/...` → upload JUnit + logs.
`workflow_dispatch` exposes `label` and `depth` inputs for ad-hoc runs.

## Troubleshooting

- **Port-forward / Mongo connect fails with "connection refused."** The
  post-port-forward retry budget is 60 s at 100 ms backoff
  (`mongo/connect.go`: `connectRetryTimeout` / `connectRetryBackoff`). If
  you consistently exceed it, the gateway pod is probably not Ready — check
  the DocumentDB CR status and the gateway container logs.
- **Backup specs all Skip.** Your Kubernetes cluster lacks the CSI snapshot CRDs
  (`VolumeSnapshotClass`, `VolumeSnapshot`) or the configured StorageClass
  isn't backed by a snapshot-capable CSI driver. `scripts/test-scripts/deploy-csi-driver.sh`
  under `operator/src/` installs a hostpath CSI driver suitable for kind.
- **TLS cert-manager spec Skips.** `cert-manager.io/v1` isn't served; install
  cert-manager (the `setup-test-environment` composite does this for you).
- **"E2E_RUN_ID was not set" warning in CI logs.** The suite auto-generates
  a run id, but cross-binary fixture sharing relies on every Ginkgo invocation
  in a CI job seeing the same value. Export `E2E_RUN_ID="${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"`
  (or similar) once at the top of the job.
- **Operator churn aborts subsequent specs.** `operatorhealth.Gate` snapshots
  the operator pod's UID + restart count at suite start; any drift flips a
  package sentinel and skips every subsequent non-`disruptive`/`upgrade` spec.
  This is working as intended — investigate why the operator restarted.

## CNPG dependency & pin policy

The suite imports CloudNative-PG's `tests/utils/*` packages as a library
(Apache-2.0, compatible with our MIT). The version is pinned in
[`go.mod`](go.mod) — currently `github.com/cloudnative-pg/cloudnative-pg
v1.28.3`. `tests/utils/*` is exported (not `internal/`) but has no stability
contract; budget roughly half a day per CNPG version bump for compat fixes
in our wrappers (`testenv`, `operatorhealth`, `portforward`). Bumps should
be single-purpose PRs gated on the full suite.
