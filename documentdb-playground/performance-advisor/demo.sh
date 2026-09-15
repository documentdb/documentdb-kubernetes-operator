#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

set -euo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(git -C "$script_dir" rev-parse --show-toplevel)
state="$script_dir/.demo"
action=${1:-help}
shift "$(( $# > 0 ? 1 : 0 ))"

fail() { printf 'live telemetry demo: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || fail "Missing $1; use the repository devcontainer for tooling."; }
check_state() {
  [[ -d "$state" && ! -L "$state" && -f "$state/owner" && ! -L "$state/owner" ]] ||
    fail "No owned demo state; run up first."
  [[ "$(<"$state/owner")" == documentdb-live-telemetry-v1 ]] || fail "Unrecognized demo ownership marker."
}
read_state() {
  [[ -f "$state/$1" && ! -L "$state/$1" ]] || fail "Missing or unsafe demo state: $1."
  REPLY=$(<"$state/$1")
}
check_run_id() { [[ "$1" =~ ^[a-f0-9]{32}$ ]] || fail "Invalid demo run identity."; }

if [[ "$action" == help || "$action" == --help || "$action" == -h ]]; then
  printf '%s\n' \
    'Usage: bash documentdb-playground/performance-advisor/demo.sh up|run|down' \
    '  up           Build and deploy a fresh, isolated Linux/amd64 kind environment.' \
    '  run          Start a bounded Copilot observer and three gated live windows.' \
    '  run --manual Wait for an externally connected MCP observer instead.' \
    '  down         Delete only this demo cluster and its private runtime state.' \
    '  observe      Stdio MCP adapter, launched by the session-only MCP config.' \
    'Host invocations use the matching running devcontainer; nothing is installed.'
  exit 0
fi
case "$action" in up|run|down|observe|_workload) ;; *) fail "Unknown command: $action." ;; esac
[[ $# -le 1 ]] || fail "Too many arguments."
if [[ "$action" == down && ! -e "$state" && ! -L "$state" ]]; then
  printf 'No owned demo state exists; no resources were touched.\n'
  exit 0
fi

# Keep Copilot where it is already installed; send repository tooling to the container.
if [[ "$action" == run ]]; then
  [[ $# -eq 0 || "$1" == --manual ]] || fail "run accepts only --manual."
  manual=${1:-}
  check_state
  [[ -f "$state/up-complete" ]] || fail "Setup did not finish; inspect up output and run down."
  need timeout
  if [[ -z "$manual" ]]; then
    command -v copilot >/dev/null ||
      fail "Copilot CLI is not installed here. Run from your host terminal, or use run --manual."
  fi
  need od
  run_id=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  check_run_id "$run_id"
  run_dir="$state/run-$run_id"
  mkdir -m 700 -- "$run_dir"
  workload_pid=
  observer_pid=
  output_pid=
  finished=false
  cleanup_run() {
    local rc=$?
    trap - EXIT INT TERM
    if [[ "$finished" != true ]]; then
      touch -- "$run_dir/cancel"
      if [[ -n "$observer_pid" ]] && kill -0 "$observer_pid" 2>/dev/null; then
        kill -TERM "$observer_pid"
      fi
      if [[ -n "$output_pid" ]] && kill -0 "$output_pid" 2>/dev/null; then
        kill -TERM "$output_pid"
      fi
    fi
    if [[ -n "$observer_pid" ]]; then wait "$observer_pid" || :; fi
    if [[ -n "$output_pid" ]]; then wait "$output_pid" || :; fi
    if [[ -n "$workload_pid" ]]; then wait "$workload_pid" || :; fi
    if [[ "$rc" -ne 0 ]]; then
      printf 'Demo did not pass. Workload details: %s/workload.log\nRun down to remove the owned cluster.\n' "$run_dir" >&2
      tail -30 "$run_dir/workload.log" >&2
    fi
    exit "$rc"
  }
  trap cleanup_run EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  bash "$script_dir/demo.sh" _workload "$run_id" >"$run_dir/workload.log" 2>&1 &
  workload_pid=$!
  for ((attempt=0; attempt<120; attempt++)); do
    [[ ! -f "$run_dir/run.json" ]] || break
    kill -0 "$workload_pid" 2>/dev/null || fail "Workload preparation failed."
    sleep 1
  done
  [[ -f "$run_dir/run.json" ]] || fail "MCP preparation timed out."
  printf 'Observer readiness gate armed. Three 90-second windows; no workload starts before a successful observer status read.\n'
  if [[ -n "$manual" ]]; then
    printf 'Connect an MCP client from the repository root using %s/demo-mcp.json and skill/SKILL.md.\n' "$script_dir"
  else
    prompt="Investigate the bounded synthetic telemetry demo using these instructions:

$(<"$script_dir/skill/SKILL.md")

Run the packaged live demo now. Use only the four live-telemetry tools.
Use ASCII punctuation; do not use em-dashes.
Start with get_capture_status, which releases the readiness gate.
Follow the appended neutral schedule. There are three 90-second trials.
While each trial is active, read db.client.operations with lookback_seconds=30
and limit=10, find a current Find request with get_recent_traces (limit=3),
and fetch that received ID with get_trace (limit=30). A metric interval must
start at or after the trial start. Inspect the live-read receipts; retry with
fresh data if either receipt is missing. Do not use old traces for a new trial.
Immediately publish a brief six-field observation for that trial while it is
still active, before waiting for the next trial. Use qualified explanations.
Poll get_capture_status for schedule changes, staying below 128 total calls.
Do not finish after trial-1 or trial-2. Stop on completed or failed, or stop_by.
On completed, return a short final comparison and the evidence limitations.
Do not read repository files, discover fault mappings, invoke other tools,
or start background agents. This user-requested run is bounded to 12 minutes."
    cd "$repo_root"
    need mkfifo
    mkfifo "$run_dir/observer.pipe"
    (
      while IFS= read -r line || [[ -n "$line" ]]; do
        printf '%s\n' "$line"
        TZ=UTC printf '%(%Y-%m-%dT%H:%M:%SZ)T\t%s\n' -1 "$line" >>"$run_dir/observer.txt"
      done
    ) <"$run_dir/observer.pipe" &
    output_pid=$!
    timeout --signal=TERM --kill-after=10s 12m copilot \
      --no-custom-instructions --disable-builtin-mcps --no-ask-user --no-auto-update \
      --additional-mcp-config="@$script_dir/demo-mcp.json" \
      --available-tools=live-telemetry-get_capture_status,live-telemetry-get_metric_window,live-telemetry-get_recent_traces,live-telemetry-get_trace \
      --allow-tool=live-telemetry --stream=on --silent --log-level=error \
      --prompt="$prompt" >"$run_dir/observer.pipe" &
    observer_pid=$!
    wait "$observer_pid"
    observer_pid=
    wait "$output_pid"
    output_pid=
    rm -- "$run_dir/observer.pipe"
  fi
  wait "$workload_pid"
  workload_pid=
  if [[ -f "$run_dir/observer.txt" ]]; then
    cp -- "$run_dir/observer.txt" "$script_dir/evidence/latest-observer.txt"
  fi
  finished=true
  printf 'Live-read gates passed. Compact proof: %s/evidence/latest-demo.json\n' "$script_dir"
  exit 0
fi

if [[ ! -e /.dockerenv && "${WORKSPACE_ROOT:-}" != "$repo_root" ]]; then
  need docker
  mapfile -t containers < <(docker ps \
    --filter "label=devcontainer.config_file=$repo_root/.devcontainer/devcontainer.json" --format '{{.ID}}')
  if [[ ${#containers[@]} -eq 0 ]]; then
    mapfile -t candidates < <(docker ps --filter label=devcontainer.config_file --format '{{.ID}}')
    for candidate in "${candidates[@]}"; do
      if docker inspect "$candidate" --format '{{range .Mounts}}{{println .Source}}{{end}}' |
        grep -Fqx -- "$repo_root"; then containers+=("$candidate"); fi
    done
  fi
  [[ ${#containers[@]} -eq 1 ]] ||
    fail "Expected one running devcontainer for this checkout; found ${#containers[@]}. Start or disambiguate it."
  container=${containers[0]}
  workspace=$(docker inspect "$container" --format "{{range .Mounts}}{{if eq .Source \"$repo_root\"}}{{println .Destination}}{{end}}{{end}}")
  [[ -n "$workspace" && "$workspace" != *$'\n'* ]] || fail "Could not resolve the shared workspace."
  if [[ "$action" == observe ]]; then
    exec docker exec -i --user vscode --workdir "$workspace" "$container" \
      bash "$workspace/documentdb-playground/performance-advisor/demo.sh" "$action" "$@"
  fi
  exec bash "$script_dir/demo-transport.sh" "$container" "$workspace" "$action" "$@"
fi

[[ "$(id -u)" -ne 0 ]] || fail "Run tooling as the devcontainer's non-root vscode user."
cd "$repo_root"
if [[ "$action" == observe ]]; then
  [[ $# -eq 0 ]] || fail "observe takes no arguments."
  check_state
  read_state current-run
  check_run_id "$REPLY"
  exec "$state/demo-observer" --dir "$state/run-$REPLY"
fi
need flock
[[ ! -L "$state" && ! -L "$script_dir/.demo.lock" ]] || fail "Refusing symlinked lifecycle state."
exec 9>"$script_dir/.demo.lock"
flock --exclusive --nonblock 9 || fail "Another lifecycle command is active; stop run before down."
for tool in docker kind kubectl jq; do need "$tool"; done

if [[ "$action" == up ]]; then
  [[ $# -eq 0 ]] || fail "up takes no arguments."
  [[ ! -e "$state" ]] || fail "Demo state already exists; run down before creating a fresh environment."
  for tool in go helm openssl; do need "$tool"; done
  [[ "$(uname -sm)" == "Linux x86_64" ]] || fail "This pinned demo requires Linux/amd64."
  mkdir -m 700 -- "$state"
  printf 'documentdb-live-telemetry-v1\n' >"$state/owner"
  mkdir -m 700 -- "$state/docker" "$state/helm"
  export DOCKER_CONFIG="$state/docker"
  export HELM_REPOSITORY_CONFIG="$state/helm/repositories.yaml"
  export HELM_REPOSITORY_CACHE="$state/helm/cache"
  export HELM_REGISTRY_CONFIG="$state/helm/registry.json"
  cluster="telemetry-demo-$(openssl rand -hex 8)"
  printf '%s\n' "$cluster" >"$state/cluster"
  docker info --format '{{.ID}}' >"$state/daemon"
  export KUBECONFIG="$state/kubeconfig"
  context="kind-$cluster"
  if kind get clusters | grep -Fqx -- "$cluster"; then fail "Generated cluster name already exists."; fi
  cleanup_setup() {
    local rc=$?
    trap - EXIT INT TERM
    if [[ "$rc" -ne 0 && -f "$state/creating-cluster" && ! -s "$state/node" ]]; then
      local created_node
      created_node=$(docker ps -a --filter "label=io.x-k8s.kind.cluster=$cluster" --format '{{.ID}}' --no-trunc)
      if [[ "$created_node" =~ ^[a-f0-9]{64}$ ]]; then printf '%s\n' "$created_node" >"$state/node"; fi
    fi
    exit "$rc"
  }
  trap cleanup_setup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  printf 'Building the isolated demo and observer before creating %s.\n' "$cluster"
  (cd "$script_dir" && go build -o "$state/demo-observer" ./cmd/demo-observer)
  (cd test/e2e && go test -c -o "$state/performance.test" ./tests/performance)
  docker build -t "live-telemetry-tool:$cluster" "$script_dir"
  docker build -t "live-telemetry-gateway:$cluster" -f "$script_dir/deploy/Dockerfile.gateway" "$script_dir"
  docker build --build-arg ARCH=amd64 --build-arg TARGETOS=linux \
    -t "live-telemetry-operator:$cluster" operator/src
  docker build --build-arg ARCH=amd64 --build-arg TARGETOS=linux \
    -t "live-telemetry-sidecar:$cluster" operator/cnpg-plugins/sidecar-injector
  touch "$state/creating-cluster"
  kind create cluster --name "$cluster" --image kindest/node:v1.35.0 \
    --kubeconfig "$KUBECONFIG" --wait 180s
  docker inspect "$cluster-control-plane" --format '{{.Id}}' >"$state/node"
  kind load docker-image --name "$cluster" \
    "live-telemetry-tool:$cluster" "live-telemetry-gateway:$cluster" \
    "live-telemetry-operator:$cluster" "live-telemetry-sidecar:$cluster"
  helm repo add demo-cnpg https://cloudnative-pg.github.io/charts/
  helm dependency build operator/documentdb-helm-chart
  helm install cert-manager oci://quay.io/jetstack/charts/cert-manager \
    --version v1.19.3 --namespace cert-manager --create-namespace \
    --set crds.enabled=true --kube-context "$context" --wait --timeout 180s
  helm install documentdb-operator operator/documentdb-helm-chart \
    --namespace documentdb-operator --create-namespace \
    --set image.documentdbk8soperator.repository=live-telemetry-operator \
    --set "image.documentdbk8soperator.tag=$cluster" \
    --set image.documentdbk8soperator.pullPolicy=Never \
    --set image.sidecarinjector.repository=live-telemetry-sidecar \
    --set "image.sidecarinjector.tag=$cluster" \
    --set image.sidecarinjector.pullPolicy=Never \
    --set documentDbVersion=0.116.0 --set gatewayImagePullPolicy=IfNotPresent \
    --kube-context "$context" --wait --timeout 240s
  sed "s@image: live-telemetry-tool:prototype@image: live-telemetry-tool:$cluster@" "$script_dir/deploy/tool.yaml" |
    kubectl --context "$context" apply -f -
  openssl rand -hex 24 |
    jq -R 'if test("^[a-f0-9]{48}$") then
      {apiVersion:"v1",kind:"Secret",metadata:{name:"documentdb-credentials",namespace:"live-telemetry"},type:"Opaque",stringData:{username:"telemetry_user",password:.}}
      else error("invalid synthetic credential") end' |
    kubectl --context "$context" create -f -
  sed "s@live-telemetry-gateway:prototype@live-telemetry-gateway:$cluster@" "$script_dir/deploy/documentdb.yaml" |
    kubectl --context "$context" apply -f -
  kubectl --context "$context" -n live-telemetry rollout status deployment/live-telemetry-tool --timeout=90s
  kubectl --context "$context" -n live-telemetry wait --for=create pod/telemetry-db-1 --timeout=180s
  kubectl --context "$context" -n live-telemetry wait --for=condition=Ready pod/telemetry-db-1 --timeout=300s
  kubectl --context "$context" -n live-telemetry get pods -o json |
    jq '[.items[] | {pod:.metadata.name,uid:.metadata.uid,containers:[.status.containerStatuses[] | {name,image,imageID}]}]' \
      >"$state/images.json"
  touch "$state/up-complete"
  printf 'Demo is ready: %s. Run demo.sh run from the terminal with your authenticated Copilot CLI.\n' "$context"
  exit 0
fi

check_state
read_state cluster
cluster=$REPLY
[[ "$cluster" =~ ^telemetry-demo-[a-f0-9]{16}$ ]] || fail "Refusing an unrecognized cluster name."
read_state daemon
[[ "$(docker info --format '{{.ID}}')" == "$REPLY" ]] || fail "Docker daemon changed; refusing to operate on another environment."
export KUBECONFIG="$state/kubeconfig"
context="kind-$cluster"
if [[ -f "$state/node" ]]; then
  read_state node
  node=$REPLY
  [[ "$node" =~ ^[a-f0-9]{64}$ ]] || fail "Invalid recorded node identity."
  actual=$(docker ps -a --filter "label=io.x-k8s.kind.cluster=$cluster" --format '{{.ID}}' --no-trunc)
  [[ -z "$actual" || "$actual" == "$node" ]] || fail "The cluster node no longer matches this demo's ownership record."
else
  actual=$(docker ps -a --filter "label=io.x-k8s.kind.cluster=$cluster" --format '{{.ID}}' --no-trunc)
  [[ -z "$actual" ]] || fail "A node exists without a completed ownership record; refusing deletion."
fi

if [[ "$action" == down ]]; then
  [[ $# -eq 0 ]] || fail "down takes no arguments."
  if [[ -n "$actual" ]]; then kind delete cluster --name "$cluster"; fi
  [[ -z "$(docker ps -a --filter "label=io.x-k8s.kind.cluster=$cluster" --format '{{.ID}}')" ]] ||
    fail "Owned cluster removal did not finish."
  [[ "$(realpath "$state")" == "$script_dir/.demo" ]] || fail "Runtime state moved; refusing cleanup."
  rm -rf -- "$state"
  printf 'Owned cluster and private runtime state removed; local image cache and selected evidence preserved.\n'
  exit 0
fi

[[ "$action" == _workload && $# -eq 1 ]] || fail "Invalid workload invocation."
check_run_id "$1"
run_id=$1
run_dir="$state/run-$run_id"
[[ -d "$run_dir" && ! -L "$run_dir" && -n "$actual" ]] || fail "Missing run directory or owned node."
[[ -f "$state/up-complete" ]] || fail "Demo setup is incomplete."
kubectl --context "$context" -n live-telemetry wait --for=condition=Ready pod/telemetry-db-1 --timeout=60s
forward_pid=
workload_pid=
cleanup_workload() {
  local rc=$?
  trap - EXIT INT TERM
  touch "$run_dir/cancel"
  if [[ -n "$workload_pid" ]] && kill -0 "$workload_pid" 2>/dev/null; then
    kill -TERM "$workload_pid"
    wait "$workload_pid" || :
  fi
  if [[ -n "$forward_pid" ]] && kill -0 "$forward_pid" 2>/dev/null; then
    kill -TERM "$forward_pid"
    wait "$forward_pid" || :
  fi
  exit "$rc"
}
trap cleanup_workload EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
kubectl --context "$context" -n live-telemetry port-forward \
  deployment/live-telemetry-tool --address=127.0.0.1 :8080 >"$run_dir/forward.log" 2>&1 &
forward_pid=$!
port=
for ((attempt=0; attempt<100; attempt++)); do
  kill -0 "$forward_pid" 2>/dev/null || fail "MCP port-forward exited."
  port=$(sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9][0-9]*\) -> 8080$/\1/p' "$run_dir/forward.log")
  [[ -z "$port" ]] || break
  sleep 0.1
done
[[ "$port" =~ ^[0-9]+$ ]] || fail "MCP port-forward did not become ready."
endpoint="http://127.0.0.1:$port/mcp"
printf '%s\n' "$run_id" >"$state/current-run"
"$state/demo-observer" --action prepare --dir "$run_dir" --endpoint "$endpoint"
export E2E_LIVE_TELEMETRY=1 E2E_LIVE_TELEMETRY_MCP_URL="$endpoint" E2E_LIVE_TELEMETRY_DEMO_DIR="$run_dir"
cd "$repo_root/test/e2e"
timeout --signal=TERM --kill-after=10s 12m "$state/performance.test" \
  -test.run TestPerformance -test.v -test.timeout 13m \
  -ginkgo.v -ginkgo.label-filter=live-telemetry-trials &
workload_pid=$!
wait "$workload_pid"
workload_pid=
for ((attempt=0; attempt<60; attempt++)); do
  if jq -e '.disconnected == true' "$run_dir/observer.json" >/dev/null; then break; fi
  sleep 1
done
"$state/demo-observer" --action report --dir "$run_dir" --output "$script_dir/evidence/latest-demo.json"
