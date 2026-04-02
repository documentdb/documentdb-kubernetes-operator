# Telemetry GUID Strategy: UUID vs Deterministic Hash

## Overview

This document analyzes the trade-offs between using randomly generated UUIDs versus deterministic hashes (SHA256) for telemetry correlation IDs in the DocumentDB Kubernetes Operator.

## Option 1: UUID with Annotation Persistence

This approach generates a random UUID and persists it as a Kubernetes annotation on the resource:

```go
func (m *GUIDManager) getOrCreateID(ctx context.Context, obj client.Object, annotationKey string) (string, error) {
    // Check if ID already exists in annotation
    existingID := getAnnotation(obj, annotationKey)
    if existingID != "" {
        return existingID, nil
    }
    
    // Generate new UUID and persist it
    newID := uuid.New().String()
    annotations[annotationKey] = newID
    m.client.Update(ctx, obj)
    return newID, nil
}
```

### Failure Scenarios

If the operator restarts **before** the UUID annotation is successfully persisted:
1. A new UUID is generated on the next reconciliation
2. Telemetry events before and after the restart cannot be correlated
3. The same cluster appears as two different entities in Application Insights

| Scenario | UUID Behavior | Impact |
|----------|---------------|--------|
| Operator restart after annotation persisted | Same UUID retrieved | No impact |
| Operator restart before annotation persisted | New UUID generated | Correlation broken |
| Annotation accidentally deleted | New UUID generated | Correlation broken |
| Resource recreated with same name | New UUID generated | Correct (different resource) |

## Option 2: Deterministic Hash (SHA256)

Generate a hash from resource properties. There are two options:

## Option 2A: User-Determined ID (Without UID)

```go
func GenerateClusterID(namespace, name string) string {
    data := fmt.Sprintf("%s/%s", namespace, name)
    hash := sha256.Sum256([]byte(data))
    return hex.EncodeToString(hash[:16]) // 32-char hex string
}
```

**Behavior:**
- ID is determined by what the **user chose** for namespace and cluster name
- If user deletes and recreates cluster with same name → **same ID**
- Tracks "this cluster identity" as the user conceptualizes it

**Advantages:**
- Matches user mental model ("my-production-cluster" stays the same)
- Telemetry continuity when recreating clusters to fix issues
- Predictable from user-visible properties
- Simpler - fewer inputs to hash

**Disadvantages:**
- Cannot distinguish between original cluster and replacement cluster
- If user reuses names for unrelated clusters, telemetry gets mixed

## Option 2B: Resource-Determined ID (With UID)

```go
func GenerateClusterID(namespace, name string, uid types.UID) string {
    data := fmt.Sprintf("%s/%s/%s", namespace, name, uid)
    hash := sha256.Sum256([]byte(data))
    return hex.EncodeToString(hash[:16]) // 32-char hex string
}
```

**Behavior:**
- ID includes Kubernetes UID (auto-generated per resource instance)
- If user deletes and recreates cluster with same name → **different ID** (new UID)
- Tracks "this specific Kubernetes resource instance"

**Advantages:**
- Can distinguish "cluster-v1 that was deleted" from "cluster-v2 that replaced it"
- More precise tracking of resource lifecycles
- No telemetry mixing if names are reused

**Disadvantages:**
- Breaks user mental model - recreated cluster appears as new entity
- Telemetry history fragmented across cluster recreations
- UID is not user-visible, making correlation harder to reason about

### Option 2A vs 2B Comparison

| Scenario | Option 2A (No UID) | Option 2B (With UID) |
|----------|-------------------|---------------------|
| Operator restart | Same ID  | Same ID  |
| Cluster recreated with same name | Same ID | Different ID |
| User renames cluster | Different ID | Different ID |
| Distinguishes cluster versions |  No |  Yes |
| Matches user mental model |  Yes |  No |
| Telemetry continuity on recreate |  Yes |  No |

## Comparison

### Option 1: UUID Approach

**Advantages:**
- Guaranteed uniqueness across all systems globally
- No risk of hash collisions
- Standard format recognized by most tools
- Can distinguish resources with same name/namespace but different lifecycles

**Disadvantages:**
- Requires successful persistence to annotation
- Race condition during operator restart before persistence
- Adds write operation to every new resource
- Annotation could be accidentally modified/deleted

### Option 2: Deterministic Hash Approach

**Advantages:**
- Always produces same ID for same resource (namespace + name + UID)
- No persistence required - can be computed on-demand
- Survives operator restarts without any state
- No additional Kubernetes API calls needed
- Idempotent - same input always yields same output

**Disadvantages:**
- Theoretical hash collision risk (negligible with SHA256)
- If UID is not included, recreated resources get same ID (may or may not be desired)
- Less "random" - determined by resource properties
- Hashes are less human-readable than UUIDs

## Implementation Examples

### Option 2A Implementation (Without UID)

```go
package telemetry

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    
    "sigs.k8s.io/controller-runtime/pkg/client"
)

const hashPrefix = "documentdb-telemetry"

// GenerateClusterID creates a deterministic telemetry ID for a DocumentDB cluster.
// The ID is derived from namespace and name only.
func GenerateClusterID(obj client.Object) string {
    return generateHash("cluster", obj.GetNamespace(), obj.GetName())
}

// GenerateBackupID creates a deterministic telemetry ID for a Backup.
func GenerateBackupID(obj client.Object) string {
    return generateHash("backup", obj.GetNamespace(), obj.GetName())
}

// GenerateScheduledBackupID creates a deterministic telemetry ID for a ScheduledBackup.
func GenerateScheduledBackupID(obj client.Object) string {
    return generateHash("scheduledbackup", obj.GetNamespace(), obj.GetName())
}

func generateHash(resourceType, namespace, name string) string {
    data := fmt.Sprintf("%s:%s:%s/%s", hashPrefix, resourceType, namespace, name)
    hash := sha256.Sum256([]byte(data))
    // Use first 16 bytes (32 hex chars) - sufficient uniqueness, reasonable length
    return hex.EncodeToString(hash[:16])
}
```

### Option 2B Implementation (With UID)

```go
package telemetry

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    
    "k8s.io/apimachinery/pkg/types"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

const hashPrefix = "documentdb-telemetry"

// GenerateClusterID creates a deterministic telemetry ID for a DocumentDB cluster.
// The ID is derived from namespace, name, and UID.
func GenerateClusterID(obj client.Object) string {
    return generateHash("cluster", obj.GetNamespace(), obj.GetName(), obj.GetUID())
}

func generateHash(resourceType, namespace, name string, uid types.UID) string {
    data := fmt.Sprintf("%s:%s:%s/%s/%s", hashPrefix, resourceType, namespace, name, uid)
    hash := sha256.Sum256([]byte(data))
    return hex.EncodeToString(hash[:16])
}
```

## Summary

| Criteria | Option 1 (UUID) | Option 2A (Hash, No UID) | Option 2B (Hash, With UID) |
|----------|-----------------|--------------------------|----------------------------|
| Survives operator restart | Sometimes* | Always | Always |
| Requires persistence | Yes | No | No |
| API calls | 1 write per resource | 0 | 0 |
| Collision risk | None | Negligible | Negligible |
| Cluster recreated same name | New ID | Same ID | New ID |
| Matches user mental model | No | Yes | No |
| Implementation complexity | Higher | Lower | Lower |

*Only if annotation was successfully persisted before restart
