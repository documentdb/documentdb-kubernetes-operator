// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"cmp"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
	ctrl "sigs.k8s.io/controller-runtime"
)

func GetCnpgClusterSpec(req ctrl.Request, documentdb *dbpreview.DocumentDB, documentdb_image, serviceAccountName, storageClass string, isPrimaryRegion bool, log logr.Logger) *cnpgv1.Cluster {
	sidecarPluginName := documentdb.Spec.SidecarInjectorPluginName
	if sidecarPluginName == "" {
		sidecarPluginName = util.DEFAULT_SIDECAR_INJECTOR_PLUGIN
	}

	// Get the gateway image for this DocumentDB instance
	gatewayImage := util.GetGatewayImageForDocumentDB(documentdb)
	log.Info("Creating CNPG cluster with gateway image", "gatewayImage", gatewayImage, "documentdbName", documentdb.Name, "specGatewayImage", documentdb.Spec.GatewayImage)

	credentialSecretName := documentdb.Spec.DocumentDbCredentialSecret
	if credentialSecretName == "" {
		credentialSecretName = util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET
	}

	// Configure storage class - use specified storage class or nil for default
	var storageClassPointer *string
	if storageClass != "" {
		storageClassPointer = &storageClass
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
				Instances: documentdb.Spec.InstancesPerNode,
				ImageName: documentdb_image,
				StorageConfiguration: cnpgv1.StorageConfiguration{
					StorageClass: storageClassPointer, // Use configured storage class or default
					Size:         documentdb.Spec.Resource.Storage.PvcSize,
				},
				InheritedMetadata: getInheritedMetadataLabels(documentdb.Name),
				Plugins: func() []cnpgv1.PluginConfiguration {
					params := map[string]string{"gatewayImage": gatewayImage}
					// If TLS is ready, surface secret name to plugin so it can mount certs.
					if documentdb.Status.TLS != nil && documentdb.Status.TLS.Ready && documentdb.Status.TLS.SecretName != "" {
						params["gatewayTLSSecret"] = documentdb.Status.TLS.SecretName
					}
					return []cnpgv1.PluginConfiguration{{
						Name:       sidecarPluginName,
						Enabled:    pointer.Bool(true),
						Parameters: params,
					}}
				}(),
				PostgresUID: 105,
				PostgresGID: 108,
				PostgresConfiguration: cnpgv1.PostgresConfiguration{
					AdditionalLibraries: []string{"pg_cron", "pg_documentdb_core", "pg_documentdb"},
					Parameters: map[string]string{
						"cron.database_name":    "postgres",
						"max_replication_slots": "10",
						"max_wal_senders":       "10",
					},
					PgHBA: []string{
						"host all all 0.0.0.0/0 trust",
						"host all all ::0/0 trust",
						"host replication all all trust",
					},
				},
				Bootstrap: getBootstrapConfiguration(documentdb, isPrimaryRegion, log),
				LogLevel:  cmp.Or(documentdb.Spec.LogLevel, "info"),
				Backup: &cnpgv1.BackupConfiguration{
					VolumeSnapshot: &cnpgv1.VolumeSnapshotConfiguration{
						SnapshotOwnerReference: "backup", // Set owner reference to 'backup' so that snapshots are deleted when Backup resource is deleted
					},
					Target: cnpgv1.BackupTarget("primary"),
				},
				Affinity: documentdb.Spec.Affinity,
			}
			spec.MaxStopDelay = getMaxStopDelayOrDefault(documentdb)
			return spec
		}(),
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

	return getDefaultBootstrapConfiguration()
}

func getDefaultBootstrapConfiguration() *cnpgv1.BootstrapConfiguration {
	return &cnpgv1.BootstrapConfiguration{
		InitDB: &cnpgv1.BootstrapInitDB{
			PostInitSQL: []string{
				"CREATE EXTENSION documentdb CASCADE",
				"CREATE ROLE documentdb WITH LOGIN PASSWORD 'Admin100'",
				"ALTER ROLE documentdb WITH SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS",
			},
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
