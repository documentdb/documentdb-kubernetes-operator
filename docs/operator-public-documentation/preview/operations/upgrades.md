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

### DocumentDB Operator Upgrade Notes

- The DocumentDB operator upgrade does **not** restart your DocumentDB cluster pods.
- CRD changes are applied automatically by the Helm chart.

## Component Upgrades

Updating `spec.documentDBVersion` upgrades **both** the DocumentDB extension and the gateway together, since they share the same version.

!!! danger
    **Downgrades are not supported.** If you attempt to set a `documentDBVersion` lower than the currently installed schema version, the operator will reject the change. This is because the extension may have applied schema migrations that have no corresponding downgrade path.

**Trigger the upgrade:**

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

**Monitor the upgrade:**

```bash
# Watch the rolling restart
kubectl get pods -n default -w

# Check DocumentDB cluster status
kubectl get documentdb my-cluster -n default

# Check the schema version after upgrade
kubectl get documentdb my-cluster -n default -o jsonpath='{.status.schemaVersion}'
```

**How it works:**

1. You update the `spec.documentDBVersion` field.
2. The operator detects the version change and updates both the database image and the gateway sidecar image.
3. The operator triggers a **rolling restart** of all pods in the DocumentDB cluster.
4. After all pods are healthy, the operator runs `ALTER EXTENSION documentdb UPDATE` to update the database schema.
5. The operator tracks the schema version in `status.schemaVersion`.

During the rolling restart:

1. Replicas are upgraded first, one at a time.
2. Once all replicas are healthy with the new version, the primary is upgraded.
3. A brief failover occurs when the primary pod restarts.

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

### Pre-Upgrade Checklist
- [ ] **Back up the DocumentDB cluster** — create an on-demand [backup](backup-and-restore.md) before upgrading.
- [ ] **Check the CHANGELOG** — review release notes for breaking changes.
- [ ] **Verify DocumentDB cluster health** — ensure all instances are running and healthy.
- [ ] **Plan for brief downtime** — the primary failover causes a few seconds of write unavailability.
