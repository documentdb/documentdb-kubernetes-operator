# Changelog

## [Unreleased]

### Major Features
- **ImageVolume Deployment**: The operator uses ImageVolume (GA in Kubernetes 1.35) to mount the DocumentDB extension as a separate image alongside a standard PostgreSQL base image.

### Breaking Changes
- **Removed combined mode support**: The legacy combined-image deployment mode for Kubernetes < 1.35 has been removed. Kubernetes 1.35+ is now required.
- **Deb-based container images**: Container images switched from source-compiled builds to deb-based packages under `ghcr.io/documentdb/documentdb-kubernetes-operator/`. The extension and gateway are now separate images with versioned tags (e.g., `:0.110.0`).
- **PostgreSQL base image changed to Debian trixie**: The default `postgresImage` changed from `postgresql:18-minimal-bookworm` to `postgresql:18-minimal-trixie` (Debian 13) to satisfy the deb-based extension's GLIBC requirements. Existing clusters that don't explicitly set `postgresImage` will use the new base on upgrade.

### Enhancements & Fixes
- CI E2E test matrix updated to cover K8s 1.35+ on amd64 and arm64 architectures
- Kind setup script (`kind_with_registry.sh`) defaults to K8s 1.35 node image for local development
- Public documentation updated to require Kubernetes 1.35+

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
