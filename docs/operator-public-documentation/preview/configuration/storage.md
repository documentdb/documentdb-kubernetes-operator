---
title: Storage Configuration
description: Configure persistent storage for DocumentDB including storage classes, PVC sizing, volume expansion, reclaim policies, and disk encryption across AKS, EKS, and GKE.
tags:
  - configuration
  - storage
  - encryption
---

# Storage Configuration

## Overview

Storage controls how DocumentDB persists data — including disk size, storage type, retention behavior, and encryption.

Each DocumentDB instance stores its data on a Kubernetes [PersistentVolume (PV)](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) provisioned through a [PersistentVolumeClaim (PVC)](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims). You need to specify at least the disk size; optionally, you can choose a storage class for your cloud provider and control what happens to the data when the DocumentDB cluster is deleted. Configure storage through the `spec.resource.storage` field:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
spec:
  resource:
    storage:
      pvcSize: 100Gi                           # Required: storage size
      storageClass: managed-csi-premium         # Optional: defaults to Kubernetes default StorageClass
      persistentVolumeReclaimPolicy: Retain     # Optional: Retain (default) or Delete
```

For the full field reference, see [StorageConfiguration](../api-reference.md#storageconfiguration) in the API Reference.

## Disk Size (`pvcSize`)

The `pvcSize` field sets how much disk space each DocumentDB instance gets. This is set at DocumentDB cluster creation time. Online resizing is **coming soon** — see [#298](https://github.com/documentdb/documentdb-kubernetes-operator/issues/298) for tracking.

## Reclaim Policy (`persistentVolumeReclaimPolicy`)

The `persistentVolumeReclaimPolicy` field controls what happens to your data when a DocumentDB cluster is deleted:

| Policy | Behavior |
|--------|----------|
| `Retain` (default) | Data is preserved after DocumentDB deletion. **Recommended for production.** |
| `Delete` | Data is permanently deleted with the DocumentDB cluster. Suitable for development. |

With `Retain`, you can recover data even after the DocumentDB cluster is gone. See [PersistentVolume Retention and Recovery](../backup-and-restore.md#persistentvolume-retention-and-recovery) for restore steps.

## Storage Classes (`storageClass`)

The `storageClass` field selects which type of underlying disk (e.g., SSD vs HDD) to provision. See [Kubernetes StorageClass](https://kubernetes.io/docs/concepts/storage/storage-classes/) for details. If you don't specify one, Kubernetes uses the default StorageClass in your Kubernetes cluster.

To see available StorageClasses and which one is the default:

```bash
kubectl get storageclass
```

The default is marked with `(default)` in the output.

## Disk Encryption

Disk encryption protects your data at rest — if someone gains physical access to the underlying storage, the data is unreadable without the encryption key. Most cloud providers enable this by default, but EKS requires explicit configuration.

| Provider | Default Encryption | Customer-Managed Keys |
|----------|-------------------|----------------------|
| **AKS** | ✅ Enabled (platform-managed keys) | [Azure Disk Encryption with CMK](https://learn.microsoft.com/azure/aks/azure-disk-customer-managed-keys) |
| **GKE** | ✅ Enabled (Google-managed keys) | [CMEK for GKE persistent disks](https://cloud.google.com/kubernetes-engine/docs/how-to/using-cmek) |
| **EKS** | ❌ **Not enabled by default** | [EBS CSI driver encryption](https://docs.aws.amazon.com/eks/latest/userguide/ebs-csi.html) — set `encrypted: "true"` in StorageClass |

!!! warning
    For production on EKS, always create a StorageClass with `encrypted: "true"` to ensure data at rest is protected.

    ```yaml
    apiVersion: storage.k8s.io/v1
    kind: StorageClass
    metadata:
      name: ebs-sc-encrypted
    provisioner: ebs.csi.aws.com
    parameters:
      type: gp3
      encrypted: "true"
      # kmsKeyId: arn:aws:kms:<region>:<account-id>:key/<key-id>  # Optional: customer-managed key
    reclaimPolicy: Delete
    volumeBindingMode: WaitForFirstConsumer
    allowVolumeExpansion: true
    ```

## PersistentVolume Security

As a defense-in-depth measure, the operator automatically applies security-hardening mount options to all DocumentDB volumes. These prevent common attack vectors even if a container is compromised:

| Mount Option | What it prevents |
|--------------|------------------|
| `nodev` | Blocks creation of device files that could access host hardware |
| `nosuid` | Blocks privilege escalation via setuid/setgid binaries |
| `noexec` | Blocks execution of malicious binaries written to the data volume |
