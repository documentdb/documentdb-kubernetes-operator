# Deploy on Azure Kubernetes Service (AKS)

This guide covers the general AKS deployment model for DocumentDB Kubernetes Operator.

## Quick Start

For automated deployment, use the playground scripts:

```bash
cd documentdb-playground/aks-setup/scripts
./create-cluster.sh --deploy-instance
```

See [AKS Setup Scripts](/documentdb-playground/aks-setup/README.md) for options.

## Understanding the Configuration

### Azure LoadBalancer Annotations

When using AKS, you'll want to specify in the documentDB CRD that "aks" is the
environment in the spec as below

```yaml
spec:
    environment: "aks"
```

The operator adds Azure-specific Service annotations when the cluster type is AKS:

```yaml
annotations:
  service.beta.kubernetes.io/azure-load-balancer-internal: "true"
```

This instructs kubernetes to assign the Load Balancer an IP which can be accessed
from outside the cluster.

### Storage Class

AKS uses the built-in `managed-csi` storage class by default (`StandardSSD_LRS`).
For production workloads, you can use a Premium SSD storage class such as `managed-csi-premium`

```yaml
spec:
    resource:
        storage:
            storageClass: managed-csi-premium
```

Other classes can be viewed at
<https://learn.microsoft.com/en-us/azure/aks/concepts-storage#storage-classes>

## Monitoring and Troubleshooting

### Validate Cluster and Workloads

```bash
kubectl get nodes
kubectl get pods --all-namespaces
kubectl get documentdb -A
kubectl get pvc -A
kubectl get svc -A -w
```

### Access DocumentDB

```bash
# Get external IP
kubectl get svc documentdb-service-sample-documentdb -n documentdb-instance-ns

# Get credentials
kubectl get secret documentdb-credentials \
  -n documentdb-instance-ns \
  -o jsonpath='{.data.username}' | base64 -d
kubectl get secret documentdb-credentials \
  -n documentdb-instance-ns \
  -o jsonpath='{.data.password}' | base64 -d
```

Connection string format:

```text
mongodb://username:password@EXTERNAL-IP:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true
```

### Common Issues

LoadBalancer pending:

```bash
az aks show --resource-group RESOURCE_GROUP --name CLUSTER_NAME --query networkProfile
```

PVC binding failures:

```bash
kubectl get storageclass
kubectl get pods -n kube-system | grep csi-azuredisk
```

Operator startup issues:

```bash
kubectl logs -n documentdb-operator deployment/documentdb-operator
```

## Cost and Security Considerations

### Cost Optimization

- Use smaller VM SKUs for development (for example `Standard_B2s`)
- Reduce node count in non-production environments
- Use Standard SSD where Premium SSD is not required

### Security Baseline

- Managed identity for Azure resource access
- Network policies enabled
- Encryption at rest on managed disks
- TLS for database traffic
- Azure RBAC integration

### Hardening examples

```bash
az aks enable-addons \
  --resource-group RESOURCE_GROUP \
  --name CLUSTER_NAME \
  --addons azure-policy

az aks enable-addons \
  --resource-group RESOURCE_GROUP \
  --name CLUSTER_NAME \
  --addons azure-keyvault-secrets-provider
```

## Additional Resources

- [AKS Documentation](https://docs.microsoft.com/en-us/azure/aks/)
- [Azure CNI Networking](https://docs.microsoft.com/en-us/azure/aks/configure-azure-cni)
- [Azure Load Balancer](https://docs.microsoft.com/en-us/azure/load-balancer/)
