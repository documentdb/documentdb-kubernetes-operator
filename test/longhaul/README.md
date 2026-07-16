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

> **HA topology required for upgrade tests.** The `upgrade-documentdb` operation
> auto-skips when `spec.instancesPerNode < 2` because a single-instance cluster
> has no standby to absorb writes during the rolling restart — the upgrade
> would produce real (true-positive) downtime that no operator change can
> prevent. Run with `instancesPerNode: 2` (or `3`) to exercise the HA upgrade
> path. The skip is "free": no cooldown is consumed, and the next 10s scheduler
> tick re-evaluates eligibility, so scaling up at any point makes the upgrade
> immediately schedulable.

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
  --from-literal=uri='mongodb://docdb:YourPass@documentdb-service-documentdb-cluster.documentdb-test-ns.svc:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true' \
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
| `LONGHAUL_MAX_DURATION` | No | `30m` | Max test duration. Use `0s` for run-until-failure. |
| `LONGHAUL_NUM_WRITERS` | No | `5` | Number of concurrent writers. |
| `LONGHAUL_OP_COOLDOWN` | No | `5m` | Cooldown between management operations. |
| `LONGHAUL_RECOVERY_TIMEOUT` | No | `5m` | Max wait for cluster recovery after an operation. |
| `LONGHAUL_MIN_INSTANCES` | No | `1` | Minimum `spec.instancesPerNode` for scale-down operations (CRD lower bound: 1). |
| `LONGHAUL_MAX_INSTANCES` | No | `3` | Maximum `spec.instancesPerNode` for scale-up operations (CRD upper bound: 3). |
| `LONGHAUL_REPORT_INTERVAL` | No | `1h` | How often to write checkpoint reports to ConfigMap. |
| `LONGHAUL_RESET_DATA` | No | `false` | If `true`, drop the workload collection on startup. Off by default so a Deployment pod restart preserves durability history. |

## CI Safety

The long haul test binary is deployed as a Kubernetes Deployment on a dedicated AKS
cluster. It does **not** run in any PR-gated CI workflow. Because a Deployment
auto-restarts crashed pods, the source of truth for "did the test pass?" is the
`longhaul-report` ConfigMap and the GitHub Actions annotations, not the pod
status.

The config unit tests (`test/longhaul/config/`) run unconditionally and are included in normal
CI test runs — they are fast (~0.002s) and require no cluster.

## Relationship to `test/e2e/`

The `test/e2e/` Ginkgo suite (added in PR #346) and this long haul harness are **separate
modules with intentionally different shapes**. They share a problem domain (exercising a
DocumentDB cluster) but answer different questions:

| | `test/e2e/` | `test/longhaul/` |
|---|---|---|
| Shape | Go test binary (Ginkgo specs) | Standalone long-running daemon |
| Lifetime | Minutes per spec | Days–weeks per run |
| Asserts | One behavior per spec, then exits | Continuous invariants over time |
| Failure mode | `t.Fail` per spec | Journal entry + alert + auto-restart |
| Cluster | Created + torn down per run | Long-lived dedicated AKS cluster |
| Operator API | Typed (`previewv1.DocumentDB` via controller-runtime) | Typed (`previewv1.DocumentDB` via controller-runtime + `test/shared/documentdb` helpers) |

### Code that is shared today

The harness consumes the `test/shared/` module (extracted in PR #401):

- `test/shared/documentdb` — typed `DocumentDB` CR helpers (`Get`, `IsHealthy`,
  `PatchInstances`, `PatchSpec`). The monitor's `K8sClusterClient` uses these
  as the single source of truth for the readiness predicate so longhaul and
  e2e can't drift on what "healthy" means.
- `test/shared/mongo` — `NewFromURI` for the data-plane connection.

### Future opportunities

The e2e suite has additional helpers in `test/e2e/pkg/e2eutils/` that this
harness will likely consume as it grows:

- `e2eutils/mongo` — `BuildURI` (URL-escapes username/password), TLS-from-CA-bundle,
  `Handle` with port-forward + secret-backed credentials. The long haul driver
  currently takes a raw `LONGHAUL_DOCUMENTDB_URI` string; when it moves to per-secret
  credentials or in-cluster TLS, these helpers become directly applicable.
- `e2eutils/operatorhealth` — pod-ready / CRD-ready gating used during e2e setup.
  The monitor's `isPodReady` could delegate to this.
- `e2eutils/clusterprobe` — CRD presence checks.
