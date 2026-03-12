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

Upgrades keep your DocumentDB deployment current with the latest features, security patches, and bug fixes. The operator uses rolling updates to minimize downtime — replicas restart first, then the primary fails over, so writes are interrupted for only seconds.

There are three types of upgrades in a DocumentDB deployment:

| Upgrade Type | What Changes | How to Trigger |
|-------------|-------------|----------------|
| **DocumentDB operator upgrade** | The Kubernetes operator itself | Helm chart upgrade |
| **Extension upgrade** | The DocumentDB PostgreSQL extension | Update `spec.documentDBVersion` or `spec.documentDBImage` |
| **Gateway upgrade** | The DocumentDB gateway sidecar | Update `spec.gatewayImage` |

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

# Verify cluster health
kubectl get documentdb -n <namespace>
```

### DocumentDB Operator Upgrade Notes

- The DocumentDB operator upgrade does **not** restart your DocumentDB cluster pods.
- CRD changes are applied automatically by the Helm chart.
- If the new DocumentDB operator version includes updated default images, DocumentDB clusters will be reconciled with the new images on the next reconciliation cycle.

## Component Upgrades

=== "Extension Upgrade"

    The DocumentDB extension (the PostgreSQL extension that provides MongoDB compatibility) can be upgraded independently.

    **How it works:**

    1. You update the `spec.documentDBVersion` or `spec.documentDBImage` field.
    2. The operator detects the version change and updates the database image.
    3. The operator triggers a **rolling restart** of all pods in the DocumentDB cluster.
    4. After all pods are healthy, the operator runs `ALTER EXTENSION documentdb UPDATE` to update the database schema.
    5. The operator tracks the schema version in `status.schemaVersion`.

    **Trigger the upgrade:**

    Update the version:

    ```yaml
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-cluster
      namespace: default
    spec:
      documentDBVersion: "<new-version>"  # New version
    ```

    Or specify a custom image directly:

    ```yaml
    spec:
      documentDBImage: "ghcr.io/microsoft/documentdb/documentdb:<new-version>"
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    **Monitor the upgrade:**

    ```bash
    # Watch the rolling restart
    kubectl get pods -n default -w

    # Check DocumentDB cluster status
    kubectl get documentdb my-cluster -n default

    # Check the schema version after upgrade
    kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
    ```

    During the rolling restart:

    1. Replicas are upgraded first, one at a time.
    2. Once all replicas are healthy with the new version, the primary is upgraded.
    3. A brief failover occurs when the primary pod restarts.

    !!! danger
        **Downgrades are not supported.** If you attempt to set a `documentDBVersion` lower than the currently installed schema version, the operator will reject the change. This is because the extension may have applied schema migrations that have no corresponding downgrade path.

    If a version mismatch is detected where the binary version is lower than the schema version, the operator logs a warning and skips the upgrade.

=== "Gateway Upgrade"

    The DocumentDB gateway (the MongoDB-compatible proxy) runs as a sidecar container alongside each PostgreSQL instance.

    **Trigger the upgrade:**

    ```yaml
    spec:
      gatewayImage: "ghcr.io/microsoft/documentdb/gateway:<new-version>"
    ```

    ```bash
    kubectl apply -f documentdb.yaml
    ```

    **How it works:**

    1. The operator updates the plugin parameters with the new gateway image.
    2. The operator adds a restart annotation (`kubectl.kubernetes.io/restartedAt`) to trigger pod restarts.
    3. The operator performs a rolling restart of all pods.
    4. Each pod comes up with the new gateway sidecar image.

    If only the gateway image changes (no extension change), the operator forces a rolling restart via annotation. This ensures the new gateway image is picked up without requiring an extension upgrade.

## Rolling Update Behavior

All upgrades use a rolling update strategy:

1. **Replicas first** — replica instances are restarted one at a time.
2. **Primary last** — the primary is restarted after all replicas are healthy.
3. **Automatic failover** — when the primary restarts, a replica is promoted as the new primary. The old primary rejoins as a replica after restart.
4. **Zero data loss** — WAL continuity is maintained throughout the process.

### Expected Behavior During Upgrades

| Phase | Read Availability | Write Availability |
|-------|-------------------|-------------------|
| Replica restart | ✅ Full (via primary) | ✅ Full |
| Primary restart | ✅ Partial (via replicas) | ⚠️ Brief interruption (seconds) |
| Failover complete | ✅ Full | ✅ Full |

!!! note
    With a single-instance DocumentDB cluster (`instancesPerNode: 1`), upgrades cause a brief period of complete unavailability while the pod restarts. For zero-downtime upgrades, use `instancesPerNode: 3`.

## Pre-Upgrade Checklist

- [ ] **Back up the DocumentDB cluster** — create an on-demand [backup](backup-and-restore.md) before any upgrade.
- [ ] **Check the CHANGELOG** — review release notes for breaking changes.
- [ ] **Verify DocumentDB cluster health** — ensure all instances are running and the DocumentDB cluster is in a healthy state.
- [ ] **Plan for brief downtime** — even with rolling updates, the primary failover causes a few seconds of write unavailability.
- [ ] **Test in a non-production environment** — validate the upgrade process in a staging DocumentDB cluster first.

## Troubleshooting

### Pods Not Restarting After Version Change

**Possible causes**:

- The operator has not reconciled yet. Check operator logs:
  ```bash
  kubectl logs -n documentdb-operator deployment/documentdb-operator --tail=50
  ```
- The image pull is failing. Check pod events:
  ```bash
  kubectl describe pod <pod-name> -n <namespace>
  ```

### Extension Upgrade Fails

**Symptoms**: DocumentDB cluster health degrades after upgrade or `ALTER EXTENSION` fails.

**Actions**:

1. Check operator logs for schema migration errors.
2. Verify the new extension version is compatible with the current schema.
3. Check PostgreSQL logs inside the pod:
   ```bash
   kubectl exec -it <pod-name> -n <namespace> -c postgres -- cat /controller/log/postgresql.log
   ```

### Rollback Blocked

If a downgrade is blocked by rollback protection, the only path forward is:

1. Restore from a pre-upgrade [backup](backup-and-restore.md) to a new DocumentDB cluster.
2. Apply the desired (older) version to the new DocumentDB cluster.

## Next Steps

- [Backup and Restore](backup-and-restore.md) — protect your data before upgrades
- [Failover](failover.md) — understand failover during upgrades
- [Maintenance](maintenance.md) — routine operational tasks
