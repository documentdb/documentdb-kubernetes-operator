# Changelog

## [Unreleased]

### Upgrade notes
- **`documentdb.io/v1` API**: `DocumentDB`, `Backup` and `ScheduledBackup` graduate from `documentdb.io/preview` to the stable `documentdb.io/v1` API, which is now the storage version. `preview` remains served with an identical schema and returns a deprecation warning; it will be removed in a future release. **Apply the updated CRDs before running `helm upgrade`** (Helm does not upgrade CRDs), otherwise the new operator cannot start. Update manifests to `apiVersion: documentdb.io/v1`; no other field changes are needed. The Go API package moved from `operator/src/api/preview` to `operator/src/api/v1`, the validating webhook path is now `/validate-documentdb-io-v1-documentdb`, and `kubectl documentdb` uses `v1`. The public documentation moved from `.../preview/` to `.../v1/`; old URLs redirect. See [Migrating to the `documentdb.io/v1` API](docs/operator-public-documentation/v1/operations/upgrades.md#migrating-to-the-documentdbiov1-api).

### Security
- Require Go 1.26.10 across all six modules and pin Go builder images to that patch release to address [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) in `crypto/x509`. Unit-test workflows now resolve Go from the operator module instead of an older hard-coded version.

### Bug Fixes
- Preserve naming choices across concurrent TLS status updates and lost annotations, use optimistic locking for managed Certificate changes, and hold long-name endpoints until their managed certificate covers the hostname. Service selectors now converge through promotion-pending and resumed states.
- Encode configured health metric names as YAML strings, support an explicit monitoring password Secret for products using SCRAM authentication, and support dotted extension versions through `PostgresProfile.ExtensionVersionFormat`.
- Give each cluster its own gateway Service when `documentdb-service-<name>` exceeds 63 characters: long names are shortened with a stable hash of the cluster name instead of plain truncation, which could make two clusters share one Service and publish the other cluster's endpoint. Clusters that already own a plainly truncated Service or gateway certificate keep that name. The operator records the chosen name in the `documentdb.io/gateway-service-name` annotation, so recreating the Service keeps the same hostname, and adds any missing Service DNS names to its managed gateway certificate (the operator ClusterRole now includes `patch` on cert-manager Certificates). Rolling the operator back to an earlier release is not supported for these long-name clusters. The operator now refuses a same-named Service controlled by another resource, gateway certificate DNS names follow the actual Service name, and `kubectl documentdb status` finds the Service by its owning DocumentDB instead of a name substring.
- Quote product-defined extension and replication role names in lifecycle SQL, and use a product-independent ImageVolume probe name so display names such as `Cosmos DB` do not bypass capability checks.

### Major Features
- **Product-neutral operator core**: The CNPG `Cluster` renderer, sync, PostgreSQL parameter merge, resource split/validation and extension lifecycle SQL are now driven by a `ProductProfile` and `ClusterIntent` instead of the `DocumentDB` custom resource, and are exported for other operator modules through `operator/src/pkg/core`. The sidecar injector accepts optional `gatewayContainerName`, `gatewayPort`, `gatewayArgs`, `gatewayUsernameEnv` and `gatewayPasswordEnv` parameters for non-DocumentDB gateways. The operator keeps these parameters in sync on existing clusters, and the client Service port follows the gateway port. Other products can build multi-region topologies with `core.NewReplicationContext` and name the OTel SQL health metric with `NamingProfile.HealthMetricName` (DocumentDB keeps `documentdb.postgres.up`). The cluster, certificate, persistent volume, backup and scheduled-backup reconcilers and the admission validation rules are generic over the custom resource type through a `ClusterAdapter`, with DocumentDB as the first implementation; `pkg/core` exports them too. Default DocumentDB CNPG rendering and CRD schemas are unchanged. RBAC permits the naming annotation and certificate updates described above; long-name gateway Services have the documented compatibility behavior. See [Product-neutral operator core](docs/designs/product-neutral-core.md).

## [1.0.0] - 2026-10-19

First generally available operator release, for DocumentDB 1.0. The operator and the DocumentDB engine keep separate version numbers; this chart deploys engine `1.0.0` (extension schema `1.0-0`) by default. See [Upgrading to DocumentDB 1.0](docs/operator-public-documentation/v1/operations/upgrades.md#upgrading-to-documentdb-10).

This entry covers everything since `0.3.0`, including `1.0.0-rc1`.

### Upgrade notes
- **Default DocumentDB engine is now `1.0.0`** (was `0.110.0` in chart `0.3.0` and `1.0-RC2` in `1.0.0-rc1`). Clusters that do not pin `spec.documentDBVersion` roll to the `1.0.0` binary when you upgrade the chart, except for images set explicitly in `spec.image`. The schema stays where it is unless `spec.schemaVersion` is set (`"auto"` migrates immediately); moving from `0.x` to `1.0-0` runs `ALTER EXTENSION documentdb UPDATE`, which cannot be undone. In-place upgrade from `1.0-RC2` is not supported: pin RC clusters to `1.0-RC2` before upgrading the chart and migrate to a new cluster.

### Major Features
- **Configurable image registry and repositories**: The Helm chart now exposes an `image.registry` prefix (default `ghcr.io`) plus relative per-component `repository` values, so every operator and data-plane image (operator, sidecar-injector, wal-replica, documentdb extension, gateway, and the optional OpenTelemetry collector / PostgreSQL operand) can be re-homed to a private or mirrored registry without rebuilding the operator. A `repository` that already includes a host (contains `.` or `:`, or is `localhost`) is used verbatim and ignores the prefix, letting you point a single image at a different registry. Repositories are host/path only — the tag is supplied via each component's `tag` field (or `documentDbVersion` for the data-plane images), and a tag or digest embedded in a `repository` is rejected at render time. For the optional OpenTelemetry collector and PostgreSQL operand images (which have no fallback tag source), the `repository` and `tag` must be set together — supplying only one is rejected at render time so a mistyped override can never leak a floating `:latest` or be silently dropped. Default rendering is unchanged (`ghcr.io/documentdb/...`), so existing deployments are unaffected.
- **Fail-fast ImageVolume capability check**: The operator now depends on the Kubernetes [ImageVolume](https://kubernetes.io/docs/concepts/storage/volumes/#image) feature to mount the DocumentDB extension into PostgreSQL pods. Instead of gating on a Kubernetes version number, the validating webhook performs a capability probe (a server-side dry-run) when a `DocumentDB` is created and **rejects the resource with an actionable error if ImageVolume is unavailable**, so you find out immediately instead of waiting for pods that never become ready. ImageVolume is GA (on by default) in Kubernetes **1.35+**; on **1.33/1.34** it is beta and must be enabled via the `ImageVolume` feature gate on a containerd/CRI-O runtime. The Helm chart's `kubeVersion` floor is relaxed to `>= 1.33.0-0` accordingly. See [Before you start](docs/operator-public-documentation/v1/getting-started/before-you-start.md).
- **DocumentDB extension packages from PGDG**: The database image build now resolves `postgresql-18-documentdb` from the [PGDG APT repository](https://apt.postgresql.org/) (`trixie-pgdg`), pinning one version and SHA256 across both architectures and validating `default_version` before building. Replaces the upstream Debian 13 release assets that stopped being published after `0.115`.
- **Dedicated monitoring identity**: The OTel Collector sidecar connects to PostgreSQL as its own login-only role instead of a shared identity (#420).
- **Restore compatibility validation**: The webhook rejects restoring a backup or retained PersistentVolume into a cluster whose engine binary is older than the source schema (#440).
- **PostgreSQL/replication TLS via `spec.tls.postgres`**: A new `spec.tls.postgres` field (backed by CloudNative-PG's `CertificatesConfiguration`) lets you supply your own CA and certificate Secrets for PostgreSQL server and replication connections instead of the CloudNative-PG self-signed defaults. Supported keys are `serverTLSSecret` + `serverCASecret` (server certificate) and `replicationTLSSecret` + `clientCASecret` (the `streaming_replica` client certificate). A CEL validation rule enforces the pairing invariants: server and replication secrets must each be provided together, and `serverTLSSecret` requires `replicationTLSSecret`. When client certificates are provided, cross-region `externalClusters` connect with `sslmode=require` (or `verify-full` when a server CA is present) and present the replication client certificate. See the [TLS configuration guide](docs/operator-public-documentation/v1/configuration/tls.md). (#416)
- **Release-candidate and source-built data-plane images**: `build_documentdb_images.yml` builds the extension image from a pinned upstream tag for RCs (#470), and for stable releases with `package_source=source` when PGDG has not packaged the release yet (#478).

### Bug Fixes
- Address dependency vulnerabilities reported by govulncheck, including gRPC (#429, #436, #468, #473).
- Track the floating PostgreSQL 18 image again after temporarily pinning 18.4 to avoid a crash in the DocumentDB extension (#445, #459).

### Breaking Changes
- **Image pull-policy Helm values moved under `image`**: The top-level `gatewayImagePullPolicy` and `documentDbImagePullPolicy` chart values have been removed and replaced by `image.gateway.pullPolicy` and `image.documentdb.pullPolicy` (both default `IfNotPresent`), aligning them with the other per-component image settings. Existing values files or `--set` overrides using the old keys no longer take effect; update them to the new nested keys. (Preview-only breaking change.)

### Testing and documentation
- Expand E2E schema-upgrade coverage (automatic migration, rollback, HA, unpullable version) and add expired-backup cleanup tests (#426, #443, #393).
- Add long-haul data-protection verification, retention pruning, and operator/primary pod kill operations (#419, #421, #422).
- Introduce a product-neutral `ClusterIntent` seam with no functional change (#446).
- Add multi-cloud deployment docs and an Arc + Fleet hybrid setup (#399, #267).

## [1.0.0-rc1] - 2026-10-15

Release candidate; superseded by `1.0.0`. The chart defaults to DocumentDB `1.0-RC2`, not `0.117.0` as the original release notes stated: `0.117.0` was the default only until #471 moved it to the promoted RC2 images. Note that the docs published for this release show `--version 1.0-rc1`; install it with `--version 1.0.0-rc1`.

## [0.3.0] - 2026-07-15

### Security
- **Bumped CloudNative-PG dependency from chart 0.27.0 (app 1.28.0) to 0.28.1 (app 1.29.1)** to pick up the fix for [CVE-2026-44477 / GHSA-423p-g724-fr39](https://github.com/cloudnative-pg/cloudnative-pg/security/advisories/GHSA-423p-g724-fr39): a privilege-escalation vulnerability in the CNPG metrics exporter that could allow a low-privilege PostgreSQL user to escalate to superuser and execute arbitrary commands in the database pod. Operators upgrading via `helm upgrade` will get the patched CNPG operator automatically.
- **Hardened PostgreSQL `pg_hba` rules**: The operator no longer emits the permissive `host all all 0.0.0.0/0 trust` / `host all all ::0/0 trust` rules that allowed passwordless PostgreSQL connections from any address. Non-replication access is now limited to `host all all localhost trust` (the gateway sidecar, which connects over the shared pod loopback), and cross-instance/cross-region replication uses `hostssl replication streaming_replica all cert`. Clients that previously relied on unauthenticated direct PostgreSQL access over the pod network will be rejected after upgrade; use the DocumentDB gateway (SCRAM-SHA-256) for application traffic. In multi-region deployments where `spec.tls.postgres` is not set, replication falls back to `host replication streaming_replica all trust`, which relies entirely on network-layer security (e.g. an Istio/service-mesh mTLS boundary) — provide `spec.tls.postgres` certificates to require TLS client certificates for replication instead.

### Major Features
- **io_uring opt-in feature gate**: A new `IOUring` feature gate (`spec.featureGates.IOUring: true`) enables PostgreSQL 18 asynchronous I/O (`io_method=io_uring`). Because the container runtime's default seccomp profile strips the `io_uring_setup/enter/register` syscalls, the operator also relaxes the postgres container seccomp profile — pointing the pods at a hardened Localhost profile that re-allows only those three syscalls — when the gate is on, so no external Kyverno policy is needed. It is **opt-in** (default off) since io_uring relaxes the sandbox. The Localhost profile path is operator-level config via the Helm value `operator.ioUring.seccompProfile` (default `profiles/documentdb-iouring.json`). See [io_uring documentation](docs/operator-public-documentation/io-uring.md) and the [feature playground](documentdb-playground/io-uring-feature/).
- **Gateway OTLP metrics in the per-pod sidecar**: when `spec.monitoring.enabled=true`, the OTel Collector sidecar now exposes an OTLP/gRPC receiver on `127.0.0.1:4317` and the documentdb-gateway is configured (via `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_METRICS_ENABLED`) to push its `db_client_*` metrics there. The sidecar's existing prometheus exporter re-exports them alongside the existing `documentdb.postgres.up` sqlquery output, with per-pod attribution added by the collector's resource processor. No new CRD fields; this turns on automatically wherever monitoring was already enabled.
- **Two-Phase Extension Upgrade**: New `spec.schemaVersion` field separates binary upgrades (`spec.documentDBVersion`) from irreversible schema migrations (`ALTER EXTENSION UPDATE`). The default behavior gives you a rollback-safe window — update the binary first, validate, then finalize the schema. Set `schemaVersion: "auto"` for single-step upgrades in development environments. See the [upgrade guide](docs/operator-public-documentation/v1/operations/upgrades.md) for details.

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
