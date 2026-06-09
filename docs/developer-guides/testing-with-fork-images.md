# Quick End-to-End Testing With Your Fork's Images

This guide walks through verifying operator and DocumentDB changes end-to-end on a real Kubernetes cluster (Kind, AKS, EKS, GKE) by publishing candidate images from your own GitHub fork's Container Registry (GHCR) and installing the local Helm chart against them.

It covers two independent image tracks — pick whichever you actually changed:

| Track | What it ships | Repo to fork & build from | Workflow to run |
|---|---|---|---|
| **Operator track** | `operator`, `sidecar` | This repo (`documentdb/documentdb-kubernetes-operator`) | [`RELEASE - Build Operator Candidate Images`](../../.github/workflows/build_operator_images.yml) |
| **Database track** | `documentdb` (extension), `gateway` | Upstream [`documentdb/documentdb`](https://github.com/documentdb/documentdb) **then** this repo | DocumentDB release pipeline → [`RELEASE - Build DocumentDB Candidate Images`](../../.github/workflows/build_documentdb_images.yml) |

If your change is purely Go controller code, skip Step 1 entirely and use the upstream `0.110.0` (or any released) database images.

---

## Prerequisites

- A fork of [`documentdb/documentdb-kubernetes-operator`](https://github.com/documentdb/documentdb-kubernetes-operator) with Actions enabled
- (If changing the database) a fork of [`documentdb/documentdb`](https://github.com/documentdb/documentdb) with Actions enabled
- A Kubernetes cluster with cert-manager installed (`v1.19+`, aligned with the [version compatibility matrix in AGENTS.md](../../AGENTS.md#version-compatibility-matrix)) and Kubernetes `1.35+`
- A Kubernetes node runtime backed by `containerd` or `CRI-O`. The operator requires the `ImageVolume` feature (GA in Kubernetes 1.35) to mount the DocumentDB extension image, which the legacy Docker runtime does not support.
- `kubectl`, `helm 3.x`, `git`, `curl`, `jq`
- GHCR packages on your fork must be **public** for the Helm install below to pull without auth (forks of public repos default to public, but double-check on `https://github.com/<your-gh-user>?tab=packages`). If you must keep them private, create a `dockerconfigjson` pull secret in the `documentdb-operator` namespace and pass `--set imagePullSecrets[0].name=<secret>` to `helm upgrade`; otherwise pods will fail with `ImagePullBackOff` at startup.

---

## Step 1 — (Database track only) Build extension + gateway from a documentdb fork

Skip this step if you don't need to change the DocumentDB extension or gateway.

1. **Fork** [`documentdb/documentdb`](https://github.com/documentdb/documentdb) and push your changes.
2. **Run the DocumentDB release pipeline** on your fork (typically `Release` workflow). This must publish:
    - A GitHub release named `v<MAJOR>.<MINOR>-<PATCH>` (note the dash before patch — for example `v0.110-0`).
    - Per-arch `.deb` assets attached to that release: `deb13-postgresql-18-documentdb_<MAJOR>.<MINOR>-<PATCH>_amd64.deb` and `_arm64.deb`.
    - A `documentdb-local` GHCR image: `ghcr.io/<your-gh-user>/documentdb/documentdb-local:pg17-<MAJOR>.<MINOR>.<PATCH>`.

    The operator-side workflow probes for these exact paths in [its verify steps](../../.github/workflows/build_documentdb_images.yml) (`Verify public extension release assets` and `Verify public gateway source image`).

3. **In your operator fork**, run **Actions → `RELEASE - Build DocumentDB Candidate Images` → Run workflow**. Provide these inputs:
    - `version`: `0.110.0` (or whatever you released in step 2)
    - `documentdb_extension_github_repo`: `<your-gh-user>/documentdb`
    - `documentdb_gateway_image_repo`: `ghcr.io/<your-gh-user>/documentdb/documentdb-local`

4. After the run finishes, your fork has the candidate tag (and per-arch variants):

    ```text
    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/documentdb:<version>-build-<run_id>-<attempt>-<sha>
    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/gateway:<version>-build-<run_id>-<attempt>-<sha>
    ```

    The workflow only publishes this computed `image_tag` (printed at the top of the run summary as `candidate_version`); it does **not** retag to the bare `<version>`. The bare-version retag is performed by [`release_documentdb_images.yml`](../../.github/workflows/release_documentdb_images.yml), which is the GA promotion path and should not be run for fork testing. Use the candidate tag from the run summary directly in Step 3.

---

## Step 2 — Build operator + sidecar images from this repo

1. Push your operator changes to a branch on your fork (for example `rayhan/fix-tls-mode`).
2. **Actions → `RELEASE - Build Operator Candidate Images` → Run workflow**. Pass:
    - `version`: a unique tag for this iteration, for example `0.2.0-tls-test-1`.
3. The workflow tags images as `<version>-test` and pushes a multi-arch manifest:

    ```text
    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/operator:0.2.0-tls-test-1-test
    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/sidecar:0.2.0-tls-test-1-test
    ```

    (See [build_operator_images.yml#L29-L30](../../.github/workflows/build_operator_images.yml).)

4. Verify the tag is queryable from anywhere (no auth needed for public packages):

    ```bash
    TOKEN=$(curl -s "https://ghcr.io/token?service=ghcr.io&scope=repository:<your-gh-user>/documentdb-kubernetes-operator/operator:pull" | jq -r .token)
    curl -sI -H "Authorization: Bearer $TOKEN" \
      https://ghcr.io/v2/<your-gh-user>/documentdb-kubernetes-operator/operator/manifests/0.2.0-tls-test-1-test \
      | head -3
    # Expect: HTTP/2 200
    ```

---

## Step 3 — Install the local Helm chart against your fork's images

The Helm chart in this repo (`operator/documentdb-helm-chart/`) lets you point the **operator** and **sidecar** images at your fork via `--set` flags. The chart does **not** redirect the `documentdb` and `gateway` container repositories — those default to `ghcr.io/documentdb/documentdb-kubernetes-operator/{documentdb,gateway}:<documentDbVersion>` and can only be overridden per-instance on the `DocumentDB` CR via `spec.documentDBImage` / `spec.gatewayImage` (see the [Deploy a test DocumentDB instance](#deploy-a-test-documentdb-instance) section below).

```bash
GH_USER=<your-gh-user>
OP_TAG=0.2.0-tls-test-1-test            # operator/sidecar tag from Step 2

cd operator/documentdb-helm-chart
helm dependency update

helm upgrade --install documentdb-operator . \
  --namespace documentdb-operator --create-namespace \
  --set image.documentdbk8soperator.repository=ghcr.io/${GH_USER}/documentdb-kubernetes-operator/operator \
  --set image.sidecarinjector.repository=ghcr.io/${GH_USER}/documentdb-kubernetes-operator/sidecar \
  --set-string image.documentdbk8soperator.tag=${OP_TAG} \
  --set-string image.sidecarinjector.tag=${OP_TAG} \
  --wait --timeout 10m
```

> **Helm doesn't auto-update CRDs**, so reapply them whenever the API types change. From inside `operator/documentdb-helm-chart` (where the previous command left you):
>
> ```bash
> kubectl apply -f ./crds/
> ```

Verify rollout:

```bash
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
```

### Deploy a test DocumentDB instance

Create a namespace and credentials Secret first (the operator expects keys `username` and `password`; the default Secret name is `documentdb-credentials` — see [quickstart-kind.md](../operator-public-documentation/preview/getting-started/quickstart-kind.md#create-credentials) for the canonical example):

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: documentdb-test
---
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-test
type: Opaque
stringData:
  username: dev_user
  password: DevPassword123
EOF
```

Then apply the test DocumentDB cluster, substituting the candidate tag you noted from the Step 1.4 run summary into `DB_TAG`:

```bash
GH_USER=<your-gh-user>
DB_TAG=<version>-build-<run_id>-<attempt>-<sha>   # candidate_version from the Step 1.4 run summary
                                                  # If you skipped Step 1, use an upstream tag (for example, 0.110.0)
                                                  # and point the images at ghcr.io/documentdb/...

kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: dev-test
  namespace: documentdb-test
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  documentDBImage: ghcr.io/${GH_USER}/documentdb-kubernetes-operator/documentdb:${DB_TAG}
  gatewayImage:    ghcr.io/${GH_USER}/documentdb-kubernetes-operator/gateway:${DB_TAG}
  resource:
    storage:
      pvcSize: 5Gi
  exposeViaService:
    serviceType: ClusterIP
EOF
```

Inspect status:

```bash
kubectl get documentdb -n documentdb-test
kubectl get documentdb dev-test -n documentdb-test -o jsonpath='{.status.connectionString}'
kubectl logs -n documentdb-operator deploy/documentdb-operator --tail=50
```

---

## Iterating

Push a fresh tag (for example `0.2.0-tls-test-2`), rerun the operator build workflow, then either:

- **Quick swap** (no re-deploy): update images in place
    ```bash
    NEW_TAG=0.2.0-tls-test-2-test
    kubectl set image -n documentdb-operator deploy/documentdb-operator \
      documentdb-operator=ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/operator:${NEW_TAG}
    kubectl set image -n cnpg-system deploy/sidecar-injector \
      cnpg-i-sidecar-injector=ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/sidecar:${NEW_TAG}
    kubectl rollout status -n documentdb-operator deploy/documentdb-operator
    ```
- **Full reinstall**: `helm uninstall documentdb-operator -n documentdb-operator` then rerun the `helm upgrade --install` block above.

If the CRD changed between iterations, reapply (from inside `operator/documentdb-helm-chart`): `kubectl apply -f ./crds/`. If you're running from the repo root instead, use `kubectl apply -f operator/documentdb-helm-chart/crds/`.

---

## Cleanup

```bash
kubectl delete documentdb --all -A
helm uninstall documentdb-operator -n documentdb-operator
helm uninstall documentdb-operator-cloudnative-pg -n cnpg-system 2>/dev/null || true
kubectl delete ns documentdb-operator cnpg-system --ignore-not-found
```

---

## Related

- [Development environment](development-environment.md) — daily workflow, devcontainer, make targets
- [Sidecar injector plugin configuration](sidecar-injector-plugin-configuration.md)
- [build_operator_images.yml](../../.github/workflows/build_operator_images.yml)
- [build_documentdb_images.yml](../../.github/workflows/build_documentdb_images.yml)
- [release_operator.yml](../../.github/workflows/release_operator.yml) — the GA promotion path (test gate + retag + Helm chart publish), distinct from the candidate build flow above
