# WAL Replica Pod Manager (CNPG-I Plugin)

This plugin creates a standalone WAL receiver deployment alongside a [CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg/) cluster. It automatically provisions a Deployment named `<cluster-name>-wal-receiver` that continuously streams Write-Ahead Log (WAL) files from the primary PostgreSQL cluster using `pg_receivewal`, with support for both synchronous and asynchronous replication modes.

## Features

- **Automated WAL Streaming**: Continuously receives and stores WAL files from the primary cluster
- **Persistent Storage**: Automatically creates and manages a PersistentVolumeClaim for WAL storage
- **TLS Security**: Uses cluster certificates for secure replication connections
- **Replication Slot Management**: Automatically creates and manages a dedicated replication slot (`wal_replica`)
- **Synchronous Replication Support**: Configurable synchronous/asynchronous replication modes
- **Cluster Lifecycle Management**: Automatically manages resources with proper owner references

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
| `synchronous` | string | `inactive` | Replication mode: `active` (synchronous) or `inactive` (asynchronous) |
| `walDirectory` | string | `/var/lib/postgresql/wal` | Directory path for storing received WAL files |
| `walPVCSize` | string | `10Gi` | Size of the PersistentVolumeClaim for WAL storage |

#### Synchronous Modes

- **`active`**: Enables synchronous replication with `--synchronous` flag
- **`inactive`**: Standard asynchronous replication (default)

## Architecture

The plugin creates the following Kubernetes resources:

1. **Deployment**: `<cluster-name>-wal-receiver`
   - Single replica pod running `pg_receivewal`
   - Configured with proper security context (user: 105, group: 103)
   - Automatic restart policy for high availability

2. **PersistentVolumeClaim**: `<cluster-name>-wal-receiver`
   - Stores received WAL files persistently
   - Uses `ReadWriteOnce` access mode
   - Configurable size via `walPVCSize` parameter

3. **Volume Mounts**:
   - WAL storage: Mounted at configured `walDirectory`
   - TLS certificates: Mounted from cluster certificate secrets
   - CA certificates: Mounted for SSL verification

## Security

The plugin implements comprehensive security measures:

- **TLS Encryption**: All replication connections use SSL/TLS
- **Certificate Management**: Automatically mounts cluster CA and client certificates
- **User Privileges**: Runs with dedicated PostgreSQL user and group IDs
- **Connection Authentication**: Uses `streaming_replica` user with certificate-based auth

## Prerequisites

- CloudNativePG operator installed and running
- CNPG-I (CloudNativePG Interface) framework deployed
- Cluster with enabled replication slots synchronization
- Sufficient storage for WAL files retention

## Installation

### Building from Source

```bash
# Clone the repository
git clone https://github.com/documentdb/cnpg-i-wal-replica
cd cnpg-i-wal-replica

# Build the binary
go build -o bin/cnpg-i-wal-replica main.go
```

### Using Docker

```bash
# Build container image
docker build -t cnpg-i-wal-replica:latest .
```

### Deployment Scripts

```bash
# Make scripts executable
chmod +x scripts/build.sh scripts/run.sh

# Build and run
./scripts/build.sh
./scripts/run.sh
```

## Monitoring and Observability

The WAL receiver pod provides verbose logging when enabled, including:

- Connection status to primary cluster
- WAL file reception progress
- Replication slot status
- SSL/TLS connection details

## Examples

See the `doc/examples/` directory for complete cluster configurations:

- [`cluster-example.yaml`](doc/examples/cluster-example.yaml): Basic configuration
- [`cluster-example-no-parameters.yaml`](doc/examples/cluster-example-no-parameters.yaml): Default settings
- [`cluster-example-with-mistake.yaml`](doc/examples/cluster-example-with-mistake.yaml): Common configuration errors

## Development

### Project Structure

```
├── cmd/plugin/          # Plugin command-line interface
├── internal/
│   ├── config/         # Configuration management
│   ├── identity/       # Plugin identity and metadata
│   ├── k8sclient/      # Kubernetes client utilities
│   ├── operator/       # Operator implementations
│   └── reconciler/     # Resource reconciliation logic
├── kubernetes/         # Kubernetes manifests
├── pkg/metadata/       # Plugin metadata and constants
└── scripts/           # Build and deployment scripts
```

### Running Tests

```bash
go test ./...
```

See [`doc/development.md`](doc/development.md) for detailed development guidelines.

## Limitations and Future Enhancements

### Current Limitations

- Fixed compression level (disabled: `--compress 0`)
- No built-in WAL retention/cleanup policies
- Limited resource configuration options

### Planned Enhancements

- [ ] Configurable resource requests and limits
- [ ] WAL retention and garbage collection policies  
- [ ] Health checks and readiness probes
- [ ] Metrics exposure for monitoring integration
- [ ] Multi-zone/region WAL archiving support
- [ ] Backup integration with existing CNPG backup strategies

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](../../CONTRIBUTING.md) for guidelines on how to contribute to this project.

