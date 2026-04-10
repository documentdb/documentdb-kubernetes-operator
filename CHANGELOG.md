# Changelog

## [Unreleased]

### Major Features
- **Two-Phase Extension Upgrade**: New `spec.schemaVersion` field separates binary upgrades (`spec.documentDBVersion`) from irreversible schema migrations (`ALTER EXTENSION UPDATE`). The default behavior gives you a rollback-safe window — update the binary first, validate, then finalize the schema. Set `schemaVersion: "auto"` for single-step upgrades in development environments. See the [upgrade guide](docs/operator-public-documentation/preview/operations/upgrades.md) for details.

### Breaking Changes
- **Validating webhook added**: A new `ValidatingWebhookConfiguration` enforces that `spec.schemaVersion` never exceeds the binary version and blocks `spec.documentDBVersion` rollbacks below the committed schema version. This requires [cert-manager](https://cert-manager.io/) to be installed in the cluster (it is already a prerequisite for the sidecar injector). Existing clusters upgrading to this release will have the webhook activated automatically via `helm upgrade`.

## [0.2.0] - 2026-03-25

### Major Features
- **ImageVolume Deployment**: The operator uses ImageVolume (GA in Kubernetes 1.35) to mount the DocumentDB extension as a separate image alongside a standard PostgreSQL base image
- **DocumentDB Upgrade Support**: Configurable PostgresImage and ImageVolume extensions for seamless upgrades
- **Sync Service & ChangeStreams**: DocumentDB sync service and ChangeStreams feature gate
- **Affinity Configuration**: Pod scheduling passthrough for affinity rules
- **PersistentVolume Management**: PV retention, security mount options, and PV recovery support
- **CNPG In-Place Updates**: Support for CloudNative-PG in-place updates

### Breaking Changes
- **Kubernetes 1.35+ required**: The legacy combined-image deployment mode for Kubernetes < 1.35 has been removed. Kubernetes 1.35+ is now required.
- **Deb-based container images**: Container images switched from source-compiled builds to deb-based packages under `ghcr.io/documentdb/documentdb-kubernetes-operator/`. The extension and gateway are now separate images with versioned tags (e.g., `:0.109.0`).
- **PostgreSQL base image changed to Debian trixie**: The default `postgresImage` changed from `postgresql:18-minimal-bookworm` to `postgresql:18-minimal-trixie` (Debian 13) to satisfy the deb-based extension's GLIBC requirements. Existing clusters that don't explicitly set `postgresImage` will use the new base on upgrade.

### Bug Fixes
- Gateway pods now restart when TLS secret name changes
- Fixed PV labeling for multi-cluster lookups
- Fixed Go toolchain vulnerabilities (upgraded to 1.25.8)

### Documentation
- Added comprehensive AKS and AWS EKS deployment guides
- Added high availability documentation for local HA configuration
- Added auto-generated CRD API reference documentation
- Added architecture, prerequisites, and FAQ documentation

## [0.1.3] - 2025-12-12

### Major Features
- **Change the CRD API version to match documentdb.io**

## [0.1.2] - 2025-12-05

### Major Features
- **Local High-Availability Support**
- **Single Cluster Backup and Restore**
- **MultiCloud Setup Guide**

### Enhancements & Fixes
- Documentation to configure OpenTelemetry, Prometheus and Grafana
- Bug Fix: Show Status and Connection String in Status
- Update scripts and docs for Multi-Region and Multi-Cloud Setup
- Add Cert Manager to Operator
