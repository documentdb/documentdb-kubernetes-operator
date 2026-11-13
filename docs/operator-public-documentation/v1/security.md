---
title: Security
description: Security defaults, operator permissions, and a hardening checklist for DocumentDB clusters managed by the DocumentDB Kubernetes Operator.
tags:
  - security
  - tls
  - rbac
---

# Security

This page summarizes what the operator secures for you, what it needs access to, and what you should configure for production. Kubernetes cluster security (API server access, etcd encryption, node hardening) remains your responsibility.

## Secure by default

| Area | Default behavior |
|------|------------------|
| **Client encryption** | The gateway always uses TLS; it cannot be turned off. `spec.tls.gateway.mode` defaults to `SelfSigned`, a cert-manager certificate that is renewed automatically. See [TLS](configuration/tls.md). |
| **Client authentication** | Username and password over SCRAM-SHA-256. Credentials come from the Secret in `spec.documentDbCredentialSecret` (keys `username` and `password`) and reach the gateway as Secret-referenced environment variables, not command-line arguments. |
| **Database pods** | PostgreSQL containers use CloudNativePG's security context: non-root, read-only root filesystem, no privilege escalation, all capabilities dropped, `RuntimeDefault` seccomp. |
| **Gateway and OTel sidecars** | Non-root, no privilege escalation, all capabilities dropped, `RuntimeDefault` seccomp. |
| **Operator and plugins** | Non-root, no privilege escalation, all capabilities dropped, `RuntimeDefault` seccomp. |
| **Internal traffic** | CloudNativePG reaches the sidecar-injector and WAL-replica plugins over gRPC with mutual TLS. The validating webhook serves a cert-manager certificate. |
| **Monitoring** | When monitoring is enabled, the OTel Collector logs in as a dedicated role that is not a superuser and has no extra role memberships. |
| **Images** | Operator, sidecar, DocumentDB, gateway and combined images are signed with [cosign](https://docs.sigstore.dev/) keyless signing from this repository's GitHub Actions workflows. |

To verify an image signature (replace `operator:<version>` with any of the images above):

```bash
cosign verify \
  --certificate-identity-regexp '^https://github.com/documentdb/documentdb-kubernetes-operator/\.github/workflows/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/documentdb/documentdb-kubernetes-operator/operator:<version>
```

## What the operator can access

The operator runs with a **cluster-wide** `ClusterRole`; it can't be limited to specific namespaces. Notable permissions:

| Permission | Why |
|------------|-----|
| `secrets`: `get`, `list`, `watch` (all namespaces) | Read credential and TLS Secrets. The operator never writes Secrets. |
| `pods/exec`: `create` | Run `psql` in the primary pod to upgrade the DocumentDB extension and grant the replication role. |
| `serviceaccounts`, `roles`, `rolebindings` | Create a ServiceAccount, Role and RoleBinding for each DocumentDB cluster. |
| `nodes`: `get`, `list` | Detect the container runtime to choose the image mode. |
| `persistentvolumes`: `update`, `patch` | Apply the reclaim policy and record schema-version annotations on volumes. |

Treat the operator namespace (`documentdb-operator`) and the CloudNativePG namespace (`cnpg-system`) as privileged: limit who can create pods, read Secrets or exec in them.

## Hardening checklist

- [ ] **Use a trusted certificate.** In production, set `spec.tls.gateway.mode` to `CertManager` or `Provided` and have clients verify the server certificate instead of passing `tlsAllowInvalidCertificates=true`. See [TLS](configuration/tls.md).
- [ ] **Encrypt replication across clusters.** For multi-region deployments, set all four `spec.tls.postgres` Secrets (`replicationTLSSecret`, `clientCASecret`, `serverTLSSecret` and `serverCASecret`) so cross-cluster replication verifies the server with `sslmode=verify-full`. See [Multi-region setup](multi-region-deployment/setup.md) and [PostgreSQL certificates](configuration/tls.md#postgresql-certificates).
- [ ] **Limit network exposure.** Prefer `serviceType: ClusterIP`. A `LoadBalancer` Service may be reachable from outside the cluster; restrict it with your cloud provider's firewall or load balancer settings. The operator doesn't create NetworkPolicies; see [Network Policies](configuration/networking.md#network-policies) for the traffic to allow.
- [ ] **Protect credentials.** Use a strong password, enable [encryption at rest for Secrets](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/), and restrict `get`/`list` on Secrets in DocumentDB namespaces. Consider syncing the credential Secret from an external secret store. The Secret name can't be changed after the cluster is created.
- [ ] **Encrypt storage.** Encryption at rest and backup encryption come from your StorageClass and CSI driver; backups are VolumeSnapshots of the same volumes. Use an encrypted StorageClass and VolumeSnapshotClass.
- [ ] **Set resource limits.** The operator and plugin Deployments set CPU and memory requests only. Add limits with the Helm values `operator.resources`, `sidecarInjector.resources` and `walReplicaPlugin.resources` if your policies require them.
- [ ] **Enforce Pod Security Admission.** The chart doesn't label namespaces. Add `pod-security.kubernetes.io/warn: restricted` to DocumentDB namespaces first to find violations, then enforce the level your policy requires.
- [ ] **Enable io_uring only when needed.** The opt-in `IOUring` feature gate switches PostgreSQL pods to a `Localhost` seccomp profile that also allows `io_uring_setup`, `io_uring_enter` and `io_uring_register`. See [io_uring](../io-uring.md).
- [ ] **Keep up to date.** Apply operator patch releases and keep cert-manager on a supported, patched version.

## Current limitations

- No certificate-based (mutual TLS) client authentication; clients authenticate with username and password.
- The operator doesn't configure a minimum TLS version or cipher suites for the gateway.
- No database audit logging. Use Kubernetes audit logs and the events the operator records on DocumentDB resources.
- Helm image values accept tags, not digests.
- Release images are signed but don't include SBOMs or provenance attestations.

## Reporting a vulnerability

Don't report security vulnerabilities in public GitHub issues. Follow the private reporting process in [SECURITY.md](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/SECURITY.md).
