# Quick End-to-End Testing With Your Fork's Images

This guide walks through verifying operator and DocumentDB changes end-to-end on a real Kubernetes cluster (Kind, AKS, EKS, GKE) by publishing candidate images from your own GitHub fork's Container Registry (GHCR) and installing the local Helm chart against them.

It covers two independent image tracks — pick whichever you actually changed:

| Track | What it ships | Repo to fork & build from | Workflow to run |
|---|---|---|---|
| **Operator track** | `operator`, `sidecar`, optional `wal-replica` | This repo (`documentdb/documentdb-kubernetes-operator`) | [`RELEASE - Build Operator Candidate Images`](../../.github/workflows/build_operator_images.yml) |
| **Database track** | `documentdb` (extension), `gateway` | Upstream [`documentdb/documentdb`](https://github.com/documentdb/documentdb) **then** this repo | DocumentDB release pipeline → [`RELEASE - Build DocumentDB Candidate Images`](../../.github/workflows/build_documentdb_images.yml) |

If your change is purely Go controller code, skip Step 1 entirely and use the upstream `0.110.0` (or any released) database images.

---

## Prerequisites

- A fork of [`documentdb/documentdb-kubernetes-operator`](https://github.com/documentdb/documentdb-kubernetes-operator) with Actions enabled
- (If changing the database) a fork of [`documentdb/documentdb`](https://github.com/documentdb/documentdb) with Actions enabled
- A Kubernetes cluster with cert-manager installed (`v1.13.2+`) and Kubernetes `1.35+`
- `kubectl`, `helm 3.x`, `git`
- GHCR packages on your fork should be **public** (default for public forks). Otherwise create a `dockerconfigjson` pull secret and reference it via `imagePullSecrets`.

---

## Step 1 — (Database track only) Build extension + gateway from a documentdb fork

Skip this step if you don't need to change the DocumentDB extension or gateway.

1. **Fork** [`documentdb/documentdb`](https://github.com/documentdb/documentdb) and push your changes.
2. **Run the DocumentDB release pipeline** on your fork (typically `Release` workflow). This must publish:
    - A GitHub release named `v<MAJOR>.<MINOR>-<PATCH>` (note the dash before patch — for example `v0.110-0`).
    - Per-arch `.deb` assets attached to that release: `deb13-postgresql-18-documentdb_<MAJOR>.<MINOR>-<PATCH>_amd64.deb` and `_arm64.deb`.
    - A `documentdb-local` GHCR image: `ghcr.io/<your-gh-user>/documentdb/documentdb-local:pg17-<MAJOR>.<MINOR>.<PATCH>`.

    The operator-side workflow probes for these exact paths in [its verify steps](../../.github/workflows/build_documentdb_images.yml#L72-L88).

3. **In your operator fork**, run **Actions → `RELEASE - Build DocumentDB Candidate Images` → Run workflow**. Provide these inputs:
    - `version`: `0.110.0` (or whatever you released in step 2)
    - `documentdb_extension_github_repo`: `<your-gh-user>/documentdb`
    - `documentdb_gateway_image_repo`: `ghcr.io/<your-gh-user>/documentdb/documentdb-local`

4. After the run finishes, your fork has:

    ```text
    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/documentdb:0.110.0
    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/gateway:0.110.0
    ```

    (The actual published tag is the `image_tag` output computed by the workflow — usually `<version>-build-<run_id>-<attempt>-<sha>`. The workflow also retags it with the bare `<version>` for convenience; check the run summary.)

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

The Helm chart in this repo (`operator/documentdb-helm-chart/`) lets you point each image at your fork via `--set` flags.

```bash
GH_USER=<your-gh-user>
OP_TAG=0.2.0-tls-test-1-test            # from Step 2
DB_TAG=0.110.0                          # from Step 1, or upstream e.g. 0.110.0

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

> **Helm doesn't auto-update CRDs**, so reapply them whenever the API types change:
>
> ```bash
> kubectl apply -f operator/documentdb-helm-chart/crds/
> ```

Verify rollout:

```bash
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
```

### Deploy a test DocumentDB instance

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: dev-test
  namespace: documentdb-test
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  documentDBImage: ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/documentdb:0.110.0
  gatewayImage:    ghcr.io/<your-gh-user>/documentdb-kubernetes-operator/gateway:0.110.0
  resource:
    storage:
      pvcSize: 5Gi
  exposeViaService:
    serviceType: ClusterIP
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

If the CRD changed between iterations, reapply: `kubectl apply -f operator/documentdb-helm-chart/crds/`.

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
