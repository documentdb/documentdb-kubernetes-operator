# Container images on Microsoft Container Registry

To ship the operator as an Azure Arc cluster extension, every image the extension pulls has to come from Microsoft Container Registry (MCR). This document lists each image the operator deploys, where it comes from on MCR, and the changes this repository needs to get there.

For how images are built, versioned, and released to GHCR, see [Container image management](image-management.md).

## Constraints

- **Arc extensions pull from MCR.** GHCR and Docker Hub aren't supported image sources for an extension's stable release.
- **MCR publishes third-party open-source images only when they're built from source**, under `mcr.microsoft.com/oss/`. Upstream images can't be retagged into this project's MCR namespace.

## Image inventory

With default settings, a DocumentDB deployment pulls four images built by this project and two from CloudNative-PG. Two more are pulled only when an opt-in feature is on.

| Image | Pulled when | Source today | On MCR |
|-------|-------------|--------------|--------|
| operator, sidecar | Always | GHCR (this project) | `mcr.microsoft.com/documentdb/documentdb-kubernetes-operator/<image>` |
| documentdb (extension), gateway | Always | GHCR (this project) | `mcr.microsoft.com/documentdb/documentdb-kubernetes-operator/<image>` |
| Helm chart | At install | `oci://ghcr.io/documentdb/documentdb-operator` | `oci://mcr.microsoft.com/documentdb/helmchart/documentdb-operator` |
| CloudNativePG operator | Always: the CNPG controller, and the bootstrap init container in every PostgreSQL pod | `ghcr.io/cloudnative-pg/cloudnative-pg:1.29.1` | Not yet, see [CloudNativePG operator](#cloudnativepg-operator) |
| PostgreSQL 18 | Always | `ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` | Not yet, see [PostgreSQL](#postgresql) |
| OpenTelemetry Collector | Only with `spec.monitoring.enabled` | `otel/opentelemetry-collector-contrib:0.149.0` (Docker Hub) | `mcr.microsoft.com/oss/v2/otel/opentelemetry-collector-contrib:v0.148.0` |
| nginx | Only for cross-cloud replication over Fleet or Istio networking | `nginx:alpine` (Docker Hub, hard-coded) | Not yet, see [Required changes](#required-changes) |

`registry.k8s.io/pause:3.10` appears in the ImageVolume capability probe, but the probe pods are server-side dry runs and the image is never pulled. The wal-replica image isn't published to MCR until it has a Dockerfile and a build.

## Images built by this project

Releases are published to MCR at the same path as on GHCR, so moving a deployment only changes the registry host:

```text
ghcr.io/documentdb/documentdb-kubernetes-operator/<image>:<tag>
mcr.microsoft.com/documentdb/documentdb-kubernetes-operator/<image>:<tag>
```

Images are copied from GHCR by digest rather than rebuilt. MCR serves the same multi-arch index (`linux/amd64` and `linux/arm64`) with the same digests, so the cosign signatures produced by this repository's workflows verify against the MCR copy when `COSIGN_REPOSITORY` points at the GHCR repository.

The Helm chart on MCR is the GHCR chart with `image.registry` set to `mcr.microsoft.com`, so its digest differs from GHCR's. Its chart version and default image tags are unchanged.

## Third-party images

### CloudNativePG operator

The CNPG image runs the CNPG controller and also bootstraps every PostgreSQL pod as an init container, so a single override, `cloudnative-pg.image.repository` in the chart values, moves both.

- **Preview:** served from a Microsoft-owned Azure Container Registry with anonymous pull. The upstream image is imported unchanged and pinned by digest.
- **Stable:** built from source with a [Dalec](https://github.com/project-dalec/dalec) spec through Azure Container Upstream, and published as `mcr.microsoft.com/oss/v2/cloudnative-pg/cloudnative-pg`. CNPG is a static Go binary, so it fits an Azure Linux build. This project owns and maintains the spec, including upstream version bumps.

### PostgreSQL

The DocumentDB extension image is built against Debian trixie (glibc), so the PostgreSQL image has to match it.

- **Preview:** served from the same Azure Container Registry, pinned to a specific 18.x minor instead of the floating `18-minimal-trixie` tag. PostgreSQL 18.6 and later need extension 0.116.0 or later.
- **Stable:** not decided yet. The options are:

| Option | What it takes | Trade-off |
|--------|---------------|-----------|
| Azure Linux | Build the extension for Azure Linux and use an Azure Linux PostgreSQL 18 image | Minimal images and no Debian dependency, but the extension image changes and the PostgreSQL image must meet CNPG's operand requirements |
| Debian through Dalec | Build PostgreSQL 18 on Debian trixie from source with a Dalec spec | The extension stays as built today, but there's no minimal Debian image and a second spec to maintain |

## Required changes

These changes in this repository are needed before every default image can come from MCR:

- [ ] **PostgreSQL default.** `spec.image.postgres` has a `+kubebuilder:default` of `ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` (`operator/src/api/preview/documentdb_types.go`). The API server fills it in whenever a DocumentDB resource sets any field under `spec.image`, and it then wins over the chart's `image.postgres` (`POSTGRES_IMAGE`). When `spec.image` is omitted and `image.postgres` is unset, CNPG falls back to its own GHCR default. Resolve the default in the operator instead, so `image.postgres` applies to every DocumentDB cluster.
- [ ] **nginx image.** `physical_replication.go` hard-codes `nginx:alpine` for the cross-cloud token service. Make it configurable through the chart, like `image.otelCollector`, and default it to an MCR-hosted image.
- [ ] **OpenTelemetry Collector.** Validate the operator with `v0.148.0`, the version on MCR, or request a newer build. Then set `image.otelCollector` for MCR deployments.
- [ ] **Operator base image.** The operator's final stage is `mcr.microsoft.com/devcontainers/base:alpine` (`operator/src/Dockerfile`). Move it to a distroless or Azure Linux base, like the sidecar's `scratch` stage, to match the distroless or minimal base this repository asks for in `AGENTS.md`.
- [ ] **Upgrade design examples.** `docs/designs/upgrade-design/` uses `mcr.microsoft.com/documentdb/documentdb:<tag>`, which doesn't exist. Update the examples to the paths in [Image inventory](#image-inventory).

## Open questions

- Which stable path for PostgreSQL: Azure Linux or Debian through Dalec?
- Should the chart ship an MCR values file, so that one `-f` flag moves every image, or should each consumer set the overrides?
