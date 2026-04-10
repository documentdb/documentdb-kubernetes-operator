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

The operator is deployed via Helm. Upgrading it does **not** restart your DocumentDB cluster pods or change any cluster components.

!!! info
    The operator Helm chart bundles [CloudNative-PG](https://cloudnative-pg.io/) as a dependency. Upgrading the operator automatically upgrades the bundled CloudNative-PG version.

### Step 1: Update the Helm Repository

```bash
helm repo update documentdb
```

### Step 2: Review Available Versions

```bash
helm search repo documentdb/documentdb-operator --versions
```

!!! note
    Per the [release strategy](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/docs/designs/release-strategy.md), each minor version is supported for three months after the next minor release. Plan to upgrade within this window.

### Step 3: Apply Updated CRDs

Helm only installs CRDs on initial `helm install` — it does **not** update them on `helm upgrade`. If the new operator version introduces CRD schema changes, you must apply them manually first:

```bash
# Set this to the release tag you are upgrading to (e.g., 0.2.0)
TARGET_VERSION=0.2.0

kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/documentdb/documentdb-kubernetes-operator/${TARGET_VERSION}/operator/documentdb-helm-chart/crds/documentdb.io_dbs.yaml \
  -f https://raw.githubusercontent.com/documentdb/documentdb-kubernetes-operator/${TARGET_VERSION}/operator/documentdb-helm-chart/crds/documentdb.io_backups.yaml \
  -f https://raw.githubusercontent.com/documentdb/documentdb-kubernetes-operator/${TARGET_VERSION}/operator/documentdb-helm-chart/crds/documentdb.io_scheduledbackups.yaml
```

Server-side apply (`--server-side --force-conflicts`) is required because the DocumentDB CRD is too large for the `last-applied-configuration` annotation used by client-side `kubectl apply`.

!!! warning
    Always use CRDs from the **same version** as the Helm chart you are installing. Using CRDs from `main` or a different release may introduce schema mismatches.

### Step 4: Upgrade the Operator

```bash
helm upgrade documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator \
  --wait
```

!!! tip
    Add `--atomic` to automatically roll back the release if the upgrade fails:

    ```bash
    helm upgrade documentdb-operator documentdb/documentdb-operator \
      --namespace documentdb-operator \
      --atomic
    ```

### Step 5: Verify the Upgrade

```bash
# Check operator deployment
kubectl get deployment -n documentdb-operator

# Check operator logs for errors
kubectl logs -n documentdb-operator deployment/documentdb-operator --tail=50
```

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

---

## Upgrading DocumentDB Clusters

!!! warning "Migration note for existing clusters"
    If you are upgrading from operator version 0.2.0 or earlier, the new operator introduces a validating webhook and the `spec.schemaVersion` field. After upgrading the operator, your existing clusters will continue to work without changes — `schemaVersion` defaults to unset (binary-only upgrades). No action is needed unless you want to enable automatic schema upgrades via `schemaVersion: "auto"`.

Upgrading a DocumentDB cluster has two dimensions: the **binary** (container images) and the **schema** (database catalog). You control each independently:

| Field | What It Does | Reversible? |
|-------|-------------|-------------|
| `spec.documentDBVersion` | Updates the **binary** — the extension image and gateway sidecar are replaced via rolling restart. | ✅ Yes — revert the field to roll back. |
| `spec.schemaVersion` | Runs `ALTER EXTENSION UPDATE` to migrate the **database schema** to match the binary. | ❌ No — schema changes are permanent. |

Think of it as: **`documentDBVersion` installs the software, `schemaVersion` applies the database migration.**

!!! info "Why two fields?"
    The binary (container image) can be swapped freely — if something goes wrong, revert `documentDBVersion` and the pods roll back to the previous image. But `ALTER EXTENSION UPDATE` modifies database catalog tables and cannot be undone. Separating these two steps gives you a safe rollback window between deploying new code and committing the schema change.

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

Choose the approach that matches your use case:

=== "Production (two-phase upgrade)"

    **Step 1: Update the binary version.** The schema stays unchanged — this is safe to roll back.

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/preview
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
    apiVersion: documentdb.io/preview
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
        This pattern keeps a permanent rollback window. The schema is always one version behind the binary, so you never commit a schema change until the *next* binary has proven stable with it.

=== "Development (auto mode)"

    Update both the binary and schema in one step:

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/preview
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
      documentDBImage: "ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb:<version>"
    ```

    This overrides only the database extension image while keeping the gateway at the version set by `documentDBVersion`.

=== "Gateway Image Override"

    ```yaml
    spec:
      gatewayImage: "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway:<version>"
    ```

    This overrides only the gateway sidecar image while keeping the extension at the version set by `documentDBVersion`.
