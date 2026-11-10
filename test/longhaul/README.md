# Long Haul Tests

Long haul tests validate that DocumentDB Kubernetes Operator clusters remain healthy under
continuous load over extended periods. They run a canary workload that writes and reads data,
performs management operations, and checks for data integrity.

See the [design document](../../docs/designs/long-haul-test-design.md) for architecture and rationale.

## Quick Start

### Prerequisites

- A running Kubernetes cluster with DocumentDB deployed
- `kubectl` configured to access the cluster
- Go 1.26+

> **HA topology required for upgrade / failover ops.** `upgrade-documentdb` and
> `kill-primary-pod` require `spec.instancesPerNode >= 2` (a standby to absorb
> writes). Behaviour when the precondition is unmet differs by mode: in
> **random** mode they auto-skip (no cooldown consumed; re-evaluated on the next
> tick), but in **sequence** mode there is *no* skip — the runner waits for the
> precondition up to `LONGHAUL_RECOVERY_TIMEOUT` and then fails the sequence. So
> run with `instancesPerNode: 2` (or `3`), or place a `scale-up` earlier in the
> sequence, to exercise them. (See the
> [design doc](../../docs/designs/long-haul-test-design.md#ha-preconditions) for
> the rationale.)

### Run the Config Unit Tests

These are fast and require no cluster:

```bash
cd test/longhaul
go test ./config/ -v
```

### Run Locally

Useful for iterating on driver code against a real cluster without rebuilding the
container image. The driver auto-falls back to `~/.kube/config` when not running
in-cluster, so the same binary works in both modes.

You need network reachability from your machine to the DocumentDB gateway port
(10260). If you're behind a firewall that blocks it, use the in-cluster deployment
path below instead.

```bash
cd test/longhaul
NS=documentdb-test-ns

# 1. Port-forward the gateway service in another terminal and leave it running.
kubectl port-forward -n $NS svc/documentdb-service-documentdb-cluster 10260:10260

# 2. Read credentials from the secret the operator created.
USER=$(kubectl get secret documentdb-credentials -n $NS -o jsonpath='{.data.username}' | base64 -d)
PASS=$(kubectl get secret documentdb-credentials -n $NS -o jsonpath='{.data.password}' | base64 -d)

# 3. Run the driver. Override LONGHAUL_MAX_DURATION for short dev iterations.
LONGHAUL_DOCUMENTDB_URI="mongodb://${USER}:${PASS}@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true" \
LONGHAUL_CLUSTER_NAME=documentdb-cluster \
LONGHAUL_NAMESPACE=$NS \
LONGHAUL_MAX_DURATION=5m \
go run ./cmd/longhaul/
```

### Deploy as Kubernetes Deployment (Recommended for Real Runs)

This is the intended deployment model. The test runs inside the cluster with direct
access to the DocumentDB service (no port-forward needed).

**Production path (CI):** the `LONGHAUL - Build Test Driver Image` workflow builds
the image to GHCR; the `LONGHAUL - Deploy Test Driver to AKS` workflow rolls it
onto the cluster using a long-lived ServiceAccount-token kubeconfig stored in the
`LONGHAUL_KUBECONFIG` repo secret. Trigger both via the Actions tab.

**Manual path (one-off / local cluster):**

```bash
# Build from the REPOSITORY ROOT (not test/longhaul/) so the replace
# paths in test/longhaul/go.mod (../shared and ../../operator/src) resolve.
cd <repo-root>

# 1. Build and push the container image (or use the GHCR image from CI).
docker build -t <your-registry>/longhaul-test:latest -f test/longhaul/Dockerfile .
docker push <your-registry>/longhaul-test:latest

# 2. Create the DocumentDB credentials secret
kubectl create secret generic longhaul-documentdb-credentials \
  --from-literal=uri='mongodb://<username>:<password>@documentdb-service-documentdb-cluster.documentdb-test-ns.svc:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true' \
  -n documentdb-test-ns

# 3. Deploy RBAC and Deployment. deployment.yaml has placeholders
#    __OWNER__ and __IMAGE_TAG__ that are normally substituted by the
#    deploy workflow; for a manual apply, sed them yourself or edit
#    the file in place. setup.yaml also has __LONGHAUL_PASSWORD__.
sed -i "s/__LONGHAUL_PASSWORD__/$(openssl rand -base64 24)/" test/longhaul/deploy/setup.yaml
kubectl apply -f test/longhaul/deploy/setup.yaml
kubectl apply -f test/longhaul/deploy/rbac.yaml
sed -e 's|__OWNER__|<your-registry>|g' \
    -e 's|__IMAGE_TAG__|latest|g' \
    test/longhaul/deploy/deployment.yaml | kubectl apply -f -

# 4. Monitor progress
kubectl logs -f deployment/longhaul-test -n documentdb-test-ns

# 5. Check status (Deployment auto-restarts pods on crash, so use
#    the report ConfigMap or alerts as the source of truth for "did
#    the test pass?", not the pod status alone).
kubectl get deployment longhaul-test -n documentdb-test-ns
kubectl get configmap longhaul-report -n documentdb-test-ns -o yaml
```

To roll a new image (e.g. after a code change rebuilt by CI):

```bash
kubectl -n documentdb-test-ns set image deployment/longhaul-test \
  driver=ghcr.io/<owner>/documentdb-kubernetes-operator/longhaul-test:sha-abc1234
kubectl -n documentdb-test-ns rollout status deployment/longhaul-test
```

## Configuration

All configuration is via environment variables.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGHAUL_DOCUMENTDB_URI` | Yes | — | Connection string to the DocumentDB gateway. |
| `LONGHAUL_CLUSTER_NAME` | Yes | — | Name of the target DocumentDB cluster CR. |
| `LONGHAUL_NAMESPACE` | No | `default` | Kubernetes namespace of the target cluster. |
| `LONGHAUL_OPERATOR_NAMESPACE` | No | `documentdb-operator` | Namespace of the DocumentDB operator Deployment (target of the `kill-operator-pod` chaos op). |
| `LONGHAUL_MAX_DURATION` | No | `30m` | Max test duration. Use `0s` for run-until-failure. |
| `LONGHAUL_NUM_WRITERS` | No | `5` | Number of concurrent writers. |
| `LONGHAUL_OPERATION_MODE` | No | `random` | Operation runner: `random`, `sequence`, or `disabled`. |
| `LONGHAUL_OPERATION_SEQUENCE` | No | empty | Comma-separated stable operation names. Required and used only in `sequence` mode; rejected in `random`/`disabled` mode. Whitespace is trimmed, and duplicate or unknown names are rejected. |
| `LONGHAUL_OP_COOLDOWN` | No | `5m` | Minimum spacing between operations. Random mode only — `sequence` mode paces ops by the steady-state/recovery gates. |
| `LONGHAUL_RECOVERY_TIMEOUT` | No | `10m` | Max wait for cluster recovery after an operation. |
| `LONGHAUL_STEADY_STATE_WAIT` | No | `60s` | Continuous healthy duration required by the steady-state gate. |
| `LONGHAUL_MIN_INSTANCES` | No | `1` | Minimum `spec.instancesPerNode` for scale-down operations (CRD lower bound: 1). |
| `LONGHAUL_MAX_INSTANCES` | No | `3` | Maximum `spec.instancesPerNode` for scale-up operations (CRD upper bound: 3). |
| `LONGHAUL_REPORT_INTERVAL` | No | `1h` | How often to write checkpoint reports to ConfigMap. |
| `LONGHAUL_BACKUP_ENABLED` | No | `true` | Enable the ScheduledBackup + retention verifier. |
| `LONGHAUL_BACKUP_SCHEDULE` | No | `0 */6 * * *` | Cron schedule for the canary `ScheduledBackup`. |
| `LONGHAUL_BACKUP_RETENTION_DAYS` | No | `1` | Retention window applied to child backups; also used to derive the retention-leak deadline. |
| `LONGHAUL_BACKUP_VERIFY_INTERVAL` | No | `5m` | How often the backup verifier samples the `ScheduledBackup` and its children. Lower it for short bounded runs (e.g. the smoke gate uses `30s`) so the periodic loop fires several times within the window. |
| `LONGHAUL_RESET_DATA` | No | `false` | If `true`, drop the workload collection on startup. Off by default so a Deployment pod restart preserves durability history. |
| `LONGHAUL_RETAIN_PER_WRITER` | No | `2000000` | Retention window: most-recent verified documents kept per writer before the pruner deletes older ones, bounding disk usage. `0` disables pruning (unbounded growth). |
| `LONGHAUL_PRUNE_INTERVAL` | No | `5m` | How often the pruner trims old documents. Lower it for short bounded runs (e.g. the smoke gate uses `30s`) so a prune fires within the window. |

### Data Protection (ScheduledBackup + retention)

When `LONGHAUL_BACKUP_ENABLED` is true, the driver maintains a `ScheduledBackup`
named `<cluster>-longhaul` (matching the run's schedule/retention; an existing CR
is reconciled in place, never recreated, so backup history is preserved across
restarts and parameter changes) and runs a verifier alongside the workload. The
verifier FAILs the run if backups stop completing for 3 consecutive schedules, or
if an expired backup is not garbage-collected (a retention leak); `skipped`
backups (e.g. on a standby) are tolerated. See the
[design document](../../docs/designs/long-haul-test-design.md#backup-verification)
for exactly what it checks and why.

> **RBAC.** The driver ServiceAccount needs `create`/`get`/`list`/`update` on
> `scheduledbackups.documentdb.io` and `list` on `backups.documentdb.io`. These
> verbs are granted by the `longhaul-test` Role in `deploy/rbac.yaml`; without
> them the backup verifier logs an error and the rest of the run continues.

## Operations

`random` mode preserves the production long-haul behavior: the scheduler picks
weighted eligible operations every 10 seconds, runs one disruptive operation at
a time, and applies the global cooldown. `sequence` mode runs each configured
operation exactly once and in order, stopping on the first execution,
precondition, recovery, or policy failure; a successful or failed sequence
emits its final report and exits immediately instead of waiting for
`LONGHAUL_MAX_DURATION`. `disabled` mode runs no operations. All modes keep the
continuous writer/verifier workload active.

Current stable operation names:

| Operation | Kind | Notes |
|-----------|------|-------|
| `scale-up` / `scale-down` | Topology | Adjusts `spec.instancesPerNode` within `[MIN, MAX]`. Only adds/removes a standby, so the primary write path is untouched (near-zero outage budget). |
| `upgrade-documentdb` | Topology | In-place version upgrade; requires HA (`instancesPerNode>=2`). |
| `kill-operator-pod` | Chaos | Deletes the operator pod; asserts the data plane keeps serving (near-zero outage budget). |
| `kill-primary-pod` | Chaos | Deletes the CNPG primary pod to exercise automatic failover; requires HA (`instancesPerNode>=2`). |

Each operation has a write-outage budget: the scale ops and `kill-operator-pod`
keep writes up (near-zero budget), `kill-primary-pod` tolerates a single
failover, and `upgrade-documentdb` a cross-version switchover. See the
[design document](../../docs/designs/long-haul-test-design.md#outage-budgets)
for the exact budgets and rationale.

Operation execution failures are terminal verdict failures in both `random` and
`sequence` modes. The `longhaul-report` ConfigMap exposes `operation-status`,
`operation-results` JSON (one result per sequenced operation), and, in random
mode, `operation-aggregates` JSON (passed/failed counts per operation),
alongside the `result` and `latest-report` fields.

### RBAC for chaos operations

`deploy/rbac.yaml` already grants everything the driver ServiceAccount needs, so
`kubectl apply -f deploy/rbac.yaml` is all that's required. The one non-obvious
part: the chaos operations delete pods, and `kill-operator-pod` deletes the
operator pod in the **operator's** namespace — not the driver's. That
cross-namespace access is granted by a separate Role/RoleBinding scoped to
`LONGHAUL_OPERATOR_NAMESPACE` (default `documentdb-operator`). If your operator
runs in a different namespace, set that variable and update the binding to
match. (`kill-primary-pod` stays within the cluster namespace and needs no extra
setup.)

## CI Safety

The production long-haul binary runs as a Kubernetes Deployment on a dedicated
AKS cluster. A short PR smoke workflow (`.github/workflows/longhaul-smoke.yml`)
runs the same driver and manifests against kind in **sequence mode**, exercising
every operation once (scale up, scale down, upgrade DocumentDB, kill the operator
pod, kill the primary pod — including a real cross-version upgrade) and asserting
the `longhaul-report` ConfigMap reaches a `PASS` / `COMPLETE` verdict. Because a
Deployment auto-restarts exited pods, that report (and the GitHub Actions
annotations) — not the pod status — is the source of truth for "did the test
pass?".

The config unit tests (`test/longhaul/config/`) run unconditionally and are included in normal
CI test runs — they are fast (~0.002s) and require no cluster.

## Relationship to `test/e2e/`

The `test/e2e/` Ginkgo suite and this long-haul harness are **separate modules
with intentionally different shapes** that share the `test/shared/` helpers
(`test/shared/documentdb` CR helpers and `test/shared/mongo`). See the
[design document](../../docs/designs/long-haul-test-design.md#relationship-to-teste2e)
for the full comparison, the shared code today, and future opportunities.
