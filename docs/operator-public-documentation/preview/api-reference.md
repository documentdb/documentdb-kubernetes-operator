# API Reference

## Packages
- [documentdb.io/preview](#documentdbiopreview)


## documentdb.io/preview

Package preview contains API Schema definitions for the db preview API group.

### Resource Types
- [Backup](#backup)
- [DocumentDB](#documentdb)
- [ScheduledBackup](#scheduledbackup)



#### Backup









| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `documentdb.io/preview` | | |
| `kind` _string_ | `Backup` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[BackupSpec](#backupspec)_ |  |  |  |


#### BackupConfiguration



BackupConfiguration defines backup settings for DocumentDB.



_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `retentionDays` _integer_ | RetentionDays specifies how many days backups should be retained.<br />If not specified, the default retention period is 30 days. | 30 | Maximum: 365 <br />Minimum: 1 <br />Optional: \{\} <br /> |


#### BackupSpec



BackupSpec defines the desired state of Backup.



_Appears in:_
- [Backup](#backup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cluster` _[LocalObjectReference](https://pkg.go.dev/github.com/cloudnative-pg/cloudnative-pg/api/v1#LocalObjectReference)_ | Cluster specifies the DocumentDB cluster to backup.<br />The cluster must exist in the same namespace as the Backup resource. |  | Required: \{\} <br /> |
| `retentionDays` _integer_ | RetentionDays specifies how many days the backup should be retained.<br />If not specified, the default retention period from the cluster's backup retention policy will be used. |  | Optional: \{\} <br /> |


#### BootstrapConfiguration



BootstrapConfiguration defines how to bootstrap a DocumentDB cluster.



_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `recovery` _[RecoveryConfiguration](#recoveryconfiguration)_ | Recovery configures recovery from a backup. |  | Optional: \{\} <br /> |


#### CertManagerTLS



CertManagerTLS holds parameters for cert-manager driven certificates.



_Appears in:_
- [GatewayTLS](#gatewaytls)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `issuerRef` _[IssuerRef](#issuerref)_ |  |  |  |
| `dnsNames` _string array_ | DNSNames for the certificate SANs. If empty, operator will add Service DNS names. |  |  |
| `secretName` _string_ | SecretName optional explicit name for the target secret. If empty a default is chosen. |  |  |


#### ClusterReplication







_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `crossCloudNetworkingStrategy` _string_ | CrossCloudNetworking determines which type of networking mechanics for the replication |  | Enum: [AzureFleet Istio None] <br /> |
| `primary` _string_ | Primary is the name of the primary cluster for replication. |  |  |
| `clusterList` _[MemberCluster](#membercluster) array_ | ClusterList is the list of clusters participating in replication. |  |  |
| `highAvailability` _boolean_ | Whether or not to have replicas on the primary cluster. |  |  |


#### DocumentDB



DocumentDB is the Schema for the dbs API.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `documentdb.io/preview` | | |
| `kind` _string_ | `DocumentDB` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[DocumentDBSpec](#documentdbspec)_ |  |  |  |


#### DocumentDBSpec



DocumentDBSpec defines the desired state of DocumentDB.



_Appears in:_
- [DocumentDB](#documentdb)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `nodeCount` _integer_ | NodeCount is the number of nodes in the DocumentDB cluster. Must be 1. |  | Maximum: 1 <br />Minimum: 1 <br /> |
| `instancesPerNode` _integer_ | InstancesPerNode is the number of DocumentDB instances per node. Range: 1-3. |  | Maximum: 3 <br />Minimum: 1 <br /> |
| `resource` _[Resource](#resource)_ | Resource specifies the storage resources for DocumentDB. |  |  |
| `documentDBVersion` _string_ | DocumentDBVersion specifies the version for all DocumentDB components (engine, gateway).<br />When set, this overrides the default versions for documentDBImage and gatewayImage.<br />Individual image fields take precedence over this version. |  |  |
| `documentDBImage` _string_ | DocumentDBImage is the container image to use for DocumentDB.<br />Changing this is not recommended for most users.<br />If not specified, defaults based on documentDBVersion or operator defaults. |  |  |
| `gatewayImage` _string_ | GatewayImage is the container image to use for the DocumentDB Gateway sidecar.<br />Changing this is not recommended for most users.<br />If not specified, defaults to a version that matches the DocumentDB operator version. |  |  |
| `postgresImage` _string_ | PostgresImage is the container image to use for the PostgreSQL server.<br />If not specified, defaults to the last stable PostgreSQL version compatible with DocumentDB.<br />Must use trixie (Debian 13) base to match the extension's GLIBC requirements. | ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie | Optional: \{\} <br /> |
| `documentDbCredentialSecret` _string_ | DocumentDbCredentialSecret is the name of the Kubernetes Secret containing credentials<br />for the DocumentDB gateway (expects keys `username` and `password`). If omitted,<br />a default secret name `documentdb-credentials` is used. |  |  |
| `clusterReplication` _[ClusterReplication](#clusterreplication)_ | ClusterReplication configures cross-cluster replication for DocumentDB. |  |  |
| `sidecarInjectorPluginName` _string_ | SidecarInjectorPluginName is the name of the sidecar injector plugin to use. |  |  |
| `walReplicaPluginName` _string_ | WalReplicaPluginName is the name of the wal replica plugin to use. |  |  |
| `exposeViaService` _[ExposeViaService](#exposeviaservice)_ | ExposeViaService configures how to expose DocumentDB via a Kubernetes service.<br />This can be a LoadBalancer or ClusterIP service. |  |  |
| `environment` _string_ | Environment specifies the cloud environment for deployment<br />This determines cloud-specific service annotations for LoadBalancer services |  | Enum: [eks aks gke] <br /> |
| `timeouts` _[Timeouts](#timeouts)_ |  |  |  |
| `tls` _[TLSConfiguration](#tlsconfiguration)_ | TLS configures certificate management for DocumentDB components. |  |  |
| `logLevel` _string_ | Overrides default log level for the DocumentDB cluster. |  |  |
| `bootstrap` _[BootstrapConfiguration](#bootstrapconfiguration)_ | Bootstrap configures the initialization of the DocumentDB cluster. |  | Optional: \{\} <br /> |
| `backup` _[BackupConfiguration](#backupconfiguration)_ | Backup configures backup settings for DocumentDB. |  | Optional: \{\} <br /> |
| `featureGates` _object (keys:string, values:boolean)_ | FeatureGates enables or disables optional DocumentDB features.<br />Keys are PascalCase feature names following the Kubernetes feature gate convention.<br />Example: \{"ChangeStreams": true\}<br />IMPORTANT: When adding a new feature gate, update ALL of the following:<br />1. Add a new FeatureGate* constant in documentdb_types.go<br />2. Add the key name to the XValidation CEL rule's allowed list below<br />3. Add a default entry in the featureGateDefaults map in documentdb_types.go |  | Optional: \{\} <br /> |
| `affinity` _[AffinityConfiguration](https://pkg.go.dev/github.com/cloudnative-pg/cloudnative-pg/api/v1#AffinityConfiguration)_ | Affinity/Anti-affinity rules for Pods (cnpg passthrough) |  | Optional: \{\} <br /> |


#### ExposeViaService







_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `serviceType` _string_ | ServiceType determines the type of service to expose for DocumentDB. |  | Enum: [LoadBalancer ClusterIP] <br /> |


#### GatewayTLS



GatewayTLS defines TLS configuration for the gateway sidecar (Phase 1: certificate provisioning only)



_Appears in:_
- [TLSConfiguration](#tlsconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `mode` _string_ | Mode selects the TLS management strategy. |  | Enum: [Disabled SelfSigned CertManager Provided] <br /> |
| `certManager` _[CertManagerTLS](#certmanagertls)_ | CertManager config when Mode=CertManager. |  |  |
| `provided` _[ProvidedTLS](#providedtls)_ | Provided secret reference when Mode=Provided. |  |  |


#### GlobalEndpointsTLS



GlobalEndpointsTLS acts as a placeholder for future global endpoint TLS settings.



_Appears in:_
- [TLSConfiguration](#tlsconfiguration)



#### IssuerRef



IssuerRef references a cert-manager Issuer or ClusterIssuer.



_Appears in:_
- [CertManagerTLS](#certmanagertls)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  |  |
| `kind` _string_ | Kind of issuer (Issuer or ClusterIssuer). Defaults to Issuer. |  |  |
| `group` _string_ | Group defaults to cert-manager.io |  |  |


#### MemberCluster







_Appears in:_
- [ClusterReplication](#clusterreplication)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the member cluster. |  |  |
| `environment` _string_ | EnvironmentOverride is the cloud environment of the member cluster.<br />Will default to the global setting |  | Enum: [eks aks gke] <br /> |
| `storageClass` _string_ | StorageClassOverride specifies the storage class for DocumentDB persistent volumes in this member cluster. |  |  |


#### PVRecoveryConfiguration



PVRecoveryConfiguration defines settings for recovering from a retained PersistentVolume.



_Appears in:_
- [RecoveryConfiguration](#recoveryconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the name of the PersistentVolume to recover from.<br />The PV must exist and be in Available or Released state. |  | MinLength: 1 <br /> |


#### PostgresTLS



PostgresTLS acts as a placeholder for future Postgres TLS settings.



_Appears in:_
- [TLSConfiguration](#tlsconfiguration)



#### ProvidedTLS



ProvidedTLS references an existing secret that contains tls.crt/tls.key (and optional ca.crt).



_Appears in:_
- [GatewayTLS](#gatewaytls)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretName` _string_ |  |  |  |


#### RecoveryConfiguration



RecoveryConfiguration defines recovery settings for bootstrapping a DocumentDB cluster.



_Appears in:_
- [BootstrapConfiguration](#bootstrapconfiguration)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `backup` _[LocalObjectReference](https://pkg.go.dev/github.com/cloudnative-pg/cloudnative-pg/api/v1#LocalObjectReference)_ | Backup specifies the source backup to restore from. |  | Optional: \{\} <br /> |
| `persistentVolume` _[PVRecoveryConfiguration](#pvrecoveryconfiguration)_ | PersistentVolume specifies the PV to restore from.<br />The operator will create a temporary PVC bound to this PV, use it for CNPG recovery,<br />and delete the temporary PVC after the cluster is healthy.<br />Cannot be used together with Backup. |  | Optional: \{\} <br /> |


#### Resource







_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `storage` _[StorageConfiguration](#storageconfiguration)_ | Storage configuration for DocumentDB persistent volumes. |  |  |


#### ScheduledBackup









| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `documentdb.io/preview` | | |
| `kind` _string_ | `ScheduledBackup` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.35/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ScheduledBackupSpec](#scheduledbackupspec)_ |  |  |  |


#### ScheduledBackupSpec



ScheduledBackupSpec defines the desired state of ScheduledBackup



_Appears in:_
- [ScheduledBackup](#scheduledbackup)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `cluster` _[LocalObjectReference](https://pkg.go.dev/github.com/cloudnative-pg/cloudnative-pg/api/v1#LocalObjectReference)_ | Cluster specifies the DocumentDB cluster to backup.<br />The cluster must exist in the same namespace as the ScheduledBackup resource. |  | Required: \{\} <br /> |
| `schedule` _string_ | Schedule defines when backups should be created using cron expression format.<br />See https://pkg.go.dev/github.com/robfig/cron#hdr-CRON_Expression_Format |  | Required: \{\} <br /> |
| `retentionDays` _integer_ | RetentionDays specifies how many days the backups should be retained.<br />If not specified, the default retention period from the cluster's backup retention policy will be used. |  | Optional: \{\} <br /> |


#### StorageConfiguration







_Appears in:_
- [Resource](#resource)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `pvcSize` _string_ | PvcSize is the size of the persistent volume claim for DocumentDB storage (e.g., "10Gi"). |  |  |
| `storageClass` _string_ | StorageClass specifies the storage class for DocumentDB persistent volumes.<br />If not specified, the cluster's default storage class will be used. |  |  |
| `persistentVolumeReclaimPolicy` _string_ | PersistentVolumeReclaimPolicy controls what happens to the PersistentVolume when<br />the DocumentDB cluster is deleted.<br />When a DocumentDB cluster is deleted, the following chain of deletions occurs:<br />DocumentDB deletion → CNPG Cluster deletion → PVC deletion → PV deletion (based on this policy)<br />Options:<br />  - Retain (default): The PV is preserved after cluster deletion, allowing manual<br />    data recovery or forensic analysis. Use for production workloads where data<br />    safety is critical. Orphaned PVs must be manually deleted when no longer needed.<br />  - Delete: The PV is automatically deleted when the PVC is deleted. Use for development,<br />    testing, or ephemeral environments where data persistence is not required.<br />WARNING: Setting this to "Delete" means all data will be permanently lost when<br />the DocumentDB cluster is deleted. This cannot be undone. | Retain | Enum: [Retain Delete] <br />Optional: \{\} <br /> |


#### TLSConfiguration



TLSConfiguration aggregates TLS settings across DocumentDB components.



_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `gateway` _[GatewayTLS](#gatewaytls)_ | Gateway configures TLS for the gateway sidecar (Phase 1: certificate provisioning only). |  |  |
| `postgres` _[PostgresTLS](#postgrestls)_ | Postgres configures TLS for the Postgres server (placeholder for future phases). |  |  |
| `globalEndpoints` _[GlobalEndpointsTLS](#globalendpointstls)_ | GlobalEndpoints configures TLS for global endpoints (placeholder for future phases). |  |  |


#### Timeouts







_Appears in:_
- [DocumentDBSpec](#documentdbspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `stopDelay` _integer_ |  |  | Maximum: 1800 <br />Minimum: 0 <br /> |


