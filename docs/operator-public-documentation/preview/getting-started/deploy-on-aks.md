---
title: Deploy on Azure Kubernetes Service
description: Complete guide for deploying the DocumentDB Kubernetes Operator on Azure Kubernetes Service (AKS)
tags:
  - aks
  - azure
  - deployment
---

Learn how to deploy the DocumentDB Kubernetes Operator on AKS.

## Quick start

Before you begin, make sure you have:

- [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli) installed
- Logged in with `az login`

For automated deployment, use the playground scripts:

```bash
cd documentdb-playground/aks-setup/scripts
./create-cluster.sh --deploy-instance
```

For complete automation details, see the
[AKS setup README](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/aks-setup).

## Understanding the configuration

### Azure load balancer annotations

When using AKS, set the `DocumentDB` `spec.environment` field to `aks`.
Supported values are `aks`, `eks`, and `gke`. If you omit this field, the
operator does not apply cloud-specific service annotations. For field details,
see the [API reference](https://documentdb.io/documentdb-kubernetes-operator/latest/preview/api-reference/).

```yaml
spec:
  environment: "aks"
```

When `spec.environment: "aks"` is set, the operator adds Azure-specific service
annotations:

```yaml
annotations:
  service.beta.kubernetes.io/azure-load-balancer-external: "true"
```

The `service.beta.kubernetes.io/azure-load-balancer-external` annotation is set
by the operator for AKS deployments. It is not a generic Kubernetes annotation.
This setting helps AKS provision an external load balancer with an IP address
that can be reached outside the cluster. For AKS behavior and supported service
[Use a standard public load balancer in AKS][aks-standard-public-lb] and
[AKS load balancer annotations](https://learn.microsoft.com/azure/aks/load-balancer-standard#additional-customizations-via-kubernetes-annotations).

### Storage class

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

- [AKS storage classes](https://learn.microsoft.com/azure/aks/concepts-storage#storage-classes)
- [Azure Disk CSI driver on AKS](https://learn.microsoft.com/azure/aks/azure-disk-csi)
- [DocumentDB storage configuration](../advanced-configuration/README.md#storage-configuration)

## Monitoring and troubleshooting

### Common issues

If the service remains in `Pending`, verify AKS network profile and load balancer
configuration:

```bash
az aks show --resource-group RESOURCE_GROUP --name CLUSTER_NAME --query networkProfile
```

If PVCs do not bind, verify your storage classes and that Azure Disk CSI driver
pods are healthy:

```bash
kubectl get storageclass
kubectl get pods -n kube-system | grep csi-azuredisk
```

## Cost and security considerations

### Cost optimization

- Use smaller virtual machine (VM) sizes for development, such as `Standard_B2s`
- Reduce node count in non-production environments
- Use Standard SSD where Premium SSD is not required
- Review [AKS pricing](https://azure.microsoft.com/pricing/details/kubernetes-service/)
  for current rates

### Security baseline

- [Managed identity](https://learn.microsoft.com/azure/aks/use-managed-identity)
  for Azure resource access
- [Network policies](https://learn.microsoft.com/azure/aks/use-network-policies)
  enabled
- [Encryption at rest](https://learn.microsoft.com/azure/aks/enable-host-encryption)
  on managed disks
- [TLS configuration](../advanced-configuration/README.md#tls-configuration)
  for database traffic
- [Azure RBAC integration](https://learn.microsoft.com/azure/aks/manage-azure-rbac)

### Hardening examples

Use AKS add-ons to enforce policy and integrate external secret sources. Learn
more about
[Azure Policy for Kubernetes][aks-policy-for-kubernetes] and the
[Key Vault Secrets Store CSI Driver](https://learn.microsoft.com/azure/aks/csi-secrets-store-driver).

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

## Additional resources

- [AKS documentation](https://learn.microsoft.com/azure/aks/)
- [AKS best practices](https://learn.microsoft.com/azure/aks/best-practices)
- [AKS cluster security best practices](https://learn.microsoft.com/azure/aks/operator-best-practices-cluster-security)
- [Azure CNI networking](https://learn.microsoft.com/azure/aks/configure-azure-cni)
- [Azure load balancer](https://learn.microsoft.com/azure/load-balancer/)
- [DocumentDB preview getting started](../index.md)

[aks-standard-public-lb]: https://learn.microsoft.com/azure/aks/load-balancer-standard
[aks-policy-for-kubernetes]: https://learn.microsoft.com/azure/governance/policy/concepts/policy-for-kubernetes
