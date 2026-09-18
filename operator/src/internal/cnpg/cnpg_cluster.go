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
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
	ctrl "sigs.k8s.io/controller-runtime"
)

// GetCnpgClusterSpec renders the Cluster for a DocumentDB instance. The
// documentdbImage argument overrides the resolved extension image; pass "" to
// use the image resolved from the instance. req and serviceAccountName are
// accepted for call-site compatibility but no longer consumed: object
// coordinates now come from the intent's Identity, and the service account is
// not read by the renderer.
func GetCnpgClusterSpec(req ctrl.Request, documentdb *dbpreview.DocumentDB, documentdbImage, serviceAccountName, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	intent := product.DocumentDBAdapter{}.ToClusterIntent(documentdb)
	if documentdbImage != "" {
		intent.Images.PostgresExtension = documentdbImage
	}
	return GetCnpgClusterSpecFromIntent(intent, storageClass, isPrimaryRegion, log)
}

// GetCnpgClusterSpecFromIntent renders a CNPG Cluster from a product-neutral
// ClusterIntent plus reconcile-time inputs (storage class, region role) that
// are not part of any product's spec. This is the seam the reconciler drives:
// every render input comes from these arguments rather than from loose
// parameters or product-specific lookups.
func GetCnpgClusterSpecFromIntent(intent product.ClusterIntent, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	split := ComputeResourceSplitFromResource(intent.Resource, intent.Monitoring.Enabled, DefaultSplitConfig())

	sidecarPluginName := intent.Plugins.SidecarInjectorName

	gatewayImage := intent.Images.Gateway
	log.Info("Creating CNPG cluster with gateway image", "gatewayImage", gatewayImage, "documentdbName", intent.Identity.Name)

	credentialSecretName := intent.CredentialSecret

	// Configure storage class - use specified storage class or nil for default
	var storageClassPointer *string
	if storageClass != "" {
		storageClassPointer = &storageClass
	}

	// Set ImageVolumeSource.PullPolicy for the extension image when configured.
	// This addresses the fact that ImageVolume sources DO support pull policies
	// (via corev1.ImageVolumeSource.PullPolicy), unlike regular container images
	// which only support pull policies on container specs.
	extensionImageSource := corev1.ImageVolumeSource{Reference: intent.Images.PostgresExtension}
	if pullPolicy := parsePullPolicy(os.Getenv(util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV)); pullPolicy != "" {
		extensionImageSource.PullPolicy = pullPolicy
	}

	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      intent.Identity.Name,
			Namespace: intent.Identity.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         intent.Identity.APIVersion,
					Kind:               intent.Identity.Kind,
					Name:               intent.Identity.Name,
					UID:                intent.Identity.UID,
					Controller:         &[]bool{true}[0], // This cluster is controlled by the DocumentDB instance
					BlockOwnerDeletion: &[]bool{true}[0], // Block DocumentDB deletion until cluster is deleted
				},
			},
		},
		Spec: func() cnpgv1.ClusterSpec {
			spec := cnpgv1.ClusterSpec{
				Instances:           intent.Topology.InstancesPerNode,
				ImageName:           intent.Images.Postgres,
				ImagePullSecrets:    toCNPGImagePullSecrets(intent.Images.PullSecrets),
				PrimaryUpdateMethod: cnpgv1.PrimaryUpdateMethodSwitchover,
				StorageConfiguration: cnpgv1.StorageConfiguration{
					StorageClass: storageClassPointer, // Use configured storage class or default
					Size:         intent.Storage.PvcSize,
				},
				InheritedMetadata: getInheritedMetadataLabels(intent.Identity.Name),
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
					if intent.TLS.GatewaySecretName != "" {
						params["gatewayTLSSecret"] = intent.TLS.GatewaySecretName
					}
					// Pass monitoring parameters to plugin for OTel sidecar injection.
					// Sidecar is only injected when monitoring is enabled.
					// Config hash triggers operator-initiated rolling restart on config changes.
					if split.MonitoringEnabled {
						params["otelCollectorImage"] = util.DEFAULT_OTEL_COLLECTOR_IMAGE
						params["otelConfigMapName"] = otelcfg.ConfigMapName(intent.Identity.Name)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_MEMORY_REQUEST, split.OTel.MemoryRequest)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_MEMORY_LIMIT, split.OTel.MemoryLimit)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_CPU_REQUEST, split.OTel.CPURequest)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_CPU_LIMIT, split.OTel.CPULimit)
						if promPort := otelcfg.ResolvePrometheusPort(intent.Monitoring); promPort > 0 {
							params["prometheusPort"] = fmt.Sprintf("%d", promPort)
						}
						// Compute config hash for change detection. The operator triggers a
						// rolling restart (via restart annotation) when plugin parameters
						// change, ensuring pods pick up new config.
						if configData, err := otelcfg.GenerateConfigMapData(intent.Identity.Name, intent.Identity.Namespace, intent.Monitoring); err == nil {
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
				PostgresConfiguration: buildPostgresConfiguration(MergeParametersResolved(intent.Postgres.Parameters, intent.FeatureGates, split.PostgresMemoryBytes), extensionImageSource),
				Bootstrap:             bootstrapConfigurationFromIntent(intent, isPrimaryRegion, log),
				LogLevel:              cmp.Or(intent.LogLevel, "info"),
				Certificates:          intent.TLS.PostgresCertificates,
				Backup: &cnpgv1.BackupConfiguration{
					VolumeSnapshot: &cnpgv1.VolumeSnapshotConfiguration{
						SnapshotOwnerReference: "backup", // Set owner reference to 'backup' so that snapshots are deleted when Backup resource is deleted
					},
					Target: cnpgv1.BackupTarget("primary"),
				},
				Affinity:  intent.Topology.Affinity,
				Resources: buildResourceRequirements(split.Postgres),
			}
			spec.MaxStopDelay = intent.Timeouts.StopDelay
			applyPostgresProcessIdentity(&spec, intent)
			applyIOUringSeccomp(&spec, intent)
			applyOtelMonitorRoleFromIntent(&spec, intent.Monitoring.Enabled)

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

func bootstrapConfigurationFromIntent(intent product.ClusterIntent, isPrimaryRegion bool, log logr.Logger) *cnpgv1.BootstrapConfiguration {
	if isPrimaryRegion && intent.Bootstrap.Recovery != nil {
		recovery := intent.Bootstrap.Recovery

		// Handle backup recovery
		if recovery.BackupName != "" {
			log.Info("DocumentDB cluster will be bootstrapped from backup", "backupName", recovery.BackupName)
			return &cnpgv1.BootstrapConfiguration{
				Recovery: &cnpgv1.BootstrapRecovery{
					Backup: &cnpgv1.BackupSource{
						LocalObjectReference: cnpgv1.LocalObjectReference{Name: recovery.BackupName},
					},
				},
			}
		}

		// Handle PV recovery (via temporary PVC created by the controller)
		if recovery.PersistentVolumeName != "" {
			tempPVCName := util.TempPVCNameForPVRecovery(intent.Identity.Name)
			log.Info("DocumentDB cluster will be bootstrapped from PV via temp PVC",
				"pvName", recovery.PersistentVolumeName, "tempPVC", tempPVCName)
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

	return defaultBootstrapConfigurationFromIntent(intent)
}

// getBootstrapConfiguration adapts a DocumentDB instance onto the intent-based
// bootstrap builder. Retained for direct callers that hold the custom resource.
func getBootstrapConfiguration(documentdb *dbpreview.DocumentDB, isPrimaryRegion bool, log logr.Logger) *cnpgv1.BootstrapConfiguration {
	return bootstrapConfigurationFromIntent(product.DocumentDBAdapter{}.ToClusterIntent(documentdb), isPrimaryRegion, log)
}

func defaultBootstrapConfigurationFromIntent(intent product.ClusterIntent) *cnpgv1.BootstrapConfiguration {
	postInitSQL := []string{
		"CREATE EXTENSION documentdb CASCADE",
		"CREATE ROLE documentdb WITH LOGIN PASSWORD 'Admin100'",
		"ALTER ROLE documentdb WITH SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS",
	}
	if len(intent.Postgres.PostInitSQL) > 0 {
		postInitSQL = append(postInitSQL, intent.Postgres.PostInitSQL...)
	}
	return &cnpgv1.BootstrapConfiguration{
		InitDB: &cnpgv1.BootstrapInitDB{
			PostInitSQL: postInitSQL,
		},
	}
}

// getDefaultBootstrapConfiguration adapts a DocumentDB instance onto the
// intent-based default bootstrap builder. Retained for direct callers.
func getDefaultBootstrapConfiguration(documentdb *dbpreview.DocumentDB) *cnpgv1.BootstrapConfiguration {
	return defaultBootstrapConfigurationFromIntent(product.DocumentDBAdapter{}.ToClusterIntent(documentdb))
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
func applyPostgresProcessIdentity(spec *cnpgv1.ClusterSpec, intent product.ClusterIntent) {
	if intent.Postgres.UID != nil {
		spec.PostgresUID = *intent.Postgres.UID
	}
	if intent.Postgres.GID != nil {
		spec.PostgresGID = *intent.Postgres.GID
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
func applyIOUringSeccomp(spec *cnpgv1.ClusterSpec, intent product.ClusterIntent) {
	if !intent.FeatureGates.IOUring {
		return
	}
	profile := cmp.Or(os.Getenv(util.IOURING_SECCOMP_PROFILE_ENV), util.DEFAULT_IOURING_SECCOMP_PROFILE)
	spec.SeccompProfile = &corev1.SeccompProfile{
		Type:             corev1.SeccompProfileTypeLocalhost,
		LocalhostProfile: pointer.String(profile),
	}
}

// applyOtelMonitorRole declares the PostgreSQL identity used by the OTel
// Collector sidecar. When monitoring is disabled, the role remains declared
// with ensure=absent so CNPG removes any role left by an earlier configuration.
//
// PostgreSQL host authentication currently uses trust, so a generated password
// would not be checked. Disable the role password explicitly until authentication
// is tightened rather than creating an unused credential and widening Secret RBAC.
func applyOtelMonitorRoleFromIntent(spec *cnpgv1.ClusterSpec, monitoringEnabled bool) {
	if spec.Managed == nil {
		spec.Managed = &cnpgv1.ManagedConfiguration{}
	}
	role := absentOtelMonitorRole()
	if monitoringEnabled {
		// The current health query is SELECT 1, so the role needs LOGIN only
		// and is not granted broad monitoring memberships.
		role = cnpgv1.RoleConfiguration{
			Name:            otelcfg.MonitorRoleName,
			Comment:         "Dedicated role for the OTel Collector monitoring sidecar",
			Ensure:          cnpgv1.EnsurePresent,
			Login:           true,
			DisablePassword: true,
			// Set the CNPG/CRD-defaulted fields explicitly so the desired role
			// matches the API-server-defaulted form stored on the live cluster,
			// keeping SyncCnpgCluster's diff stable (no perpetual re-patching).
			ConnectionLimit: -1,
			Inherit:         pointer.Bool(true),
		}
	}
	spec.Managed.Roles = append(spec.Managed.Roles, role)
}

func absentOtelMonitorRole() cnpgv1.RoleConfiguration {
	return cnpgv1.RoleConfiguration{
		Name:            otelcfg.MonitorRoleName,
		Ensure:          cnpgv1.EnsureAbsent,
		ConnectionLimit: -1,
		Inherit:         pointer.Bool(true),
	}
}

// for the cluster.
//
// The operator declares the DocumentDB extension via CNPG's Extensions
// stanza (mounted from spec.image.documentDB as an ImageVolumeSource),
// sets a fixed AdditionalLibraries list, and applies a small set of
// operator-managed GUCs.
func buildPostgresConfiguration(parameters map[string]string, extensionImageSource corev1.ImageVolumeSource) cnpgv1.PostgresConfiguration {
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
		Parameters:          parameters,
		PgHBA:               pgHBA,
	}
}
