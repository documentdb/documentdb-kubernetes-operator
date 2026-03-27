---
title: Upgrades
description: Upgrade the DocumentDB operator Helm chart and CRDs, update DocumentDB extension and gateway images per cluster, and roll back to a previous version.
tags:
  - operations
  - upgrades
  - rolling-update
---

# Upgrades

## Overview

Upgrades keep your DocumentDB deployment current with the latest features, security patches, and bug fixes.

There are two types of upgrades in a DocumentDB deployment:

| Upgrade Type | What Changes | How to Trigger |
|-------------|-------------|----------------|
| **DocumentDB operator** | The Kubernetes operator + bundled CloudNative-PG | Helm chart upgrade |
| **DocumentDB components** | Extension + gateway (same version) | Update `spec.documentDBVersion` |

!!! info
    The operator Helm chart bundles [CloudNative-PG](https://cloudnative-pg.io/) as a dependency. Upgrading the operator automatically upgrades the bundled CloudNative-PG version — this cannot be skipped separately.

## DocumentDB Operator Upgrade

The DocumentDB operator is deployed via Helm. Upgrade it by updating the Helm release.

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

### Step 4: Upgrade the DocumentDB Operator

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

### Rollback

If the new operator version causes issues, roll back to the previous Helm release:

```bash
# List release history
helm history documentdb-operator -n documentdb-operator

# Rollback to the previous revision
helm rollback documentdb-operator -n documentdb-operator
```

!!! note
    `helm rollback` reverts the operator deployment but does **not** revert CRDs. This is usually safe — CRD changes are additive, and the older operator ignores fields it does not recognize. Do **not** revert CRDs unless the release notes explicitly instruct you to, as removing fields from a CRD can invalidate existing resources.

### DocumentDB Operator Upgrade Notes

- The DocumentDB operator upgrade does **not** restart your DocumentDB cluster pods.
- After upgrading the operator, update individual DocumentDB clusters to the latest component version. See [Component Upgrades](#component-upgrades) below.

## Component Upgrades

Updating `spec.documentDBVersion` upgrades **both** the DocumentDB extension and the gateway together, since they share the same version.

### Schema Version Control

**The operator never changes your database schema unless you ask.**

- Set `documentDBVersion` → updates the binary (safe to roll back)
- Set `schemaVersion` → updates the database schema (irreversible)
- Set `schemaVersion: "auto"` → schema auto-updates with binary

Think of it as: **`documentDBVersion` installs the software, `schemaVersion` applies the database migration.**

| `spec.schemaVersion` | Behavior | Use case |
|----------------------|----------|----------|
| Not set (default) | Binary upgrades. Schema stays at current version until you set `schemaVersion`. | Production — gives you a rollback-safe window before committing the schema change. |
| `"auto"` | Schema updates automatically with binary. No rollback window. | Development and testing — simple, one-step upgrades. |
| Explicit version (e.g. `"0.112.0"`) | Schema updates to exactly that version. | Controlled rollouts — you choose when and what version to finalize. |

### Pre-Upgrade Checklist

1. **Check the CHANGELOG** — review release notes for breaking changes.
2. **Verify DocumentDB cluster health** — ensure all instances are running and healthy.
3. **Back up the DocumentDB cluster** — create an on-demand [backup](backup-and-restore.md) before upgrading.

### Step 1: Update the DocumentDB Version

!!! danger
    **Downgrades are not supported.** Once the schema has been updated (`status.schemaVersion` shows the new version), the operator **blocks** any attempt to set `documentDBVersion` to a version lower than the installed schema version. If you need to recover after a schema update, restore from backup. See [Rollback and Recovery](#rollback-and-recovery).

=== "Default (controlled upgrade)"

    Update only the binary version. The schema stays at the current version until you explicitly finalize:

    ```yaml title="documentdb.yaml"
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-cluster
      namespace: default
    spec:
      documentDBVersion: "<new-version>"
      # schemaVersion is not set — schema stays, safe to roll back
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    After the binary upgrade completes and you've validated the cluster, finalize the schema:

    ```bash
    kubectl patch documentdb my-cluster -n default \
      --type merge -p '{"spec":{"schemaVersion":"<new-version>"}}'
    ```

    !!! tip
        On subsequent upgrades, just update `documentDBVersion` again. The schema stays pinned at the previous `schemaVersion` value until you update it.

=== "Auto mode"

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

### Step 2: Monitor the Upgrade

```bash
# Watch the rolling restart
kubectl get pods -n default -w

# Check DocumentDB cluster status
kubectl get documentdb my-cluster -n default

# Check the schema version after upgrade
kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
```

### Rollback and Recovery

Whether you can roll back depends on whether the schema has been updated:

=== "Schema not yet updated (rollback safe)"

    If `status.schemaVersion` still shows the **previous** version, the extension schema migration has not run yet. You can safely roll back by reverting `spec.documentDBVersion` to the previous value:

    ```bash
    # Check the current schema version
    kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
    ```

    If the schema version is unchanged, revert the `spec.documentDBVersion` field in your manifest and reapply:

    ```bash
    kubectl apply -f documentdb.yaml
    ```

=== "Schema already updated (rollback blocked)"

    If `status.schemaVersion` shows the **new** version, the schema migration has already been applied and **cannot be reversed**. The operator will **block** any attempt to roll back the binary image below the installed schema version, because running an older binary against a newer schema is untested and may cause data corruption.

    To recover: Use the backup you created in the [Pre-Upgrade Checklist](#pre-upgrade-checklist) to restore the DocumentDB cluster to its pre-upgrade state. See [Backup and Restore](backup-and-restore.md) for instructions.

!!! tip
    This is why the default two-phase mode exists — it gives you a rollback-safe window before committing the schema change. Always back up before upgrading, and validate the new binary before setting `schemaVersion`.

### How It Works

1. You update the `spec.documentDBVersion` field.
2. The operator detects the version change and updates both the database image and the gateway sidecar image.
3. The underlying cluster manager performs a **rolling restart**: replicas are restarted first one at a time, then the **primary is restarted in place**. Expect a brief period of downtime while the primary pod restarts.
4. After the primary pod is healthy, the operator checks `spec.schemaVersion`:
    - **Not set (default)**: The operator **skips** the schema migration and emits a `SchemaUpdateAvailable` event. The binary is updated but the database schema is unchanged — you can safely roll back by reverting `documentDBVersion`.
    - **`"auto"`**: The operator runs `ALTER EXTENSION documentdb UPDATE` to update the database schema to match the binary. This is irreversible.
    - **Explicit version**: The operator runs `ALTER EXTENSION documentdb UPDATE TO '<version>'` to update to that specific version. This is irreversible.
5. The operator tracks the current schema version in `status.schemaVersion`.

### Advanced: Independent Image Overrides

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
