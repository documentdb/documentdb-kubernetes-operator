# Container Image Management

This document describes how the DocumentDB Kubernetes Operator manages, builds, versions, and releases container images.

## Table of Contents

- [Overview](#overview)
- [Image Inventory](#image-inventory)
- [Version Tracks](#version-tracks)
- [How the Operator Resolves Images at Runtime](#how-the-operator-resolves-images-at-runtime)
- [Helm Chart Configuration](#helm-chart-configuration)
- [Build Pipelines](#build-pipelines)
- [Release Pipelines](#release-pipelines)
- [Test Pipelines](#test-pipelines)
- [Local Development](#local-development)
- [Version Synchronization Points](#version-synchronization-points)
- [Architecture Support](#architecture-support)
- [Security](#security)

---

## Overview

The project manages **5 container images** across two independent version tracks:

- **Operator track**: images built from Go source code in this repository
- **Database track**: extension `.deb` from [PGDG](https://apt.postgresql.org/) (`trixie-pgdg`), gateway payload from the upstream [`documentdb/documentdb`](https://github.com/documentdb/documentdb) `documentdb-local` image

All images are published to **GitHub Container Registry (GHCR)** under `ghcr.io/documentdb/documentdb-kubernetes-operator/`. A sixth image (PostgreSQL) comes from the CloudNative-PG project and is consumed as-is.

---

## Image Inventory

### Operator Track — Built from This Repository

| Image | GHCR Path | Source | Dockerfile | Purpose |
|-------|-----------|--------|------------|---------|
| **operator** | `.../operator` | `operator/src/` (Go) | `operator/src/Dockerfile` | Main reconciliation controller for DocumentDB CRDs |
| **sidecar** | `.../sidecar` | `operator/cnpg-plugins/sidecar-injector/` (Go) | `operator/cnpg-plugins/sidecar-injector/Dockerfile` | CNPG plugin that injects the gateway sidecar into database pods |
| **wal-replica** | `.../wal-replica` | `operator/cnpg-plugins/wal-replica/` (Go) | *(planned)* | WAL-based read replica plugin (feature-flagged, disabled by default) |

### Database Track — Built from External Source

| Image | GHCR Path | Source | Dockerfile | Purpose |
|-------|-----------|--------|------------|---------|
| **documentdb** | `.../documentdb` | `postgresql-18-documentdb` from the PGDG APT repository (`trixie-pgdg`) | `.github/dockerfiles/Dockerfile_extension` | DocumentDB PostgreSQL extension files for CNPG ImageVolume mode |
| **gateway** | `.../gateway` | Public gateway payload copied from `ghcr.io/documentdb/documentdb/documentdb-local:pg17-<version>` | `.github/dockerfiles/Dockerfile_gateway_public_image` | MongoDB wire-protocol gateway binary (Rust) |

### External Image (Not Built Here)

| Image | Full Reference | Source | Purpose |
|-------|---------------|--------|---------|
| **PostgreSQL** | `ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` | CloudNative-PG project | Base PostgreSQL server image for CNPG clusters |

---

## Version Tracks

The two version tracks are **independent** — they follow different release cadences and use different version numbering.

| Aspect | Operator Track | Database Track |
|--------|---------------|----------------|
| **Images** | operator, sidecar, wal-replica | documentdb, gateway |
| **Source repo** | This repo (Go) | `documentdb/documentdb` (C + Rust) |
| **Version source** | `Chart.appVersion` in `Chart.yaml` | `documentDbVersion` in `values.yaml` |
| **Current version** | see `Chart.appVersion` in `Chart.yaml` | see `documentDbVersion` in `values.yaml` |
| **Build workflow** | `build_operator_images.yml` | `build_documentdb_images.yml` |
| **Release workflow** | `release_operator.yml` | `release_documentdb_images.yml` |
| **Tag example** | `ghcr.io/.../operator:<appVersion>` | `ghcr.io/.../documentdb:<documentDbVersion>` |

### Why Two Tracks?

The DocumentDB extension and gateway are developed in a separate repository (`documentdb/documentdb`) and iterate at a different cadence than the Kubernetes operator. Decoupling allows:

- Operator bug fixes without rebuilding database images (~2 min vs ~15+ min)
- Database upgrades without a full operator release
- Image tags that reflect actual component versions
- Independent testing and promotion pipelines

---

## How the Operator Resolves Images at Runtime

The operator binary determines which database images to use through a priority chain. This logic lives in `operator/src/internal/utils/util.go`.

### DocumentDB Extension Image (`GetDocumentDBImageForInstance()`)

```
Priority (highest → lowest):
1. spec.image.documentDB          ← CR field: full image URI override
2. spec.documentDBVersion        ← CR field: used as tag with hardcoded repo
3. env DOCUMENTDB_VERSION        ← from Helm chart (documentDbVersion in values.yaml)
4. ChangeStreams feature gate     ← temporary override for changestream images
5. DEFAULT_DOCUMENTDB_IMAGE      ← compiled-in default (constants.go)
```

### Gateway Image (`GetGatewayImageForDocumentDB()`)

```
Priority (highest → lowest):
1. spec.image.gateway             ← CR field: full image URI override
2. spec.documentDBVersion        ← CR field: used as tag with hardcoded repo
3. env DOCUMENTDB_VERSION        ← from Helm chart (documentDbVersion in values.yaml)
4. ChangeStreams feature gate     ← temporary override for changestream images
5. DEFAULT_GATEWAY_IMAGE          ← compiled-in default (constants.go)
```

### PostgreSQL Image

Set via `spec.image.postgres` in the DocumentDB CR. Defaults to `ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` (hardcoded in the CRD schema).

### How Images Flow into Pods

```
DocumentDB CR spec
    │
    ▼
Operator controller (documentdb_controller.go)
    ├── Resolves documentdbImage via GetDocumentDBImageForInstance()
    ├── Resolves gatewayImage via GetGatewayImageForDocumentDB()
    │
    ▼
CNPG Cluster spec (cnpg_cluster.go)
    ├── documentdbImage → ImageVolumeSource (mounted as read-only volume)
    ├── gatewayImage → passed as plugin parameter to sidecar-injector
    │
    ▼
Sidecar Injector Plugin (lifecycle.go)
    └── Reads gatewayImage from plugin parameters
        └── Injects gateway container with that image into each database pod
```

---

## Helm Chart Configuration

The Helm chart (`operator/documentdb-helm-chart/`) coordinates deployment of operator-track images and passes database version configuration to the operator.

### Chart.yaml

```yaml
version: 0.1.3           # Chart version
appVersion: "0.1.3"       # Default tag for operator/sidecar/wal-replica images
```

### values.yaml

```yaml
# Database image version (extension + gateway) — independent of Chart.appVersion
documentDbVersion: "0.117.0"

image:
  documentdbk8soperator:
    repository: ghcr.io/documentdb/documentdb-kubernetes-operator/operator
    pullPolicy: Always
  sidecarinjector:
    repository: ghcr.io/documentdb/documentdb-kubernetes-operator/sidecar
    pullPolicy: Always
  walreplica:
    repository: ghcr.io/documentdb/documentdb-kubernetes-operator/wal-replica
    pullPolicy: Always
```

### Image Tag Resolution in Templates

**Operator-track images** are composed by the `documentdb.imageRef` helper, which
prepends `image.registry` to a relative repository (or uses the repository
verbatim when it already carries a host, i.e. its first segment contains `.` or
`:`, or equals `localhost`) and appends the tag, defaulting to `Chart.AppVersion`:
```yaml
image: "{{ include "documentdb.imageRef" (dict "name" "image.documentdbk8soperator.repository" "registry" .Values.image.registry "repo" .Values.image.documentdbk8soperator.repository "tag" (.Values.image.documentdbk8soperator.tag | default .Chart.AppVersion)) }}"
```
Repositories are host/path only: an empty registry/repository, or a tag or digest
embedded in the repository, fails rendering (naming the offending setting) rather
than producing an invalid reference.

**Database version** is passed as an environment variable:
```yaml
{{- if .Values.documentDbVersion }}
- name: DOCUMENTDB_VERSION
  value: "{{ .Values.documentDbVersion }}"
{{- end }}
```

The `DOCUMENTDB_VERSION` env var feeds into the operator's image resolution chain (priority 3). If not set, the operator uses its compiled-in defaults.

---

## Build Pipelines

### Operator Image Build (`build_operator_images.yml`)

Builds operator and sidecar images from this repo's Go source.

| Aspect | Details |
|--------|---------|
| **Trigger** | `workflow_dispatch`, `push` to `main` (operator source paths) |
| **Images** | operator, sidecar |
| **Dockerfiles** | `operator/src/Dockerfile`, `operator/cnpg-plugins/sidecar-injector/Dockerfile` |
| **Tag pattern** | `{version}-test` (candidate), `{version}-test-{arch}` (per-arch) |
| **Build time** | ~2 minutes |
| **Multi-arch** | amd64 + arm64 → multi-arch manifest |
| **Signing** | cosign keyless (OIDC) |

### Database Image Build (`build_documentdb_images.yml`)

Builds documentdb extension and gateway images from published DocumentDB artifacts.

| Aspect | Details |
|--------|---------|
| **Trigger** | `workflow_dispatch`, `repository_dispatch` (from upstream) |
| **Images** | documentdb, gateway |
| **Dockerfiles** | `.github/dockerfiles/Dockerfile_extension`, `.github/dockerfiles/Dockerfile_gateway_public_image` |
| **Tag pattern** | `{documentdb_version}-build-{run_id}-{attempt}-{sha}` (candidate) |
| **Build time** | ~5 minutes (package download + image build) |
| **Multi-arch** | amd64 + arm64 → multi-arch manifest |
| **Signing** | cosign keyless (OIDC) |
| **Version detection** | Workflow input / repository dispatch payload (defaults to released `0.113.0`) |

The build process:
1. Resolves the released DocumentDB version
2. Pins `postgresql-18-documentdb` from the PGDG `trixie-pgdg` APT index (one version + SHA256 for both arches)
3. Verifies the public `documentdb-local:pg17-<version>` image exists
4. Downloads and validates each `.deb` (checksum, name, version, arch, `default_version`)
5. Builds `Dockerfile_extension` (installs pg_cron, pgvector, postgis alongside)
6. Builds `Dockerfile_gateway_public_image` from the upstream gateway payload

> **Why PGDG?** Upstream stopped publishing Debian 13 `.deb` assets after `0.115`. PGDG publishes
> the same package for `trixie` on both architectures. PGDG versions carry a packaging revision
> (`0.116-0` → `0.116-0-1.pgdg13+1`), so the workflow resolves the index rather than constructing
> a URL, and asserts `default_version` rather than relying on the Debian version string.

### Dockerfile Details

#### Operator (`operator/src/Dockerfile`)
- **Base**: `mcr.microsoft.com/oss/go/microsoft/golang:1.26-azurelinux3.0` → `scratch`
- **Multi-stage**: 2 stages (builder → scratch)
- **Entrypoint**: `/manager`

#### Sidecar (`operator/cnpg-plugins/sidecar-injector/Dockerfile`)
- **Base**: Same Go Azure Linux image → `scratch`
- **Multi-stage**: 2 stages
- **Entrypoint**: `/app/bin/cnpg-i-sidecar-injector`

#### DocumentDB Extension (`.github/dockerfiles/Dockerfile_extension`)
- **Base**: `ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie` → `scratch`
- **Multi-stage**: 2 stages
- **No entrypoint** — this is an ImageVolume source, not a runnable container
- Follows the [cloudnative-pg/postgres-extensions-containers](https://github.com/cloudnative-pg/postgres-extensions-containers) pattern
- Installs DocumentDB extension + pg_cron + pgvector + PostGIS
- Copies only extension artifacts (`.so`, `.control`, `.sql`, bitcode) and required system libraries
- Resolves Debian-alternatives symlinks (they break in ImageVolume mode)

#### Gateway (`.github/dockerfiles/Dockerfile_gateway_public_image`)
- **Base**: `debian:trixie-slim`
- **Multi-stage**: public `documentdb-local` source image → slim runtime image
- **Entrypoint**: `/bin/bash /home/documentdb/gateway/scripts/gateway_entrypoint.sh`
- Runs as non-root `documentdb` user (UID 1000)
- Copies `documentdb_gateway`, `SetupConfiguration.json`, and `utils.sh` from the upstream public image

---

## Release Pipelines

### Operator Release (`release_operator.yml`)

Promotes operator/sidecar candidate images and publishes the Helm chart.

```
Inputs:
  candidate_version: "0.1.3-test"   ← source tag
  version: "0.1.3"                  ← target release tag
  source_ref: <git tag or commit>   ← for Helm chart packaging

Flow:
  1. Test Gate (optional, parallel)
     ├── test-E2E.yml
     ├── test-integration.yml
     └── test-backup-and-restore.yml
  
  2. Promote Images
     └── docker buildx imagetools create
         -t .../operator:0.1.3  .../operator:0.1.3-test
         -t .../sidecar:0.1.3   .../sidecar:0.1.3-test
  
  3. Publish Helm Chart
     ├── Update Chart.yaml (version + appVersion)
     ├── helm package + helm push to oci://ghcr.io/{owner}
     └── Publish to GitHub Pages (Helm repo index)
```

### Database Image Release (`release_documentdb_images.yml`)

Promotes documentdb/gateway candidate images and auto-creates a PR to update defaults.

```
Inputs:
  candidate_version: "0.111.0-build-123456789-1-deadbee"   ← source tag
  version: "0.111.0"                  ← target release tag
  update_defaults: true               ← create PR to bump versions

Flow:
  1. Promote Images
     └── docker buildx imagetools create
         -t .../documentdb:0.111.0  .../documentdb:0.111.0-test
         -t .../gateway:0.111.0     .../gateway:0.111.0-test
  
  2. Update Defaults (auto-PR)
     ├── constants.go: DEFAULT_DOCUMENTDB_IMAGE, DEFAULT_GATEWAY_IMAGE
     ├── config.go: sidecar plugin default gateway image
     ├── values.yaml: documentDbVersion
     ├── test-backup-and-restore.yml: fallback images
     └── Opens PR: "chore: bump DocumentDB images to 0.111.0"
```

---

## Test Pipelines

### Test Build (`test-build-and-package.yml`)

Reusable workflow that builds ALL images (operator + database) locally for test validation. Called by all test workflows when no external `image_tag` is provided.

- All images share a single test tag: `{version}-test-{run_id}-{arch}`
- Images are built with `--load` and saved as `.tar` artifacts (not pushed to GHCR)
- Also produces arch-specific Helm chart packages

### Test Workflows

| Workflow | Trigger | Special Image Logic |
|----------|---------|---------------------|
| `test-E2E.yml` | push/PR/schedule/dispatch | Standard — local build or external images |
| `test-integration.yml` | push/PR/dispatch | Standard — local build or external images |
| `test-backup-and-restore.yml` | push/PR/schedule/dispatch | Has hardcoded fallback images for external mode |
| `test-upgrade-and-rollback.yml` | push/PR/schedule/dispatch | Old/new image comparison; combined image for chart ≤0.1.3 |
| `test-unit.yml` | push/PR | No container images |

### Image Loading (`setup-test-environment` action)

The `.github/actions/setup-test-environment/action.yml` composite action handles loading images into Kind clusters:

- **Local build path**: `docker load` from `.tar` → `kind load docker-image`
- **External image path**: `docker pull` from GHCR → `kind load docker-image`
- Supports separate `documentdb-image-tag` for independent database image versioning in tests
- Falls back to `image-tag` (operator tag) for backward compatibility

---

## Local Development

### Makefile Targets (`operator/src/Makefile`)

```bash
make docker-build    # Build operator image: docker build -t ${IMG} .
make docker-push     # Push operator image
make docker-buildx   # Multi-platform build and push
```

### Deploy Script (`operator/src/scripts/development/deploy.sh`)

```bash
# Defaults
REGISTRY=localhost:5001
OPERATOR_IMAGE=${REGISTRY}/operator
PLUGIN_IMAGE=${REGISTRY}/sidecar-injector
TAG=0.1.1

# Usage: builds and deploys to a local Kind cluster
DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh
```

The script uses `kind_with_registry.sh` to set up a `registry:2` container on `localhost:5001`, connected to the Kind cluster's network.

---

## Version Synchronization Points

When bumping database image versions, the following locations must be updated (automated by `release_documentdb_images.yml`):

| File | Field | Example |
|------|-------|---------|
| `operator/src/internal/utils/constants.go` | `DEFAULT_DOCUMENTDB_IMAGE` | `...documentdb:0.117.0` |
| `operator/src/internal/utils/constants.go` | `DEFAULT_GATEWAY_IMAGE` | `...gateway:0.117.0` |
| `operator/cnpg-plugins/sidecar-injector/internal/config/config.go` | Default gateway image | `...gateway:0.117.0` |
| `operator/cnpg-plugins/sidecar-injector/internal/config/config_test.go` | Expected gateway image | `...gateway:0.117.0` |
| `operator/documentdb-helm-chart/values.yaml` | `documentDbVersion` | `"0.117.0"` |
| `operator/documentdb-helm-chart/tests/02_sidecar_injector_test.yaml` | `DOCUMENTDB_VERSION` env value | `"0.117.0"` |
| `operator/documentdb-helm-chart/tests/09_operator_deployment_test.yaml` | `DOCUMENTDB_VERSION` env value | `"0.117.0"` |
| `test/e2e/tests/upgrade/helpers_test.go` | `defaultOldDocumentDBVersion` / `defaultNewDocumentDBVersion` pair | `"0.116.0"` -> `"0.117.0"` |
| `.github/workflows/build_documentdb_images.yml` | `DEFAULT_DOCUMENTDB_VERSION`, input default | `0.113.0` |
| `.github/workflows/release_documentdb_images.yml` | Input default | `0.113.0` |
| `.github/dockerfiles/Dockerfile_gateway_public_image` | `SOURCE_IMAGE` ARG default | `...pg17-0.113.0` |

When bumping operator versions, update:

| File | Field | Example |
|------|-------|---------|
| `operator/documentdb-helm-chart/Chart.yaml` | `version`, `appVersion` | `0.1.3` |
| `CHANGELOG.md` | New version entry | `## [0.1.3]` |

> **Note**: The `constants.go` and `config.go` defaults must be kept in sync — this is enforced by a `NOTE: Keep in sync` comment in both files.

---

## Architecture Support

All images support multi-architecture builds:

| Platform | CI Runner | Notes |
|----------|-----------|-------|
| `linux/amd64` | `ubuntu-22.04` | Primary architecture |
| `linux/arm64` | `ubuntu-22.04-arm` | ARM support |

Build workflows produce per-arch images with `-amd64`/`-arm64` suffixes, then create multi-arch manifests using `docker manifest create --amend`.

---

## Security

| Aspect | Implementation |
|--------|---------------|
| **Image signing** | cosign keyless (OIDC-based) during build workflows |
| **Signature verification** | Certificate identity matching in the same workflow |
| **Minimal images** | Operator/sidecar use `scratch`; extension uses `scratch`; gateway uses `debian:trixie-slim` |
| **Non-root execution** | Gateway: UID 1000 (`documentdb`); operator/sidecar: from `scratch` with no shell |
| **No pull secrets** | GHCR public packages; no `imagePullSecrets` in chart |

---

## Deprecated Workflows

The following workflows are deprecated and will be removed in a future release:

| Deprecated | Replaced By |
|-----------|-------------|
| `build_images.yml` | `build_operator_images.yml` + `build_documentdb_images.yml` |
| `release_images.yml` | `release_operator.yml` + `release_documentdb_images.yml` |

The deprecated workflows built and released all 4 images together, coupling the operator and database version tracks. The new split workflows allow independent release cycles.
