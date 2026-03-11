---
title: Secrets Management
description: Managing credentials and secrets for DocumentDB Kubernetes Operator including auto-generated credentials, custom secrets, external secrets integration, and rotation procedures.
tags:
  - security
  - secrets
  - credentials
---

# Secrets Management

This guide covers managing credentials and secrets for DocumentDB deployments, including credential creation, external secrets integration, and rotation procedures.

## Overview

DocumentDB requires credentials stored in Kubernetes Secrets:

- **Database credentials** - Username and password for client authentication
- **TLS certificates** - Certificates for encrypted connections (managed separately, see [TLS Configuration](../advanced-configuration/README.md#tls-configuration))

```mermaid
flowchart LR
    subgraph Secrets["Kubernetes Secrets"]
        Creds[documentdb-credentials]
        TLS[TLS Certificates]
    end
    
    subgraph External["External Systems (Optional)"]
        KV[Azure Key Vault]
        Vault[HashiCorp Vault]
        SM[AWS Secrets Manager]
    end
    
    External -->|sync| Secrets
    Secrets --> DB[(DocumentDB)]
```

## Creating Credential Secrets

### Basic Secret Creation

Create a secret with username and password:

```bash
kubectl create secret generic documentdb-credentials \
    --namespace documentdb-instance-ns \
    --from-literal=username=docdbadmin \
    --from-literal=password='YourSecurePassword123!'
```

Or using a YAML manifest:

```yaml title="documentdb-credentials.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-instance-ns
type: Opaque
stringData:
  username: docdbadmin
  password: YourSecurePassword123!  # (1)!
```

1. Use `stringData` for plain text values. Kubernetes will base64-encode them automatically.

### Reference in DocumentDB Spec

```yaml title="documentdb-with-credentials.yaml"
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
  namespace: documentdb-instance-ns
spec:
  nodeCount: 1
  instancesPerNode: 3
  documentDbCredentialSecret: documentdb-credentials  # (1)!
```

1. The secret must exist in the same namespace as the DocumentDB resource.

### Secret Requirements

The credential secret must contain these keys:

| Key | Required | Description |
|-----|----------|-------------|
| `username` | Yes | Database username for authentication |
| `password` | Yes | Database password |

### Password Requirements

For secure passwords:

- Minimum 12 characters
- Mix of uppercase, lowercase, numbers, and special characters
- Avoid dictionary words
- Don't reuse passwords across environments

Generate a secure password:

```bash
# Using openssl
openssl rand -base64 24

# Using pwgen (if installed)
pwgen -s 24 1
```

## Retrieving Credentials

### View Secret Contents

```bash
# Decode username
kubectl get secret documentdb-credentials -n documentdb-instance-ns \
    -o jsonpath='{.data.username}' | base64 -d && echo

# Decode password
kubectl get secret documentdb-credentials -n documentdb-instance-ns \
    -o jsonpath='{.data.password}' | base64 -d && echo
```

### Build Connection String

```bash
# Get credentials and build connection string
NAMESPACE="documentdb-instance-ns"
SECRET_NAME="documentdb-credentials"
DB_NAME="my-documentdb"

USERNAME=$(kubectl get secret $SECRET_NAME -n $NAMESPACE -o jsonpath='{.data.username}' | base64 -d)
PASSWORD=$(kubectl get secret $SECRET_NAME -n $NAMESPACE -o jsonpath='{.data.password}' | base64 -d)
HOST=$(kubectl get svc documentdb-service-$DB_NAME -n $NAMESPACE -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

echo "mongodb://${USERNAME}:${PASSWORD}@${HOST}:10260/"
```

## External Secrets Integration

For production environments, integrate with external secret management systems.

### Azure Key Vault

Use the [External Secrets Operator](https://external-secrets.io/) to sync secrets from Azure Key Vault.

**1. Install External Secrets Operator:**

```bash
helm repo add external-secrets https://charts.external-secrets.io
helm install external-secrets external-secrets/external-secrets \
    -n external-secrets --create-namespace
```

**2. Create SecretStore for Key Vault:**

```yaml title="azure-secret-store.yaml"
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: azure-keyvault
  namespace: documentdb-instance-ns
spec:
  provider:
    azurekv:
      tenantId: "<tenant-id>"
      vaultUrl: "https://my-keyvault.vault.azure.net"
      authType: WorkloadIdentity
      serviceAccountRef:
        name: external-secrets-sa
```

**3. Create ExternalSecret:**

```yaml title="external-secret-keyvault.yaml"
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: documentdb-credentials
  namespace: documentdb-instance-ns
spec:
  refreshInterval: 1h  # (1)!
  secretStoreRef:
    name: azure-keyvault
    kind: SecretStore
  target:
    name: documentdb-credentials
    creationPolicy: Owner
  data:
    - secretKey: username
      remoteRef:
        key: documentdb-username  # Key Vault secret name
    - secretKey: password
      remoteRef:
        key: documentdb-password  # Key Vault secret name
```

1. How often to sync from Key Vault. Balance security (frequent refresh) with API limits.

### HashiCorp Vault

**1. Configure SecretStore for Vault:**

```yaml title="vault-secret-store.yaml"
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: hashicorp-vault
  namespace: documentdb-instance-ns
spec:
  provider:
    vault:
      server: "https://vault.example.com"
      path: "secret"
      version: "v2"
      auth:
        kubernetes:
          mountPath: "kubernetes"
          role: "documentdb-role"
          serviceAccountRef:
            name: external-secrets-sa
```

**2. Create ExternalSecret:**

```yaml title="external-secret-vault.yaml"
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: documentdb-credentials
  namespace: documentdb-instance-ns
spec:
  refreshInterval: 30m
  secretStoreRef:
    name: hashicorp-vault
    kind: SecretStore
  target:
    name: documentdb-credentials
  data:
    - secretKey: username
      remoteRef:
        key: documentdb/credentials
        property: username
    - secretKey: password
      remoteRef:
        key: documentdb/credentials
        property: password
```

### AWS Secrets Manager

**1. Configure SecretStore for AWS Secrets Manager:**

```yaml title="aws-secret-store.yaml"
apiVersion: external-secrets.io/v1beta1
kind: SecretStore
metadata:
  name: aws-secrets-manager
  namespace: documentdb-instance-ns
spec:
  provider:
    aws:
      service: SecretsManager
      region: us-west-2
      auth:
        jwt:
          serviceAccountRef:
            name: external-secrets-sa
```

**2. Create ExternalSecret:**

```yaml title="external-secret-aws.yaml"
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: documentdb-credentials
  namespace: documentdb-instance-ns
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secrets-manager
    kind: SecretStore
  target:
    name: documentdb-credentials
  data:
    - secretKey: username
      remoteRef:
        key: documentdb/credentials
        property: username
    - secretKey: password
      remoteRef:
        key: documentdb/credentials
        property: password
```

## Secret Rotation

### Manual Rotation Procedure

!!! warning "Application Impact"
    Rotating credentials requires updating all connected applications. Plan rotation during maintenance windows.

**Step 1: Create new secret with updated credentials:**

```bash
kubectl create secret generic documentdb-credentials-new \
    --namespace documentdb-instance-ns \
    --from-literal=username=docdbadmin \
    --from-literal=password='NewSecurePassword456!'
```

**Step 2: Update DocumentDB to use new secret:**

```bash
kubectl patch documentdb my-documentdb -n documentdb-instance-ns \
    --type='json' \
    -p='[{"op": "replace", "path": "/spec/documentDbCredentialSecret", "value": "documentdb-credentials-new"}]'
```

**Step 3: Update application connection strings**

Update all applications to use the new credentials.

**Step 4: Verify connectivity:**

```bash
# Test connection with new credentials
kubectl exec -it app-pod -n app-namespace -- \
    mongosh "mongodb://docdbadmin:NewSecurePassword456!@my-documentdb:10260/"
```

**Step 5: Delete old secret:**

```bash
kubectl delete secret documentdb-credentials -n documentdb-instance-ns
```

### Automated Rotation with External Secrets

When using External Secrets Operator, rotation is handled by the external system:

1. Update the credential in Key Vault/Vault/Secrets Manager
2. External Secrets Operator syncs the new value (based on `refreshInterval`)
3. Pods picking up the secret will use new credentials on restart

!!! note "Pod Restart"
    Most applications cache credentials at startup. You may need to restart pods to pick up rotated credentials:
    ```bash
    kubectl rollout restart deployment/my-app -n app-namespace
    ```

## Secrets Encryption at Rest

### Enable Kubernetes Secrets Encryption

Ensure secrets are encrypted in etcd:

=== "AKS"

    ```bash
    # Secrets encryption is enabled by default in AKS
    # Verify with:
    az aks show --resource-group <rg> --name <cluster> \
        --query "securityProfile.azureKeyVaultKms"
    ```

=== "EKS"

    ```bash
    # Enable envelope encryption with KMS
    aws eks update-cluster-config \
        --name my-cluster \
        --encryption-config '[{
            "provider": {"keyArn": "arn:aws:kms:us-west-2:111122223333:key/xxx"},
            "resources": ["secrets"]
        }]'
    ```

=== "GKE"

    ```bash
    # Enable application-layer secrets encryption
    gcloud container clusters update my-cluster \
        --database-encryption-key=projects/my-project/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key
    ```

## Security Best Practices

### Do

- ✅ Use external secrets management for production
- ✅ Enable secrets encryption at rest
- ✅ Use strong, unique passwords per environment
- ✅ Rotate credentials regularly (90 days recommended)
- ✅ Use RBAC to restrict secret access
- ✅ Audit secret access with Kubernetes audit logging

### Don't

- ❌ Store credentials in ConfigMaps
- ❌ Commit secrets to version control
- ❌ Use the same credentials across environments
- ❌ Share credentials between applications
- ❌ Log or print credentials in application code

### Restricting Secret Access

Limit who can read credentials:

```yaml title="secret-reader-role.yaml"
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: documentdb-secret-reader
  namespace: documentdb-instance-ns
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: ["documentdb-credentials"]  # Only this secret
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: app-secret-reader
  namespace: documentdb-instance-ns
subjects:
  - kind: ServiceAccount
    name: my-app-sa
    namespace: app-namespace
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: documentdb-secret-reader
```

## Troubleshooting

### Secret Not Found

```bash
# Verify secret exists
kubectl get secret documentdb-credentials -n documentdb-instance-ns

# Check DocumentDB events for errors
kubectl describe documentdb my-documentdb -n documentdb-instance-ns | grep -A5 Events
```

### Authentication Failed

```bash
# Verify credentials in secret
kubectl get secret documentdb-credentials -n documentdb-instance-ns \
    -o jsonpath='{.data.username}' | base64 -d

# Check if password has special characters that need escaping
kubectl get secret documentdb-credentials -n documentdb-instance-ns \
    -o jsonpath='{.data.password}' | base64 -d
```

### External Secrets Not Syncing

```bash
# Check ExternalSecret status
kubectl get externalsecret documentdb-credentials -n documentdb-instance-ns

# Check External Secrets Operator logs
kubectl logs -n external-secrets -l app.kubernetes.io/name=external-secrets

# Describe for detailed errors
kubectl describe externalsecret documentdb-credentials -n documentdb-instance-ns
```

## Next Steps

- [RBAC Configuration](rbac.md) - Configure role-based access control
- [Network Policies](network-policies.md) - Restrict network access
- [TLS Configuration](../advanced-configuration/README.md#tls-configuration) - Configure TLS certificates
- [Security Overview](overview.md) - Complete security model
