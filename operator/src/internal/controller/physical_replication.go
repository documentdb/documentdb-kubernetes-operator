// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
	fleetv1alpha1 "go.goms.io/fleet-networking/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	demotionTokenPollInterval = 5 * time.Second
	demotionTokenWaitTimeout  = 10 * time.Minute
)

func (r *DocumentDBReconciler) AddClusterReplicationToClusterSpec(
	ctx context.Context,
	documentdb *dbpreview.DocumentDB,
	replicationContext *util.ReplicationContext,
	cnpgCluster *cnpgv1.Cluster,
) error {
	if replicationContext.IsAzureFleetNetworking() {
		err := r.CreateServiceImportAndExport(ctx, replicationContext, documentdb)
		if err != nil {
			return err
		}
	} else if replicationContext.IsIstioNetworking() {
		err := r.CreateIstioRemoteServices(ctx, replicationContext, documentdb)
		if err != nil {
			return err
		}
	}

	// No more errors possible, so we can safely edit the spec
	cnpgCluster.Name = replicationContext.Self

	if !replicationContext.IsPrimary() {
		cnpgCluster.Spec.InheritedMetadata.Labels[util.LABEL_REPLICATION_CLUSTER_TYPE] = "replica"
		cnpgCluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{
			PgBaseBackup: &cnpgv1.BootstrapPgBaseBackup{
				Source:   replicationContext.PrimaryCluster,
				Database: "postgres",
				Owner:    "postgres",
			},
		}
	} else if documentdb.Spec.ClusterReplication.HighAvailability {
		// If primary and HA we want a local standby and a slot for the WAL replica
		// TODO change to 2 when WAL replica is available
		cnpgCluster.Spec.Instances = 3
		// Restoring from backup won't have PostInitSQL configured
		if cnpgCluster.Spec.Bootstrap != nil && cnpgCluster.Spec.Bootstrap.InitDB != nil && cnpgCluster.Spec.Bootstrap.InitDB.PostInitSQL != nil {
			cnpgCluster.Spec.Bootstrap.InitDB.PostInitSQL = append(
				cnpgCluster.Spec.Bootstrap.InitDB.PostInitSQL,
				"select * from pg_create_physical_replication_slot('wal_replica');")
		}
		// Also need to configure quorum writes
		cnpgCluster.Spec.PostgresConfiguration.Synchronous = &cnpgv1.SynchronousReplicaConfiguration{
			Method:          cnpgv1.SynchronousReplicaConfigurationMethodAny,
			Number:          3,
			StandbyNamesPre: replicationContext.CreateStandbyNamesList(),
			DataDurability:  cnpgv1.DataDurabilityLevelRequired,
		}
		trueVal := true
		cnpgCluster.Spec.ReplicationSlots = &cnpgv1.ReplicationSlotsConfiguration{
			SynchronizeReplicas: &cnpgv1.SynchronizeReplicasConfiguration{
				Enabled: &trueVal,
			},
		}

		/* TODO re-enable when we have a WAL replica image
		walReplicaPluginName := documentdb.Spec.WalReplicaPluginName
		if walReplicaPluginName == "" {
			walReplicaPluginName = util.DEFAULT_WAL_REPLICA_PLUGIN
		}
		cnpgCluster.Spec.Plugins = append(cnpgCluster.Spec.Plugins,
			cnpgv1.PluginConfiguration{
				Name: walReplicaPluginName,
			})
		*/
	}

	cnpgCluster.Spec.ReplicaCluster = &cnpgv1.ReplicaClusterConfiguration{
		Source:  replicationContext.GetReplicationSource(),
		Primary: replicationContext.PrimaryCluster,
		Self:    replicationContext.Self,
	}

	if replicationContext.IsAzureFleetNetworking() {
		// need to create services for each of the other clusters
		cnpgCluster.Spec.Managed = &cnpgv1.ManagedConfiguration{
			Services: &cnpgv1.ManagedServices{
				Additional: []cnpgv1.ManagedService{},
			},
		}
		for serviceName := range replicationContext.GenerateOutgoingServiceNames(documentdb.Name, documentdb.Namespace) {
			cnpgCluster.Spec.Managed.Services.Additional = append(cnpgCluster.Spec.Managed.Services.Additional,
				cnpgv1.ManagedService{
					SelectorType: cnpgv1.ServiceSelectorTypeRW,
					ServiceTemplate: cnpgv1.ServiceTemplateSpec{
						ObjectMeta: cnpgv1.Metadata{
							Name: serviceName,
						},
					},
				})
		}
	}
	selfHost := replicationContext.Self + "-rw." + documentdb.Namespace + ".svc"
	cnpgCluster.Spec.ExternalClusters = []cnpgv1.ExternalCluster{
		{
			Name: replicationContext.Self,
			ConnectionParameters: map[string]string{
				"host":   selfHost,
				"port":   "5432",
				"dbname": "postgres",
				"user":   "postgres",
			},
		},
	}
	for clusterName, serviceName := range replicationContext.GenerateExternalClusterServices(documentdb.Name, documentdb.Namespace, replicationContext.IsAzureFleetNetworking()) {
		cnpgCluster.Spec.ExternalClusters = append(cnpgCluster.Spec.ExternalClusters, cnpgv1.ExternalCluster{
			Name: clusterName,
			ConnectionParameters: map[string]string{
				"host":   serviceName,
				"port":   "5432",
				"dbname": "postgres",
				"user":   "postgres",
			},
		})
	}

	return nil
}

func (r *DocumentDBReconciler) CreateIstioRemoteServices(ctx context.Context, replicationContext *util.ReplicationContext, documentdb *dbpreview.DocumentDB) error {
	// Create dummy -rw services for remote clusters so DNS resolution works
	// These services have non-matching selectors, so they have no local endpoints
	// Istio will automatically route traffic through the east-west gateway
	for _, remoteCluster := range replicationContext.Others {
		// Create the -rw (read-write/primary) service for each remote cluster
		serviceNameRW := remoteCluster + "-rw"
		foundServiceRW := &corev1.Service{}
		err := r.Get(ctx, types.NamespacedName{Name: serviceNameRW, Namespace: documentdb.Namespace}, foundServiceRW)
		if err != nil && errors.IsNotFound(err) {
			log.Log.Info("Creating Istio dummy service for remote cluster", "service", serviceNameRW, "cluster", remoteCluster)

			serviceRW := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceNameRW,
					Namespace: documentdb.Namespace,
					Labels: map[string]string{
						"cnpg.io/cluster": remoteCluster,
						"replica_type":    "primary",
					},
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "postgres",
							Port:       5432,
							Protocol:   corev1.ProtocolTCP,
							TargetPort: intstr.FromInt(5432),
						},
					},
					Selector: map[string]string{
						// Non-matching selector ensures no local endpoints
						"cnpg.io/cluster": "does-not-exist",
						"cnpg.io/podRole": "does-not-exist",
					},
					SessionAffinity: corev1.ServiceAffinityNone,
					Type:            corev1.ServiceTypeClusterIP,
				},
			}

			err = r.Create(ctx, serviceRW)
			if err != nil {
				return fmt.Errorf("failed to create Istio dummy service %s: %w", serviceNameRW, err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to check for existing service %s: %w", serviceNameRW, err)
		}
	}

	return nil
}

func (r *DocumentDBReconciler) CreateServiceImportAndExport(ctx context.Context, replicationContext *util.ReplicationContext, documentdb *dbpreview.DocumentDB) error {
	for serviceName := range replicationContext.GenerateOutgoingServiceNames(documentdb.Name, documentdb.Namespace) {
		foundServiceExport := &fleetv1alpha1.ServiceExport{}
		err := r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: documentdb.Namespace}, foundServiceExport)
		if errors.IsNotFound(err) {
			log.Log.Info("Service Export not found. Creating a new Service Export " + serviceName)

			// Service Export
			ringServiceExport := &fleetv1alpha1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceName,
					Namespace: documentdb.Namespace,
				},
			}
			err = r.Create(ctx, ringServiceExport)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// Below is true because this function is only called if we are fleet enabled
	for sourceServiceName := range replicationContext.GenerateIncomingServiceNames(documentdb.Name, documentdb.Namespace) {
		foundMCS := &fleetv1alpha1.MultiClusterService{}
		err := r.Get(ctx, types.NamespacedName{Name: sourceServiceName, Namespace: documentdb.Namespace}, foundMCS)
		if err != nil && errors.IsNotFound(err) {
			log.Log.Info("Multi Cluster Service not found. Creating a new Multi Cluster Service")
			// Multi Cluster Service
			foundMCS = &fleetv1alpha1.MultiClusterService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceServiceName,
					Namespace: documentdb.Namespace,
				},
				Spec: fleetv1alpha1.MultiClusterServiceSpec{
					ServiceImport: fleetv1alpha1.ServiceImportRef{
						Name: sourceServiceName,
					},
				},
			}
			err = r.Create(ctx, foundMCS)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *DocumentDBReconciler) TryUpdateCluster(ctx context.Context, current, desired *cnpgv1.Cluster, documentdb *dbpreview.DocumentDB, replicationContext *util.ReplicationContext) (error, time.Duration) {
	if current.Spec.ReplicaCluster == nil || desired.Spec.ReplicaCluster == nil {
		// FOR NOW assume that we aren't going to turn on or off physical replication
		return nil, -1
	}

	if current.Spec.ReplicaCluster.Self != desired.Spec.ReplicaCluster.Self {
		return fmt.Errorf("self cannot be changed"), time.Second * 60
	}

	// Create JSON patch operations for all replica cluster updates
	var patchOps []util.JSONPatch

	// Update if the primary has changed
	primaryChanged := current.Spec.ReplicaCluster.Primary != desired.Spec.ReplicaCluster.Primary
	if primaryChanged {
		err, refreshTime := r.getPrimaryChangePatchOps(ctx, &patchOps, current, desired, documentdb, replicationContext)
		if refreshTime > 0 || err != nil {
			return err, refreshTime
		}
	}

	// Update if the cluster list has changed
	replicasChanged := externalClusterNamesChanged(current.Spec.ExternalClusters, desired.Spec.ExternalClusters)
	if replicasChanged {
		log.Log.Info("Updating external clusters")
		getReplicasChangePatchOps(&patchOps, desired, replicationContext)
	}

	if len(patchOps) > 0 {
		patch, err := json.Marshal(patchOps)
		if err != nil {
			return fmt.Errorf("failed to marshal patch operations: %w", err), time.Second * 10
		}
		err = r.Client.Patch(ctx, current, client.RawPatch(types.JSONPatchType, patch))
		if err != nil {
			return err, time.Second * 10
		}
	}

	return nil, -1
}

func (r *DocumentDBReconciler) getPrimaryChangePatchOps(ctx context.Context, patchOps *[]util.JSONPatch, current, desired *cnpgv1.Cluster, documentdb *dbpreview.DocumentDB, replicationContext *util.ReplicationContext) (error, time.Duration) {
	if current.Spec.ReplicaCluster.Primary == current.Spec.ReplicaCluster.Self {
		// Primary => replica
		// demote
		*patchOps = append(*patchOps, util.JSONPatch{
			Op:    util.JSON_PATCH_OP_REPLACE,
			Path:  util.JSON_PATCH_PATH_REPLICA_CLUSTER,
			Value: desired.Spec.ReplicaCluster,
		})

		if documentdb.Spec.ClusterReplication.HighAvailability {
			// need to remove quorum writes and num instances
			// Only add remove operation if synchronous field exists, otherwise there's an error
			// TODO this wouldn't be true if our "wait for token" logic wasn't reliant on a failure
			if current.Spec.PostgresConfiguration.Synchronous != nil {
				*patchOps = append(*patchOps, util.JSONPatch{
					Op:   util.JSON_PATCH_OP_REMOVE,
					Path: util.JSON_PATCH_PATH_POSTGRES_CONFIG_SYNC,
				})
			}
			*patchOps = append(*patchOps, util.JSONPatch{
				Op:    util.JSON_PATCH_OP_REPLACE,
				Path:  util.JSON_PATCH_PATH_INSTANCES,
				Value: desired.Spec.Instances,
			})
			*patchOps = append(*patchOps, util.JSONPatch{
				Op:    util.JSON_PATCH_OP_REPLACE,
				Path:  util.JSON_PATCH_PATH_PLUGINS,
				Value: desired.Spec.Plugins,
			})
		}

		log.Log.Info("Applying patch for Primary => Replica transition", "cluster", current.Name)

		// push out the  promotion token when it's available
		nn := types.NamespacedName{Name: current.Name, Namespace: current.Namespace}
		go r.waitForDemotionTokenAndCreateService(nn, replicationContext)

	} else if desired.Spec.ReplicaCluster.Primary == current.Spec.ReplicaCluster.Self {
		// Replica => primary
		// Look for the token if this is a managed failover
		oldPrimaryAvailable := slices.Contains(
			replicationContext.Others,
			current.Spec.ReplicaCluster.Primary)

		replicaClusterConfig := desired.Spec.ReplicaCluster
		// If the old primary is available, we can read the token from it
		if oldPrimaryAvailable {
			token, err, refreshTime := r.ReadToken(ctx, documentdb.Namespace, replicationContext)
			if err != nil || refreshTime > 0 {
				return err, refreshTime
			}
			log.Log.Info("Token read successfully", "token", token)

			// Update the configuration with the token
			replicaClusterConfig.PromotionToken = token
		}

		*patchOps = append(*patchOps, util.JSONPatch{
			Op:    util.JSON_PATCH_OP_REPLACE,
			Path:  util.JSON_PATCH_PATH_REPLICA_CLUSTER,
			Value: replicaClusterConfig,
		})

		if documentdb.Spec.ClusterReplication.HighAvailability {
			// need to add second instance and wal replica
			*patchOps = append(*patchOps, util.JSONPatch{
				Op:    util.JSON_PATCH_OP_REPLACE,
				Path:  util.JSON_PATCH_PATH_POSTGRES_CONFIG,
				Value: desired.Spec.PostgresConfiguration,
			})
			*patchOps = append(*patchOps, util.JSONPatch{
				Op:    util.JSON_PATCH_OP_REPLACE,
				Path:  util.JSON_PATCH_PATH_INSTANCES,
				Value: desired.Spec.Instances,
			})
			*patchOps = append(*patchOps, util.JSONPatch{
				Op:    util.JSON_PATCH_OP_REPLACE,
				Path:  util.JSON_PATCH_PATH_PLUGINS,
				Value: desired.Spec.Plugins,
			})
			*patchOps = append(*patchOps, util.JSONPatch{
				Op:    util.JSON_PATCH_OP_REPLACE,
				Path:  util.JSON_PATCH_PATH_REPLICATION_SLOTS,
				Value: desired.Spec.ReplicationSlots,
			})
		}
		log.Log.Info("Applying patch for Replica => Primary transition", "cluster", current.Name, "hasToken", replicaClusterConfig.PromotionToken != "")
	} else {
		// Replica => replica
		*patchOps = append(*patchOps, util.JSONPatch{
			Op:    util.JSON_PATCH_OP_REPLACE,
			Path:  util.JSON_PATCH_PATH_REPLICA_CLUSTER,
			Value: desired.Spec.ReplicaCluster,
		})

		log.Log.Info("Applying patch for Replica => Replica transition", "cluster", current.Name)
	}

	return nil, -1
}

func externalClusterNamesChanged(currentClusters, desiredClusters []cnpgv1.ExternalCluster) bool {
	if len(currentClusters) != len(desiredClusters) {
		return true
	}

	if len(currentClusters) == 0 {
		return false
	}

	nameSet := make(map[string]bool, len(currentClusters))
	for _, cluster := range currentClusters {
		nameSet[cluster.Name] = true
	}

	for _, cluster := range desiredClusters {
		if found := nameSet[cluster.Name]; !found {
			return true
		}
		delete(nameSet, cluster.Name)
	}

	return len(nameSet) != 0
}

func getReplicasChangePatchOps(patchOps *[]util.JSONPatch, desired *cnpgv1.Cluster, replicationContext *util.ReplicationContext) {
	*patchOps = append(*patchOps, util.JSONPatch{
		Op:    util.JSON_PATCH_OP_REPLACE,
		Path:  util.JSON_PATCH_PATH_EXTERNAL_CLUSTERS,
		Value: desired.Spec.ExternalClusters,
	})
	if replicationContext.IsAzureFleetNetworking() {
		*patchOps = append(*patchOps, util.JSONPatch{
			Op:    util.JSON_PATCH_OP_REPLACE,
			Path:  util.JSON_PATCH_PATH_MANAGED_SERVICES,
			Value: desired.Spec.Managed.Services.Additional,
		})
	}
	if replicationContext.IsPrimary() {
		*patchOps = append(*patchOps, util.JSONPatch{
			Op:    util.JSON_PATCH_OP_REPLACE,
			Path:  util.JSON_PATCH_PATH_SYNCHRONOUS,
			Value: desired.Spec.PostgresConfiguration.Synchronous,
		})
	}
}

func (r *DocumentDBReconciler) ReadToken(ctx context.Context, namespace string, replicationContext *util.ReplicationContext) (string, error, time.Duration) {
	tokenServiceName := "promotion-token"

	// If we are not using cross-cloud networking, we only need to read the token from the configmap
	if !replicationContext.IsAzureFleetNetworking() && !replicationContext.IsIstioNetworking() {
		configMap := &corev1.ConfigMap{}
		err := r.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, configMap)
		if err != nil {
			return "", err, time.Second * 10
		}
		if configMap.Data["index.html"] == "" {
			return "", fmt.Errorf("token not found in configmap"), time.Second * 10
		}
		return configMap.Data["index.html"], nil, -1
	}

	// For Istio, create a dummy service so DNS resolution works
	if replicationContext.IsIstioNetworking() {
		foundService := &corev1.Service{}
		err := r.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, foundService)
		if err != nil && errors.IsNotFound(err) {
			log.Log.Info("Creating Istio dummy service for promotion token", "service", tokenServiceName)

			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tokenServiceName,
					Namespace: namespace,
					Labels: map[string]string{
						"app": tokenServiceName,
					},
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							Protocol:   corev1.ProtocolTCP,
							TargetPort: intstr.FromInt(80),
						},
					},
					Selector: map[string]string{
						// Non-matching selector ensures no local endpoints
						"app": "does-not-exist",
					},
				},
			}

			err = r.Create(ctx, service)
			if err != nil && !errors.IsAlreadyExists(err) {
				return "", fmt.Errorf("failed to create Istio dummy service for promotion token: %w", err), time.Second * 10
			}
		} else if err != nil {
			return "", fmt.Errorf("failed to check for existing service %s: %w", tokenServiceName, err), time.Second * 10
		}

		// Read token via HTTP through Istio service mesh
		tokenRequestUrl := fmt.Sprintf("http://%s.%s.svc", tokenServiceName, namespace)
		resp, err := http.Get(tokenRequestUrl)
		if err != nil {
			return "", fmt.Errorf("failed to get token from service: %w", err), time.Second * 10
		}
		defer resp.Body.Close()

		token, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read token: %w", err), time.Second * 10
		}

		return string(token[:]), nil, -1
	}

	// This is the AzureFleet case
	foundMCS := &fleetv1alpha1.MultiClusterService{}
	err := r.Get(ctx, types.NamespacedName{Name: tokenServiceName, Namespace: namespace}, foundMCS)
	if err != nil && errors.IsNotFound(err) {
		foundMCS = &fleetv1alpha1.MultiClusterService{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tokenServiceName,
				Namespace: namespace,
			},
			Spec: fleetv1alpha1.MultiClusterServiceSpec{
				ServiceImport: fleetv1alpha1.ServiceImportRef{
					Name: tokenServiceName,
				},
			},
		}
		err = r.Create(ctx, foundMCS)
		if err != nil {
			return "", err, time.Second * 10
		}
	} else if err != nil {
		return "", err, time.Second * 10
	}

	tokenRequestUrl := fmt.Sprintf("http://%s-%s.fleet-system.svc", namespace, tokenServiceName)
	resp, err := http.Get(tokenRequestUrl)
	if err != nil {
		return "", fmt.Errorf("failed to get token from service: %w", err), time.Second * 10
	}

	token, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token: %w", err), time.Second * 10
	}

	// Need to convert byte array to byte slice before converting to string
	return string(token[:]), nil, -1
}

func (r *DocumentDBReconciler) waitForDemotionTokenAndCreateService(clusterNN types.NamespacedName, replicationContext *util.ReplicationContext) {
	ctx := context.Background()
	ticker := time.NewTicker(demotionTokenPollInterval)
	timeout := time.NewTimer(demotionTokenWaitTimeout)
	defer ticker.Stop()
	defer timeout.Stop()

	for {
		select {
		case <-ticker.C:
			done, err := r.ensureTokenServiceResources(ctx, clusterNN, replicationContext)
			if err != nil {
				log.Log.Error(err, "Failed to create token service resources", "cluster", clusterNN.Name)
			}
			if done {
				return
			}
		case <-timeout.C:
			log.Log.Info("Timed out waiting for demotion token", "cluster", clusterNN.Name, "timeout", demotionTokenWaitTimeout)
			return
		}
	}
}

// Returns true when token service resources are ready
func (r *DocumentDBReconciler) ensureTokenServiceResources(ctx context.Context, clusterNN types.NamespacedName, replicationContext *util.ReplicationContext) (bool, error) {
	cluster := &cnpgv1.Cluster{}
	if err := r.Client.Get(ctx, clusterNN, cluster); err != nil {
		return false, err
	}

	token := cluster.Status.DemotionToken
	if token == "" {
		return false, nil
	}

	tokenServiceName := "promotion-token"
	labels := map[string]string{
		"app": tokenServiceName,
	}

	// Create ConfigMap with token and nginx config
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tokenServiceName,
			Namespace: clusterNN.Namespace,
		},
		Data: map[string]string{
			"index.html": token,
		},
	}

	err := r.Client.Create(ctx, configMap)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			configMap.Data["index.html"] = token
			err = r.Client.Update(ctx, configMap)
			if err != nil {
				return false, fmt.Errorf("failed to update token ConfigMap: %w", err)
			}
		} else {
			return false, fmt.Errorf("failed to create token ConfigMap: %w", err)
		}
	}

	// When not using cross-cloud networking, just transfer with the configmap
	if !replicationContext.IsAzureFleetNetworking() && !replicationContext.IsIstioNetworking() {
		return true, nil
	}

	// Create nginx Pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tokenServiceName,
			Namespace: clusterNN.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "nginx",
					Image: "nginx:alpine",
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 80,
							Protocol:      corev1.ProtocolTCP,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      tokenServiceName,
							MountPath: "usr/share/nginx/html",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: tokenServiceName,
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: tokenServiceName,
							},
						},
					},
				},
			},
		},
	}

	err = r.Client.Create(ctx, pod)
	if err != nil && !errors.IsAlreadyExists(err) {
		return false, fmt.Errorf("failed to create nginx Pod: %w", err)
	}

	// Create Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tokenServiceName,
			Namespace: clusterNN.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	err = r.Client.Create(ctx, service)
	if err != nil && !errors.IsAlreadyExists(err) {
		return false, fmt.Errorf("failed to create Service: %w", err)
	}

	// Create ServiceExport only for fleet networking
	if replicationContext.IsAzureFleetNetworking() {
		serviceExport := &fleetv1alpha1.ServiceExport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tokenServiceName,
				Namespace: clusterNN.Namespace,
			},
		}

		err = r.Client.Create(ctx, serviceExport)
		if err != nil && !errors.IsAlreadyExists(err) {
			return false, fmt.Errorf("failed to create ServiceExport: %w", err)
		}
	}

	return true, nil
}
