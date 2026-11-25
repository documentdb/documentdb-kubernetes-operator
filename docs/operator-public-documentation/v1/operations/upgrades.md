---
title: Upgrades
description: Upgrade the DocumentDB operator and DocumentDB clusters, including version and schema management.
tags:
  - operations
  - upgrades
  - rolling-update
---

# Upgrades

## Overview

Upgrades keep your DocumentDB deployment current with the latest features, security patches, and bug fixes.

A DocumentDB deployment has two independently upgradable components:

| Component | What Changes | How to Upgrade |
|-----------|-------------|----------------|
| **DocumentDB Operator** | Operator binary + bundled CloudNative-PG | `helm upgrade` |
| **DocumentDB Clusters** | Extension binary + gateway sidecar + database schema | Update `spec.documentDBVersion` and optionally `spec.schemaVersion` |

---

## Upgrading the Operator

The operator is deployed via Helm. Clusters that pin `spec.documentDBVersion`, or both `spec.image.documentDB` and `spec.image.gateway`, keep their DocumentDB engine images across an operator upgrade.

!!! warning "Unpinned clusters follow the chart's engine version"
    A cluster without `spec.documentDBVersion` runs the engine version bundled with the chart for every image it does not set explicitly in `spec.image`. Upgrading the chart rolls those images to the new chart's engine version (a rolling restart), and a cluster with `spec.schemaVersion: "auto"` then migrates its schema immediately. To control when the engine changes, pin `spec.documentDBVersion` on each cluster before upgrading the operator.

!!! info
    The operator Helm chart bundles [CloudNative-PG](https://cloudnative-pg.io/) as a dependency. Upgrading the operator automatically upgrades the bundled CloudNative-PG version.

### Step 1: Choose a Target Version

The DocumentDB operator Helm chart is published as an OCI artifact at `oci://ghcr.io/documentdb/documentdb-operator`. OCI registries do not support `helm repo update` or `helm search repo`, so available chart versions are listed on the GitHub Releases page:

<https://github.com/documentdb/documentdb-kubernetes-operator/releases>

Pin the version you intend to upgrade to in a shell variable used by the rest of this guide:

```bash
# Chart version to upgrade to, for example 1.2.0. Release tags use the same
# value (no "v" prefix), so it also selects the CRDs in Step 3.
TARGET_VERSION="<release-version>"
```

### Step 2: Review the Release Notes

Open the GitHub release page for `${TARGET_VERSION}` to review breaking changes, new features, and any required migration steps before proceeding.

!!! note
    Per the [release strategy](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/docs/designs/release-strategy.md), each minor version is supported for three months after the next minor release. Plan to upgrade within this window.

### Step 3: Apply Updated CRDs

Helm only installs CRDs on initial `helm install` — it does **not** update them on `helm upgrade`. If the new operator version introduces CRD schema changes, you must apply them manually first:

```bash
kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/documentdb/documentdb-kubernetes-operator/${TARGET_VERSION}/operator/documentdb-helm-chart/crds/documentdb.io_dbs.yaml \
  -f https://raw.githubusercontent.com/documentdb/documentdb-kubernetes-operator/${TARGET_VERSION}/operator/documentdb-helm-chart/crds/documentdb.io_backups.yaml \
  -f https://raw.githubusercontent.com/documentdb/documentdb-kubernetes-operator/${TARGET_VERSION}/operator/documentdb-helm-chart/crds/documentdb.io_scheduledbackups.yaml
```

Server-side apply (`--server-side --force-conflicts`) is required because the DocumentDB CRD is too large for the `last-applied-configuration` annotation used by client-side `kubectl apply`.

!!! warning
    Always use CRDs from the **same version** as the Helm chart you are installing. Using CRDs from `main` or a different release may introduce schema mismatches.

!!! important "Upgrading from 1.1.x or earlier"
    This step is **required** when upgrading to operator `1.2.0` or later from `1.1.x`, `1.0.x` or earlier. Starting with `1.2.0`, the operator uses the `documentdb.io/v1` API, which older CRDs do not serve; the new operator cannot start until the updated CRDs are applied. See [Migrating to the `documentdb.io/v1` API](#migrating-to-the-documentdbiov1-api).

### Step 4: Upgrade the Operator

```bash
helm upgrade documentdb-operator oci://ghcr.io/documentdb/documentdb-operator \
  --version ${TARGET_VERSION} \
  --namespace documentdb-operator \
  --wait
```

!!! warning "Pinned image tags are kept"
    When you pass no new values, `helm upgrade` reuses the values from your previous install. If you set `image.documentdbk8soperator.tag` or `image.sidecarinjector.tag` (or other image overrides) at install time, the new chart deploys but the operator keeps running the old images. Add `--reset-values` to use the new chart's defaults, and pass again any settings you still need (for example `--set image.registry=...`):

    ```bash
    helm upgrade documentdb-operator oci://ghcr.io/documentdb/documentdb-operator \
      --version ${TARGET_VERSION} \
      --namespace documentdb-operator \
      --reset-values \
      --wait
    ```

    Run `helm get values documentdb-operator -n documentdb-operator` to see which values your release currently overrides.

!!! tip
    Add `--atomic` to automatically roll back the release if the upgrade fails:

    ```bash
    helm upgrade documentdb-operator oci://ghcr.io/documentdb/documentdb-operator \
      --version ${TARGET_VERSION} \
      --namespace documentdb-operator \
      --atomic
    ```

### Step 5: Verify the Upgrade

```bash
# Wait for the operator and sidecar injector rollouts to finish
kubectl rollout status deployment/documentdb-operator -n documentdb-operator --timeout=5m
kubectl rollout status deployment/sidecar-injector -n cnpg-system --timeout=5m

# Check the operator and sidecar run the new version's images
kubectl get deployment documentdb-operator -n documentdb-operator \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl get deployment sidecar-injector -n cnpg-system \
  -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'

# Check your DocumentDB clusters
kubectl get documentdb -A

# Check operator logs for errors
kubectl logs -n documentdb-operator deployment/documentdb-operator --tail=50
```

The upgrade succeeded when:

- Both `rollout status` commands report `successfully rolled out`.
- Both images end in `:${TARGET_VERSION}`. If they still show the old tag, your release pins image tags; see [Pinned image tags are kept](#step-4-upgrade-the-operator).
- Every DocumentDB cluster reports `Cluster in healthy state`.
- The operator logs show no repeated errors. A few `the object has been modified` messages right after the restart are retries and can be ignored.
- Your application can read and write through the gateway, for example a `mongosh` insert and find.

### Operator Rollback

If the new operator version causes issues, roll back to the previous Helm release:

```bash
# List release history
helm history documentdb-operator -n documentdb-operator

# Rollback to the previous revision
helm rollback documentdb-operator -n documentdb-operator
```

!!! note
    `helm rollback` reverts the operator deployment but does **not** revert CRDs. This is usually safe — CRD changes are additive, and the older operator ignores fields it does not recognize. Do **not** revert CRDs unless the [release notes](https://github.com/documentdb/documentdb-kubernetes-operator/releases) explicitly instruct you to, as removing fields from a CRD can invalidate existing resources.

!!! note "Rolling back from 1.2.0 to 1.1.x"
    Keep the `1.2.0` CRDs when rolling back. They still serve the deprecated `documentdb.io/preview` version that `1.1.x` operators use, so the older operator keeps working. Do not re-apply the `1.1.x` CRDs: they do not serve `documentdb.io/v1`, and the API server rejects them once objects are stored as `v1`.

---

### Migrating to the `documentdb.io/v1` API

Operators before `1.2.0` (`1.0.x` and `1.1.x`) served the `DocumentDB`, `Backup` and `ScheduledBackup` resources only as `documentdb.io/preview`. Operator `1.2.0` graduates them to the stable `documentdb.io/v1` API:

- `v1` is the storage version and the version the operator uses.
- `preview` is **deprecated** but still served with an identical schema, so existing resources, manifests and GitOps pipelines keep working. Requests that use it return a deprecation warning.
- No conversion webhook is involved: the API server converts between the two versions by changing only `apiVersion`.

After [applying the updated CRDs](#step-3-apply-updated-crds) and upgrading the operator:

1. Change `apiVersion: documentdb.io/preview` to `apiVersion: documentdb.io/v1` in your manifests, Helm values and GitOps sources. No other fields change.
2. Optionally, rewrite existing resources so they are stored as `v1`. This is needed before a future release removes `preview`:

    ```bash
    for kind in dbs.documentdb.io backups.documentdb.io scheduledbackups.documentdb.io; do
      kubectl get "$kind" -A -o json | kubectl replace -f -
    done
    kubectl get crd dbs.documentdb.io -o jsonpath='{.status.storedVersions}'
    ```

    Once every resource has been rewritten, remove `preview` from each CRD's `status.storedVersions` (see [Upgrade existing objects to a new stored version](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/#upgrade-existing-objects-to-a-new-stored-version)):

    ```bash
    for crd in dbs backups scheduledbackups; do
      kubectl patch crd "$crd.documentdb.io" --subresource=status --type=merge \
        -p '{"status":{"storedVersions":["v1"]}}'
    done
    ```

## Upgrading DocumentDB Clusters

!!! warning "Migration note for existing clusters"
    If you are upgrading from operator version 0.2.0 or earlier, the new operator introduces a validating webhook and the `spec.schemaVersion` field. After upgrading the operator, your existing clusters will continue to work without changes — `schemaVersion` defaults to unset (binary-only upgrades). No action is needed unless you want to enable automatic schema upgrades via `schemaVersion: "auto"`.

Upgrading a DocumentDB cluster has two dimensions: the **binary** (container images) and the **schema** (database catalog). You control each independently:

| Field | What It Does | Reversible? |
|-------|-------------|-------------|
| `spec.documentDBVersion` | Updates the **binary** — the extension image and gateway sidecar are replaced via rolling restart. | ✅ Yes, provided the target binary supports the current schema (it must be ≥ `status.schemaVersion`). See [Rollback and Recovery](#rollback-and-recovery). |
| `spec.schemaVersion` | Runs `ALTER EXTENSION UPDATE` to migrate the **database schema** to match the binary. | ❌ No — schema changes are permanent. |

Think of it as: **`documentDBVersion` installs the software, `schemaVersion` applies the database migration.**

!!! info "Why two fields?"
    The binary (container image) can be rolled back while the schema hasn't changed — revert `documentDBVersion` to a version that is ≥ `status.schemaVersion` and the pods roll back to that image. But `ALTER EXTENSION UPDATE` modifies database catalog tables and cannot be undone. Separating these two steps gives you a safe rollback window between deploying new code and committing the schema change.

### Schema Version Modes

| `spec.schemaVersion` | Behavior | Recommended For |
|----------------------|----------|-----------------|
| *(not set)* — default | Only the binary upgrades. The schema stays at its current version until you explicitly set `schemaVersion`. | **Production** — gives you a rollback-safe window before committing the schema change. |
| `"auto"` | Schema updates automatically whenever the binary version changes. | **Development and testing** — simple, one-step upgrades. |
| Explicit version (e.g., `"0.112.0"`) | Schema updates to exactly that version. | **Controlled rollouts** — you choose when and what version to finalize. |

### Pre-Upgrade Checklist

1. **Check the [DocumentDB release notes](https://github.com/documentdb/documentdb/releases)** — review for breaking changes or new features.
2. **Verify DocumentDB cluster health** — ensure all instances are running and healthy:

    ```bash
    kubectl get documentdb my-cluster -n default
    kubectl get pods -n default -l documentdb.io/cluster=my-cluster
    ```
3. **Back up the DocumentDB cluster** — create an on-demand [backup](backup-and-restore.md) before upgrading.

### Upgrade Walkthrough

Choose the approach that matches your use case. The YAML examples show only the fields to change: update them in your existing manifest and keep all other required configuration unchanged.

=== "Production (two-phase upgrade)"

    **Step 1: Update the binary version.** The schema stays unchanged — this is safe to roll back.

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/v1
    kind: DocumentDB
    metadata:
      name: my-cluster
      namespace: default
    spec:
      documentDBVersion: "<new-version>"
      # schemaVersion is not set — schema stays at current version
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    **Step 2: Validate.** Confirm the cluster is healthy and the new binary works as expected.

    ```bash
    # Watch the rolling restart
    kubectl get pods -n default -w

    # Check cluster status
    kubectl get documentdb my-cluster -n default

    # Verify the schema version has NOT changed
    kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
    ```

    **Step 3: Finalize the schema.** Once you're confident the new binary is stable, commit the schema migration:

    ```bash
    kubectl patch documentdb my-cluster -n default \
      --type merge -p '{"spec":{"schemaVersion":"<new-version>"}}'
    ```

    !!! tip
        On subsequent upgrades, just update `documentDBVersion` again. The schema stays pinned at the previous `schemaVersion` value until you update it.

=== "Production (rolling safety gap)"

    Keep the binary always one version ahead of the schema. This ensures you can roll back at any time because the running binary has already been validated with the current schema.

    **Example:** Your cluster is at binary `0.110.0` with schema `0.110.0`. A new version `0.111.0` is available.

    **Step 1: Upgrade the binary and finalize the *previous* schema together.**

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/v1
    kind: DocumentDB
    metadata:
      name: my-cluster
      namespace: default
    spec:
      documentDBVersion: "0.111.0"     # upgrade binary to new version
      schemaVersion: "0.110.0"          # finalize schema to current (previous) version
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    Now the binary is `0.111.0` and the schema is `0.110.0`. Each new binary version is backward-compatible with the previous schema version, so this is safe.

    **Step 2: Validate.** Run your tests. If something goes wrong, revert `documentDBVersion` to `0.110.0` — the schema is still at `0.110.0`, so rollback is safe.

    **On the next upgrade** (e.g., `0.112.0`), repeat the pattern:

    ```yaml
    spec:
      documentDBVersion: "0.112.0"     # upgrade binary to next version
      schemaVersion: "0.111.0"          # finalize schema to previous binary version
    ```

    !!! info
        This pattern preserves a rollback option to the previous binary only when that binary supports the resulting schema. The schema stays one version behind the binary, so you don't commit a schema change until the *next* binary has proven stable with it. Confirm compatibility in the release notes before each upgrade.

=== "Development (auto mode)"

    Update both the binary and schema in one step:

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/v1
    kind: DocumentDB
    metadata:
      name: my-cluster
      namespace: default
    spec:
      documentDBVersion: "<new-version>"
      schemaVersion: "auto"
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    !!! warning
        With `schemaVersion: "auto"`, the schema migration is irreversible once applied. You cannot roll back to the previous version — only restore from backup.

### Monitoring the Upgrade

```bash
# Watch the rolling restart
kubectl get pods -n default -w

# Check DocumentDB cluster status
kubectl get documentdb my-cluster -n default

# Check the current schema version
kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
```

### Rollback and Recovery

Two rules govern rollback:

1. **Schema cannot be rolled back.** `ALTER EXTENSION UPDATE` modifies database catalog tables permanently. There is no `ALTER EXTENSION DOWNGRADE`.
2. **`documentDBVersion` cannot be set below `status.schemaVersion`.** The operator blocks this because running an older binary against a newer schema is untested and may cause data corruption.

Whether you can roll back depends on whether the schema has been updated:

=== "Roll back binary only (documentDBVersion ≥ schemaVersion)"

    If `status.schemaVersion` still shows the **previous** version, the schema migration has not run yet. You can safely roll back by reverting `spec.documentDBVersion` to any version that is ≥ `status.schemaVersion`:

    ```bash
    # Verify the schema version is unchanged
    kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
    ```

    If the schema version is unchanged, revert `spec.documentDBVersion` in your manifest and reapply:

    ```bash
    kubectl apply -f documentdb.yaml
    ```

=== "Roll back both binary and schema (restore from backup)"

    If `status.schemaVersion` shows the **new** version, the schema migration has already been applied. At this point:

    - You **cannot** revert `schemaVersion` — the database schema change is permanent.
    - You **cannot** set `documentDBVersion` below `status.schemaVersion` — the operator rejects it.

    To recover: restore from the backup you created in the [Pre-Upgrade Checklist](#pre-upgrade-checklist). See [Backup and Restore](backup-and-restore.md) for instructions.

!!! tip
    This is why the default two-phase mode exists — it gives you a rollback-safe window before committing the schema change. Always back up before upgrading, and validate the new binary before setting `schemaVersion`.

### Engine 1.0.1 security update

DocumentDB engine `1.0.1` is the `1.0.0` engine rebuilt on patched Debian packages. It fixes the zlib vulnerabilities [CVE-2026-85091](https://nvd.nist.gov/vuln/detail/CVE-2026-85091) and [CVE-2026-27171](https://nvd.nist.gov/vuln/detail/CVE-2026-27171) in the `documentdb`, `gateway` and `documentdb-combined` `1.0.0` images. The extension schema is unchanged (`1.0-0`), so moving to `1.0.1` is a rolling restart with no schema migration.

Operator `1.2.1` and later deploy `1.0.1` by default. Earlier operators (`1.0.x`, `1.1.x`, `1.2.0`) default to `1.0.0`; you can move to `1.0.1` without upgrading the operator:

- **One cluster:** set `spec.documentDBVersion` to `1.0.1`:

    ```bash
    kubectl patch documentdb my-cluster -n default \
      --type merge -p '{"spec":{"documentDBVersion":"1.0.1"}}'
    ```

- **Every cluster that doesn't pin `spec.documentDBVersion`:** change the chart's default engine and keep your current chart version:

    ```bash
    helm upgrade documentdb-operator oci://ghcr.io/documentdb/documentdb-operator \
      --version <your-current-chart-version> \
      --namespace documentdb-operator \
      --reuse-values --set documentDbVersion=1.0.1 --wait
    ```

If a cluster sets `spec.image.documentDB` or `spec.image.gateway`, change those tags to `1.0.1` as well; explicit images take precedence. Don't set `spec.schemaVersion: "1.0.1"`: engine `1.0.1` has no schema change.

### Upgrading to DocumentDB 1.0

Run DocumentDB 1.0 on operator `1.0.0`, the release tested with it. Before upgrading the operator, pin `spec.documentDBVersion` on every cluster to the version it runs now (see the warning in [Upgrading the Operator](#upgrading-the-operator)), and remove `schemaVersion: "auto"` so nothing migrates before you choose to. Then upgrade the operator and move each cluster:

| Current engine | Steps |
|----------------|-------|
| `0.117.0` or earlier `0.x` | Two-phase upgrade: set `documentDBVersion: "1.0.0"`, validate, then set `schemaVersion: "1.0.0"`. This runs `ALTER EXTENSION documentdb UPDATE TO '1.0-0'`, which cannot be undone. |
| `1.0-RC2` (from chart `1.0.0-rc1`) | In-place upgrade is not supported: release-candidate clusters are for fresh instances only. Keep the cluster pinned to `1.0-RC2`, create a new `1.0.0` cluster, and move your data with `mongodump`/`mongorestore`. |

For a `0.x` cluster:

```bash
kubectl patch documentdb my-cluster -n default \
  --type merge -p '{"spec":{"documentDBVersion":"1.0.0"}}'
# After validating the new binary:
kubectl patch documentdb my-cluster -n default \
  --type merge -p '{"spec":{"schemaVersion":"1.0.0"}}'
```

If the cluster sets `spec.image.documentDB` or `spec.image.gateway`, explicit images take precedence over `documentDBVersion`. Remove them in the same first patch:

```bash
kubectl patch documentdb my-cluster -n default --type merge \
  -p '{"spec":{"documentDBVersion":"1.0.0","image":{"documentDB":null,"gateway":null}}}'
```

---

## Multi-Region Upgrades

When running DocumentDB across multiple regions or clusters, use the two-phase upgrade pattern across all regions:

1. **Back up every region.** Create a backup in each region before starting.
2. **Upgrade the binary in all regions.** Update `spec.documentDBVersion` in every cluster. Validate that all regions are healthy and replication is working correctly with the new binary.
3. **Finalize the schema in all regions.** Once every region is running the new binary successfully, set `spec.schemaVersion` across all clusters.

This keeps a rollback-safe window — if any region fails the binary upgrade, you can revert `documentDBVersion` everywhere before any schema change is committed.

!!! note
    Multi-region upgrade orchestration is performed manually — the operator manages individual clusters and does not coordinate across regions automatically.

---

## Advanced: Independent Image Overrides

In most cases, use `spec.documentDBVersion` to upgrade both components together. For advanced scenarios, you can override individual images:

=== "Extension Image Override"

    ```yaml
    spec:
      image:
        documentDB: "ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb:<version>"
    ```

    This overrides only the database extension image while keeping the gateway at the version set by `documentDBVersion`.

=== "Gateway Image Override"

    ```yaml
    spec:
      image:
        gateway: "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway:<version>"
    ```

    This overrides only the gateway sidecar image while keeping the extension at the version set by `documentDBVersion`.
