# IOUring Feature Gate Playground

Demonstrate the DocumentDB operator's native `IOUring` feature gate. This is the
supported opt-in path for PostgreSQL 18 `io_method = io_uring` in DocumentDB.

`io_uring` can improve heavy read-I/O paths, but it is also a recurring Linux
kernel exploit surface. Container runtimes therefore remove the
`io_uring_setup`, `io_uring_enter`, and `io_uring_register` syscalls from
`RuntimeDefault` seccomp profiles. The feature is opt-in so clusters keep the
hardened default unless an operator admin deliberately enables it.

When `spec.featureGates.IOUring: true` is set on a `DocumentDB` resource, the
operator does two things:

1. Sets PostgreSQL `io_method=io_uring` on the generated CNPG `Cluster`.
2. Relaxes the postgres pod seccomp profile, using operator-level env config:
   - `DOCUMENTDB_IOURING_SECCOMP_MODE=localhost` (default, hardened)
   - `DOCUMENTDB_IOURING_SECCOMP_PROFILE=profiles/documentdb-iouring.json`
     (default Localhost profile path)
   - or `DOCUMENTDB_IOURING_SECCOMP_MODE=unconfined` (simplest, least secure)

No Kyverno mutation policy is needed here; the DocumentDB operator owns both the
PostgreSQL parameter and the seccomp wiring.

```mermaid
flowchart LR
  DB[DocumentDB CR<br/>featureGates.IOUring=true] --> OP[DocumentDB operator]
  OP -->|io_method=io_uring| CNPG[CNPG Cluster]
  OP -->|seccompProfile<br/>Localhost or Unconfined| CNPG
  CNPG --> PG[PostgreSQL 18 pod]
  PG --> PVC[(PVC / storage)]
```

## Prerequisites

- DocumentDB operator version that includes the native `IOUring` feature gate.
- PostgreSQL 18 image (`ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` in
  `manifests/documentdb-iouring.yaml`).
- Linux nodes with `io_uring_disabled=0`:
  ```bash
  kubectl debug node/<node> -it --image=busybox:1.36 -- chroot /host cat /proc/sys/kernel/io_uring_disabled
  ```
- For `localhost` mode, the profile must exist on every node that can run
  postgres pods at:
  `/var/lib/kubelet/seccomp/profiles/documentdb-iouring.json`.

## Quick start: local kind cluster (Localhost seccomp)

Run from this directory so the kind `extraMounts` path resolves to `./seccomp`:

```bash
cd documentdb-playground/io-uring-feature

kind create cluster --config kind/kind-cluster.yaml

# Install cert-manager + the DocumentDB operator per the project docs, setting
# the io_uring seccomp mode via Helm values (first-class config):
#   helm upgrade --install documentdb-operator <chart> -n documentdb-operator \
#     --set operator.ioUring.seccompMode=localhost \
#     --set operator.ioUring.seccompProfile=profiles/documentdb-iouring.json
# Already-installed operator? Patch the deployment env instead:
kubectl patch deployment documentdb-operator -n documentdb-operator \
  --type strategic --patch-file operator-values/localhost-patch.yaml
kubectl rollout status deployment/documentdb-operator -n documentdb-operator

kubectl apply -f manifests/documentdb-iouring.yaml
```

The kind config mounts `./seccomp/documentdb-iouring.json` into each node as the
operator's default Localhost profile path, so the DaemonSet installer is not
needed for kind. `localhost` is also the operator's built-in default, so the
`--set` flags above are only required to override the mode/profile.

## Quick start: real cluster (Localhost seccomp)

```bash
cd documentdb-playground/io-uring-feature

# Install the profile on every node.
kubectl apply -k seccomp/
kubectl rollout status ds/documentdb-iouring-seccomp-installer -n kube-system --timeout=180s

# Install cert-manager + the DocumentDB operator per the project docs, setting
# the io_uring seccomp mode via Helm values (first-class config):
#   helm upgrade --install documentdb-operator <chart> -n documentdb-operator \
#     --set operator.ioUring.seccompMode=localhost \
#     --set operator.ioUring.seccompProfile=profiles/documentdb-iouring.json
# Already-installed operator? Patch the deployment env instead:
kubectl patch deployment documentdb-operator -n documentdb-operator \
  --type strategic --patch-file operator-values/localhost-patch.yaml
kubectl rollout status deployment/documentdb-operator -n documentdb-operator

kubectl apply -f manifests/documentdb-iouring.yaml
```

If your operator namespace or deployment name differs, adjust the commands. The
profile filename intentionally matches the operator default:
`profiles/documentdb-iouring.json`.

## Quick start: unconfined seccomp (no node profile)

Use this only for a quick proof of functionality. It disables the seccomp sandbox
for postgres pods that enable IOUring.

```bash
cd documentdb-playground/io-uring-feature

# Helm values: --set operator.ioUring.seccompMode=unconfined
# Or patch an already-installed operator:
kubectl patch deployment documentdb-operator -n documentdb-operator \
  --type strategic --patch-file operator-values/unconfined-patch.yaml
kubectl rollout status deployment/documentdb-operator -n documentdb-operator

kubectl apply -f manifests/documentdb-iouring.yaml
```

## Verification

Wait for the generated CNPG cluster and primary pod:

```bash
kubectl get documentdb -n iouring-demo iouring-demo
kubectl get cluster.postgresql.cnpg.io -n iouring-demo iouring-demo
kubectl get pods -n iouring-demo -l cnpg.io/cluster=iouring-demo

POD=$(kubectl get pod -n iouring-demo \
  -l cnpg.io/cluster=iouring-demo,cnpg.io/instanceRole=primary \
  -o jsonpath='{.items[0].metadata.name}')
```

Confirm PostgreSQL is using `io_uring`:

```bash
kubectl exec -n iouring-demo "$POD" -c postgres -- \
  psql -U postgres -tAc 'SHOW io_method;'
# expected: io_uring
```

Confirm seccomp was set by the operator:

```bash
kubectl get cluster.postgresql.cnpg.io iouring-demo -n iouring-demo \
  -o jsonpath='{.spec.seccompProfile}{"\n"}'
kubectl get pod -n iouring-demo "$POD" \
  -o jsonpath='{.spec.securityContext.seccompProfile}{"\n"}'
# localhost default: {"type":"Localhost","localhostProfile":"profiles/documentdb-iouring.json"}
# unconfined:        {"type":"Unconfined"}
```

Confirm postgres is not crashlooping and `pg_stat_io` reads are visible:

```bash
kubectl get pod -n iouring-demo "$POD" \
  -o jsonpath='{range .status.containerStatuses[*]}{.name}{" restarts="}{.restartCount}{" ready="}{.ready}{"\n"}{end}'

kubectl exec -n iouring-demo "$POD" -c postgres -- psql -U postgres -c \
  "SELECT backend_type, object, context, reads, read_time FROM pg_stat_io WHERE reads > 0 ORDER BY reads DESC LIMIT 10;"
```

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `FATAL: could not setup io_uring queue: Operation not permitted` | RuntimeDefault still blocks the io_uring syscalls | Ensure `spec.featureGates.IOUring: true`, restart the operator after setting `DOCUMENTDB_IOURING_SECCOMP_MODE`, and recreate/restart postgres pods. |
| Same crash in `localhost` mode | Profile missing or wrong path on a node | Verify `/var/lib/kubelet/seccomp/profiles/documentdb-iouring.json` exists on every node, or set `DOCUMENTDB_IOURING_SECCOMP_PROFILE` to the installed relative path. |
| Pod shows `Unconfined` when Localhost was expected | Operator env is set to `unconfined` | Patch the deployment with `operator-values/localhost-patch.yaml` and wait for rollout. |
| `SHOW io_method;` is not `io_uring` | Feature gate not applied or old operator version | Check `kubectl get documentdb -n iouring-demo iouring-demo -o yaml` and operator logs. |

## How this differs from `../io-uring-benchmark/`

- `io-uring-feature/` is the supported operator-native opt-in: users set
  `spec.featureGates.IOUring: true`; the operator sets both `io_method` and
  seccomp on the generated CNPG `Cluster`.
- `io-uring-benchmark/` is a raw benchmark harness: it changes
  `spec.postgres.parameters.io_method` directly and uses external Kyverno
  policies to mutate seccomp. That remains useful for benchmarking multiple
  `io_method` values, but it is not the normal feature-gate workflow.

## File reference

| Path | Purpose |
|---|---|
| `manifests/documentdb-iouring.yaml` | Namespace, demo credentials Secret, and `DocumentDB` CR with `featureGates.IOUring: true`. |
| `seccomp/documentdb-iouring.json` | Curated RuntimeDefault-equivalent profile plus `io_uring_*` syscalls. |
| `seccomp/deploy-seccomp-daemonset.yaml` + `seccomp/kustomization.yaml` | Installs the profile to real cluster nodes. |
| `kind/kind-cluster.yaml` | Local kind cluster config that mounts `./seccomp` into kubelet's Localhost profile directory. |
| `operator-values/*.yaml` | Deployment patch snippets for operator `DOCUMENTDB_IOURING_*` env vars. |
