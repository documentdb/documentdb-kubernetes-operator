# Changelog

## [Unreleased]

### Security
- **Bumped CloudNative-PG dependency from chart 0.27.0 (app 1.28.0) to 0.28.1 (app 1.29.1)** to pick up the fix for [CVE-2026-44477 / GHSA-423p-g724-fr39](https://github.com/cloudnative-pg/cloudnative-pg/security/advisories/GHSA-423p-g724-fr39): a privilege-escalation vulnerability in the CNPG metrics exporter that could allow a low-privilege PostgreSQL user to escalate to superuser and execute arbitrary commands in the database pod. Operators upgrading via `helm upgrade` will get the patched CNPG operator automatically.

### Major Features
- **io_uring opt-in feature gate**: A new `IOUring` feature gate (`spec.featureGates.IOUring: true`) enables PostgreSQL 18 asynchronous I/O (`io_method=io_uring`). Because the container runtime's default seccomp profile strips the `io_uring_setup/enter/register` syscalls, the operator also relaxes the postgres container seccomp profile — pointing the pods at a hardened Localhost profile that re-allows only those three syscalls — when the gate is on, so no external Kyverno policy is needed. It is **opt-in** (default off) since io_uring relaxes the sandbox. The Localhost profile path is operator-level config via the Helm value `operator.ioUring.seccompProfile` (default `profiles/documentdb-iouring.json`). See [io_uring documentation](docs/operator-public-documentation/io-uring.md) and the [feature playground](documentdb-playground/io-uring-feature/).
- **Gateway OTLP metrics in the per-pod sidecar**: when `spec.monitoring.enabled=true`, the OTel Collector sidecar now exposes an OTLP/gRPC receiver on `127.0.0.1:4317` and the documentdb-gateway is configured (via `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_METRICS_ENABLED`) to push its `db_client_*` metrics there. The sidecar's existing prometheus exporter re-exports them alongside the existing `documentdb.postgres.up` sqlquery output, with per-pod attribution added by the collector's resource processor. No new CRD fields; this turns on automatically wherever monitoring was already enabled.
- **Two-Phase Extension Upgrade**: New `spec.schemaVersion` field separates binary upgrades (`spec.documentDBVersion`) from irreversible schema migrations (`ALTER EXTENSION UPDATE`). The default behavior gives you a rollback-safe window — update the binary first, validate, then finalize the schema. Set `schemaVersion: "auto"` for single-step upgrades in development environments. See the [upgrade guide](docs/operator-public-documentation/preview/operations/upgrades.md) for details.

### Behavioral Changes
- **Sidecar memory & CPU isolation**: `spec.resource.memory` and `spec.resource.cpu` are now treated as the total pod resource envelope. The operator reserves resources for the documentdb-gateway sidecar (memory default 18.75% of the envelope, capped at 32Gi) and, when `spec.monitoring.enabled` is true, the OTel collector sidecar (default memory limit 128Mi, CPU request 50m / limit 200m), then gives PostgreSQL the remainder and recomputes its memory-aware parameters (`shared_buffers`, etc.) accordingly. This isolates a gateway/collector leak so it is OOM-killed in its own container instead of crowding out PostgreSQL. The split is configurable per component via `spec.resource.{gateway,database,otel}` and fleet-wide via operator Helm values. The envelope is **optional**: you may omit `spec.resource.memory`/`cpu` for a dimension when it is set explicitly on both the gateway and the database (the effective envelope is then the sum); a partially specified dimension without an envelope is rejected by the validating webhook. Existing clusters adopt the new split (and a one-time rolling restart) on their next reconcile.

### Breaking Changes
- **CRD restructure into domain-grouped stanzas**: image, postgres and plugin fields have moved into dedicated groups. Migrate as follows: `spec.documentDBImage` → `spec.image.documentDB`, `spec.gatewayImage` → `spec.image.gateway`, `spec.postgresImage` → `spec.image.postgres`, `spec.sidecarInjectorPluginName` → `spec.plugins.sidecarInjectorName`. A new `spec.postgres` group exposes `uid`, `gid` and `postInitSQL` (the operator's mandatory bootstrap statements always run first; user statements are appended after). A new root-level `spec.imagePullSecrets` is propagated to the underlying CNPG cluster.
- **Validating webhook added**: A new `ValidatingWebhookConfiguration` enforces that `spec.schemaVersion` never exceeds the binary version and blocks `spec.documentDBVersion` rollbacks below the committed schema version. This requires [cert-manager](https://cert-manager.io/) to be installed in the cluster (it is already a prerequisite for the sidecar injector). Existing clusters upgrading to this release will have the webhook activated automatically via `helm upgrade`.
- **Removed `Disabled` TLS gateway mode**: The `spec.tls.gateway.mode: Disabled` option has been removed to eliminate the security risk of plaintext Mongo wire protocol traffic. Previously, `Disabled` mode served connections in plaintext, contradicting the `Disabled` tab in `tls.md` which described the mode as a self-signed bootstrap. Empty or unset mode now defaults to `SelfSigned`, and the controller fails closed (also defaulting to `SelfSigned`) if a legacy `Disabled` value is encountered on a stored object. Users with `mode: Disabled` should remove this setting or explicitly set `mode: SelfSigned` — the gateway will automatically use a cert-manager generated self-signed certificate. See [issue #356](https://github.com/documentdb/documentdb-kubernetes-operator/issues/356) for details.

### Playground & Examples
- **io_uring feature playground**: New `documentdb-playground/io-uring-feature/` demonstrates the operator-native `IOUring` opt-in, modeled on the upstream cnpg-playground seccomp approach — a kind cluster that `extraMount`s the curated Localhost seccomp profile, a DaemonSet installer for real clusters, the DocumentDB CR with `spec.featureGates.IOUring: true`, and verification steps. No Kyverno policy is required.
- **Container metrics reference collector**: The telemetry playground now includes a reference OpenTelemetry Collector DaemonSet under `documentdb-playground/telemetry/container-metrics/` for clusters that do not already collect kubelet-backed container metrics. It scrapes each node's local kubelet for container, pod, and node CPU/memory/network/filesystem metrics and exposes them via Prometheus. The production operator chart does not install this platform-level collector; tenant DocumentDB clusters do not receive kubelet privileges.

### Testing infrastructure
- **Unified E2E test suite ([#346](https://github.com/documentdb/documentdb-kubernetes-operator/pull/346))**: The four legacy end-to-end workflows (`test-integration.yml`, `test-E2E.yml`, `test-backup-and-restore.yml`, `test-upgrade-and-rollback.yml`) and their bash / JavaScript (mongosh) / Python (pymongo) glue have been replaced by a single Go / Ginkgo v2 / Gomega suite under `test/e2e/`. Specs are organised by CRD operation (lifecycle, scale, data, performance, backup, tls, feature gates, exposure, status, upgrade), reuse CloudNative-PG's `tests/utils` packages as a library, and speak the Mongo wire protocol via `go.mongodb.org/mongo-driver/v2`.

### Breaking changes for contributors
- **Local E2E invocation changed.** Tests are now run via `ginkgo` against an already-provisioned cluster, not via `npm test` / bash scripts. Typical invocation:
  ```bash
  cd test/e2e
  ginkgo -r --label-filter=smoke ./tests/...
  ```
  Label selection replaces per-workflow entry points; depth is controlled by `TEST_DEPTH` (0=Highest … 4=Lowest). See [`test/e2e/README.md`](test/e2e/README.md) for prereqs, the full env-var table (including `E2E_RUN_ID` and the `E2E_UPGRADE_*` upgrade-suite variables), and troubleshooting.
- **Design rationale** for the migration — scope, fixture tiers, parallelism model, CNPG reuse strategy — is documented in [`docs/designs/e2e-test-suite.md`](docs/designs/e2e-test-suite.md).

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
- **PostgreSQL base image changed to Debian trixie**: The default `image.postgres` changed from `postgresql:18-minimal-bookworm` to `postgresql:18-minimal-trixie` (Debian 13) to satisfy the deb-based extension's GLIBC requirements. Existing clusters that don't explicitly set `image.postgres` will use the new base on upgrade.

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
