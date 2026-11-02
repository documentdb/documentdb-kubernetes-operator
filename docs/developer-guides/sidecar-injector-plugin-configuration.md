# Sidecar Injector Plugin Configuration

## Overview

The DocumentDB Sidecar Injector is a CNPG plugin that automatically injects the DocumentDB Gateway container into PostgreSQL pods. The plugin supports multiple configuration parameters that can be customized through the DocumentDB custom resource specification. This follows CNPG's plugin parameter pattern where configuration is passed from the operator to the sidecar injector plugin.

## Configuration Flow

```
DocumentDB CR Spec
    ↓
DocumentDB Controller 
    ↓ 
CNPG Cluster with Plugin Parameters
    ↓
Sidecar Injector Plugin
    ↓
Pod with Configured Sidecar Containers and Metadata
```

## Configuration Parameters

The sidecar injector plugin supports the following configuration parameters:

### 1. Gateway Image Configuration

Controls which DocumentDB Gateway container image is injected into PostgreSQL pods.

#### Configuration Options

**Per DocumentDB Instance (Explicit):**
```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: "10Gi"
  # Explicitly specify gateway image
  image:
    gateway: "ghcr.io/microsoft/documentdb/documentdb-local:17"
  exposeViaService:
    serviceType: "ClusterIP"
```

**Built-in Fallback:**
If no explicit spec configuration is set, the system falls back to:
```
ghcr.io/microsoft/documentdb/documentdb-local:16
```

### 2. Pod Labels Configuration

Controls additional labels that are applied to injected pods.

```yaml
# Example: Custom labels via plugin parameters
plugins:
  - name: cnpg-i-sidecar-injector.documentdb.io
    parameters:
      labels: '{"environment":"production","team":"data"}'
```

### 3. Pod Annotations Configuration

Controls additional annotations that are applied to injected pods.

```yaml
# Example: Custom annotations via plugin parameters
plugins:
  - name: cnpg-i-sidecar-injector.documentdb.io
    parameters:
      annotations: '{"prometheus.io/scrape":"true","prometheus.io/port":"8080"}'
```

### 4. Gateway Container Contract

These optional parameters let a product other than DocumentDB run its own
gateway with the same injector. The DocumentDB operator never sets them, so the
defaults below apply.

| Parameter | Default | Description |
| --- | --- | --- |
| `gatewayContainerName` | `documentdb-gateway` | Name of the injected gateway container |
| `gatewayPort` | `10260` | Gateway container port |
| `gatewayArgs` | DocumentDB gateway args | JSON array that replaces `--create-user <bool> --start-pg false --pg-port 5432` and the TLS `--cert-path`/`--key-file` flags verbatim |
| `gatewayUsernameEnv` | `USERNAME` | Env var that receives the credential secret's `username` key |
| `gatewayPasswordEnv` | `PASSWORD` | Env var that receives the credential secret's `password` key |

The operator forwards these from `ProductProfile.Gateway` (see
[Product-neutral operator core](../designs/product-neutral-core.md)).

```yaml
plugins:
  - name: cnpg-i-sidecar-injector.documentdb.io
    parameters:
      gatewayContainerName: "pgcosmos-gateway"
      gatewayPort: "8081"
      gatewayArgs: '["--port","8081"]'
```

## CNPG Plugin Parameters

When a product uses password authentication for the SQL monitoring role, the
optional `otelPasswordSecret` parameter names the same namespace-local
`kubernetes.io/basic-auth` Secret that CNPG's `otel_monitor` managed role uses.
The injector sets the collector's `PGPASSWORD` from its `password` key.
`ProductProfile.Postgres.MonitoringPasswordSecret` wires both references; the
default DocumentDB configuration omits this parameter. No trust rules are
added to the product's authentication policy.

The DocumentDB controller automatically passes all configuration parameters to the sidecar injector plugin via CNPG's plugin parameter mechanism:

```yaml
# Generated CNPG Cluster with all plugin parameters
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: my-documentdb
spec:
  plugins:
    - name: cnpg-i-sidecar-injector.documentdb.io
      parameters:
        gatewayImage: "ghcr.io/microsoft/documentdb/documentdb-local:17"
        labels: '{"environment":"production","team":"data"}'
        annotations: '{"prometheus.io/scrape":"true"}'
```

## Configuration Examples

### Basic Configuration (Gateway Image Only)

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: basic-documentdb
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: "10Gi"
  image:
    gateway: "ghcr.io/microsoft/documentdb/documentdb-local:17"
```

### Advanced Configuration (All Parameters)

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: advanced-documentdb
spec:
  nodeCount: 1
  instancesPerNode: 1
  resource:
    storage:
      pvcSize: "20Gi"
  image:
    gateway: "ghcr.io/microsoft/documentdb/documentdb-local:17"
  plugins:
    sidecarInjectorName: "cnpg-i-sidecar-injector.documentdb.io"
  exposeViaService:
    serviceType: "LoadBalancer"
```

