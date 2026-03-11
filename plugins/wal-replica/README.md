# WAL Replica Pod Manager (CNPG-I Plugin)

This plugin creates a standalone WAL receiver deployment alongside a [CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg/) cluster. It automatically provisions a Deployment named `<cluster-name>-wal-receiver` that continuously streams Write-Ahead Log (WAL) files from the primary PostgreSQL cluster using `pg_receivewal`, with support for both synchronous and asynchronous replication modes.

## Architecture

The plugin uses the [CNPG-I](https://github.com/cloudnative-pg/cnpg-i) (CloudNativePG Interface) gRPC protocol to extend CloudNativePG without forking the operator.

```

                   Kubernetes Cluster                     │
                                                          │
    gRPC/TLS    ┌────────────────────┐  │  ┌──────────
  │  CloudNativePG│◄─────────────►│  WAL Replica Plugin │  │
  Operator                  │  (this plugin)      │  ││    
  └──────┬───────┘               └────────┬───────────┘  │
         │                                │               │
         │ manages                        │ creates       │
         ▼                                ▼               │
  ┌──────────────┐               ┌────────────────────┐  │
  │ CNPG Cluster │  WAL stream   │  WAL Receiver      │  │
  │ (Primary PG) │◄─────────────│  Deployment + PVC  │  │
  │  pg_receivewal  (└──────────────┘)└──────────

```

### CNPG-I Interfaces Implemented

| Interface | Purpose |
|-----------|---------|
| **identity** | Declares plugin metadata and advertised capabilities |
| **operator** | Validates and mutates Cluster resources via webhooks |
| **reconciler** | Post-reconcile hook creates/updates the WAL receiver Deployment and PVC |

> **Note:** The `SetStatusInCluster` capability is currently disabled due to an [oscillation bug](https://github.com/documentdb/documentdb-kubernetes-operator/pull/74) where the enabled field alternates on every reconciliation. The `MutateCluster` webhook is registered but not fully implemented upstream in CNPG as of v1.28; defaults are applied in the reconciler as a workaround.

## Features

- **Automated WAL Streaming**: Continuously receives and stores WAL files from the primary cluster
- **Persistent Storage**: Automatically creates and manages a PersistentVolumeClaim for WAL storage
- **TLS Security**: Uses cluster certificates for secure replication connections
- **Replication Slot Management**: Automatically creates and manages a dedicated replication slot (`wal_replica`)
- **Synchronous Replication Support**: Configurable synchronous/asynchronous replication modes
- **Health Probes**: Liveness and readiness probes on the WAL receiver container
- **Cluster Lifecycle Management**: Proper OwnerReferences ensure resources are cleaned up when the cluster is deleted
- **Deployment Updates**: Configuration changes are automatically patched onto the existing Deployment

## Configuration

Add the plugin to your Cluster specification:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: my-cluster
spec:
  instances: 3

  plugins:
  - name: cnpg-i-wal-replica.documentdb.io
    parameters:
      image: "ghcr.io/cloudnative-pg/postgresql:16"
      replicationHost: "my-cluster-rw"
      synchronous: "active"
      walDirectory: "/var/lib/postgresql/wal"
      walPVCSize: "20Gi"
      verbose: "true"
      compression: "0"

  replicationSlots:
    synchronizeReplicas:
      enabled: true

  storage:
    size: 10Gi
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `image` | string | Cluster status image | Container image providing `pg_receivewal` binary |
| `replicationHost` | string | `<cluster>-rw` | Primary host endpoint for WAL streaming |
| `synchronous` | string | `inactive` | Replication mode: `active` or `inactive` |
| `walDirectory` | string | `/var/lib/postgresql/wal` | Directory path for storing received WAL files |
| `walPVCSize` | string | `10Gi` | Size of the PersistentVolumeClaim for WAL storage |
| `verbose` | string | `true` | Enable verbose `pg_receivewal` output (`true` / `false`) |
| `compression` | string | `0` | Compression level for WAL files (0-9, 0=disabled) |

## Deployment

The plugin runs as an independent Deployment and is discovered by CNPG via a Kubernetes Service with the `cnpgi.io/pluginName` label:

```bash
# Deploy using kustomize
kubectl apply -k kubernetes/
```

See the `kubernetes/` directory for the full set of manifests (Deployment, Service, RBAC, TLS certificates).

## Development

### Build

```bash
go build -o bin/cnpg-i-wal-replica main.go
```

### Test

```bash
go test ./...
```

### Project Structure

```
 cmd/plugin/          # Plugin command-line interface
 internal/
 config/         # Configuration management and validation   ├
   ├── identity/       # Plugin identity and capabilities
   ├── k8sclient/      # Kubernetes client utilities
   ├── operator/       # Operator hooks (validate, mutate, status)
   └── reconciler/     # Reconciliation logic (Deployment + PVC management)
 kubernetes/         # Kubernetes manifests
 pkg/metadata/       # Plugin metadata constants
 scripts/           # Build and deployment scripts
```

See [`doc/development.md`](doc/development.md) for detailed development guidelines.

## Limitations and Known Issues

- `MutateCluster` is not fully implemented upstream in CNPG v1.28; defaults are applied in the reconciler
- `SetStatusInCluster` is disabled due to status oscillation bug
- Fixed replication slot name (`wal_replica`)
- No built-in WAL retention/cleanup policies

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
