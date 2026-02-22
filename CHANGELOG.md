# Changelog

## [Unreleased]

### Major Features
- **Dual-Mode Deployment**: The operator auto-detects the Kubernetes cluster version at startup and selects the deployment strategy accordingly.
  - On Kubernetes 1.35+, the operator uses ImageVolume to mount the DocumentDB extension as a separate image alongside a standard PostgreSQL base image.
  - On Kubernetes < 1.35, the operator falls back to a single combined image (`documentdb-local`) that bundles PostgreSQL and the DocumentDB extension together. This mode is deprecated and will be removed in a future release.
  - Extension upgrades (`ALTER EXTENSION`) are only performed in ImageVolume mode; combined mode is self-contained.
  - No user configuration is required — the same `DocumentDB` custom resource works on all supported Kubernetes versions.

### Enhancements & Fixes
- CI E2E test matrix expanded to cover both K8s 1.34 (combined mode) and K8s 1.35 (ImageVolume mode) on amd64 and arm64 architectures
- Kind setup script (`kind_with_registry.sh`) defaults to K8s 1.35 node image for local development
- Public documentation updated with Kubernetes 1.35+ recommendation and deprecation notice for older versions

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
