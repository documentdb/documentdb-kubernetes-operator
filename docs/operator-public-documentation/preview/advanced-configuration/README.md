# Advanced Configuration

This section covers advanced configuration options for the DocumentDB Kubernetes Operator.

## Table of Contents

- [High Availability](#high-availability)
- [Scheduling](#scheduling)
- [Security](#security)

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

## Scheduling

Configure pod affinity for a documentdb cluster's database pods. This replicates
the cnpg operator's scheduling framework. See <https://cloudnative-pg.io/docs/1.28/scheduling/>

```yaml
spec:
  affinity:
  ...
```

## Security

Security best practices for DocumentDB deployments.

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

- [Networking](../configuration/networking.md) — Service types, connection methods, and Network Policies
- [TLS](../configuration/tls.md) — Certificate modes, rotation, and troubleshooting
- [Storage](../configuration/storage.md) — PVC sizing, storage classes, and disk encryption
- [GitHub Repository](https://github.com/documentdb/documentdb-kubernetes-operator)
