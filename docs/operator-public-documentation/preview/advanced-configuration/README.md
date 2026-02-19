# Advanced Configuration

This section covers advanced configuration options for the DocumentDB Kubernetes Operator.

## Table of Contents

- [TLS Configuration](#tls-configuration)
- [High Availability](#high-availability)
- [Storage Configuration](#storage-configuration)
- [Resource Management](#resource-management)
- [Security](#security)

## TLS Configuration

The operator supports three TLS modes for secure gateway connections, each suited to different operational requirements.

### TLS Modes

1. **SelfSigned** — Automatic certificate management using cert-manager with self-signed certificates
    - Best for: Development, testing, and environments without external PKI
    - Zero external dependencies
    - Automatic certificate rotation

2. **Provided** — Use certificates from Azure Key Vault via Secrets Store CSI driver
    - Best for: Production environments with centralized certificate management
    - Enterprise PKI integration
    - Azure Key Vault integration

3. **CertManager** — Use custom cert-manager issuers (for example, Let's Encrypt or a corporate CA)
    - Best for: Production environments with existing cert-manager infrastructure
    - Flexible issuer support
    - Industry-standard certificates

### Getting Started with TLS

For comprehensive TLS setup and testing documentation, see:

- **[Complete TLS Setup Guide](../../../../documentdb-playground/tls/README.md)** — Quick start with automated scripts, detailed configuration for each TLS mode, troubleshooting, and best practices
- **[E2E Testing Guide](../../../../documentdb-playground/tls/E2E-TESTING.md)** — Automated and manual testing, validation procedures, and CI/CD integration examples

### Quick TLS Setup

For the fastest TLS setup, use the automated script:

```bash
cd documentdb-playground/tls/scripts

# Complete E2E setup (AKS + DocumentDB + TLS)
./create-cluster.sh \
  --suffix mytest \
  --subscription-id <your-subscription-id>
```

This command will:

- Create an AKS cluster with all required addons
- Install cert-manager and the CSI driver
- Deploy the DocumentDB operator
- Configure and validate both SelfSigned and Provided TLS modes

**Duration**: ~25–30 minutes

### TLS Configuration Examples

#### SelfSigned Mode

SelfSigned mode requires no additional configuration beyond setting the mode:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-selfsigned
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: 10Gi
  tls:
    gateway:
      mode: SelfSigned
```

#### Provided Mode (Azure Key Vault)

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-provided
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: 10Gi
  tls:
    gateway:
      mode: Provided
      provided:
        secretName: documentdb-tls-akv
```

#### CertManager Mode with a custom issuer

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-certmanager
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: 10Gi
  tls:
    gateway:
      mode: CertManager
      certManager:
        issuerRef:
          name: letsencrypt-prod
          kind: ClusterIssuer
        dnsNames:
          - documentdb.example.com
          - "*.documentdb.example.com"
```

### TLS Status and Monitoring

Check the TLS status of your DocumentDB instance:

```bash
kubectl get documentdb <name> -n <namespace> -o jsonpath='{.status.tls}' | jq
```

Example output:

```json
{
  "ready": true,
  "secretName": "documentdb-gateway-cert-tls",
  "message": ""
}
```

### Certificate Rotation

The operator handles certificate rotation automatically:

- **SelfSigned and CertManager modes**: cert-manager rotates certificates before expiration
- **Provided mode**: Sync certificates from Azure Key Vault on rotation

Monitor certificate expiration:

```bash
# Check certificate expiration
kubectl get certificate -n <namespace> <cert-name> -o jsonpath='{.status.notAfter}'

# Inspect the TLS secret directly
kubectl get secret -n <namespace> <tls-secret-name> -o jsonpath='{.data.tls\.crt}' | \
  base64 -d | openssl x509 -noout -dates
```

### Troubleshooting TLS

For comprehensive troubleshooting, see the [E2E Testing Guide](../../../../documentdb-playground/tls/E2E-TESTING.md#troubleshooting).

Common issues:

1. **Certificate not ready** — Check cert-manager logs and certificate status
2. **Connection failures** — Verify service endpoints and TLS handshake
3. **Azure Key Vault access denied** — Check managed identity and RBAC permissions

Quick diagnostics:

```bash
# Check DocumentDB TLS status
kubectl describe documentdb <name> -n <namespace>

# Check certificate status
kubectl describe certificate -n <namespace>

# Check cert-manager logs
kubectl logs -n cert-manager deployment/cert-manager

# Test TLS handshake
EXTERNAL_IP=$(kubectl get svc -n <namespace> -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}')
openssl s_client -connect $EXTERNAL_IP:10260
```

---

## High Availability

Deploy multiple instances for automatic failover and read scalability.

### Multi-Instance Setup

Set `instancesPerNode` to 3 to create a primary with two replicas:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-ha
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: premium-ssd
```

### Recommended Settings

- **Minimum instances**: 3 for production workloads
- **Storage class**: Use premium SSDs for production
- **Resource requests**: Set appropriate CPU and memory limits

---

## Storage Configuration

Configure persistent storage for DocumentDB instances.

### Storage Classes

```yaml
spec:
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: premium-ssd  # Azure Premium SSD
```

### Volume Expansion

```bash
# Ensure storage class allows volume expansion
kubectl get storageclass <storage-class> -o jsonpath='{.allowVolumeExpansion}'

# Patch DocumentDB for larger storage
kubectl patch documentdb <name> -n <namespace> --type='json' \
  -p='[{"op": "replace", "path": "/spec/resource/storage/pvcSize", "value":"200Gi"}]'
```

---

## Resource Management

Configure resource requests and limits for optimal performance.

### Example Configuration

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-resources
  namespace: default
spec:
  nodeCount: 1
  instancesPerNode: 3
  resource:
    storage:
      pvcSize: 100Gi
```

### Recommendations

- **Development**: 1 CPU, 2 GiB memory
- **Production**: 2–4 CPUs, 4–8 GiB memory
- **High-load**: 4–8 CPUs, 8–16 GiB memory

---

## Security

Security best practices for DocumentDB deployments.

### Network Policies

Restrict network access to DocumentDB:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: documentdb-access
  namespace: default
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: documentdb
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: app-namespace
    ports:
    - protocol: TCP
      port: 10260
```

### RBAC

The operator requires specific permissions to manage DocumentDB resources. The Helm chart automatically creates the necessary RBAC rules.

### Secrets Management

Retrieve credentials from the Kubernetes Secret you created:

```bash
# Decode username
kubectl get secret documentdb-credentials -n <namespace> \
  -o jsonpath='{.data.username}' | base64 -d

# Decode password
kubectl get secret documentdb-credentials -n <namespace> \
  -o jsonpath='{.data.password}' | base64 -d
```

For production, consider using:

- Azure Key Vault for secrets (via Secrets Store CSI driver)
- HashiCorp Vault integration
- External Secrets Operator

---

## Additional Resources

- [Public Documentation](https://documentdb.io/documentdb-kubernetes-operator/preview/)
- [TLS Setup Guide](../../../../documentdb-playground/tls/README.md)
- [E2E Testing Guide](../../../../documentdb-playground/tls/E2E-TESTING.md)
- [GitHub Repository](https://github.com/documentdb/documentdb-kubernetes-operator)
