# Live telemetry prototype

One Go process receives OTLP/gRPC metrics and traces, retains a bounded,
redacted in-memory window, and exposes four read-only MCP tools. There is no
model in the ingestion path, separate receiver service, database connection,
historical store, log receiver, dashboard, or automatic remediation.

This is a synthetic-data playground, not a production advisor. The operator
change on this prototype branch adds an OTLP traces pipeline. Prometheus and
the SQL receiver remain metrics-only. No CRD or gateway source changes are
needed.

```text
gateway -> existing Collector sidecar -> OTLP/gRPC listener
                                             |
                                  bounded in-process capture
                                             |
                                  read-only MCP Streamable HTTP
                                             |
                                    existing agent + skill
```

## Bounds and interpretation

| Surface | Limit or behavior |
| --- | --- |
| Retention | Five minutes by observation/end time; background expiration every second. |
| Retained payload | 64 MiB, excluding Go heap/index overhead and in-flight protocol buffers. |
| Cardinality | 4,096 metric series, 50,000 points, 20,000 spans. |
| OTLP request | 1 MiB, including the decompressed payload; 8,192 structural/data items. |
| Ingestion | Four connections, one concurrent stream per connection, two active handlers; no application work queue. |
| MCP | Loopback HTTP only, stateless sessions, two active readers, bounded connections and timeouts. |
| MCP input/output | 8 KiB request bodies; responses at most 128 KiB with explicit truncation. |
| Queries | At most 1,000 metric observations, 20 recent traces, or 200 spans for one trace. Byte limits can reduce these further. |
| Freshness | Both event and arrival times; stale warning after 90 seconds. |
| Restart | All captured data is lost; a new capture-session ID is generated. |

Supported metrics are gauges, sums, and explicit histograms. Summary and
exponential histogram points are explicitly rejected. Cumulative differences
stay within one stream and reset epoch. Delta intervals are not prorated at
window boundaries. Gauges are not differentiated. Histogram distributions are
retained without fabricated percentiles. Ambiguous intervals, missing values,
resets, and arithmetic overflow prevent misleading aggregates.
Freshness warnings apply to selected metric series, so a continuing health
gauge cannot conceal stale request metrics. Up to eight exemplars per point
retain validated trace/span IDs, event times, values, and filtered attributes;
invalid or excess exemplars produce explicit omission counts and warnings.

Resources must match the configured `documentdb.cluster` and
`k8s.namespace.name`. Resource, scope, and point attributes are allowlisted;
query text, collection/database names, document content, span events, links,
and status messages are not exposed. Hidden attributes still participate in
capture-local keyed stream identity, preventing redaction from merging
distinct streams. These identities are not query fingerprints.
Known diagnostic span/scope names and versions are preserved; unknown ones
receive capture-local keyed pseudonyms, even when their spelling looks like
an identifier. All HTTP requests must contain one JSON-RPC object; legacy
batch requests are rejected before SDK dispatch.

Without sufficient source-instance identity, metric exports are kept separate.
Matching visible operation names does not establish cross-metric correlation.
Identical span IDs are deduplicated within the retained window, conflicting
duplicates are flagged, and the first observation is preserved. Metric delivery
is not promised exactly once. Trace completeness is never asserted.

## Three-command live demo

From the repository root in your host/WSL terminal, with this checkout's
devcontainer running and Copilot CLI already authenticated:

```bash
bash documentdb-playground/performance-advisor/demo.sh up
bash documentdb-playground/performance-advisor/demo.sh run
bash documentdb-playground/performance-advisor/demo.sh down
```

`up` discovers the matching devcontainer from its configuration label and
shared mount, runs tooling as `vscode`, builds from this checkout, and creates
a new uniquely named kind cluster. It never installs host toolchains. Each
environment has its own image tags, synthetic secret, private kubeconfig,
Helm repository configuration, and anonymous Docker configuration. Rebuild
with `down` followed by `up` after changing source or manifests. Existing demo
state is never overwritten.

`run` starts the loopback MCP port-forward and one bounded Copilot session in
the terminal environment where Copilot is installed. The session-only
`demo-mcp.json` launches a stdio adapter through the same devcontainer. It
forwards exactly the four read-only telemetry tools, adds neutral schedule
metadata, and records compact successful-read receipts. It is not another
OTLP receiver, does not provide database tools, and does not change global
agent settings or add a model to ingestion. The default model is not
overridden.

No workload setup begins until that observer successfully reads capture
status for the current run and capture. Initialization or an open port alone
does not pass. Each of three 90-second windows must contain a successful
`get_metric_window` read with a positive, wholly in-window request-count
interval and a `get_trace` read for a received in-window request. A health
gauge, trace listing, stale capture, or retrospective read cannot pass.
Readiness expires after three minutes; the session is limited to 12 minutes
and 128 tool attempts. The observer receives only neutral boundaries, not
fault labels or the workload source.

Watch the observer describe each active window, then inspect
`evidence/latest-demo.json` for receipt and event timestamps and
`evidence/latest-observer.txt` for timestamped prose. The gate proves
live client reads, not the correctness or timing of model prose, a complete
trace, lossless delivery, or a unique diagnosis. The final interpretation still
needs human review. Timing values can vary across runs; the demo does not
assert latency thresholds, per-query CPU, or gateway-pool contention.

For another MCP-capable agent, use `run --manual`, launch its client from the
repository root with `demo-mcp.json`, and supply `skill/SKILL.md`. The same
readiness and per-window gates apply. Tool responses include the current
trial's boundaries and read receipts; use them instead of parsing terminal
indentation. Capture-status calls can wait up to ten seconds for a schedule
change after the required reads have arrived.

Ctrl+C cancels the workload and observer; then run `down`. Host-side lifecycle
cancellation is forwarded only to the recorded container process group after
checking its PID, start time, and session identity, not to the devcontainer.
`down` verifies the
recorded Docker daemon and exact kind node identity before deletion, refuses
foreign or replaced state, and refuses to race an active lifecycle command.
It removes only the owned cluster and `.demo/` runtime state, including the
port-forward's configuration and in-memory capture. Local build images and
the ignored compact evidence and observer output remain. A second `down`
without owned state is a no-op. No images are published.

If `up` fails, its owned state remains for diagnosis and explicit `down`;
there is no success-shaped fallback. A failed observer or missed live-read
window makes `run` fail. The detailed manual commands below are available for
investigation, but must not be mixed with the wrapper's owned environment.

## Build and protocol checks

Run these commands **inside this repository's devcontainer**, from the
repository root, using the non-root `vscode` user. Do not install toolchains on
the host.

```bash
(cd documentdb-playground/performance-advisor &&
  go build ./... &&
  go test -race ./... &&
  go vet ./...)

(cd operator/src &&
  go test ./internal/otel &&
  make build)

# Includes generated OTLP, real gzip export, resource enrichment, and MCP reads.
(cd documentdb-playground/performance-advisor &&
  RUN_COLLECTOR_TEST=1 go test -race ./internal/server -run TestRealCollector -count=1 -v)
```

The real-Collector test starts and removes only its uniquely named local test
container. It uses the same Collector release as the sidecar, pinned to
`sha256:0fba96233274f6d665ac8831ad99dfe6479a9a20459f6e2719c0d20945773b46`.

If public Docker pulls fail because a forwarded desktop credential helper
cannot run, use an empty, temporary `DOCKER_CONFIG` for these anonymous public
pulls. Do not modify or print the user's credential configuration.

## Reproduce the isolated Kubernetes path

The following Linux/amd64 setup creates a **new local kind cluster** and an
isolated kubeconfig. Do not substitute a shared cluster. It requires Docker,
kind, Helm, kubectl, and the repository devcontainer.

```bash
set -euo pipefail
export KIND_NAME="live-telemetry-prototype-$(date +%s)"
export KUBECONFIG="/tmp/${KIND_NAME}.kubeconfig"
export CONTEXT="kind-${KIND_NAME}"
test ! -e "$KUBECONFIG"
kind create cluster --name "$KIND_NAME" --image kindest/node:v1.35.0 \
  --kubeconfig "$KUBECONFIG" --wait 180s

docker build -t live-telemetry-tool:prototype \
  documentdb-playground/performance-advisor
docker build -t live-telemetry-gateway:prototype \
  -f documentdb-playground/performance-advisor/deploy/Dockerfile.gateway \
  documentdb-playground/performance-advisor
docker build --build-arg ARCH=amd64 --build-arg TARGETOS=linux \
  -t live-telemetry-operator:prototype operator/src
docker build --build-arg ARCH=amd64 --build-arg TARGETOS=linux \
  -t live-telemetry-sidecar:prototype operator/cnpg-plugins/sidecar-injector

kind load docker-image --name "$KIND_NAME" \
  live-telemetry-tool:prototype live-telemetry-gateway:prototype \
  live-telemetry-operator:prototype live-telemetry-sidecar:prototype

helm dependency build operator/documentdb-helm-chart
helm install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version v1.19.3 --namespace cert-manager --create-namespace \
  --set crds.enabled=true --kube-context "$CONTEXT" --wait --timeout 180s
helm install documentdb-operator operator/documentdb-helm-chart \
  --namespace documentdb-operator --create-namespace \
  --set image.documentdbk8soperator.repository=live-telemetry-operator \
  --set image.documentdbk8soperator.tag=prototype \
  --set image.documentdbk8soperator.pullPolicy=Never \
  --set image.sidecarinjector.repository=live-telemetry-sidecar \
  --set image.sidecarinjector.tag=prototype \
  --set image.sidecarinjector.pullPolicy=Never \
  --set documentDbVersion=0.116.0 \
  --set gatewayImagePullPolicy=IfNotPresent \
  --kube-context "$CONTEXT" --wait --timeout 240s

kubectl --context "$CONTEXT" apply \
  -f documentdb-playground/performance-advisor/deploy/tool.yaml
secret=$(openssl rand -hex 24)
test -n "$secret"
kubectl --context "$CONTEXT" -n live-telemetry create secret generic \
  documentdb-credentials --from-literal=username=telemetry_user \
  --from-literal=password="$secret"
unset secret
kubectl --context "$CONTEXT" apply \
  -f documentdb-playground/performance-advisor/deploy/documentdb.yaml
kubectl --context "$CONTEXT" -n live-telemetry rollout status \
  deployment/live-telemetry-tool --timeout=90s
kubectl --context "$CONTEXT" -n live-telemetry wait \
  --for=create pod/telemetry-db-1 --timeout=180s
kubectl --context "$CONTEXT" -n live-telemetry wait \
  --for=condition=Ready pod/telemetry-db-1 --timeout=300s
```

Only the four local, single-platform prototype images are loaded into kind.
Let Kubernetes pull the pinned database images and the sidecar's Collector.
Loading a partially downloaded multi-platform public image archive can fail
with a missing content digest on containerd-backed Docker installations.

The gateway derivative changes only its packaged telemetry JSON: metrics and
tracing enabled, both endpoints `http://127.0.0.1:4317`, and sampler ratio `1.0`.
It preserves the binary, entrypoint, ownership, and unrelated settings.
Sampling remains parent-based with a full root sampling ratio for this small
test. SQL-comment correlation is not enabled. The 15-second metrics export
interval is unchanged.

| Input image | Immutable source |
| --- | --- |
| Gateway 0.116.0 | `sha256:1f95ba8e06e2a2626c7e9458106a6de372dd5e50ee9559267a92298ad410ccf5` |
| Extension 0.116.0 | `sha256:28e39bacc8a5d0e2f1cd3d820035c5983808059c3686babd2e27a083b96ac4f4` |
| PostgreSQL 18.4, minimal trixie | `sha256:2cf8d1c297265331b8a81aefe5b7b397ddbb836e15eaa953543f6190e8dabf98` |

The manifests use one primary, synthetic credentials, no public service, and a
tool Service exposing only OTLP. The tool has no API token, database credentials,
or writable filesystem. Its NetworkPolicy permits OTLP only from the test
database pods and denies new outbound connections. Verify policy enforcement
on the selected CNI; the deployment must remain disposable and synthetic.
In-cluster OTLP is plaintext in this operator version.

## Connect the existing agent

In a separate devcontainer terminal, keep this process in the foreground:

```bash
kubectl --context "$CONTEXT" -n live-telemetry port-forward \
  deployment/live-telemetry-tool --address=127.0.0.1 8080:8080
```

Connect an MCP Streamable HTTP client to `http://127.0.0.1:8080/mcp` and give
the agent `skill/SKILL.md`. The URL is local to the environment running
kubectl. Run the client there, or use a localhost-only editor port forward to
reach it from the host. Do not change MCP's pod binding to `0.0.0.0`.
Nothing in this package modifies global agent settings.

| Tool | Arguments |
| --- | --- |
| `get_capture_status` | `{}` |
| `get_metric_window` | `{"metric":"db.client.operations","lookback_seconds":60,"limit":100}` |
| `get_recent_traces` | `{"lookback_seconds":60,"limit":5}` |
| `get_trace` | `{"trace_id":"<received 32-character hex ID>","limit":100}` |

## Real request and reconciliation gates

```bash
(cd test/e2e &&
  E2E_LIVE_TELEMETRY=1 go test ./tests/performance \
  -run TestPerformance -count=1 -v -ginkgo.label-filter=live-telemetry)

kubectl --context "$CONTEXT" -n live-telemetry exec telemetry-db-1 \
  -c documentdb-gateway -- jq '{TelemetryOptions}' \
  /home/documentdb/gateway/pg_documentdb_gw/target/SetupConfiguration_temp.json

# Only the disposable test database pod is recreated; its PVC remains.
kubectl --context "$CONTEXT" -n live-telemetry delete pod telemetry-db-1
kubectl --context "$CONTEXT" -n live-telemetry wait \
  --for=create pod/telemetry-db-1 --timeout=180s
kubectl --context "$CONTEXT" -n live-telemetry wait \
  --for=condition=Ready pod/telemetry-db-1 --timeout=300s

# Re-run the request gate and effective-settings inspection above.
```

The request gate reuses the existing connection and seed helpers, creates and
drops only a per-test database, and requires a positive `find` request counter
and a newly received `find` span. It prints compact capture/image-independent
evidence, including event and arrival times, span names, and trace IDs.

## Controlled observations

```bash
(cd test/e2e &&
  E2E_LIVE_TELEMETRY=1 go test ./tests/performance \
  -run TestPerformance -count=1 -v -ginkgo.v \
  -ginkgo.label-filter=live-telemetry-trials)
```

This separate case uses 30,000 synthetic documents, a 10-request/second target,
and three 45-second windows. The harness owns index changes and restoration.
The observer receives neutral trial IDs and timestamps, not fault labels.
The case records selected trace evidence rather than a telemetry archive.
The final gap report must separate an observed timing change from a proven
diagnosis.

Do not substitute client-side queuing for gateway-pool contention. In the
pinned gateway, `PgConfiguration::max_connections` reads PostgreSQL's
`max_connections`, and `PgPoolSettings::adjusted_max_connections` combines it
with a reserved system budget and a floor. This is not an independent,
validated small gateway-pool cap. The pool case needs a safe control and
evidence of actual gateway waiting before it can be claimed complete.
Container CPU collection is a follow-on, not evidence already provided here.

## Cleanup

Stop the MCP port-forward with Ctrl+C. For the fresh cluster created above:

```bash
# Confirm this is the experiment's unique local cluster before deletion.
test -n "$KIND_NAME"
test "$CONTEXT" = "kind-${KIND_NAME}"
test "$KUBECONFIG" = "/tmp/${KIND_NAME}.kubeconfig"
kind get clusters
kind delete cluster --name "$KIND_NAME"
rm -- "$KUBECONFIG"
unset KUBECONFIG CONTEXT KIND_NAME
```

Deleting this owned cluster stops the tool, clears its in-memory capture, and
removes only the experiment's Kubernetes resources. Do not use this cleanup
against an existing shared cluster. No images are pushed or public tags replaced.
