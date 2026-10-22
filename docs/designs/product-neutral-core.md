# Product-neutral operator core

## Summary

The operator manages a PostgreSQL extension plus a protocol gateway on top of
[CloudNative-PG](https://cloudnative-pg.io/) (CNPG). DocumentDB is one such
product: the `documentdb` extension and the DocumentDB gateway. Other products
follow the same pattern with a different extension and gateway.

This design separates the CNPG orchestration (rendering the `Cluster`,
syncing it, PostgreSQL parameters, the resource split, and the extension
lifecycle) from the DocumentDB product, so a second operator can reuse the same
code. The reconcilers (cluster, certificate, persistent volume, backup,
scheduled backup) and the admission rules are generic too. DocumentDB behavior
is unchanged: golden snapshots of the rendered `Cluster` are byte-identical
before and after the refactor, and the controller and webhook tests pass
unmodified.

## Layers

```
 product CR (DocumentDB, ...)
        │  ClusterAdapter[T]: Intent · Spec · Status/SetStatus · Naming
        ▼
 generic reconcilers + admission rules   internal/engine, internal/validation
 (cluster, certificate, PV, backup,      (public: pkg/core)
  scheduled backup)
        │
        ▼
 ClusterIntent + ProductProfile          internal/product   (public: pkg/core)
        │
        ▼
 renderer · sync · params · split ·      internal/cnpg      (public: pkg/core)
 extension lifecycle SQL
        │
        ▼
 CNPG Cluster ──► sidecar-injector plugin (gateway contract via plugin params)
```

- **`ProductProfile`** (`internal/product/profile.go`) holds what is fixed for
  a product: name, image repositories and default tag, default credential
  secret, CNPG plugin names, the PostgreSQL contract (`PostgresProfile`), and
  the gateway contract (`GatewayProfile`).
- **`PostgresProfile`** holds the extension (`ExtensionSpec`: name and
  ImageVolume library/control/ld paths), the extra
  `shared_preload_libraries`, the bootstrap SQL, `pg_hba`, product-protected
  GUCs, and the role granted to `streaming_replica`.
- **`GatewayProfile`** holds the sidecar container name, port, args, and the
  credential env var names. Empty fields keep the DocumentDB gateway defaults.
- **`ClusterIntent`** (`internal/product/intent.go`) is the per-instance
  desired state: images, topology, storage, resources, monitoring, TLS,
  identity (owner reference), PostgreSQL settings, feature gates, bootstrap,
  and the embedded `ProductProfile`.
- **`DocumentDBAdapter`** (`internal/product/documentdb.go`) maps a `DocumentDB`
  custom resource to a `ClusterIntent`. It is the only place that reads the
  DocumentDB spec for rendering.

- **`ClusterAdapter[T]`** (`internal/product/cluster.go`) extends the adapter
  for the reconcilers: `New`, `Intent`, a neutral operational `ClusterSpec`
  (TLS, exposure, PV reclaim, environment, replication flags), a neutral
  `ClusterStatus` read/write, and `ConnectionString`. `NamingProfile` on the
  profile holds the finalizer, CR kind, Service prefix, labels, and the OTel
  metric attribute.
- **`internal/engine`** holds `ClusterReconciler[T]`,
  `CertificateReconciler[T]`, `PersistentVolumeReconciler[T]`,
  `BackupReconciler[T,B]`, and `ScheduledBackupReconciler[T,S,B]`.
  Multi-region replication plugs in through `ReplicationHooks[T]`; when it is
  nil the cluster runs single-region. Its `Context` method returns a
  `ReplicationContext` built with `NewReplicationContext(state,
  currentLocalPrimary, targetLocalPrimary)`. The state is `NoReplication`,
  `Primary`, `Replica`, or `NotPresent`; the cluster, Service, and backup
  reconcilers read it to set the replica source, skip the cluster or backups,
  and hold the client endpoint until a pending promotion finishes (current and
  target local primaries differ).
- **`internal/validation`** holds the admission rules (schema version vs.
  binary, image rollback, storage resize, restore compatibility, ImageVolume
  probe). They take `Terms` so error messages use the product's field names.
  The ImageVolume probe uses a product-independent Kubernetes name prefix;
  display names such as `Cosmos DB` are used only in diagnostics.

Profiles are trusted operator configuration, not human-readable input to be
automatically sanitized. Machine identifiers must satisfy their destination's
rules: Kubernetes domains, Service/container/Secret names, environment variable
names, PostgreSQL parameters, and OTel metric names have different constraints.
`ProductProfile.Name` is a display name, not a machine identifier.

Extension and replication role names are supplied without SQL quoting. The
lifecycle SQL helpers quote identifiers and catalog lookup literals, preserving
mixed case and supporting names containing hyphens or quotes. However,
`ExtensionSpec.Name` is also the CNPG extension mount identifier: CNPG limits it
to 1-59 characters matching `^[a-z0-9]([-a-z0-9_]*[a-z0-9])?$`. SQL quoting does
not relax that separate constraint. Bootstrap SQL is product-authored SQL and
is passed through verbatim.

The DocumentDB controllers and webhook in `internal/controller` and
`internal/webhook` are now thin wrappers over these packages. They keep their
types, method names, and RBAC markers, so `cmd/main.go`, tests, and generated
manifests are unchanged.

The renderer (`cnpg.GetCnpgClusterSpecFromIntent`) only reads the intent. The
old `GetCnpgClusterSpec`, `MergeParameters`, `ProtectedParameters`,
`ComputeResourceSplit`, and `ValidateResources` entry points remain as thin
DocumentDB wrappers.

## What moved where

| Concern | Product-neutral | DocumentDB value |
| --- | --- | --- |
| Extension mount and name | `PostgresProfile.Extension` | `documentdb` |
| `shared_preload_libraries` | `PostgresProfile.AdditionalLibraries` | `pg_cron`, `pg_documentdb_core`, `pg_documentdb` |
| initdb SQL | `PostgresProfile.BootstrapSQL` | `CREATE EXTENSION documentdb CASCADE`, ... |
| Forced GUCs | core: `max_replication_slots`, `max_wal_senders`, `max_prepared_transactions`; product: `PostgresProfile.ProtectedParameters` | `cron.database_name` |
| Replication grant | `PostgresProfile.ReplicationAdminRole` (skipped when empty) | `documentdb_admin_role` |
| Extension upgrade SQL | `cnpg.ExtensionVersionSQL` / `ExtensionUpdateSQL` | name from the profile |
| Image sync | `SyncCnpgCluster` reads the extension by name | first `Extensions` entry |
| Resource validation path | `ValidateResourcesFor(..., base)` | `spec.resource` |
| Gateway sidecar | `GatewayProfile` → plugin params | injector defaults |
| Client Service port | `GatewayProfile.Port`, else `GATEWAY_PORT` | `10260` |
| Service name prefix, finalizer, labels | `NamingProfile` | `documentdb-service-`, `documentdb.io/...` |
| OTel metric attribute | `NamingProfile` | `documentdb.cluster` |
| OTel SQL health metric | `NamingProfile.HealthMetricName` | `documentdb.postgres.up` |
| Admission error wording | `validation.Terms` | `documentDBVersion`, `spec.documentDBImage`, ... |

## Sidecar-injector gateway contract

The injector accepts five optional plugin parameters: `gatewayContainerName`,
`gatewayPort`, `gatewayArgs` (a JSON array that replaces the DocumentDB args
verbatim), `gatewayUsernameEnv`, and `gatewayPasswordEnv`. They default to the
DocumentDB gateway (`documentdb-gateway`, `10260`, `--create-user ...
--start-pg false --pg-port 5432`, `USERNAME`, `PASSWORD`). The operator sends
them only when `ProductProfile.Gateway` sets them to a non-default value (an
empty `Args` slice is sent as `[]`), matching how the injector serializes
them, so existing clusters see no parameter change and no rolling restart.
`SyncCnpgCluster` adds, updates, and removes these parameters on existing
clusters and requests a rolling restart when they change. When the gateway
port changes, the client Service's ports follow it; the Service keeps its
assigned cluster IPs and node ports. See
[the plugin configuration guide](../developer-guides/sidecar-injector-plugin-configuration.md).

The configured plugin name must match the deployed plugin server's advertised
identity. The shared injector binary advertises
`cnpg-i-sidecar-injector.documentdb.io`; another product can reuse that identity
and binary with its own gateway parameters. Merely changing the profile's
plugin name does not rename the plugin server.

Gateway certificate DNS names use the same `NamingProfile.ServiceName` helper
as the gateway Service, including its 63-character truncation and removal of
trailing hyphens at the truncation boundary. Truncation preserves the legacy
naming convention, not uniqueness: product CRD name validation should reserve
room for the Service prefix to avoid collisions between truncated names.

## Wire contracts kept on purpose

These names are visible to running clusters or users. Renaming them would
restart pods or break configuration, so they stay as they are:

- The plugin parameter key `documentDbCredentialSecret` carries the credential
  secret name for every product.
- Operator environment variables keep their `DOCUMENTDB_*` names (for example
  `DOCUMENTDB_GATEWAY_MEMORY_FRACTION` and `DOCUMENTDB_IOURING_SECCOMP_PROFILE`).
- The credential secret keys are `username` and `password`, and the gateway
  TLS secret is mounted at `/tls`.

## Using the core from another operator

Go does not allow importing `internal/` packages from another module, so
`operator/src/pkg/core` re-exports the seam: type aliases for the intent and
profile types plus `RenderCluster`, `SyncCluster`, `MergeParameters`,
`ProtectedParameters`, `ComputeResourceSplit`, `ValidateResources`,
`DefaultSplitConfig`, the extension SQL helpers, and `DocumentDBProfile` as a
reference profile. `pkg/core/engine.go` re-exports the reconcilers, adapters,
`ReplicationHooks`, `NewReplicationContext` with its `ReplicationState`
values (`NoReplication`, `ReplicationPrimary`, `ReplicationReplica`,
`ReplicationNotPresent`), `NewPodSQLExecutor`, predicates, and the validation
helpers (`ValidateSchemaVersion`, `ValidateImageRollback`,
`ValidateStorageResize`, `ValidateRestoreCompatibility`,
`NewImageVolumeProbe`). It adds no behavior of its own.

For a checked-out or mirrored source tree, the consuming module can use:

```go
require github.com/documentdb/documentdb-operator v0.0.0

replace github.com/documentdb/documentdb-operator => ../documentdb-kubernetes-operator/operator/src
```

The replacement points at `operator/src`, the Go module, not the repository
root. Pin the checkout or mirror to the reviewed source commit.

A second product:

1. Defines its own CRD and a `core.ClusterAdapter[*MyCR]` that returns its
   `core.ProductProfile` (including `Naming`) and maps the CR to
   `core.ClusterIntent`, `core.ClusterSpec`, and `core.ClusterStatus`. Backup
   support adds a `core.BackupAdapter[*MyBackup]` and
   `core.ScheduledBackupAdapter[*MySchedule]`.
   `Intent.Product` must match `Profile()`, and overlapping operational inputs
   such as monitoring must agree between `Intent` and `Spec`.
2. Wires the generic reconcilers in its `main.go`:

   ```go
   adapter := cosmos.AccountAdapter{}
   if err := (&core.ClusterReconciler[*v1.Account]{
       Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
       Recorder: mgr.GetEventRecorderFor("account-controller"),
       SQLExecutor: core.NewPodSQLExecutor(mgr.GetClient(), clientset, mgr.GetConfig()),
       Adapter: adapter,
       Replication: replication, // nil -> single-region
   }).SetupWithManager(mgr, "account"); err != nil {
       return err
   }
   if err := (&core.CertificateReconciler[*v1.Account]{Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Adapter: adapter}).SetupWithManager(mgr, "account-certificate"); err != nil {
       return err
   }
   if err := (&core.PersistentVolumeReconciler[*v1.Account]{Client: mgr.GetClient(), Adapter: adapter}).SetupWithManager(mgr, "account-pv"); err != nil {
       return err
   }
   ```

   A multi-region product implements `core.ReplicationHooks[*v1.Account]` and
   returns its topology from `Context`:

   ```go
   func (accountReplication) Context(ctx context.Context, a *v1.Account) (*core.ReplicationContext, error) {
       if a.Spec.Region != a.Spec.PrimaryRegion {
           rc := core.NewReplicationContext(core.ReplicationReplica, "", "")
           rc.PrimaryCNPGClusterName = a.Spec.PrimaryRegion
           return rc, nil
       }
       // Hold the endpoint while a promotion to this region is pending.
       return core.NewReplicationContext(core.ReplicationPrimary, a.Status.LocalPrimary, a.Status.TargetPrimary), nil
   }
   ```

   It writes its own webhook on top of the `core.Validate*` helpers with its
   own `core.ValidationTerms`, and its own RBAC markers for its API group.
3. Ships the shared sidecar-injector with its gateway image, setting
   `ProductProfile.Gateway` when its gateway contract differs, and
   `NamingProfile.HealthMetricName` and `MetricClusterAttribute` for its
   monitoring names.

`pkg/core/engine_test.go` defines a fake `Account` CR in another API group and
drives `core.ClusterReconciler` with a fake client through finalizer, RBAC,
CNPG `Cluster` creation with the second product's extension, and status,
using only `pkg/core`. `pkg/core/replication_test.go` drives the cluster and
backup reconcilers with replica, not-present, and promotion-pending contexts
built with `core.NewReplicationContext`.
`pkg/core/second_product_test.go` reuses a `Cosmos DB` profile across rendering,
cluster reconciliation, gateway contract updates/removal, TLS certificates,
ImageVolume admission, and backup/PV restore metadata. It includes a separate
`AccountBackup` custom resource, a hyphenated extension name, a mixed-case
replication role, and Kubernetes name validation at probe and Service boundaries.
`pkg/core/example_test.go` renders a non-DocumentDB cluster through the facade.
`internal/cnpg/second_product_test.go` renders a synthetic `pgcosmos` product
end to end and fails if any DocumentDB string other than the documented
`documentDbCredentialSecret` key appears in the output.

## Testing guarantees

- `internal/cnpg/cnpg_golden_test.go` pins the rendered `Cluster` for seven
  DocumentDB scenarios. Any change to DocumentDB output fails the test.
  Regenerate the snapshots with `UPDATE_GOLDEN=1 go test ./internal/cnpg` only
  for intentional changes.
- Unit tests cover the `...For` functions with a non-DocumentDB field path and
  extension name, sync of a `pgcosmos` extension, the gateway contract on both
  the operator and the injector side, and the facade.

## Not in scope / follow-ups

- CRD types, `cmd/main.go` wiring, Helm charts, RBAC markers, and the kubectl
  plugin stay per product.
- DocumentDB multi-region replication (fleet networking, promotion, and
  publication/subscription) stays DocumentDB-specific behind
  `ReplicationHooks`. Another product can implement its own hooks or run
  single-region.
- `MonitoringSpec` and `CertificatesConfiguration` are reused from the
  DocumentDB API and CNPG respectively as shared types.
