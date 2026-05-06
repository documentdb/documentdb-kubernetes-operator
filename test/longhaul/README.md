# Long Haul Tests

Long haul tests validate that DocumentDB Kubernetes Operator clusters remain healthy under
continuous load over extended periods. They run a canary workload that writes and reads data,
performs management operations, and checks for data integrity.

> **Status:** Phase 1 complete. Canary workload (writers + verifiers), operation scheduler,
> health monitor, and in-cluster deployment are implemented with a placeholder cluster client.
> Phase 2 will add real Kubernetes operations. See [design document](../../docs/designs/long-haul-test-design.md).

## Project Structure

```
test/longhaul/
├── go.mod                # Separate Go module
├── README.md             # This file
├── Dockerfile            # Multi-stage build for in-cluster deployment
├── cmd/longhaul/
│   └── main.go           # Standalone binary entry point
├── deploy/
│   ├── setup.yaml        # Namespace + credentials + DocumentDB CR (bootstrap)
│   ├── deployment.yaml   # Kubernetes Deployment + ConfigMap manifest (templated)
│   └── rbac.yaml         # ServiceAccount + RBAC roles
├── config/
│   ├── config.go         # Config struct, env var loading, validation
│   └── config_test.go    # Config unit tests
├── workload/
│   ├── writer.go         # Continuous writers with sequence tracking
│   └── verifier.go       # Gap/checksum verification
├── monitor/
│   ├── health.go         # ClusterClient interface + health monitor
│   └── leakdetect.go     # Resource leak detector
├── operations/
│   ├── scale.go          # Scale up/down operations
│   └── scheduler.go      # Operation scheduling with cooldowns
├── journal/
│   └── journal.go        # Structured event log + disruption windows
└── report/
    └── checkpoint.go     # Periodic reporter (ConfigMap + stdout)
```

- **`test/longhaul/`** — The long-running canary. Designed to run for hours/days.
- **`test/longhaul/cmd/longhaul/`** — Standalone binary, deployed as a Kubernetes Job.
- **`test/longhaul/deploy/`** — Kubernetes manifests for in-cluster deployment.
- **`test/longhaul/config/`** — Config parsing and validation. Fast unit tests, safe for CI.

## Quick Start

### Prerequisites

- A running Kubernetes cluster with DocumentDB deployed
- `kubectl` configured to access the cluster
- Go 1.25+

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

For development and validation against a port-forwarded cluster:

```bash
cd test/longhaul

LONGHAUL_MONGO_URI="mongodb://user:pass@localhost:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true" \
LONGHAUL_CLUSTER_NAME=documentdb-cluster \
LONGHAUL_NAMESPACE=documentdb-test-ns \
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
cd test/longhaul

# 1. Build and push the container image (or use the GHCR image from CI).
docker build -t <your-registry>/longhaul-test:latest -f Dockerfile .
docker push <your-registry>/longhaul-test:latest

# 2. Create the MongoDB credentials secret
kubectl create secret generic longhaul-mongo-credentials \
  --from-literal=uri='mongodb://docdb:YourPass@documentdb-service-documentdb-cluster.documentdb-test-ns.svc:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsInsecure=true' \
  -n documentdb-test-ns

# 3. Deploy RBAC and Deployment. deployment.yaml has placeholders
#    __OWNER__ and __IMAGE_TAG__ that are normally substituted by the
#    deploy workflow; for a manual apply, sed them yourself or edit
#    the file in place.
kubectl apply -f deploy/setup.yaml
kubectl apply -f deploy/rbac.yaml
sed -e 's|__OWNER__|<your-registry>|g' \
    -e 's|__IMAGE_TAG__|latest|g' \
    deploy/deployment.yaml | kubectl apply -f -

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
| `LONGHAUL_MONGO_URI` | Yes | — | MongoDB connection string to the DocumentDB gateway. |
| `LONGHAUL_CLUSTER_NAME` | Yes | — | Name of the target DocumentDB cluster CR. |
| `LONGHAUL_NAMESPACE` | No | `default` | Kubernetes namespace of the target cluster. |
| `LONGHAUL_MAX_DURATION` | No | `30m` | Max test duration. Use `0s` for run-until-failure. |
| `LONGHAUL_NUM_WRITERS` | No | `5` | Number of concurrent writers. |
| `LONGHAUL_NUM_VERIFIERS` | No | `2` | Number of concurrent verifiers. |
| `LONGHAUL_OP_COOLDOWN` | No | `5m` | Cooldown between management operations. |
| `LONGHAUL_RECOVERY_TIMEOUT` | No | `5m` | Max wait for cluster recovery after an operation. |
| `LONGHAUL_MIN_INSTANCES` | No | `1` | Minimum `spec.instancesPerNode` for scale-down operations (CRD lower bound: 1). |
| `LONGHAUL_MAX_INSTANCES` | No | `3` | Maximum `spec.instancesPerNode` for scale-up operations (CRD upper bound: 3). |
| `LONGHAUL_REPORT_INTERVAL` | No | `1h` | How often to write checkpoint reports to ConfigMap. |

## CI Safety

The long haul test binary is deployed as a Kubernetes Job on a dedicated AKS cluster.
It does **not** run in any PR-gated CI workflow.

The config unit tests (`test/longhaul/config/`) run unconditionally and are included in normal
CI test runs — they are fast (~0.002s) and require no cluster.
