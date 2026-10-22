// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package engine contains the product-neutral reconcilers. A product supplies a
// product.ClusterAdapter for its custom resource and gets the full single-region
// lifecycle: gateway Service, RBAC, CNPG cluster render and sync, PV retention
// and recovery, OTel collector config, extension upgrade, status, TLS
// certificates, persistent volumes, and backups. Multi-region replication is an
// optional extension point (ReplicationHooks).
package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cnpg "github.com/documentdb/documentdb-operator/internal/cnpg"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

const (
	RequeueAfterShort = 10 * time.Second
	RequeueAfterLong  = 30 * time.Second

	// CNPGClusterHealthyPhase is the CNPG cluster status phase indicating a healthy cluster.
	// This value is from CNPG's internal status representation.
	CNPGClusterHealthyPhase = "Cluster in healthy state"
)

// SQLExecutor executes a SQL command against a CNPG cluster's primary and
// returns psql's tabular output.
type SQLExecutor func(ctx context.Context, cluster *cnpgv1.Cluster, sqlCommand string) (string, error)

// ReplicationHooks is the optional multi-region extension point of the cluster
// reconciler. Without hooks the reconciler runs every cluster as a single-region
// primary.
type ReplicationHooks[T client.Object] interface {
	// Context resolves the replication role of obj in its topology.
	Context(ctx context.Context, obj T) (*util.ReplicationContext, error)
	// AddToClusterSpec adds replication settings to the desired CNPG cluster;
	// called only when the context is replicating.
	AddToClusterSpec(ctx context.Context, obj T, rc *util.ReplicationContext, desired *cnpgv1.Cluster) error
	// SyncChanges returns extra CNPG patch operations for replication changes,
	// or a positive requeue delay to wait before syncing.
	SyncChanges(ctx context.Context, current, desired *cnpgv1.Cluster, obj T, rc *util.ReplicationContext) ([]cnpg.JSONPatch, error, time.Duration)
	// ReconcilePrimary handles failover/promotion after the cluster is synced.
	// A non-nil result stops reconciliation and is returned.
	ReconcilePrimary(ctx context.Context, obj T, rc *util.ReplicationContext, current *cnpgv1.Cluster) *ctrl.Result
	// ReconcileNetworking remediates cross-cluster networking after status is
	// updated. A non-nil result stops reconciliation and is returned.
	ReconcileNetworking(ctx context.Context, obj T, rc *util.ReplicationContext) *ctrl.Result
}

// ClusterReconciler is the product-neutral reconciler of a product's cluster
// custom resource.
type ClusterReconciler[T client.Object] struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// SQLExecutor runs SQL on the CNPG primary; see NewPodSQLExecutor.
	SQLExecutor SQLExecutor
	// Adapter maps the product custom resource onto the neutral model.
	Adapter product.ClusterAdapter[T]
	// Replication is optional; nil means single-region.
	Replication ReplicationHooks[T]
	// Mutex, when set, serializes reconciliations.
	Mutex *sync.Mutex
}

func (r *ClusterReconciler[T]) profile() product.ProductProfile { return r.Adapter.Profile() }

func (r *ClusterReconciler[T]) replicationContext(ctx context.Context, obj T) (*util.ReplicationContext, error) {
	if r.Replication != nil {
		return r.Replication.Context(ctx, obj)
	}
	spec := r.Adapter.Spec(obj)
	return util.NewSingleRegionContext(obj.GetName(), spec.Environment, spec.StorageClass), nil
}

// GatewayPort is the gateway port exposed by the Service and in the
// connection string: the profile port, or the GATEWAY_PORT env default.
func GatewayPort(p product.ProductProfile) int32 {
	if p.Gateway.Port != 0 {
		return p.Gateway.Port
	}
	return util.GetPortFor(util.GATEWAY_PORT)
}

// Reconcile drives the custom resource toward its desired state.
func (r *ClusterReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Mutex != nil {
		r.Mutex.Lock()
		defer r.Mutex.Unlock()
	}

	logger := log.FromContext(ctx)
	p := r.profile()

	obj := r.Adapter.New()
	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info(p.Name + " resource not found. Cleaning up associated resources.")
			if err := r.Cleanup(ctx, req); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get "+p.Name+" resource")
		return ctrl.Result{}, err
	}

	// Handle finalizer lifecycle (add on create, remove on delete)
	if done, result, err := r.ReconcileFinalizer(ctx, obj); done || err != nil {
		return result, err
	}

	replicationContext, err := r.replicationContext(ctx, obj)
	if err != nil {
		logger.Error(err, "Failed to determine replication context")
		return ctrl.Result{}, err
	}

	if replicationContext.IsNotPresent() {
		logger.Info(p.Name + " instance is not part of the replication setup; skipping reconciliation and deleting any present resources")
		if err := r.Cleanup(ctx, req); err != nil {
			return ctrl.Result{}, err
		}
		owner := metav1.ObjectMeta{Name: obj.GetName(), Namespace: obj.GetNamespace(), UID: obj.GetUID()}
		if err := util.DeleteOwnedResources(ctx, r.Client, owner); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	spec := r.Adapter.Spec(obj)
	var serviceIP string

	// Only create/manage the service if a service type is configured
	if spec.ServiceType != "" {
		serviceType := corev1.ServiceTypeClusterIP
		if spec.ServiceType == "LoadBalancer" {
			serviceType = corev1.ServiceTypeLoadBalancer // Public LoadBalancer service
		}

		apiVersion, kind := obj.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()
		svc := util.BuildGatewayService(util.GatewayServiceOptions{
			Name:            p.Naming.ServiceName(obj.GetName()),
			Namespace:       req.Namespace,
			ClusterName:     obj.GetName(),
			OwnerAPIVersion: apiVersion,
			OwnerKind:       kind,
			OwnerUID:        obj.GetUID(),
			Port:            GatewayPort(p),
			EndpointEnabled: replicationContext.EndpointEnabled(),
			Environment:     replicationContext.Environment,
			ServiceType:     serviceType,
		})

		foundService, err := util.UpsertService(ctx, r.Client, svc)
		if err != nil {
			logger.Error(err, "Failed to create "+p.Name+" Service; Requeuing.")
			return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
		}

		serviceIP, err = util.EnsureServiceIP(ctx, foundService)
		if err != nil {
			logger.Info(p.Name + " Service IP not assigned, pausing until update posted.")
			return ctrl.Result{}, nil
		}
	}

	// Ensure App ServiceAccount, Role and RoleBindings are created
	if err := r.EnsureRBAC(ctx, obj, req.Namespace); err != nil {
		logger.Info("Failed to create ServiceAccount, Role and RoleBinding; Requeuing.")
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	// create the CNPG Cluster
	intent := r.Adapter.Intent(obj)

	currentCnpgCluster := &cnpgv1.Cluster{}
	desiredCnpgCluster := cnpg.GetCnpgClusterSpecFromIntent(req, intent, obj.GetName(), replicationContext.StorageClass, replicationContext.IsPrimary(), logger)

	if r.Replication != nil && replicationContext.IsReplicating() {
		err = r.Replication.AddToClusterSpec(ctx, obj, replicationContext, desiredCnpgCluster)
		if err != nil {
			logger.Error(err, "Failed to add physical replication features cnpg Cluster spec")
			return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
		}
	}

	// Handle PV recovery lifecycle (create temp PVC before CNPG, cleanup after healthy)
	if result, err := r.ReconcilePVRecovery(ctx, obj, req.Namespace, desiredCnpgCluster.Name); err != nil {
		logger.Error(err, "Failed to reconcile PV recovery")
		return result, err
	} else if result.Requeue || result.RequeueAfter > 0 {
		return result, nil
	}

	// Reconcile OTel resources when monitoring is enabled.
	// The sidecar itself is added/removed via CNPG plugin parameters;
	// the operator triggers a rolling restart (via restart annotation)
	// and CNPG manages the pod rollout.
	monitoringEnabled := spec.MonitoringEnabled()
	if monitoringEnabled {
		if err := r.ReconcileOtelConfigMap(ctx, obj, req.Namespace); err != nil {
			logger.Error(err, "Failed to reconcile OTel ConfigMap")
			return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
		}
	}

	if err := r.Get(ctx, types.NamespacedName{Name: desiredCnpgCluster.Name, Namespace: req.Namespace}, currentCnpgCluster); err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, desiredCnpgCluster); err != nil {
				logger.Error(err, "Failed to create CNPG Cluster")
				return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
			}
			logger.Info("CNPG Cluster created successfully", "Cluster.Name", desiredCnpgCluster.Name, "Namespace", desiredCnpgCluster.Namespace)
			return ctrl.Result{RequeueAfter: RequeueAfterLong}, nil
		}
		logger.Error(err, "Failed to get CNPG Cluster")
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	var replicationOps []cnpg.JSONPatch
	if r.Replication != nil {
		// Build replication patch ops (may perform side effects such as token reads and service creation).
		var requeueTime time.Duration
		replicationOps, err, requeueTime = r.Replication.SyncChanges(ctx, currentCnpgCluster, desiredCnpgCluster, obj, replicationContext)
		if err != nil {
			logger.Error(err, "Failed to build replication patch ops")
		}
		if requeueTime > 0 {
			return ctrl.Result{RequeueAfter: requeueTime}, nil
		}
	}

	// Sync all CNPG Cluster changes in one atomic patch (images + plugins + replication)
	if err := cnpg.SyncCnpgCluster(ctx, r.Client, currentCnpgCluster, desiredCnpgCluster, replicationOps); err != nil {
		logger.Error(err, "Failed to sync CNPG Cluster spec")
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	// Delete the OTel ConfigMap only after the CNPG Cluster spec no longer
	// references it. If cluster sync fails, retaining it keeps the existing
	// sidecar configuration functional and allows a subsequent reconcile to retry.
	if !monitoringEnabled {
		if err := r.DeleteOtelConfigMap(ctx, obj.GetName(), req.Namespace); err != nil {
			logger.Error(err, "Failed to clean up OTel ConfigMap")
			return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
		}
	}

	if slices.Contains(currentCnpgCluster.Status.InstancesStatus[cnpgv1.PodHealthy], currentCnpgCluster.Status.CurrentPrimary) && replicationContext.IsPrimary() {
		if role := intent.Product.Postgres.ReplicationAdminRole; role != "" {
			// Check if permissions have already been granted
			output, err := r.SQLExecutor(ctx, currentCnpgCluster, cnpg.ReplicationRoleCheckSQL(role))
			if err != nil {
				logger.Error(err, "Failed to check if permissions already granted")
				return ctrl.Result{RequeueAfter: RequeueAfterLong}, nil
			}

			if !strings.Contains(output, "(1 row)") {
				if _, err := r.SQLExecutor(ctx, currentCnpgCluster, cnpg.ReplicationRoleGrantSQL(role)); err != nil {
					logger.Error(err, "Failed to grant permissions to streaming_replica")
					return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
				}
			}
		}
	}

	if r.Replication != nil {
		if result := r.Replication.ReconcilePrimary(ctx, obj, replicationContext, currentCnpgCluster); result != nil {
			return *result, nil
		}
	}

	// Update status with CNPG Cluster phase and connection string
	if err := r.Get(ctx, types.NamespacedName{Name: desiredCnpgCluster.Name, Namespace: req.Namespace}, currentCnpgCluster); err == nil {
		status := r.Adapter.Status(obj)
		statusChanged := false

		// Update phase status from CNPG Cluster
		if currentCnpgCluster.Status.Phase != "" && status.Phase != currentCnpgCluster.Status.Phase {
			status.Phase = currentCnpgCluster.Status.Phase
			statusChanged = true
		}

		// Update connection string if primary and service IP available
		if replicationContext.IsPrimary() && serviceIP != "" {
			trustTLS := status.TLS != nil && status.TLS.Ready
			newConnStr := r.Adapter.ConnectionString(obj, serviceIP, GatewayPort(p), trustTLS)
			if status.ConnectionString != newConnStr {
				status.ConnectionString = newConnStr
				statusChanged = true
			}
		}

		if statusChanged {
			r.Adapter.SetStatus(obj, status)
			if err := r.Status().Update(ctx, obj); err != nil {
				logger.Error(err, "Failed to update "+p.Name+" status")
			}
		}
	}

	if r.Replication != nil {
		if result := r.Replication.ReconcileNetworking(ctx, obj, replicationContext); result != nil {
			return *result, nil
		}
	}

	// Check if the extension needs ALTER EXTENSION UPDATE
	if err := r.HandleExtensionUpgrade(ctx, currentCnpgCluster, obj); err != nil {
		logger.Error(err, "Failed to handle "+p.Name+" extension upgrade")
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	// Don't requeue again unless there is a change
	return ctrl.Result{}, nil
}

// Cleanup removes the per-cluster ServiceAccount, Role and RoleBinding when the
// custom resource is gone.
func (r *ClusterReconciler[T]) Cleanup(ctx context.Context, req ctrl.Request) error {
	log := log.FromContext(ctx)

	// Cleanup ServiceAccount, Role and RoleBinding
	if err := util.DeleteRoleBinding(ctx, r.Client, req.Name, req.Namespace); err != nil {
		log.Error(err, "Failed to delete RoleBinding during cleanup", "RoleBindingName", req.Name)
		// Continue with other cleanup even if this fails
	}

	if err := util.DeleteServiceAccount(ctx, r.Client, req.Name, req.Namespace); err != nil {
		log.Error(err, "Failed to delete ServiceAccount during cleanup", "ServiceAccountName", req.Name)
		// Continue with other cleanup even if this fails
	}

	if err := util.DeleteRole(ctx, r.Client, req.Name, req.Namespace); err != nil {
		log.Error(err, "Failed to delete Role during cleanup", "RoleName", req.Name)
		// Continue with other cleanup even if this fails
	}

	log.Info("Cleanup process completed", r.profile().Name, req.Name, "Namespace", req.Namespace)
	return nil
}

// ReconcileFinalizer handles the finalizer lifecycle:
//   - If resource is being deleted: process deletion and remove finalizer
//   - If finalizer is missing: add it
//   - Otherwise: continue with normal reconciliation
//
// Returns (done, result, error) where done=true means reconciliation should stop.
func (r *ClusterReconciler[T]) ReconcileFinalizer(ctx context.Context, obj T) (bool, ctrl.Result, error) {
	logger := log.FromContext(ctx)
	finalizer := r.profile().Naming.Finalizer()

	// Handle deletion
	if !obj.GetDeletionTimestamp().IsZero() {
		if !controllerutil.ContainsFinalizer(obj, finalizer) {
			// Finalizer already removed, nothing to do
			return true, ctrl.Result{}, nil
		}

		// Check if PVs will be retained and emit warning
		if r.Adapter.Spec(obj).RetainsPVs() {
			if err := r.EmitPVRetentionWarning(ctx, obj); err != nil {
				// Log but don't block deletion
				logger.Error(err, "Failed to emit PV retention warning, continuing with deletion")
			}
		}

		// Remove finalizer to allow deletion to proceed
		controllerutil.RemoveFinalizer(obj, finalizer)
		if err := r.Update(ctx, obj); err != nil {
			logger.Error(err, "Failed to remove finalizer")
			return true, ctrl.Result{}, err
		}

		logger.Info("Removed finalizer, deletion will proceed")
		return true, ctrl.Result{}, nil
	}

	// Ensure finalizer is present for non-deleting resources
	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		controllerutil.AddFinalizer(obj, finalizer)
		if err := r.Update(ctx, obj); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return true, ctrl.Result{}, err
		}
		logger.Info("Added finalizer to " + r.profile().Name)
		return true, ctrl.Result{Requeue: true}, nil
	}

	// Finalizer is present and resource is not being deleted, continue reconciliation
	return false, ctrl.Result{}, nil
}

// EmitPVRetentionWarning emits a warning event listing PVs that will be retained after deletion.
func (r *ClusterReconciler[T]) EmitPVRetentionWarning(ctx context.Context, obj T) error {
	logger := log.FromContext(ctx)

	if r.Recorder == nil {
		logger.Info("Event recorder not configured, skipping PV retention warning")
		return nil
	}

	pvNames, err := r.FindPVNames(ctx, obj)
	if err != nil {
		return fmt.Errorf("failed to find PVs: %w", err)
	}

	if len(pvNames) == 0 {
		logger.V(1).Info("No PVs found for " + r.profile().Name)
		return nil
	}

	// Emit actionable warning event
	message := fmt.Sprintf(
		"PersistentVolumes retained after cluster deletion (policy=Retain). "+
			"To delete when no longer needed: kubectl delete pv %s",
		strings.Join(pvNames, " "))

	r.Recorder.Event(obj, corev1.EventTypeWarning, "PVsRetained", message)
	logger.Info("Emitted PV retention warning", "pvCount", len(pvNames), "pvNames", pvNames)

	return nil
}

// FindPVNames finds all PV names associated with a cluster through the product
// cluster/namespace labels the PV reconciler stamps on PVs. This works in both
// single and multi-cluster scenarios where CNPG cluster names may differ from
// the custom resource name.
func (r *ClusterReconciler[T]) FindPVNames(ctx context.Context, obj T) ([]string, error) {
	naming := r.profile().Naming
	pvList := &corev1.PersistentVolumeList{}
	if err := r.List(ctx, pvList,
		client.MatchingLabels{
			naming.ClusterLabel():   obj.GetName(),
			naming.NamespaceLabel(): obj.GetNamespace(),
		},
	); err != nil {
		return nil, err
	}

	pvNames := make([]string, 0, len(pvList.Items))
	for _, pv := range pvList.Items {
		pvNames = append(pvNames, pv.Name)
	}

	return pvNames, nil
}

// EnsureRBAC creates the per-cluster Role, ServiceAccount and RoleBinding.
func (r *ClusterReconciler[T]) EnsureRBAC(ctx context.Context, obj T, namespace string) error {
	log := log.FromContext(ctx)
	kind := r.profile().Name
	name := obj.GetName()

	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"pods", "services", "endpoints"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
	}

	if err := util.CreateRole(ctx, r.Client, name, namespace, rules); err != nil {
		log.Error(err, "Failed to create Role for "+kind, kind+".Name", name, "Namespace", namespace)
		return err
	}

	if err := util.CreateServiceAccount(ctx, r.Client, name, namespace); err != nil {
		log.Error(err, "Failed to create ServiceAccount for "+kind, kind+".Name", name, "Namespace", namespace)
		return err
	}

	if err := util.CreateRoleBinding(ctx, r.Client, name, namespace); err != nil {
		log.Error(err, "Failed to create RoleBinding for "+kind, kind+".Name", name, "Namespace", namespace)
		return err
	}

	return nil
}

// ReconcileOtelConfigMap ensures the OTel Collector base config ConfigMap exists and is up-to-date.
func (r *ClusterReconciler[T]) ReconcileOtelConfigMap(ctx context.Context, obj T, namespace string) error {
	logger := log.FromContext(ctx)
	cmName := otelcfg.ConfigMapName(obj.GetName())

	cm := &corev1.ConfigMap{}
	cm.Name = cmName
	cm.Namespace = namespace

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		// Set owner reference so the ConfigMap is garbage-collected with the custom resource.
		if err := controllerutil.SetControllerReference(obj, cm, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference: %w", err)
		}

		configData, err := otelcfg.GenerateConfigMapDataWithOptions(cnpg.OTelOptions(r.profile()), obj.GetName(), namespace, r.Adapter.Spec(obj).Monitoring)
		if err != nil {
			return fmt.Errorf("failed to generate OTel config: %w", err)
		}
		cm.Data = configData
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile OTel ConfigMap %s: %w", cmName, err)
	}
	if result != controllerutil.OperationResultNone {
		logger.Info("OTel ConfigMap reconciled", "name", cmName, "operation", result)
	}
	return nil
}

// DeleteOtelConfigMap removes the OTel ConfigMap when monitoring is no longer configured.
func (r *ClusterReconciler[T]) DeleteOtelConfigMap(ctx context.Context, clusterName, namespace string) error {
	logger := log.FromContext(ctx)
	cmName := otelcfg.ConfigMapName(clusterName)

	cm := &corev1.ConfigMap{}
	cm.Name = cmName
	cm.Namespace = namespace

	err := r.Delete(ctx, cm)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete OTel ConfigMap %s: %w", cmName, err)
	}
	logger.Info("OTel ConfigMap deleted", "name", cmName)
	return nil
}

// ControllerBuilder returns the controller builder watching the custom resource,
// its gateway Service, and its CNPG Cluster. Products may add more watches
// before calling Complete.
func (r *ClusterReconciler[T]) ControllerBuilder(mgr ctrl.Manager, name string) *builder.Builder {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.Adapter.New()).
		Owns(&corev1.Service{}, builder.WithPredicates(ServicePrefixPredicate(r.profile().Naming.ServicePrefix))).
		Owns(&cnpgv1.Cluster{}, builder.WithPredicates(ClusterInstanceStatusChangedPredicate())).
		Named(name)
}

// SetupWithManager registers the reconciler as controller name.
func (r *ClusterReconciler[T]) SetupWithManager(mgr ctrl.Manager, name string) error {
	if r.SQLExecutor == nil {
		return fmt.Errorf("SQLExecutor must be configured: required for SQL execution against the CNPG primary")
	}
	return r.ControllerBuilder(mgr, name).Complete(r)
}
