// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/resources/status"
	pgTime "github.com/cloudnative-pg/machinery/pkg/postgres/time"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	cnpg "github.com/documentdb/documentdb-operator/internal/cnpg"
	"github.com/documentdb/documentdb-operator/internal/engine"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

const (
	RequeueAfterShort = engine.RequeueAfterShort
	RequeueAfterLong  = engine.RequeueAfterLong

	// documentDBFinalizer ensures we can emit PV retention warnings before deletion completes
	documentDBFinalizer = "documentdb.io/pv-retention-finalizer"
)

// DocumentDBReconciler reconciles a DocumentDB object. The single-region
// lifecycle is implemented by the product-neutral engine.ClusterReconciler;
// this type supplies the DocumentDB adapter and the multi-region hooks.
type DocumentDBReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Config    *rest.Config
	Clientset kubernetes.Interface
	// Recorder emits Kubernetes events for this controller, including PV retention warnings during deletion.
	Recorder record.EventRecorder
	// SQLExecutor executes SQL commands against a CNPG cluster's primary pod.
	// Defaults to engine.NewPodSQLExecutor (real pod exec via SPDY). Override in tests
	// to inject canned responses without requiring a live Kubernetes cluster.
	SQLExecutor func(ctx context.Context, cluster *cnpgv1.Cluster, sqlCommand string) (string, error)
}

var reconcileMutex sync.Mutex

// engine returns the product-neutral reconciler bound to this reconciler's
// clients. It is built per call so struct-literal construction (as in tests)
// keeps working.
func (r *DocumentDBReconciler) engine() *engine.ClusterReconciler[*dbpreview.DocumentDB] {
	return &engine.ClusterReconciler[*dbpreview.DocumentDB]{
		Client:      r.Client,
		Scheme:      r.Scheme,
		Recorder:    r.Recorder,
		SQLExecutor: r.SQLExecutor,
		Adapter:     product.DocumentDBAdapter{},
		Replication: documentDBReplication{r},
		Mutex:       &reconcileMutex,
	}
}

// +kubebuilder:rbac:groups=documentdb.io,resources=dbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;update;patch
func (r *DocumentDBReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.engine().Reconcile(ctx, req)
}

// documentDBReplication wires DocumentDB multi-region replication into the engine.
type documentDBReplication struct{ r *DocumentDBReconciler }

func (h documentDBReplication) Context(ctx context.Context, documentdb *dbpreview.DocumentDB) (*util.ReplicationContext, error) {
	return util.GetReplicationContext(ctx, h.r.Client, *documentdb)
}

func (h documentDBReplication) AddToClusterSpec(ctx context.Context, documentdb *dbpreview.DocumentDB, rc *util.ReplicationContext, desired *cnpgv1.Cluster) error {
	return h.r.AddClusterReplicationToClusterSpec(ctx, documentdb, rc, desired)
}

func (h documentDBReplication) SyncChanges(ctx context.Context, current, desired *cnpgv1.Cluster, documentdb *dbpreview.DocumentDB, rc *util.ReplicationContext) ([]cnpg.JSONPatch, error, time.Duration) {
	return h.r.syncReplicationChanges(ctx, current, desired, documentdb, rc)
}

func (h documentDBReplication) ReconcilePrimary(ctx context.Context, documentdb *dbpreview.DocumentDB, rc *util.ReplicationContext, currentCnpgCluster *cnpgv1.Cluster) *ctrl.Result {
	logger := log.FromContext(ctx)
	if !rc.IsPrimary() || documentdb.Status.TargetPrimary == "" {
		return nil
	}
	// If these are different, we need to initiate a failover
	if documentdb.Status.TargetPrimary != currentCnpgCluster.Status.TargetPrimary {
		if err := Promote(ctx, h.r.Client, currentCnpgCluster.Namespace, currentCnpgCluster.Name, documentdb.Status.TargetPrimary); err != nil {
			logger.Error(err, "Failed to promote standby cluster to primary")
			return &ctrl.Result{RequeueAfter: RequeueAfterShort}
		}
	} else if documentdb.Status.TargetPrimary != documentdb.Status.LocalPrimary &&
		documentdb.Status.TargetPrimary == currentCnpgCluster.Status.CurrentPrimary {

		logger.Info("Marking failover as complete")
		documentdb.Status.LocalPrimary = currentCnpgCluster.Status.CurrentPrimary
		if err := h.r.Status().Update(ctx, documentdb); err != nil {
			logger.Error(err, "Failed to update DocumentDB status")
			return &ctrl.Result{RequeueAfter: RequeueAfterShort}
		}
	}
	return nil
}

func (h documentDBReplication) ReconcileNetworking(ctx context.Context, documentdb *dbpreview.DocumentDB, rc *util.ReplicationContext) *ctrl.Result {
	// Check for fleet-networking issues and attempt to remediate
	if !rc.IsAzureFleetNetworking() {
		return nil
	}
	deleted, imports, err := h.r.CleanupMismatchedServiceImports(ctx, documentdb.Namespace, rc)
	if err != nil {
		log.Log.Error(err, "Failed to cleanup ServiceImports")
		return &ctrl.Result{RequeueAfter: RequeueAfterShort}
	}
	if deleted {
		log.Log.Info("Deleted mismatched ServiceImports; requeuing to allow for proper recreation")
		return &ctrl.Result{RequeueAfter: RequeueAfterShort}
	}
	reconciled, err := h.r.ForceReconcileInternalServiceExports(ctx, documentdb.Namespace, rc, imports)
	if err != nil {
		log.Log.Error(err, "Failed to force reconcile InternalServiceExports")
		return &ctrl.Result{RequeueAfter: RequeueAfterShort}
	}
	if reconciled {
		log.Log.Info("Annotated InternalServiceExports for reconciliation; requeuing to allow fleet-networking to recreate ServiceImports")
		return &ctrl.Result{RequeueAfter: RequeueAfterLong}
	}
	return nil
}

// reconcileFinalizer handles the finalizer lifecycle; see engine.ClusterReconciler.ReconcileFinalizer.
func (r *DocumentDBReconciler) reconcileFinalizer(ctx context.Context, documentdb *dbpreview.DocumentDB) (bool, ctrl.Result, error) {
	return r.engine().ReconcileFinalizer(ctx, documentdb)
}

// emitPVRetentionWarning emits a warning event listing PVs that will be retained after deletion
func (r *DocumentDBReconciler) emitPVRetentionWarning(ctx context.Context, documentdb *dbpreview.DocumentDB) error {
	return r.engine().EmitPVRetentionWarning(ctx, documentdb)
}

// findPVsForDocumentDB finds all PV names associated with a DocumentDB cluster
// through the documentdb.io/cluster and documentdb.io/namespace PV labels.
func (r *DocumentDBReconciler) findPVsForDocumentDB(ctx context.Context, documentdb *dbpreview.DocumentDB) ([]string, error) {
	return r.engine().FindPVNames(ctx, documentdb)
}

func (r *DocumentDBReconciler) EnsureServiceAccountRoleAndRoleBinding(ctx context.Context, documentdb *dbpreview.DocumentDB, namespace string) error {
	return r.engine().EnsureRBAC(ctx, documentdb, namespace)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DocumentDBReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clientset == nil {
		return fmt.Errorf("Clientset must be configured: required for SQL execution against the CNPG primary")
	}

	if r.SQLExecutor == nil {
		r.SQLExecutor = engine.NewPodSQLExecutor(r.Client, r.Clientset, r.Config)
	}

	return r.engine().ControllerBuilder(mgr, "documentdb-controller").
		Owns(&cnpgv1.Publication{}).
		Owns(&cnpgv1.Subscription{}).
		Complete(r)
}

// COPIED FROM https://github.com/cloudnative-pg/cloudnative-pg/blob/release-1.25/internal/cmd/plugin/promote/promote.go
func Promote(ctx context.Context, cli client.Client,
	namespace, clusterName, serverName string,
) error {
	var cluster cnpgv1.Cluster

	log := log.FromContext(ctx)

	// Get the Cluster object
	err := cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: clusterName}, &cluster)
	if err != nil {
		return fmt.Errorf("cluster %s not found in namespace %s: %w", clusterName, namespace, err)
	}

	log.Info("Promoting new primary node", "serverName", serverName, "clusterName", clusterName)

	// If server name is equal to target primary, there is no need to promote
	// that instance
	if cluster.Status.TargetPrimary == serverName {
		fmt.Printf("%s is already the primary node in the cluster\n", serverName)
		return nil
	}

	// Check if the Pod exist
	var pod corev1.Pod
	err = cli.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serverName}, &pod)
	if err != nil {
		return fmt.Errorf("new primary node %s not found in namespace %s: %w", serverName, namespace, err)
	}

	// The Pod exists, let's update the cluster's status with the new target primary
	reconcileTargetPrimaryFunc := func(cluster *cnpgv1.Cluster) {
		cluster.Status.TargetPrimary = serverName
		cluster.Status.TargetPrimaryTimestamp = pgTime.GetCurrentTimestamp()
		cluster.Status.Phase = cnpgv1.PhaseSwitchover
		cluster.Status.PhaseReason = fmt.Sprintf("Switching over to %v", serverName)
	}
	if err := status.PatchWithOptimisticLock(ctx, cli, &cluster,
		reconcileTargetPrimaryFunc,
		status.SetClusterReadyCondition,
	); err != nil {
		return err
	}
	log.Info("Promotion in progress for ", "New primary", serverName, "cluster name", clusterName)
	return nil
}

// reconcilePVRecovery handles recovery from a retained PersistentVolume; see
// engine.ClusterReconciler.ReconcilePVRecovery.
func (r *DocumentDBReconciler) reconcilePVRecovery(ctx context.Context, documentdb *dbpreview.DocumentDB, namespace, cnpgClusterName string) (ctrl.Result, error) {
	return r.engine().ReconcilePVRecovery(ctx, documentdb, namespace, cnpgClusterName)
}

// parseExtensionVersionsFromOutput parses the output of the pg_available_extensions query.
func parseExtensionVersionsFromOutput(output string) (defaultVersion, installedVersion string, ok bool) {
	return engine.ParseExtensionVersions(output)
}

// handleExtensionUpgrade handles the ALTER EXTENSION lifecycle; see
// engine.ClusterReconciler.HandleExtensionUpgrade.
func (r *DocumentDBReconciler) handleExtensionUpgrade(ctx context.Context, currentCluster *cnpgv1.Cluster, documentdb *dbpreview.DocumentDB) error {
	return r.engine().HandleExtensionUpgrade(ctx, currentCluster, documentdb)
}

// updateImageStatus persists the current extension and gateway images into the DocumentDB status.
func (r *DocumentDBReconciler) updateImageStatus(ctx context.Context, documentdb *dbpreview.DocumentDB, cluster *cnpgv1.Cluster) error {
	return r.engine().UpdateImageStatus(ctx, documentdb, cluster)
}

// reconcileOtelConfigMap ensures the OTel Collector base config ConfigMap exists and is up-to-date.
func (r *DocumentDBReconciler) reconcileOtelConfigMap(ctx context.Context, documentdb *dbpreview.DocumentDB, namespace string) error {
	return r.engine().ReconcileOtelConfigMap(ctx, documentdb, namespace)
}

// deleteOtelConfigMap removes the OTel ConfigMap when monitoring is no longer configured.
func (r *DocumentDBReconciler) deleteOtelConfigMap(ctx context.Context, clusterName, namespace string) error {
	return r.engine().DeleteOtelConfigMap(ctx, clusterName, namespace)
}
