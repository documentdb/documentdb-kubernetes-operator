# WAL Replica Plugin Development Guide

This document provides comprehensive guidance for developers working with the WAL Replica plugin for CloudNativePG. It covers the CNPG-I framework capabilities used, implementation details, and development workflows.

## Overview

The WAL Replica plugin (`cnpg-i-wal-replica.documentdb.io`) is built using the CloudNativePG Interface (CNPG-I) framework, which provides a plugin architecture for extending CloudNativePG clusters with custom functionality.

### Plugin Metadata

```go
// From pkg/metadata/doc.go
const PluginName = "cnpg-i-wal-replica.documentdb.io"
Version: "0.1.0"
DisplayName: "WAL Replica Pod Manager"
License: "MIT"
Maturity: "alpha"
```

## CNPG-I Framework Concepts

### Identity Interface

The Identity interface is fundamental to all CNPG-I plugins and defines:

- **Plugin Metadata**: Name, version, description, and licensing information
- **Capabilities**: Which CNPG-I services the plugin implements
- **Readiness**: Health check mechanism for the plugin

The identity implementation is located in [`internal/identity/impl.go`](../internal/identity/impl.go).

**Key Methods:**
- `GetPluginMetadata()`: Returns plugin information from `pkg/metadata`
- `GetPluginCapabilities()`: Declares supported services (Operator + Reconciler)
- `Probe()`: Always returns ready (stateless plugin)

[CNPG-I Identity API Reference](https://github.com/cloudnative-pg/cnpg-i/blob/main/proto/identity.proto)

### Implemented Capabilities

This plugin implements two core CNPG-I capabilities:

#### 1. Operator Interface

Provides cluster-level validation and mutation capabilities through webhooks:

```go
// From internal/operator/
rpc ValidateClusterCreate(OperatorValidateClusterCreateRequest) returns (OperatorValidateClusterCreateResult)
rpc ValidateClusterChange(OperatorValidateClusterChangeRequest) returns (OperatorValidateClusterChangeResult)  
rpc MutateCluster(OperatorMutateClusterRequest) returns (OperatorMutateClusterResult)
```

**Implementation Features:**
- **Parameter Validation**: Validates `synchronous`, `walPVCSize`, and other plugin parameters
- **Default Application**: Sets default values for image, replication host, WAL directory
- **Configuration Parsing**: Converts plugin parameters to typed configuration objects

See [`internal/operator/validation.go`](../internal/operator/validation.go) and [`internal/operator/mutations.go`](../internal/operator/mutations.go).

#### 2. Reconciler Hooks Interface  

Enables resource reconciliation and custom Kubernetes resource management:

```go
// From internal/reconciler/
rpc ReconcilerHook(ReconcilerHookRequest) returns (ReconcilerHookResponse)
```

**Core Functionality:**
- **WAL Receiver Deployment**: Creates and manages the `<cluster>-wal-receiver` deployment
- **PVC Management**: Provisions persistent storage for WAL files
- **TLS Configuration**: Sets up certificate-based authentication
- **Resource Lifecycle**: Handles creation, updates, and cleanup with owner references

See [`internal/reconciler/replica.go`](../internal/reconciler/replica.go) for the main implementation.

[CNPG-I Operator API Reference](https://github.com/cloudnative-pg/cnpg-i/blob/main/proto/operator.proto)

## Architecture Deep Dive

### Configuration Management

The plugin uses a layered configuration approach:

```go
// From internal/config/config.go
type Configuration struct {
    Image           string          // Container image for pg_receivewal
    ReplicationHost string          // Primary cluster endpoint  
    Synchronous     SynchronousMode // active/inactive replication mode
    WalDirectory    string          // WAL storage path
    WalPVCSize      string          // Storage size for PVC
}
```

**Configuration Flow:**
1. Raw parameters from Cluster spec
2. Validation using `ValidateParams()`
3. Type conversion via `FromParameters()`
4. Default application with `ApplyDefaults()`

### Resource Reconciliation

The plugin creates and manages several Kubernetes resources:

#### WAL Receiver Deployment

```yaml
# Generated deployment structure
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <cluster>-wal-receiver
  ownerReferences: [<cluster>]
spec:
  containers:
  - name: wal-receiver
    image: <configured-image>
    command: ["/bin/bash", "-c"]
    args: ["<pg_receivewal-command>"]
    volumeMounts:
    - name: wal-storage
      mountPath: <walDirectory>
    - name: ca
      mountPath: /var/lib/postgresql/rootcert
    - name: tls  
      mountPath: /var/lib/postgresql/cert
```

#### pg_receivewal Command Construction

The plugin builds sophisticated `pg_receivewal` commands:

```bash
# Two-phase execution:
# 1. Create replication slot (if needed)
pg_receivewal --slot wal_replica --create-slot --if-not-exists --directory /path/to/wal --dbname "postgres://streaming_replica@host/postgres?sslmode=verify-full&..."

# 2. Continuous WAL streaming  
pg_receivewal --slot wal_replica --compress 0 --directory /path/to/wal --dbname "postgres://..." [--synchronous] [--verbose]
```

### Security Implementation

**TLS Configuration:**
- Uses cluster-managed certificates from CloudNativePG
- Mounts CA certificate for SSL verification
- Client certificate authentication for `streaming_replica` user
- `sslmode=verify-full` for maximum security

**Pod Security:**
- Runs as PostgreSQL user (`uid: 105, gid: 103`)
- Proper filesystem permissions (`fsGroup: 103`)
- Read-only certificate mounts

## Development Environment Setup

### Prerequisites

```bash
# Required tools
go 1.24.1+
docker or podman
kubectl
kind (for local testing)

# Required Kubernetes components
cloudnative-pg operator
cnpg-i framework
cert-manager (for TLS)
```

### Local Development

#### 1. Environment Setup

```bash
# Clone repository
git clone https://github.com/documentdb/cnpg-i-wal-replica
cd cnpg-i-wal-replica

# Install dependencies
go mod download

# Verify build
go build -o bin/cnpg-i-wal-replica main.go
```

#### 2. Code Structure Navigation

```
├── cmd/plugin/           # CLI interface and gRPC server setup
│   ├── doc.go           # Package documentation
│   └── plugin.go        # Main command and service registration
├── internal/
│   ├── config/          # Configuration management
│   │   ├── config.go    # Configuration types and validation
│   │   └── doc.go       # Package documentation
│   ├── identity/        # Plugin identity implementation
│   │   ├── impl.go      # Identity service methods
│   │   └── doc.go       # Package documentation
│   ├── k8sclient/       # Kubernetes client utilities
│   │   ├── k8sclient.go # Client initialization and management
│   │   └── doc.go       # Package documentation
│   ├── operator/        # Operator interface implementation
│   │   ├── impl.go      # Core operator methods
│   │   ├── mutations.go # Cluster mutation logic
│   │   ├── status.go    # Status reporting
│   │   ├── validation.go# Parameter validation
│   │   └── doc.go       # Package documentation
│   └── reconciler/      # Resource reconciliation
│       ├── impl.go      # Reconciler hook implementation
│       ├── replica.go   # WAL receiver resource management
│       └── doc.go       # Package documentation
├── kubernetes/          # Deployment manifests
└── pkg/metadata/        # Plugin metadata constants
```

#### 3. Testing Locally

```bash
# Build and test
./scripts/build.sh

# Run with debugging
./scripts/run.sh

# Test configuration parsing
go test ./internal/config/

# Test reconciliation logic  
go test ./internal/reconciler/
```

### Container Development

#### Building Images

```bash
# Local build
docker build -t wal-replica-plugin:dev .

# Multi-arch build
docker buildx build --platform linux/amd64,linux/arm64 -t wal-replica-plugin:latest .
```

#### Kubernetes Testing

```bash
# Load into kind cluster
kind load docker-image wal-replica-plugin:dev

# Apply manifests
kubectl apply -f kubernetes/

# Deploy test cluster
kubectl apply -f doc/examples/cluster-example.yaml
```

## Extending the Plugin

### Adding New Parameters

1. **Define in Configuration**:
```go
// internal/config/config.go
const MyNewParam = "myNewParam"

type Configuration struct {
    // existing fields...
    MyNewValue string
}
```

2. **Add Validation**:
```go
// internal/config/config.go  
func ValidateParams(helper *common.Plugin) []*operator.ValidationError {
    // existing validation...
    
    if raw, present := helper.Parameters[MyNewParam]; present {
        // Add validation logic
    }
}
```

3. **Update Reconciliation**:
```go
// internal/reconciler/replica.go
func CreateWalReplica(ctx context.Context, cluster *cnpgv1.Cluster) error {
    // Use configuration.MyNewValue in resource creation
}
```

### Adding Resource Management

```go
// Example: Adding a Service resource
func createWalReceiverService(ctx context.Context, cluster *cnpgv1.Cluster) error {
    service := &corev1.Service{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("%s-wal-receiver", cluster.Name),
            Namespace: cluster.Namespace,
            OwnerReferences: []metav1.OwnerReference{{
                APIVersion: cluster.APIVersion,
                Kind:       cluster.Kind,
                Name:       cluster.Name,
                UID:        cluster.UID,
            }},
        },
        Spec: corev1.ServiceSpec{
            Selector: map[string]string{"app": fmt.Sprintf("%s-wal-receiver", cluster.Name)},
            Ports: []corev1.ServicePort{{
                Port:       5432,
                TargetPort: intstr.FromInt(5432),
            }},
        },
    }
    
    return k8sclient.MustGet().Create(ctx, service)
}
```

## Debugging and Troubleshooting

### Common Development Issues

1. **gRPC Connection Problems**:
```bash
# Check plugin registration
kubectl logs -l app=cnpg-i-wal-replica

# Verify TLS certificates
kubectl describe secret <cluster>-ca-secret
```

2. **Resource Creation Failures**:
```bash
# Check reconciler logs
kubectl logs deployment/<cluster>-wal-receiver

# Verify owner references
kubectl get deployment <cluster>-wal-receiver -o yaml
```

3. **Parameter Validation Errors**:
```bash
# Check cluster events
kubectl describe cluster <cluster-name>

# Review validation logs
kubectl logs -l app=cnpg-operator
```

### Testing Configurations

```yaml
# Test with minimal parameters
spec:
  plugins:
  - name: cnpg-i-wal-replica.documentdb.io
    # No parameters - should use all defaults

# Test with full configuration
spec:
  plugins:
  - name: cnpg-i-wal-replica.documentdb.io
    parameters:
      image: "postgres:16"
      replicationHost: "my-cluster-rw" 
      synchronous: "active"
      walDirectory: "/custom/wal/path"
      walPVCSize: "50Gi"
```

## CI/CD Integration

### GitHub Actions Workflow

The repository includes automated workflows for:

- **Build Verification**: Compiles plugin for multiple architectures
- **Container Publishing**: Builds and pushes container images
- **Manifest Generation**: Creates deployment artifacts
- **Integration Testing**: Tests against live CloudNativePG clusters

### Deployment Artifacts

Generated manifests include:
- Plugin deployment with proper RBAC
- Certificate management for TLS
- Service definitions for plugin discovery
- Example cluster configurations

## Contributing Guidelines

### Code Standards

- Follow Go conventions and `gofmt` formatting
- Add comprehensive unit tests for new functionality
- Document all public interfaces and complex logic
- Use structured logging with appropriate levels

### Pull Request Process

1. Fork and create feature branch
2. Implement changes with tests
3. Update documentation
4. Submit PR with detailed description
5. Address review feedback

### Testing Requirements

- Unit tests for all new configuration parameters
- Integration tests for resource reconciliation
- End-to-end testing with real CloudNativePG clusters
- Performance testing for WAL streaming scenarios

## Future Development Roadmap

### Planned Enhancements

- **Enhanced Monitoring**: Prometheus metrics for WAL streaming
- **Multi-Zone Support**: Cross-region WAL archival capabilities  
- **Backup Integration**: Coordination with CloudNativePG backup strategies
- **Resource Optimization**: Configurable resource requests/limits
- **Advanced Filtering**: WAL file retention and cleanup policies
- **Replica Support**: Extension to replica clusters for cascading replication

### API Stability

- Current API is alpha-level with potential breaking changes
- Plugin interface follows CNPG-I versioning conventions
- Configuration parameters may evolve based on user feedback

## Resources

- [CloudNativePG Documentation](https://cloudnative-pg.io/)
- [CNPG-I Framework](https://github.com/cloudnative-pg/cnpg-i)
- [PostgreSQL WAL Documentation](https://www.postgresql.org/docs/current/wal.html)
- [Plugin Examples Repository](https://github.com/cloudnative-pg/cnpg-i-examples)
