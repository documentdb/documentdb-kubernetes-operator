---
title: Deploy on Google Kubernetes Engine
description: Complete guide for deploying the DocumentDB Kubernetes Operator on Google Kubernetes Engine (GKE)
tags:
  - gke
  - google-cloud
  - deployment
---

Learn how to deploy the DocumentDB Kubernetes Operator on GKE.

## Understanding the configuration

### GKE load balancer annotation

When using GKE, set the `DocumentDB` `spec.environment` field to `gke`.
Supported values are `aks`, `eks`, and `gke`. If you omit this field, the
operator doesn't apply cloud-specific service annotations. For field details,
see the [API reference](../api-reference.md).

```yaml
spec:
  environment: "gke"
```

When `spec.environment: "gke"` is set, the operator adds Google Cloud-specific
service annotations:

```yaml
annotations:
  cloud.google.com/load-balancer-type: "External"
```

This annotation tells GKE to provision an external Google Cloud load balancer
for the gateway Service so that it's reachable from outside the Kubernetes
cluster. For details on the underlying behavior and additional annotations you
can layer on top, see
[GKE Service parameters](https://cloud.google.com/kubernetes-engine/docs/concepts/service-parameters)
and
[Configure load balancing](https://cloud.google.com/kubernetes-engine/docs/concepts/service-load-balancer).

### Storage class

GKE provisions the
[Compute Engine persistent disk CSI driver](https://cloud.google.com/kubernetes-engine/docs/concepts/persistent-volumes#pd_csi_driver)
by default and ships several built-in storage classes. The default class is
`standard-rwo`, which is backed by balanced persistent disks. For production
workloads, use a class that matches your performance and availability
requirements, such as `premium-rwo`.

```yaml
spec:
  resource:
    storage:
      storageClass: premium-rwo
```

| Storage class  | Disk type     | Use case                                       |
| -------------- | ------------- | ---------------------------------------------- |
| `standard-rwo` | `pd-balanced` | General purpose, balanced cost and performance |
| `premium-rwo`  | `pd-ssd`      | Production workloads requiring SSD performance |

For available classes, see:

- [GKE storage classes](https://cloud.google.com/kubernetes-engine/docs/concepts/storage-overview#storage_classes)
- [Compute Engine persistent disk CSI driver](https://cloud.google.com/kubernetes-engine/docs/concepts/persistent-volumes#pd_csi_driver)
- [DocumentDB storage configuration](../configuration/storage.md)

## Monitoring and troubleshooting

### Common issues

If the Service stays in `Pending`, verify the GKE network configuration and
load balancer setup:

```bash
kubectl get svc -n documentdb-instance-ns
kubectl describe svc documentdb-service-sample-documentdb -n documentdb-instance-ns
gcloud compute forwarding-rules list --project PROJECT_ID
```

If PVCs don't bind, verify your storage classes and that the Compute Engine
persistent disk CSI driver pods are healthy:

```bash
kubectl get storageclass
kubectl get pvc -A
kubectl describe pvc PVC_NAME -n NAMESPACE
kubectl get pods -n kube-system -l k8s-app=gcp-compute-persistent-disk-csi-driver
```

## Cost and security considerations

### Cost optimization

- Use smaller machine types for development workloads, such as `e2-small`
- Use standard storage classes where SSD performance isn't required
- Scale node pools down in non-production environments
- Review [GKE pricing](https://cloud.google.com/kubernetes-engine/pricing) for
  current rates

### Security baseline

- [Workload Identity](https://cloud.google.com/kubernetes-engine/docs/concepts/workload-identity)
  for Google Cloud API access
- [Network policies](https://cloud.google.com/kubernetes-engine/docs/how-to/network-policy)
  enabled
- [Encryption at rest](https://cloud.google.com/kubernetes-engine/docs/how-to/using-cmek)
  for persistent disks
- [TLS configuration](../configuration/tls.md) for database traffic
- [Least-privilege IAM service accounts](https://cloud.google.com/iam/docs/best-practices-service-accounts)

### Hardening examples

```bash
# Enable network policy enforcement
gcloud container clusters update CLUSTER_NAME \
  --location REGION_OR_ZONE \
  --enable-network-policy

# Enable Workload Identity
gcloud container clusters update CLUSTER_NAME \
  --location REGION_OR_ZONE \
  --workload-pool=PROJECT_ID.svc.id.goog
```

## Additional resources

- [GKE documentation](https://cloud.google.com/kubernetes-engine/docs)
- [GKE security best practices](https://cloud.google.com/kubernetes-engine/docs/concepts/security-overview)
- [GKE networking](https://cloud.google.com/kubernetes-engine/docs/concepts/network-overview)
- [GKE persistent storage](https://cloud.google.com/kubernetes-engine/docs/concepts/persistent-volumes)
- [DocumentDB preview getting started](../index.md)
