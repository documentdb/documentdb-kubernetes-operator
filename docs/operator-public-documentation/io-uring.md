---
title: io_uring async I/O feature gate
description: Enable PostgreSQL 18 asynchronous I/O (io_method=io_uring) in DocumentDB through the IOUring feature gate, including the seccomp trade-offs, operator configuration, prerequisites, verification, and troubleshooting.
tags:
  - configuration
  - feature-gates
  - performance
  - security
  - io_uring
---

# io_uring async I/O feature gate

The `IOUring` feature gate enables PostgreSQL 18's asynchronous I/O backend (`io_method=io_uring`) for a DocumentDB cluster. Because io_uring requires relaxing the container's seccomp sandbox, the feature is **opt-in** and disabled by default.

## Overview

PostgreSQL 18 introduces a pluggable asynchronous I/O subsystem. On Linux, the `io_uring` backend submits read I/O through the kernel's [io_uring](https://en.wikipedia.org/wiki/Io_uring) interface, which overlaps storage latency instead of blocking on each read.

DocumentDB doesn't turn this on for you automatically, for one reason: **security**. io_uring has been a recurring kernel-exploit surface, so the container runtime's `RuntimeDefault` seccomp profile strips the `io_uring_setup`, `io_uring_enter`, and `io_uring_register` syscalls. CloudNative-PG (CNPG) runs the PostgreSQL pods with `seccompProfile=RuntimeDefault`, so without intervention PostgreSQL crashes at startup with:

```text
FATAL: could not setup io_uring queue: Operation not permitted
```

Enabling io_uring therefore means relaxing seccomp — a security trade-off that the Kubernetes cluster operator must consciously accept. DocumentDB makes that choice explicit through the `IOUring` feature gate rather than enabling it silently.

!!! note
    The `IOUring` gate controls one DocumentDB cluster. The seccomp *profile* (which Localhost profile the operator points the pods at) is configured once on the operator and applies to every DocumentDB cluster it manages. See [Seccomp configuration](#seccomp-configuration).

## What enabling the gate does

When you set `spec.featureGates.IOUring: true`, the operator does two things natively — **no external Kyverno policy or admission webhook is required**:

1. **Sets `io_method=io_uring`** as a protected PostgreSQL parameter. This value can't be overridden through `spec.postgres.parameters`. (See [PostgreSQL parameter tuning](postgresql-tuning.md) for how protected parameters work.)
2. **Relaxes the PostgreSQL container's seccomp profile** so the three io_uring syscalls are allowed. The operator points the CNPG cluster's pod security context at a Localhost seccomp profile (see [Seccomp configuration](#seccomp-configuration)).

When the gate is disabled (the default), the operator changes nothing and CNPG keeps its hardened `RuntimeDefault` profile.

## How to enable

Add the feature gate to your DocumentDB custom resource:

```yaml title="documentdb.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: "50Gi"
  featureGates:
    IOUring: true   # (1)!
```

1. Opt in to PostgreSQL 18 `io_method=io_uring`. The operator also relaxes the PostgreSQL container seccomp profile using the operator-level Localhost profile.

For the full field reference, see [DocumentDBSpec](preview/api-reference.md#documentdbspec) in the API Reference.

## Seccomp configuration

Which Localhost seccomp profile the operator points the pods at is **operator-level configuration**, set through an environment variable on the operator deployment. The same profile applies to **all** DocumentDB clusters managed by that operator.

| Environment variable | Values | Default | Description |
|----------------------|--------|---------|-------------|
| `DOCUMENTDB_IOURING_SECCOMP_PROFILE` | profile path | `profiles/documentdb-iouring.json` | Localhost profile path, relative to the node's kubelet seccomp root (`/var/lib/kubelet/seccomp`). |

With the bundled Helm chart, set this through a first-class value (preferred):

```bash
helm upgrade --install documentdb-operator <chart> -n documentdb-operator \
  --set operator.ioUring.seccompProfile=profiles/documentdb-iouring.json
```

Leaving the value empty keeps the operator's built-in default
(`profiles/documentdb-iouring.json`). For an already-installed operator you can patch
the manager container env directly instead:

```yaml title="operator-deployment.yaml (excerpt)"
spec:
  template:
    spec:
      containers:
        - name: documentdb-operator
          env:
            - name: DOCUMENTDB_IOURING_SECCOMP_PROFILE
              value: "profiles/documentdb-iouring.json"
```

The operator points the PostgreSQL pods at a **Localhost** seccomp profile that re-allows only the three io_uring syscalls on top of the runtime default. This keeps the rest of the sandbox intact.

The referenced profile JSON — the upstream `RuntimeDefault` profile **plus** `io_uring_setup`, `io_uring_enter`, and `io_uring_register` — **must be pre-installed on every node that runs PostgreSQL pods**, at the path resolved under `/var/lib/kubelet/seccomp`. If the profile is missing on a node, the pod scheduled there fails to start.

The hands-on [io_uring feature playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/io-uring-feature) provides the curated profile plus a kind `extraMount` and a DaemonSet installer that distribute it to every node.

!!! warning "Security trade-off"
    Relaxing seccomp — even with the hardened Localhost profile that re-allows only the three io_uring syscalls — widens the kernel attack surface. io_uring has been a recurring kernel-exploit vector, so this is a trade-off you accept as the cluster operator. That is why the gate is opt-in and disabled by default.

## Prerequisites

- **PostgreSQL 18 image.** `io_method=io_uring` exists only in PostgreSQL 18 and later. Make sure the cluster runs a PG18 image.
- **A node kernel with io_uring enabled.** The nodes must run a kernel that exposes io_uring with `io_uring_disabled=0`. Modern AKS, EKS, and GKE node images qualify.
- **The seccomp profile installed on nodes.** The Localhost profile referenced by `DOCUMENTDB_IOURING_SECCOMP_PROFILE` must exist on every node that runs PostgreSQL pods. The [io_uring feature playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/io-uring-feature) automates this.

## Verification

After enabling the gate and waiting for the rolling restart to finish, confirm io_uring is active.

1. **Check that the PostgreSQL pods are running** (not CrashLooping):

    ```bash
    kubectl get pods -n <namespace> -l documentdb.io/cluster=<cluster-name>
    ```

2. **Confirm `io_method` is `io_uring`** by connecting to PostgreSQL:

    ```bash
    kubectl exec -it <pod-name> -n <namespace> -c postgres -- \
      psql -U postgres -c "SHOW io_method;"
    ```

    Expected output:

    ```text
     io_method
    -----------
     io_uring
    (1 row)
    ```

3. **Inspect the pod's seccomp profile** to confirm the operator relaxed it:

    ```bash
    kubectl get pod <pod-name> -n <namespace> \
      -o jsonpath='{.spec.securityContext.seccompProfile}'
    ```

    This shows `{"type":"Localhost","localhostProfile":"profiles/documentdb-iouring.json"}`.

4. **Confirm reads are flowing through the I/O path** with `pg_stat_io`:

    ```bash
    kubectl exec -it <pod-name> -n <namespace> -c postgres -- \
      psql -U postgres -c "SELECT backend_type, object, context, reads FROM pg_stat_io WHERE reads > 0;"
    ```

## Performance

io_uring's measured benefit is primarily **tail-latency stability on I/O-bound scans**, not raw throughput. On Azure Premium SSD at low concurrency, `io_method=io_uring` delivers lower, more predictable p95/p99 latency on heavy range scans and reduces in-engine read-wait time, while point lookups and aggregate throughput are largely unchanged.

## Troubleshooting

### PostgreSQL CrashLoops with "could not setup io_uring queue"

If a PostgreSQL pod restarts repeatedly and its logs show:

```text
FATAL: could not setup io_uring queue: Operation not permitted
```

the seccomp profile wasn't relaxed for that pod. Check, in order:

- **Profile not installed on the node.** The profile JSON is missing on the node where the pod is scheduled. Install it on every node that runs PostgreSQL pods — the [io_uring feature playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/io-uring-feature) DaemonSet handles this.
- **Wrong profile path.** `DOCUMENTDB_IOURING_SECCOMP_PROFILE` doesn't match the actual file path under `/var/lib/kubelet/seccomp` on the node. Align the env var with the installed file.
- **Operator not restarted.** The profile env var changed but the operator deployment wasn't rolled, so new clusters still reference the old path. Verify the pod's seccomp profile with the [verification](#verification) command above.

## Related

- [PostgreSQL parameter tuning](postgresql-tuning.md) — how protected parameters such as `io_method` are managed
- [API Reference: DocumentDBSpec](preview/api-reference.md#documentdbspec) — the `featureGates` field
- [io_uring feature playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/io-uring-feature) — kind `extraMount`, DaemonSet installer, and curated profile
