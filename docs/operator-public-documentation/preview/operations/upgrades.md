---
title: Upgrades
description: Upgrade the DocumentDB operator, extension, and gateway image with rolling updates, rollback protection, and zero-downtime strategies.
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
| **DocumentDB operator** | The Kubernetes operator itself | Helm chart upgrade |
| **DocumentDB components** | Extension + gateway (same version) | Update `spec.documentDBVersion` |

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

### Step 3: Upgrade the DocumentDB Operator

```bash
helm upgrade documentdb-operator documentdb/documentdb-operator \
  --namespace documentdb-operator \
  --wait
```

### Step 4: Verify the Upgrade

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

### DocumentDB Operator Upgrade Notes

- The DocumentDB operator upgrade does **not** restart your DocumentDB cluster pods.
- CRD changes are applied automatically by the Helm chart.

## Component Upgrades

Updating `spec.documentDBVersion` upgrades **both** the DocumentDB extension and the gateway together, since they share the same version.

### Pre-Upgrade Checklist

- [ ] **Check the CHANGELOG** — review release notes for breaking changes.
- [ ] **Verify DocumentDB cluster health** — ensure all instances are running and healthy.
- [ ] **Back up the DocumentDB cluster** — create an on-demand [backup](backup-and-restore.md) before upgrading.

### Step 1: Update the DocumentDB Version

!!! danger
    **Downgrades are not supported.** If you attempt to set a `documentDBVersion` lower than the currently installed schema version, the operator will reject the change. This is because the extension may have applied schema migrations that have no corresponding downgrade path.

```yaml title="documentdb.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  documentDBVersion: "<new-version>"
```

```bash
kubectl apply -f documentdb.yaml
```

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

=== "Schema not yet updated"

    If `status.schemaVersion` still shows the **previous** version, the extension schema migration has not run yet. You can roll back by reverting `spec.documentDBVersion` to the previous value:

    ```bash
    # Check the current schema version
    kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
    ```

    If the schema version is unchanged, revert the `spec.documentDBVersion` field in your manifest and reapply:

    ```bash
    kubectl apply -f documentdb.yaml
    ```

=== "Schema already updated"

    If `status.schemaVersion` shows the **new** version, the schema migration has already been applied and **cannot be reversed**. To recover:

    1. **Restore from backup.** Use the backup you created in the [Pre-Upgrade Checklist](#pre-upgrade-checklist) to restore the DocumentDB cluster to its pre-upgrade state. See [Backup and Restore](backup-and-restore.md) for instructions.
    2. **Contact support** if the cluster is unhealthy but still running — there may be a forward fix available in a newer version.

!!! tip
    This is why backing up before a component upgrade is critical. Once the schema is updated, there is no rollback — only restore.

### How It Works

1. You update the `spec.documentDBVersion` field.
2. The operator detects the version change and updates both the database image and the gateway sidecar image.
3. The operator performs a **rolling update**: replicas are upgraded first one at a time, then a **switchover** promotes the most up-to-date replica to primary, and the former primary is shut down and restarted with the new version.
4. After all pods are healthy, the operator runs `ALTER EXTENSION documentdb UPDATE` to update the database schema.
5. The operator tracks the schema version in `status.schemaVersion`.

### Advanced: Independent Image Overrides

In most cases, use `spec.documentDBVersion` to upgrade both components together. For advanced scenarios, you can override individual images:

=== "Extension Image Override"

    ```yaml
    spec:
      documentDBImage: "ghcr.io/microsoft/documentdb/documentdb:<version>"
    ```

    This overrides only the database extension image while keeping the gateway at the version set by `documentDBVersion`.

=== "Gateway Image Override"

    ```yaml
    spec:
      gatewayImage: "ghcr.io/microsoft/documentdb/gateway:<version>"
    ```

    This overrides only the gateway sidecar image while keeping the extension at the version set by `documentDBVersion`.
