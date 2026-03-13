---
title: TLS Configuration
description: Configure TLS encryption for DocumentDB gateway connections with SelfSigned, Provided, and CertManager modes, certificate rotation, and troubleshooting.
tags:
  - configuration
  - tls
  - security
---

# TLS Configuration

## Overview

TLS encrypts connections between your applications and DocumentDB. Configure it to protect data in transit and meet your security requirements.

The DocumentDB gateway always encrypts connections — TLS is active regardless of the mode you choose. The `spec.tls.gateway.mode` field controls how the operator manages TLS certificates:

```yaml
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: my-documentdb
spec:
  tls:
    gateway:
      mode: SelfSigned   # Disabled | SelfSigned | CertManager | Provided
```

For the full field reference, see [TLSConfiguration](../api-reference.md#tlsconfiguration) in the API Reference.

## Configuration

Select your TLS mode below. Each tab shows prerequisites, the complete YAML configuration, and connection instructions.

=== "Disabled"

    **Best for:** Development and testing only

    !!! danger "Not recommended for production"

    **Prerequisites:** None

    Disabled mode means the operator does not manage TLS certificates. However, the gateway still encrypts all connections using an internally generated self-signed certificate. Clients must connect with `tls=true&tlsAllowInvalidCertificates=true`.

    ```yaml title="documentdb-tls-disabled.yaml"
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
          pvcSize: 10Gi
      exposeViaService:
        serviceType: ClusterIP
      tls:
        gateway:
          mode: Disabled
    ```

    **Connect with mongosh:**

    ```bash
    mongosh "mongodb://<username>:<password>@<host>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
    ```

=== "SelfSigned"

    **Best for:** Development, testing, and environments without external PKI (Public Key Infrastructure)

    !!! note "Prerequisites"
        [cert-manager](https://cert-manager.io/) must be installed in the Kubernetes cluster. See [Install cert-manager](../index.md#install-cert-manager) for setup instructions.

    SelfSigned mode uses cert-manager to automatically generate, manage, and rotate a self-signed server certificate (90-day validity, renewed 15 days before expiry). No additional configuration is needed beyond setting the mode.

    ```yaml title="documentdb-tls-selfsigned.yaml"
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-documentdb
      namespace: default
    spec:
      nodeCount: 1
      instancesPerNode: 3
      resource:
        storage:
          pvcSize: 10Gi
      exposeViaService:
        serviceType: ClusterIP
      tls:
        gateway:
          mode: SelfSigned
    ```

    **Connect with mongosh:**

    ```bash
    # Extract the CA certificate from the Secret
    kubectl get secret my-documentdb-gateway-cert-tls -n default \
      -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt

    # Connect with TLS
    mongosh "mongodb://<username>:<password>@<host>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256" \
      --tls --tlsCAFile ca.crt
    ```

=== "CertManager"

    **Best for:** Production with existing cert-manager infrastructure

    !!! note "Prerequisites"
        [cert-manager](https://cert-manager.io/) must be installed (see [Install cert-manager](../index.md#install-cert-manager)), plus a configured [Issuer or ClusterIssuer](https://cert-manager.io/docs/concepts/issuer/).

        ??? example "Setting up a CA Issuer with cert-manager"

            If you don't already have an Issuer, you can bootstrap a simple CA Issuer:

            ```yaml title="cert-manager-ca-issuer.yaml"
            # Step 1: A self-signed issuer to bootstrap the CA certificate
            apiVersion: cert-manager.io/v1
            kind: Issuer
            metadata:
              name: selfsigned-bootstrap
            spec:
              selfSigned: {}
            ---
            # Step 2: A CA certificate issued by the bootstrap issuer
            apiVersion: cert-manager.io/v1
            kind: Certificate
            metadata:
              name: my-ca
            spec:
              isCA: true
              commonName: my-documentdb-ca
              secretName: my-ca-secret
              duration: 8760h   # 1 year
              issuerRef:
                name: selfsigned-bootstrap
                kind: Issuer
            ---
            # Step 3: A CA issuer that signs certificates using the CA certificate
            apiVersion: cert-manager.io/v1
            kind: Issuer
            metadata:
              name: my-ca-issuer
            spec:
              ca:
                secretName: my-ca-secret
            ```

    CertManager mode lets you use your own cert-manager [Issuer](https://cert-manager.io/docs/concepts/issuer/#namespaces) (namespace-scoped) or [ClusterIssuer](https://cert-manager.io/docs/concepts/issuer/) (cluster-scoped) to issue TLS certificates for the DocumentDB gateway. This is ideal for production environments that already have PKI infrastructure (for example, a corporate CA).

    Set `issuerRef.name` and `issuerRef.kind` to match your Issuer or ClusterIssuer. The operator will automatically request a certificate and mount it in the gateway.

    ```yaml title="documentdb-tls-certmanager.yaml"
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-documentdb
      namespace: default
    spec:
      nodeCount: 1
      instancesPerNode: 3
      resource:
        storage:
          pvcSize: 100Gi
      exposeViaService:
        serviceType: ClusterIP
      tls:
        gateway:
          mode: CertManager
          certManager:
            issuerRef:
              name: my-ca-issuer # (1)!
              kind: Issuer # (2)!
            dnsNames: # (3)!
              - documentdb.example.com
            secretName: my-documentdb-tls # (4)!
    ```

    1. Must match the `metadata.name` of your Issuer or ClusterIssuer (e.g., `my-ca-issuer` from the prerequisite example above).
    2. Use [`ClusterIssuer`](https://cert-manager.io/docs/concepts/issuer/#cluster-resource) for cluster-scoped issuers, or [`Issuer`](https://cert-manager.io/docs/concepts/issuer/#namespaces) for namespace-scoped.
    3. [Subject Alternative Names](https://en.wikipedia.org/wiki/Subject_Alternative_Name) — add all DNS names clients will use to connect.
    4. Optional. The Kubernetes Secret where cert-manager stores the issued certificate — you do not need to create this Secret yourself, cert-manager generates it automatically. Defaults to `<documentdb-name>-gateway-cert-tls` if not specified.

    For a complete list of CertManager fields, see [CertManagerTLS](../api-reference.md#certmanagertls) in the API Reference.

    **Connect with mongosh:**

    If your CA is private (which most cert-manager setups are), you need `--tlsCAFile` so mongosh can verify the server certificate:

    ```bash
    # Extract the CA certificate from the Secret
    kubectl get secret my-documentdb-tls -n default \
      -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt

    # Connect with TLS
    mongosh "mongodb://<username>:<password>@<host>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256" \
      --tls --tlsCAFile ca.crt
    ```

=== "Provided"

    **Best for:** Production with centralized certificate management

    !!! note "Prerequisites"
        A Kubernetes [TLS Secret](https://kubernetes.io/docs/concepts/configuration/secret/#tls-secrets) containing `tls.crt` and `tls.key`.

        ??? example "Creating a TLS Secret"

            ```bash
            kubectl create secret generic my-documentdb-tls -n default \
              --from-file=tls.crt=server.crt \
              --from-file=tls.key=server.key \
              --from-file=ca.crt=ca.crt  # (1)!
            ```

            1. Optional. The gateway only uses `tls.crt` and `tls.key`. Including `ca.crt` stores the CA certificate in the same Secret for easy client-side retrieval.

    Provided mode lets you supply your own TLS certificates. This is ideal when certificates are managed externally (for example, from Azure Key Vault, HashiCorp Vault, or a corporate CA).

    ```yaml title="documentdb-tls-provided.yaml"
    apiVersion: documentdb.io/preview
    kind: DocumentDB
    metadata:
      name: my-documentdb
      namespace: default
    spec:
      nodeCount: 1
      instancesPerNode: 3
      resource:
        storage:
          pvcSize: 100Gi
      exposeViaService:
        serviceType: ClusterIP
      tls:
        gateway:
          mode: Provided
          provided:
            secretName: my-documentdb-tls
    ```

    **Connect with mongosh:**

    If your CA is private, you need `--tlsCAFile` so mongosh can verify the server certificate:

    ```bash
    # Connect with TLS using your CA certificate
    mongosh "mongodb://<username>:<password>@<host>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256" \
      --tls --tlsCAFile ca.crt
    ```

## Certificate Rotation

Certificate rotation is automatic and zero-downtime. When a certificate is renewed, the gateway picks up the new certificate without restarting pods.

| Mode | Rotation | Action required |
|------|----------|-----------------|
| **SelfSigned** | cert-manager auto-renews 15 days before the 90-day expiry | None |
| **CertManager** | cert-manager auto-renews based on the Certificate CR's `renewBefore` | None |
| **Provided** | You update the Secret contents (manually or via CSI driver sync) | Update the Secret |

### Monitoring Certificate Expiration

```bash
# Check certificate status via cert-manager
kubectl get certificate -n <namespace>

# Check expiration date
kubectl get secret <tls-secret> -n <namespace> \
  -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -dates

# Check DocumentDB TLS status
kubectl get documentdb <name> -n <namespace> \
  -o jsonpath='{.status.tls}' | jq
```

Example TLS status output:

```json
{
  "ready": true,
  "secretName": "my-documentdb-gateway-cert-tls",
  "message": "Gateway TLS certificate ready"
}
```

## Additional Resources

The [`documentdb-playground/tls/`](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/tls) directory provides automated scripts and end-to-end guides for TLS setup on AKS:

- 📖 **[E2E Testing Guide](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/tls/E2E-TESTING.md)** — Automated and manual E2E testing workflows for all TLS modes
- 📘 **[Manual Provided-Mode Setup](https://github.com/documentdb/documentdb-kubernetes-operator/blob/main/documentdb-playground/tls/MANUAL-PROVIDED-MODE-SETUP.md)** — Step-by-step guide for Provided TLS mode with Azure Key Vault
