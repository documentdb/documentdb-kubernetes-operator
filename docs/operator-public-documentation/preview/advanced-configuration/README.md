# Advanced Configuration

This section covers advanced configuration options for the DocumentDB Kubernetes Operator.

## Table of Contents

- [TLS Configuration](#tls-configuration)
- [High Availability](#high-availability)
- [Storage Configuration](#storage-configuration)
- [Scheduling](#scheduling)
- [Resource Management](#resource-management)
- [Security](#security)

## TLS Configuration

The DocumentDB Kubernetes Operator supports comprehensive TLS configuration for secure gateway connections. We provide three TLS modes to support different operational requirements:

### TLS Modes

1. **SelfSigned Mode** - Automatic certificate management using cert-manager with self-signed certificates
   - Best for: Development, testing, and environments without external PKI
   - Zero external dependencies
   - Automatic certificate rotation

2. **Provided Mode** - Use certificates from Azure Key Vault via Secrets Store CSI driver
   - Best for: Production environments with centralized certificate management
   - Enterprise PKI integration
   - Azure Key Vault integration

3. **CertManager Mode** - Use custom cert-manager issuers (e.g., Let's Encrypt, corporate CA)
   - Best for: Production environments with existing cert-manager infrastructure
   - Flexible issuer support
   - Industry-standard certificates

### Getting Started with TLS

For comprehensive TLS setup and testing documentation, see:

**📖 [Complete TLS Setup Guide](../../../../documentdb-playground/tls/README.md)**

This guide includes:
- Quick start with automated scripts (5-minute setup)
- Detailed configuration for each TLS mode
- Troubleshooting and best practices
- Complete script reference

**🧪 [E2E Testing Guide](../../../../documentdb-playground/tls/E2E-TESTING.md)**

This guide covers:
- Automated E2E testing with scripts
- Manual step-by-step testing
- Validation and verification procedures
- CI/CD integration examples

### Quick TLS Setup

For the fastest TLS setup, use our automated script:

```bash
cd documentdb-playground/tls/scripts

# Complete E2E setup (AKS + DocumentDB + TLS)
./create-cluster.sh \
  --suffix mytest \
  --subscription-id <your-subscription-id>
```

This single command will:
- ✅ Create AKS cluster with all required addons
- ✅ Install cert-manager and CSI driver
- ✅ Deploy DocumentDB operator
- ✅ Configure and validate both SelfSigned and Provided TLS modes

**Duration**: ~25-30 minutes

### TLS Configuration Examples

#### Example 1: SelfSigned Mode

```yaml
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-selfsigned
  namespace: default
spec:
  version: "16"
  instances: 3
  storage:
    size: 10Gi
  tls:
    mode: SelfSigned
    selfSigned:
      issuerName: documentdb-selfsigned-issuer
      certificateName: documentdb-gateway-cert
```

#### Example 2: Provided Mode (Azure Key Vault)

```yaml
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-provided
  namespace: default
spec:
  version: "16"
  instances: 3
  storage:
    size: 10Gi
  tls:
    mode: Provided
    provided:
      secretName: documentdb-tls-akv
      secretProviderClass: azure-kv-documentdb
```

#### Example 3: CertManager Mode with Let's Encrypt

```yaml
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-letsencrypt
  namespace: default
spec:
  version: "16"
  instances: 3
  storage:
    size: 10Gi
  tls:
    mode: CertManager
    certManager:
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
      commonName: documentdb.example.com
      dnsNames:
        - documentdb.example.com
        - "*.documentdb.example.com"
```

### TLS Status and Monitoring

Check TLS status of your DocumentDB instance:

```bash
# Get TLS status
kubectl get documentdb <name> -n <namespace> -o jsonpath='{.status.tls}' | jq

# Example output:
{
  "ready": true,
  "mode": "SelfSigned",
  "certificateName": "documentdb-gateway-cert",
  "secretName": "documentdb-gateway-cert-tls",
  "expirationTime": "2025-02-04T10:00:00Z"
}
```

### Certificate Rotation

The operator handles certificate rotation automatically:

- **SelfSigned & CertManager**: cert-manager rotates certificates before expiration
- **Provided Mode**: Sync certificates from Azure Key Vault on rotation

Monitor certificate expiration:

```bash
# Check certificate expiration
kubectl get certificate -n <namespace> <cert-name> -o jsonpath='{.status.notAfter}'

# Check TLS secret
kubectl get secret -n <namespace> <tls-secret-name> -o jsonpath='{.data.tls\.crt}' | \
  base64 -d | openssl x509 -noout -dates
```

### Troubleshooting TLS

For comprehensive troubleshooting, see the [E2E Testing Guide](../../../../documentdb-playground/tls/E2E-TESTING.md#troubleshooting).

Common issues:

1. **Certificate not ready**: Check cert-manager logs and certificate status
2. **Connection failures**: Verify service endpoints and TLS handshake
3. **Azure Key Vault access denied**: Check managed identity and RBAC permissions

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

Configuration for high availability DocumentDB deployments.

### Multi-Instance Setup

```yaml
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-ha
spec:
  version: "16"
  instances: 3  # Number of replicas
  storage:
    size: 100Gi
    storageClass: premium-ssd
```

### Recommended Settings

- **Minimum instances**: 3 for production
- **Storage class**: Use premium SSDs for production
- **Resource requests**: Set appropriate CPU/memory limits

---

## Storage Configuration

Configure persistent storage for DocumentDB instances.

### Storage Classes

```yaml
spec:
  storage:
    size: 100Gi
    storageClass: premium-ssd  # Azure Premium SSD
    # or: managed-csi-premium
    # or: azurefile-premium
```

### Volume Expansion

```bash
# Ensure storage class allows volume expansion
kubectl get storageclass <storage-class> -o jsonpath='{.allowVolumeExpansion}'

# Patch DocumentDB for larger storage
kubectl patch documentdb <name> -n <namespace> --type='json' \
  -p='[{"op": "replace", "path": "/spec/storage/size", "value":"200Gi"}]'
```

### PersistentVolume Security

The DocumentDB operator automatically applies security-hardening mount options to all PersistentVolumes associated with DocumentDB clusters:

| Mount Option | Description |
|--------------|-------------|
| `nodev` | Prevents device files from being interpreted on the filesystem |
| `nosuid` | Prevents setuid/setgid bits from taking effect |
| `noexec` | Prevents execution of binaries on the filesystem |

These options are automatically applied by the PV controller and require no additional configuration.

### Disk Encryption

Encryption at rest is essential for protecting sensitive database data. Here's how to configure disk encryption for each cloud provider:

#### Azure Kubernetes Service (AKS)

AKS encrypts all managed disks by default using Azure Storage Service Encryption (SSE) with platform-managed keys. No additional configuration is required.

For customer-managed keys (CMK), use Azure Disk Encryption:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: managed-csi-encrypted
provisioner: disk.csi.azure.com
parameters:
  skuName: Premium_LRS
  # For customer-managed keys, specify the disk encryption set
  diskEncryptionSetID: /subscriptions/<sub-id>/resourceGroups/<rg>/providers/Microsoft.Compute/diskEncryptionSets/<des-name>
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
```

#### Google Kubernetes Engine (GKE)

GKE encrypts all persistent disks by default using Google-managed encryption keys. No additional configuration is required.

For customer-managed encryption keys (CMEK):

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: pd-ssd-encrypted
provisioner: pd.csi.storage.gke.io
parameters:
  type: pd-ssd
  # For CMEK, specify the key
  disk-encryption-kms-key: projects/<project>/locations/<region>/keyRings/<keyring>/cryptoKeys/<key>
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
```

#### Amazon Elastic Kubernetes Service (EKS)

**Important**: Unlike AKS and GKE, EBS volumes on EKS are **not encrypted by default**. You must explicitly enable encryption in the StorageClass:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ebs-sc-encrypted
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
  encrypted: "true"  # Required for encryption
  # Optional: specify a KMS key for customer-managed encryption
  # kmsKeyId: arn:aws:kms:<region>:<account-id>:key/<key-id>
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
```

To use the encrypted storage class with DocumentDB:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-cluster
  namespace: default
spec:
  environment: eks
  resource:
    storage:
      pvcSize: 100Gi
      storageClass: ebs-sc-encrypted  # Use the encrypted storage class
  # ... other configuration
```

### Encryption Summary

| Provider | Default Encryption | Customer-Managed Keys |
|----------|-------------------|----------------------|
| AKS | ✅ Enabled (SSE) | Optional via DiskEncryptionSet |
| GKE | ✅ Enabled (Google-managed) | Optional via CMEK |
| EKS | ❌ **Not enabled** | Required: set `encrypted: "true"` in StorageClass |

**Recommendation**: For production deployments on EKS, always create a StorageClass with `encrypted: "true"` to ensure data at rest is protected.

---

## Scheduling

Configure pod affinity for a documentdb cluster's database pods. This replicates
the cnpg operator's scheduling framework. See <https://cloudnative-pg.io/docs/1.28/scheduling/>

```yaml
spec:
  affinity:
  ...
```

## Resource Management

Configure resource requests and limits for optimal performance.

### Example Configuration

```yaml
apiVersion: documentdb.io/v1
kind: DocumentDB
metadata:
  name: documentdb-resources
spec:
  version: "16"
  instances: 3
  resources:
    limits:
      cpu: "4"
      memory: "8Gi"
    requests:
      cpu: "2"
      memory: "4Gi"
```

### Recommendations

- **Development**: 1 CPU, 2Gi memory
- **Production**: 2-4 CPUs, 4-8Gi memory
- **High-load**: 4-8 CPUs, 8-16Gi memory

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

Credentials are automatically stored in Kubernetes secrets:

```bash
# View credentials (base64 encoded)
kubectl get secret documentdb-credentials -n <namespace> -o yaml

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
- External secrets operator

---

## Additional Resources

- [Main Documentation](https://microsoft.github.io/documentdb-kubernetes-operator)
- [TLS Setup Guide](../../../../documentdb-playground/tls/README.md)
- [E2E Testing Guide](../../../../documentdb-playground/tls/E2E-TESTING.md)
- [GitHub Repository](https://github.com/microsoft/documentdb-kubernetes-operator)

---

**Last Updated**: November 2025  
**Version**: v1
