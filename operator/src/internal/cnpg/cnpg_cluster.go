// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
	ctrl "sigs.k8s.io/controller-runtime"
)

// GetCnpgClusterSpec renders the Cluster for a DocumentDB instance. The
// documentdbImage argument overrides the resolved extension image; pass "" to
// use the image resolved from the instance. Retained for callers that supply an
// explicit extension image.
func GetCnpgClusterSpec(req ctrl.Request, documentdb *dbv1.DocumentDB, documentdbImage, serviceAccountName, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	intent := product.DocumentDBAdapter{}.ToClusterIntent(documentdb)
	if documentdbImage != "" {
		intent.Images.PostgresExtension = documentdbImage
	}
	return GetCnpgClusterSpecFromIntent(req, intent, serviceAccountName, storageClass, isPrimaryRegion, log)
}

// GetCnpgClusterSpecFromIntent renders a CNPG Cluster from a product-neutral
// ClusterIntent. This is the seam every product drives: all product-varying
// values come from the intent and its embedded ProductProfile, so the renderer
// has no knowledge of any product custom resource.
func GetCnpgClusterSpecFromIntent(req ctrl.Request, intent product.ClusterIntent, serviceAccountName, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	split := ComputeResourceSplitFor(intent.Resources, monitoringEnabled(intent.Monitoring), DefaultSplitConfig())

	sidecarPluginName := intent.SidecarInjectorPlugin

	gatewayImage := intent.Images.Gateway
	log.Info("Creating CNPG cluster with gateway image", "gatewayImage", gatewayImage, "name", intent.Identity.Name, "product", intent.Product.Name)

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
	extensionImageSource := corev1.ImageVolumeSource{
		Reference:  intent.Images.PostgresExtension,
		PullPolicy: intent.Images.PostgresExtensionPullPolicy,
	}

	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         intent.Identity.APIVersion,
					Kind:               intent.Identity.Kind,
					Name:               intent.Identity.Name,
					UID:                intent.Identity.UID,
					Controller:         &[]bool{true}[0], // This cluster is controlled by the product instance
					BlockOwnerDeletion: &[]bool{true}[0], // Block owner deletion until cluster is deleted
				},
			},
		},
		Spec: func() cnpgv1.ClusterSpec {
			spec := cnpgv1.ClusterSpec{
				Instances:           intent.Topology.Instances,
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
						util.PLUGIN_PARAM_GATEWAY_IMAGE:     gatewayImage,
						util.PLUGIN_PARAM_CREDENTIAL_SECRET: credentialSecretName,
					}
					if pullPolicy := intent.Images.GatewayPullPolicy; pullPolicy != "" {
						params[util.PLUGIN_PARAM_GATEWAY_IMAGE_PULL_POLICY] = pullPolicy
					}
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_MEMORY_REQUEST, split.Gateway.MemoryRequest)
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_MEMORY_LIMIT, split.Gateway.MemoryLimit)
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_CPU_REQUEST, split.Gateway.CPURequest)
					addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_CPU_LIMIT, split.Gateway.CPULimit)
					addGatewayContractParams(params, intent.Product.Gateway)
					// If TLS is ready, surface secret name to plugin so it can mount certs.
					if intent.TLS.GatewaySecret != "" {
						params[util.PLUGIN_PARAM_GATEWAY_TLS_SECRET] = intent.TLS.GatewaySecret
					}
					// Pass monitoring parameters to plugin for OTel sidecar injection.
					// Sidecar is only injected when monitoring is enabled.
					// Config hash triggers operator-initiated rolling restart on config changes.
					if split.MonitoringEnabled {
						params[util.PLUGIN_PARAM_OTEL_COLLECTOR_IMAGE] = util.OtelCollectorImage()
						params[util.PLUGIN_PARAM_OTEL_CONFIG_MAP_NAME] = otelcfg.ConfigMapName(intent.Identity.Name)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_PASSWORD_SECRET, intent.Product.Postgres.MonitoringPasswordSecret)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_MEMORY_REQUEST, split.OTel.MemoryRequest)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_MEMORY_LIMIT, split.OTel.MemoryLimit)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_CPU_REQUEST, split.OTel.CPURequest)
						addPluginParamIfSet(params, util.PLUGIN_PARAM_OTEL_CPU_LIMIT, split.OTel.CPULimit)
						if promPort := otelcfg.ResolvePrometheusPort(intent.Monitoring); promPort > 0 {
							params[util.PLUGIN_PARAM_PROMETHEUS_PORT] = fmt.Sprintf("%d", promPort)
						}
						// Compute config hash for change detection. The operator triggers a
						// rolling restart (via restart annotation) when plugin parameters
						// change, ensuring pods pick up new config.
						if configData, err := otelcfg.GenerateConfigMapDataWithOptions(OTelOptions(intent.Product), intent.Identity.Name, req.Namespace, intent.Monitoring); err == nil {
							params[util.PLUGIN_PARAM_OTEL_CONFIG_HASH] = otelcfg.HashConfigMapData(configData)
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
				PostgresConfiguration: buildPostgresConfiguration(intent, extensionImageSource, split.PostgresMemoryBytes),
				Bootstrap:             bootstrapConfigurationFromIntent(intent, isPrimaryRegion, log),
				LogLevel:              cmp.Or(intent.Postgres.LogLevel, "info"),
				Certificates:          intent.TLS.Postgres,
				Backup: &cnpgv1.BackupConfiguration{
					VolumeSnapshot: &cnpgv1.VolumeSnapshotConfiguration{
						SnapshotOwnerReference: "backup", // Set owner reference to 'backup' so that snapshots are deleted when Backup resource is deleted
					},
					Target: cnpgv1.BackupTarget("primary"),
				},
				Affinity:  intent.Topology.Affinity,
				Resources: buildResourceRequirements(split.Postgres),
			}
			spec.MaxStopDelay = stopDelayOrDefault(intent.Postgres.StopDelay)
			applyPostgresProcessIdentity(&spec, intent)
			applyIOUringSeccomp(&spec, intent)
			applyOtelMonitorRole(&spec, intent.Monitoring, intent.Product.Postgres.MonitoringPasswordSecret)

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
			log.Info("Cluster will be bootstrapped from backup", "product", intent.Product.Name, "backupName", recovery.BackupName)
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
			log.Info("Cluster will be bootstrapped from PV via temp PVC", "product", intent.Product.Name,
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
func getBootstrapConfiguration(documentdb *dbv1.DocumentDB, isPrimaryRegion bool, log logr.Logger) *cnpgv1.BootstrapConfiguration {
	return bootstrapConfigurationFromIntent(product.DocumentDBAdapter{}.ToClusterIntent(documentdb), isPrimaryRegion, log)
}

func defaultBootstrapConfigurationFromIntent(intent product.ClusterIntent) *cnpgv1.BootstrapConfiguration {
	postInitSQL := append([]string{}, intent.Product.Postgres.BootstrapSQL...)
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
func getDefaultBootstrapConfiguration(documentdb *dbv1.DocumentDB) *cnpgv1.BootstrapConfiguration {
	return defaultBootstrapConfigurationFromIntent(product.DocumentDBAdapter{}.ToClusterIntent(documentdb))
}

// getMaxStopDelayOrDefault returns StopDelay if set, otherwise util.CNPG_DEFAULT_STOP_DELAY
func getMaxStopDelayOrDefault(documentdb *dbv1.DocumentDB) int32 {
	return stopDelayOrDefault(documentdb.Spec.Timeouts.StopDelay)
}

// stopDelayOrDefault returns stopDelay if set, otherwise util.CNPG_DEFAULT_STOP_DELAY.
func stopDelayOrDefault(stopDelay int32) int32 {
	if stopDelay != 0 {
		return stopDelay
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
// Password authentication is an explicit product choice backed by an existing
// Secret. Without one, retain the password-disabled DocumentDB default.
func applyOtelMonitorRole(spec *cnpgv1.ClusterSpec, monitoring *dbv1.MonitoringSpec, passwordSecret string) {
	if spec.Managed == nil {
		spec.Managed = &cnpgv1.ManagedConfiguration{}
	}
	role := absentOtelMonitorRole()
	if monitoringEnabled(monitoring) {
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
		if passwordSecret != "" {
			role.DisablePassword = false
			role.PasswordSecret = &cnpgv1.LocalObjectReference{Name: passwordSecret}
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

// buildPostgresConfiguration renders the PostgreSQL configuration for the
// cluster from the product profile: the product extension is declared via CNPG's
// Extensions stanza (mounted from the extension image as an ImageVolumeSource),
// alongside the product's AdditionalLibraries and pg_hba, plus the merged
// operator-managed GUCs.
func buildPostgresConfiguration(intent product.ClusterIntent, extensionImageSource corev1.ImageVolumeSource, pgMemoryBytes int64) cnpgv1.PostgresConfiguration {
	pg := intent.Product.Postgres
	return cnpgv1.PostgresConfiguration{
		Extensions: []cnpgv1.ExtensionConfiguration{
			{
				Name:                 pg.Extension.Name,
				ImageVolumeSource:    extensionImageSource,
				DynamicLibraryPath:   pg.Extension.DynamicLibraryPath,
				ExtensionControlPath: pg.Extension.ExtensionControlPath,
				LdLibraryPath:        pg.Extension.LdLibraryPath,
			},
		},
		AdditionalLibraries: pg.AdditionalLibraries,
		Parameters:          MergeParametersFor(intent, pgMemoryBytes),
		PgHBA:               pg.PgHBA,
	}
}

// addGatewayContractParams forwards a product's non-default gateway contract to
// the sidecar injector. Unset fields and values equal to the injector's
// (DocumentDB gateway) defaults are omitted, matching the injector's own
// normalization so existing parameter maps are unchanged and SyncCnpgCluster
// does not fight the injector's defaulting webhook.
func addGatewayContractParams(params map[string]string, gw product.GatewayProfile) {
	if gw.ContainerName != util.PLUGIN_DEFAULT_GATEWAY_CONTAINER_NAME {
		addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_CONTAINER_NAME, gw.ContainerName)
	}
	if gw.Port > 0 && gw.Port != util.PLUGIN_DEFAULT_GATEWAY_PORT {
		params[util.PLUGIN_PARAM_GATEWAY_PORT] = strconv.FormatInt(int64(gw.Port), 10)
	}
	if gw.Args != nil {
		// json.Marshal of a []string cannot fail.
		args, _ := json.Marshal(gw.Args)
		params[util.PLUGIN_PARAM_GATEWAY_ARGS] = string(args)
	}
	if gw.UsernameEnv != util.PLUGIN_DEFAULT_GATEWAY_USERNAME_ENV {
		addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_USERNAME_ENV, gw.UsernameEnv)
	}
	if gw.PasswordEnv != util.PLUGIN_DEFAULT_GATEWAY_PASSWORD_ENV {
		addPluginParamIfSet(params, util.PLUGIN_PARAM_GATEWAY_PASSWORD_ENV, gw.PasswordEnv)
	}
}

// OTelOptions maps the product naming onto the OTel collector config options.
func OTelOptions(p product.ProductProfile) otelcfg.Options {
	return otelcfg.Options{
		ClusterAttribute: p.Naming.MetricClusterAttribute,
		Generator:        p.Naming.OperatorName,
		HealthMetricName: p.Naming.HealthMetricName,
	}
}
