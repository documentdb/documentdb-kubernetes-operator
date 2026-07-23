// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"cmp"
	"fmt"
	"os"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	util "github.com/documentdb/documentdb-operator/internal/utils"
	ctrl "sigs.k8s.io/controller-runtime"
)

func GetCnpgClusterSpec(req ctrl.Request, documentdb *dbpreview.DocumentDB, documentdbImage, serviceAccountName, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	split := ComputeResourceSplit(documentdb, DefaultSplitConfig())

	sidecarPluginName := pluginsSidecarInjectorName(documentdb)
	if sidecarPluginName == "" {
		sidecarPluginName = util.DEFAULT_SIDECAR_INJECTOR_PLUGIN
	}

	// Get the gateway image for this DocumentDB instance
	gatewayImage := util.GetGatewayImageForDocumentDB(documentdb)
	log.Info("Creating CNPG cluster with gateway image", "gatewayImage", gatewayImage, "documentdbName", documentdb.Name, "specGatewayImage", imageGateway(documentdb))

	credentialSecretName := documentdb.Spec.DocumentDbCredentialSecret
	if credentialSecretName == "" {
		credentialSecretName = util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET
	}

	// Configure storage class - use specified storage class or nil for default
	var storageClassPointer *string
	if storageClass != "" {
		storageClassPointer = &storageClass
	}

	// Set ImageVolumeSource.PullPolicy for the extension image when configured.
	// This addresses the fact that ImageVolume sources DO support pull policies
	// (via corev1.ImageVolumeSource.PullPolicy), unlike regular container images
	// which only support pull policies on container specs.
	extensionImageSource := corev1.ImageVolumeSource{Reference: documentdbImage}
	if pullPolicy := parsePullPolicy(os.Getenv(util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV)); pullPolicy != "" {
		extensionImageSource.PullPolicy = pullPolicy
	}

	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         documentdb.APIVersion,
					Kind:               documentdb.Kind,
					Name:               documentdb.Name,
					UID:                documentdb.UID,
					Controller:         &[]bool{true}[0], // This cluster is controlled by the DocumentDB instance
					BlockOwnerDeletion: &[]bool{true}[0], // Block DocumentDB deletion until cluster is deleted
				},
			},
		},
		Spec: func() cnpgv1.ClusterSpec {
			spec := cnpgv1.ClusterSpec{
				Instances:           documentdb.Spec.InstancesPerNode,
				ImageName:           imagePostgres(documentdb),
				ImagePullSecrets:    toCNPGImagePullSecrets(documentdb.Spec.ImagePullSecrets),
				PrimaryUpdateMethod: cnpgv1.PrimaryUpdateMethodSwitchover,
				StorageConfiguration: cnpgv1.StorageConfiguration{
					StorageClass: storageClassPointer, // Use configured storage class or default
					Size:         documentdb.Spec.Resource.Storage.PvcSize,
				},
				InheritedMetadata: getInheritedMetadataLabels(documentdb.Name),
				Plugins: func() []cnpgv1.PluginConfiguration {
					params := map[string]string{
						"gatewayImage":               gatewayImage,
						"documentDbCredentialSecret": credentialSecretName,
					}
					if pullPolicy := os.Getenv(util.GATEWAY_IMAGE_PULL_POLICY_ENV); pullPolicy != "" {
						params["gatewayImagePullPolicy"] = pullPolicy
					}
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_MEMORY_REQUEST, split.Gateway.MemoryRequest)
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_MEMORY_LIMIT, split.Gateway.MemoryLimit)
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_CPU_REQUEST, split.Gateway.CPURequest)
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_CPU_LIMIT, split.Gateway.CPULimit)
					// If TLS is ready, surface secret name to plugin so it can mount certs.
					if documentdb.Status.TLS != nil && documentdb.Status.TLS.Ready && documentdb.Status.TLS.SecretName != "" {
						params["gatewayTLSSecret"] = documentdb.Status.TLS.SecretName
					}
					// Pass monitoring parameters to plugin for OTel sidecar injection.
					// Sidecar is only injected when monitoring is enabled.
					// Config hash triggers operator-initiated rolling restart on config changes.
					if split.MonitoringEnabled {
						params["otelCollectorImage"] = util.DEFAULT_OTEL_COLLECTOR_IMAGE
						params["otelConfigMapName"] = otelcfg.ConfigMapName(documentdb.Name)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_MEMORY_REQUEST, split.OTel.MemoryRequest)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_MEMORY_LIMIT, split.OTel.MemoryLimit)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_CPU_REQUEST, split.OTel.CPURequest)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_CPU_LIMIT, split.OTel.CPULimit)
						if promPort := otelcfg.ResolvePrometheusPort(documentdb.Spec.Monitoring); promPort > 0 {
							params["prometheusPort"] = fmt.Sprintf("%d", promPort)
						}
						// Compute config hash for change detection. The operator triggers a
						// rolling restart (via restart annotation) when plugin parameters
						// change, ensuring pods pick up new config.
						if configData, err := otelcfg.GenerateConfigMapData(documentdb.Name, req.Namespace, documentdb.Spec.Monitoring); err == nil {
							params["otelConfigHash"] = otelcfg.HashConfigMapData(configData)
						} else {
							log.Error(err, "Failed to generate OTel config hash; config changes may not trigger rolling restart")
						}
					}
					return []cnpgv1.PluginConfiguration{{
						Name:       sidecarPluginName,
						Enabled:    pointer.Bool(true),
						Parameters: params,
					}}
				}(),
				PostgresConfiguration: buildPostgresConfiguration(documentdb, extensionImageSource, split.PostgresMemoryBytes),
				Bootstrap:             getBootstrapConfiguration(documentdb, isPrimaryRegion, log),
				LogLevel:              cmp.Or(documentdb.Spec.LogLevel, "info"),
				Certificates:          postgresCertificates(documentdb),
				Backup: &cnpgv1.BackupConfiguration{
					VolumeSnapshot: &cnpgv1.VolumeSnapshotConfiguration{
						SnapshotOwnerReference: "backup", // Set owner reference to 'backup' so that snapshots are deleted when Backup resource is deleted
					},
					Target: cnpgv1.BackupTarget("primary"),
				},
				Affinity:  documentdb.Spec.Affinity,
				Resources: buildResourceRequirements(split.Postgres),
			}
			spec.MaxStopDelay = getMaxStopDelayOrDefault(documentdb)
			applyPostgresProcessIdentity(&spec, documentdb)
			applyIOUringSeccomp(&spec, documentdb)

			return spec
		}(),
	}
}

func addPluginParamIfSet(params map[string]string, key, value string) {
	if value != "" {
		params[key] = value
	}
}

func getInheritedMetadataLabels(appName string) *cnpgv1.EmbeddedObjectMetadata {
	return &cnpgv1.EmbeddedObjectMetadata{
		Labels: map[string]string{
			util.LABEL_APP:          appName,
			util.LABEL_REPLICA_TYPE: "primary", // TODO: Replace with CNPG default setup
		},
	}
}

func getBootstrapConfiguration(documentdb *dbpreview.DocumentDB, isPrimaryRegion bool, log logr.Logger) *cnpgv1.BootstrapConfiguration {
	if isPrimaryRegion && documentdb.Spec.Bootstrap != nil && documentdb.Spec.Bootstrap.Recovery != nil {
		recovery := documentdb.Spec.Bootstrap.Recovery

		// Handle backup recovery
		if recovery.Backup.Name != "" {
			backupName := recovery.Backup.Name
			log.Info("DocumentDB cluster will be bootstrapped from backup", "backupName", backupName)
			return &cnpgv1.BootstrapConfiguration{
				Recovery: &cnpgv1.BootstrapRecovery{
					Backup: &cnpgv1.BackupSource{
						LocalObjectReference: recovery.Backup,
					},
				},
			}
		}

		// Handle PV recovery (via temporary PVC created by the controller)
		if recovery.PersistentVolume != nil && recovery.PersistentVolume.Name != "" {
			tempPVCName := util.TempPVCNameForPVRecovery(documentdb.Name)
			log.Info("DocumentDB cluster will be bootstrapped from PV via temp PVC",
				"pvName", recovery.PersistentVolume.Name, "tempPVC", tempPVCName)
			return &cnpgv1.BootstrapConfiguration{
				Recovery: &cnpgv1.BootstrapRecovery{
					VolumeSnapshots: &cnpgv1.DataSource{
						Storage: corev1.TypedLocalObjectReference{
							Name:     tempPVCName,
							Kind:     "PersistentVolumeClaim",
							APIGroup: pointer.String(""),
						},
					},
				},
			}
		}
	}

	return getDefaultBootstrapConfiguration(documentdb)
}

func getDefaultBootstrapConfiguration(documentdb *dbpreview.DocumentDB) *cnpgv1.BootstrapConfiguration {
	postInitSQL := []string{
		"CREATE EXTENSION documentdb CASCADE",
		"CREATE ROLE documentdb WITH LOGIN PASSWORD 'Admin100'",
		"ALTER ROLE documentdb WITH SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS",
	}
	if documentdb != nil && documentdb.Spec.Postgres != nil && len(documentdb.Spec.Postgres.PostInitSQL) > 0 {
		postInitSQL = append(postInitSQL, documentdb.Spec.Postgres.PostInitSQL...)
	}
	return &cnpgv1.BootstrapConfiguration{
		InitDB: &cnpgv1.BootstrapInitDB{
			PostInitSQL: postInitSQL,
		},
	}
}

// getMaxStopDelayOrDefault returns StopDelay if set, otherwise util.CNPG_DEFAULT_STOP_DELAY
func getMaxStopDelayOrDefault(documentdb *dbpreview.DocumentDB) int32 {
	if documentdb.Spec.Timeouts.StopDelay != 0 {
		return documentdb.Spec.Timeouts.StopDelay
	}
	return util.CNPG_DEFAULT_STOP_DELAY
}

// parseMemoryToBytes converts a Kubernetes quantity string (e.g., "2Gi", "4096Mi")
// to bytes. Returns 0 if the string is empty or "0" (meaning unlimited/unset).
func parseMemoryToBytes(memoryStr string) int64 {
	if memoryStr == "" || memoryStr == "0" {
		return 0
	}
	qty, err := resource.ParseQuantity(memoryStr)
	if err != nil {
		return 0
	}
	return qty.Value()
}

// buildResourceRequirements constructs corev1.ResourceRequirements from a
// resolved component resource split. Returns empty requirements if nothing is set.
func buildResourceRequirements(component ComponentResource) corev1.ResourceRequirements {
	reqs := corev1.ResourceRequirements{}

	requests := corev1.ResourceList{}
	if quantity, ok := parseResourceQuantity(component.MemoryRequest); ok {
		requests[corev1.ResourceMemory] = quantity
	}
	if quantity, ok := parseResourceQuantity(component.CPURequest); ok {
		requests[corev1.ResourceCPU] = quantity
	}

	limits := corev1.ResourceList{}
	if quantity, ok := parseResourceQuantity(component.MemoryLimit); ok {
		limits[corev1.ResourceMemory] = quantity
	}
	if quantity, ok := parseResourceQuantity(component.CPULimit); ok {
		limits[corev1.ResourceCPU] = quantity
	}

	if len(requests) > 0 {
		reqs.Requests = requests
	}
	if len(limits) > 0 {
		reqs.Limits = limits
	}

	return reqs
}

func parseResourceQuantity(value string) (resource.Quantity, bool) {
	if value == "" || value == "0" {
		return resource.Quantity{}, false
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, false
	}
	return quantity, true
}

// parsePullPolicy converts a string to a corev1.PullPolicy.
// Returns empty string for unrecognized values.
func parsePullPolicy(value string) corev1.PullPolicy {
	switch corev1.PullPolicy(value) {
	case corev1.PullAlways, corev1.PullNever, corev1.PullIfNotPresent:
		return corev1.PullPolicy(value)
	default:
		return ""
	}
}

// imagePostgres returns spec.image.postgres or empty string when unset.
// Nil-safe.
func imagePostgres(documentdb *dbpreview.DocumentDB) string {
	if documentdb == nil || documentdb.Spec.Image == nil {
		return ""
	}
	return documentdb.Spec.Image.Postgres
}

// imageGateway returns spec.image.gateway or empty string when unset.
// Nil-safe.
func imageGateway(documentdb *dbpreview.DocumentDB) string {
	if documentdb == nil || documentdb.Spec.Image == nil {
		return ""
	}
	return documentdb.Spec.Image.Gateway
}

// pluginsSidecarInjectorName returns spec.plugins.sidecarInjectorName
// or empty string when unset. Nil-safe.
func pluginsSidecarInjectorName(documentdb *dbpreview.DocumentDB) string {
	if documentdb == nil || documentdb.Spec.Plugins == nil {
		return ""
	}
	return documentdb.Spec.Plugins.SidecarInjectorName
}

func postgresCertificates(documentdb *dbpreview.DocumentDB) *cnpgv1.CertificatesConfiguration {
	if documentdb.Spec.TLS == nil {
		return nil
	}
	return documentdb.Spec.TLS.Postgres
}

// toCNPGImagePullSecrets translates a list of corev1.LocalObjectReference
// (the Kubernetes-native shape used on spec.imagePullSecrets) into the
// CNPG-flavoured cnpgv1.LocalObjectReference shape that
// cnpgv1.ClusterSpec.ImagePullSecrets expects.
func toCNPGImagePullSecrets(secrets []corev1.LocalObjectReference) []cnpgv1.LocalObjectReference {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]cnpgv1.LocalObjectReference, 0, len(secrets))
	for _, s := range secrets {
		if s.Name == "" {
			continue
		}
		out = append(out, cnpgv1.LocalObjectReference{Name: s.Name})
	}
	return out
}

// applyPostgresProcessIdentity wires spec.postgres.uid / spec.postgres.gid
// onto the CNPG ClusterSpec. CNPG validates that both are set together;
// the CRD enforces the same invariant via XValidation on PostgresSpec.
func applyPostgresProcessIdentity(spec *cnpgv1.ClusterSpec, documentdb *dbpreview.DocumentDB) {
	if documentdb == nil || documentdb.Spec.Postgres == nil {
		return
	}
	pg := documentdb.Spec.Postgres
	if pg.UID != nil {
		spec.PostgresUID = *pg.UID
	}
	if pg.GID != nil {
		spec.PostgresGID = *pg.GID
	}
}

// applyIOUringSeccomp relaxes the postgres container seccomp profile when the
// IOUring feature gate is enabled. CNPG runs the postgres pods with
// seccompProfile=RuntimeDefault, but the container runtime strips the
// io_uring_{setup,enter,register} syscalls from that profile, so io_method=io_uring
// would otherwise crash with "could not setup io_uring queue: Operation not permitted".
//
// The operator references a Localhost seccomp profile that re-allows only the three
// io_uring syscalls. The profile path is operator-level configuration (the same
// decision applies to every DocumentDB on the cluster) and must be installed on every
// node that runs postgres pods (see the io-uring feature playground).
//
// No-op when the gate is disabled, so CNPG keeps its RuntimeDefault.
func applyIOUringSeccomp(spec *cnpgv1.ClusterSpec, documentdb *dbpreview.DocumentDB) {
	if !dbpreview.IsFeatureGateEnabled(documentdb, dbpreview.FeatureGateIOUring) {
		return
	}
	profile := cmp.Or(os.Getenv(util.IOURING_SECCOMP_PROFILE_ENV), util.DEFAULT_IOURING_SECCOMP_PROFILE)
	spec.SeccompProfile = &corev1.SeccompProfile{
		Type:             corev1.SeccompProfileTypeLocalhost,
		LocalhostProfile: pointer.String(profile),
	}
}

// buildPostgresConfiguration returns the cnpgv1.PostgresConfiguration block
// for the cluster.
//
// The operator declares the DocumentDB extension via CNPG's Extensions
// stanza (mounted from spec.image.documentDB as an ImageVolumeSource),
// sets a fixed AdditionalLibraries list, and applies a small set of
// operator-managed GUCs.
func buildPostgresConfiguration(documentdb *dbpreview.DocumentDB, extensionImageSource corev1.ImageVolumeSource, pgMemoryBytes int64) cnpgv1.PostgresConfiguration {
	pgHBA := []string{
		"host all all localhost trust",
		"hostssl replication streaming_replica all cert",
	}

	return cnpgv1.PostgresConfiguration{
		Extensions: []cnpgv1.ExtensionConfiguration{
			{
				Name:                 "documentdb",
				ImageVolumeSource:    extensionImageSource,
				DynamicLibraryPath:   []string{"lib"},
				ExtensionControlPath: []string{"share"},
				LdLibraryPath:        []string{"lib", "system"},
			},
		},
		AdditionalLibraries: []string{"pg_cron", "pg_documentdb_core", "pg_documentdb"},
		Parameters:          MergeParameters(documentdb, pgMemoryBytes),
		PgHBA:               pgHBA,
	}
}
