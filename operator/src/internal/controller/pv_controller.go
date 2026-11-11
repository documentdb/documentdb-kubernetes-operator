// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/engine"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// PersistentVolumeReconciler reconciles PersistentVolume objects
// to set their ReclaimPolicy and mount options based on the associated DocumentDB configuration.
// The logic lives in engine.PersistentVolumeReconciler.
type PersistentVolumeReconciler struct {
	client.Client
}

func (r *PersistentVolumeReconciler) engine() *engine.PersistentVolumeReconciler[*dbv1.DocumentDB] {
	return &engine.PersistentVolumeReconciler[*dbv1.DocumentDB]{Client: r.Client, Adapter: product.DocumentDBAdapter{}}
}

// Package-level RBAC also covers PVC lifecycle operations performed by the
// DocumentDB reconciler.
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

func (r *PersistentVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.engine().Reconcile(ctx, req)
}

// applyDesiredPVConfiguration applies the desired reclaim policy, mount options, and labels to a PV.
// Returns true if any changes were made.
func (r *PersistentVolumeReconciler) applyDesiredPVConfiguration(ctx context.Context, pv *corev1.PersistentVolume, documentdb *dbv1.DocumentDB) bool {
	return r.engine().ApplyDesiredPVConfiguration(ctx, pv, documentdb)
}

// provisionerSupportsMountOptions checks if the PV's storage class provisioner supports mount options.
func (r *PersistentVolumeReconciler) provisionerSupportsMountOptions(ctx context.Context, pv *corev1.PersistentVolume) bool {
	return r.engine().ProvisionerSupportsMountOptions(ctx, pv)
}

// containsAllMountOptions checks if all desired mount options are present in current options
func containsAllMountOptions(current, desired []string) bool {
	return engine.ContainsAllMountOptions(current, desired)
}

// mergeMountOptions merges desired mount options into current, avoiding duplicates.
func mergeMountOptions(current, desired []string) []string {
	return engine.MergeMountOptions(current, desired)
}

// findDocumentDBForPV traverses the ownership chain to find the DocumentDB
// associated with a PersistentVolume:
// PV.claimRef -> PVC -> (ownerRef) CNPG Cluster -> (ownerRef) DocumentDB
func (r *PersistentVolumeReconciler) findDocumentDBForPV(ctx context.Context, pv *corev1.PersistentVolume) (*dbv1.DocumentDB, error) {
	documentdb, _, err := r.engine().FindOwnerForPV(ctx, pv)
	return documentdb, err
}

// findCNPGClusterOwner finds the CNPG Cluster that owns the given PVC
func (r *PersistentVolumeReconciler) findCNPGClusterOwner(ctx context.Context, pvc *corev1.PersistentVolumeClaim) *cnpgv1.Cluster {
	return r.engine().FindCNPGClusterOwner(ctx, pvc)
}

// findDocumentDBOwner finds the DocumentDB that owns the given CNPG Cluster
func (r *PersistentVolumeReconciler) findDocumentDBOwner(ctx context.Context, cluster *cnpgv1.Cluster) *dbv1.DocumentDB {
	documentdb, _ := r.engine().FindClusterOwner(ctx, cluster)
	return documentdb
}

// isCNPGClusterOwnerRef checks if an owner reference refers to a CNPG Cluster
func isCNPGClusterOwnerRef(ownerRef metav1.OwnerReference) bool {
	return engine.IsCNPGClusterOwnerRef(ownerRef)
}

// getDesiredReclaimPolicy returns the reclaim policy based on DocumentDB configuration
func (r *PersistentVolumeReconciler) getDesiredReclaimPolicy(documentdb *dbv1.DocumentDB) corev1.PersistentVolumeReclaimPolicy {
	return engine.DesiredReclaimPolicy(documentdb.Spec.Resource.Storage.PersistentVolumeReclaimPolicy)
}

// pvPredicate filters PV events to only process bound PVs
func pvPredicate() predicate.Predicate {
	return engine.BoundPVPredicate()
}

// SetupWithManager sets up the controller with the Manager
func (r *PersistentVolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return r.engine().SetupWithManager(mgr, "pv-controller")
}

// documentDBPVRelevantPredicate triggers PV reconciliation when a DocumentDB
// change affects the reclaim policy or the installed schema version.
func documentDBPVRelevantPredicate() predicate.Predicate {
	return (&PersistentVolumeReconciler{}).engine().PVRelevantPredicate()
}

// findPVsForDocumentDB finds all PVs associated with a DocumentDB and returns reconcile requests for them.
func (r *PersistentVolumeReconciler) findPVsForDocumentDB(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.engine().MapClusterToPVs(ctx, obj)
}
