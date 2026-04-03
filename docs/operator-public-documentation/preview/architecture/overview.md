---
title: Architecture Overview
description: Understanding the DocumentDB Kubernetes Operator architecture, components, and data flow.
tags:
  - architecture
  - concepts
  - design
search:
  boost: 2
---

# Architecture Overview

This page describes the architecture of the DocumentDB Kubernetes Operator, its components, and how they work together to manage DocumentDB clusters on Kubernetes.

## System architecture

The DocumentDB Kubernetes Operator extends Kubernetes with custom resources to declaratively manage DocumentDB clusters. It builds on [CloudNative-PG](https://cloudnative-pg.io/) for PostgreSQL management while adding the DocumentDB Gateway for MongoDB-compatible access.

```mermaid
flowchart TB
    API[Kubernetes API Server]
    
    subgraph DocDBNS[documentdb-operator namespace]
        DOC_OP[DocumentDB Operator]
    end
    
    subgraph CNPGNS[cnpg-system namespace]
        CNPG_OP[CloudNative-PG Operator]
    end
    
    subgraph App[Application Namespace]
        DOC_CR[DocumentDB CR]
        BACKUP_CR[Backup CR]
        CLUSTER_CR[CNPG Cluster CR]
        LB[External LoadBalancer]
        POD1[Pod 1 - Primary]
        POD2[Pod 2 - Replica]
        POD3[Pod 3 - Replica]
        PVC1[PVC 1]
        PVC2[PVC 2]
        PVC3[PVC 3]
    end
    
    subgraph External[External]
        CLIENT[MongoDB Client]
        VS[Volume Snapshots]
    end
    
    API --> DOC_OP
    API --> CNPG_OP
    
    CLIENT --> LB
    LB --> POD1
    
    DOC_OP --> DOC_CR
    DOC_OP --> BACKUP_CR
    DOC_OP --> CLUSTER_CR
    DOC_OP --> LB
    
    CNPG_OP --> CLUSTER_CR
    CNPG_OP --> POD1
    CNPG_OP --> POD2
    CNPG_OP --> POD3
    CNPG_OP --> VS
    
    POD1 --> PVC1
    POD2 --> PVC2
    POD3 --> PVC3
    
    POD1 --> POD2
    POD1 --> POD3
```

## Core components

### DocumentDB Operator

The DocumentDB Operator is a Kubernetes controller that manages the lifecycle of DocumentDB clusters. It is deployed as a single pod in the `documentdb-operator` namespace.

**Responsibilities:**

- Watches `DocumentDB`, `Backup`, and `ScheduledBackup` custom resources
- Creates and configures CNPG `Cluster` resources
- Manages TLS certificates via cert-manager
- Handles backup creation and retention
- Manages PersistentVolume lifecycle for data recovery

**Controllers:**

| Controller | Purpose |
|------------|---------|
| DocumentDB Controller | Reconciles `DocumentDB` CRs, creates CNPG clusters |
| Backup Controller | Handles on-demand backup requests |
| ScheduledBackup Controller | Manages scheduled backup jobs |
| Certificate Controller | Provisions TLS certificates for gateway |
| PV Controller | Manages PV labels and reclaim policies |

### CloudNative-PG Operator

[CloudNative-PG](https://cloudnative-pg.io/) is an open-source PostgreSQL operator that handles the low-level PostgreSQL cluster management. It is installed as a Helm dependency and runs in the `cnpg-system` namespace.

**Responsibilities:**

- Pod lifecycle management (create, delete, restart)
- Streaming replication setup between primary and replicas
- Automatic failover when primary becomes unhealthy
- Connection pooling and service management
- Rolling updates and maintenance operations

### DocumentDB pod architecture

Each DocumentDB instance runs as a Kubernetes pod with two containers:

```mermaid
flowchart LR
    subgraph Pod[DocumentDB Pod]
        PG[PostgreSQL - port 5432]
        GW[Gateway - port 10260]
        DATA[PGDATA Volume]
        EXT[Extension Volume]
    end
    
    CLIENT[Client] --> GW
    GW --> PG
    PG --> DATA
    PG --> EXT
```

| Container | Image | Purpose |
|-----------|-------|---------|
| **PostgreSQL** | `ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` | Database engine with DocumentDB extension |
| **Gateway** | `ghcr.io/documentdb/documentdb-kubernetes-operator/gateway` | MongoDB wire protocol translation |

| Volume | Purpose |
|--------|---------|
| **PGDATA** | PostgreSQL data directory (PVC-backed) |
| **Extension** | DocumentDB extension files (ImageVolume) |

### Services

Kubernetes Services expose DocumentDB to clients:

| Service | Created By | Purpose |
|---------|------------|----------|
| `documentdb-service-<name>` | DocumentDB Operator | External LoadBalancer for client access |
| `<cluster>-rw` | CNPG | Internal read-write access (primary only) |
| `<cluster>-r` | CNPG | Internal access to any instance |

For external access, configure `spec.exposeViaService.serviceType: LoadBalancer`. The DocumentDB operator creates the external LoadBalancer service (`documentdb-service-<name>`) that routes traffic to the primary pod.

## Data flow

### Client connection flow

```mermaid
sequenceDiagram
    participant Client as MongoDB Client
    participant LB as Load Balancer
    participant GW as Gateway
    participant PG as PostgreSQL
    participant Disk as Storage
    
    Client->>LB: Connect (MongoDB protocol)
    LB->>GW: Forward to port 10260
    GW->>GW: Parse MongoDB command
    GW->>PG: Translate to SQL
    PG->>Disk: Read/Write data
    Disk-->>PG: Data
    PG-->>GW: Result set
    GW-->>Client: BSON response
```

### Replication flow

```mermaid
flowchart LR
    subgraph Primary[Primary Instance]
        PG1[PostgreSQL]
        WAL1[WAL]
    end
    
    subgraph Replica1[Replica 1]
        PG2[PostgreSQL]
        WAL2[WAL]
    end
    
    subgraph Replica2[Replica 2]
        PG3[PostgreSQL]
        WAL3[WAL]
    end
    
    PG1 --> WAL1
    WAL1 --> PG2
    WAL1 --> PG3
    PG2 --> WAL2
    PG3 --> WAL3
```

DocumentDB uses PostgreSQL's native streaming replication:

1. **Primary** writes to the Write-Ahead Log (WAL)
2. **WAL Sender** streams changes to replicas
3. **WAL Receiver** on replicas applies changes
4. Replication is **asynchronous** by default (milliseconds of lag)

## Kubernetes resources

When you create a `DocumentDB` resource, the operator creates and manages these Kubernetes resources:

```mermaid
flowchart TD
    DOC[DocumentDB CR] --> CNPG[CNPG Cluster CR]
    DOC --> SVC_EXT[External Service]
    DOC --> CERT[Certificate]
    
    CNPG --> POD1[Pod 1]
    CNPG --> POD2[Pod 2]
    CNPG --> POD3[Pod 3]
    CNPG --> PVC1[PVC 1]
    CNPG --> PVC2[PVC 2]
    CNPG --> PVC3[PVC 3]
    CNPG --> SVC_RW[RW Service]
    CNPG --> SVC_RO[RO Service]
    CNPG --> SECRET[Postgres Secrets]
    
    PVC1 --> PV1[PV 1]
    PVC2 --> PV2[PV 2]
    PVC3 --> PV3[PV 3]
```

| Resource | Controller | Purpose |
|----------|------------|---------|
| `DocumentDB` | User | Desired state definition |
| `Cluster` (CNPG) | DocumentDB Operator | PostgreSQL cluster configuration |
| `Pod` | CNPG Operator | Database instances |
| `PVC` | CNPG Operator | Persistent storage claims |
| `PV` | Storage provisioner | Actual storage volumes |
| `Service` | CNPG Operator | Internal cluster access |
| `Service` (external) | DocumentDB Operator | External LoadBalancer access |
| `Certificate` | DocumentDB Operator | TLS certificate request |
| `Secret` | cert-manager / CNPG | Credentials and certificates |

## High availability architecture

With `instancesPerNode: 3`, the operator deploys a highly available configuration:

```mermaid
flowchart TB
    subgraph AZ1[Availability Zone A]
        POD1[Primary pod-1]
        PVC1[Storage]
    end
    
    subgraph AZ2[Availability Zone B]
        POD2[Replica pod-2]
        PVC2[Storage]
    end
    
    subgraph AZ3[Availability Zone C]
        POD3[Replica pod-3]
        PVC3[Storage]
    end
    
    SVC[Service] --> POD1
    POD1 --> PVC1
    POD2 --> PVC2
    POD3 --> PVC3
    
    POD1 --> POD2
    POD1 --> POD3
```

**Failover behavior:**

1. Primary becomes unhealthy (readiness probe fails)
2. CNPG operator selects most up-to-date replica
3. Selected replica promotes to primary (~30 seconds)
4. Service endpoints update automatically
5. Former primary restarts as replica

For detailed HA configuration, see [Advanced Configuration](../advanced-configuration/README.md#high-availability).

## Backup architecture

DocumentDB supports volume snapshot-based backups:

```mermaid
flowchart LR
    subgraph Cluster[DocumentDB Cluster]
        PRIMARY[Primary Pod]
        PVC[PVC]
    end
    
    subgraph Backup[Backup Process]
        BACKUP_CR[Backup CR]
        VS[VolumeSnapshot]
    end
    
    BACKUP_CR --> VS
    PVC --> VS
    
    subgraph Recovery[Recovery]
        NEW_PVC[New PVC]
        NEW_CLUSTER[New Cluster]
    end
    
    VS --> NEW_PVC
    NEW_PVC --> NEW_CLUSTER
```

For backup configuration, see [Backup and Restore](../backup-and-restore.md).

## Dependencies

| Dependency | Version | Purpose |
|------------|---------|---------|
| [CloudNative-PG](https://cloudnative-pg.io/) | 1.28+ | PostgreSQL cluster management |
| [cert-manager](https://cert-manager.io/) | 1.19+ | TLS certificate management |
| Kubernetes | 1.35+ | Container orchestration (ImageVolume feature required) |

## Next steps

- [Before You Start](../getting-started/before-you-start.md) - Prerequisites and terminology
- [Quickstart](../index.md) - Deploy your first DocumentDB cluster
- [API Reference](../api-reference.md) - Full CRD documentation
