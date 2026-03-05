# Deploy on Azure Kubernetes Service (AKS)

This guide covers the general AKS deployment model for the DocumentDB Kubernetes
Operator.

## Quick Start

For automated deployment, use the playground scripts:

```bash
cd documentdb-playground/aks-setup/scripts
./create-cluster.sh --deploy-instance
```

For complete automation details, see the
[AKS setup README](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/aks-setup).

## Understanding the Configuration

### Azure LoadBalancer Annotations

When using AKS, set the `DocumentDB` `spec.environment` field to `aks`.

```yaml
spec:
  environment: "aks"
```

The operator adds Azure-specific Service annotations when the cluster type is
AKS:

```yaml
annotations:
  service.beta.kubernetes.io/azure-load-balancer-external: "true"
```

This instructs kubernetes to assign the Load Balancer an IP which can be accessed
from outside the cluster.

### Storage Class

AKS uses the built-in `managed-csi` storage class by default
(`StandardSSD_LRS`). For production workloads, use a Premium SSD class such as
`managed-csi-premium`.

```yaml
spec:
  resource:
    storage:
      storageClass: managed-csi-premium
```

For available classes, see
<https://learn.microsoft.com/azure/aks/concepts-storage#storage-classes>.

## Monitoring and Troubleshooting

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

### Hardening Examples

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

- [AKS documentation](https://learn.microsoft.com/azure/aks/)
- [Azure CNI networking](https://learn.microsoft.com/azure/aks/configure-azure-cni)
- [Azure Load Balancer](https://learn.microsoft.com/azure/load-balancer/)
