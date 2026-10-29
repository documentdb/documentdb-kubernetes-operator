// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/documentdb/documentdb-operator/internal/product"
)

const (
	// cnpgAPIVersionPrefix is the prefix for CNPG API versions
	cnpgAPIVersionPrefix = "postgresql.cnpg.io"

	// ownerRefKindCluster is the Kind for CNPG Cluster owner references
	ownerRefKindCluster = "Cluster"
)

// SecurityMountOptions defines the mount options applied to PVs for security hardening:
// - nodev: Prevents device files from being interpreted on the filesystem
// - nosuid: Prevents setuid/setgid bits from taking effect
// - noexec: Prevents execution of binaries on the filesystem
//
// NOTE: These mount options are compatible with most CSI drivers and storage providers.
// However, some storage classes may not support these options, which could cause PV
// binding issues or pod startup failures. If you encounter issues with PV binding after
// deploying the operator, verify your storage provider supports these mount options.
// Common compatible providers: Azure Disk, AWS EBS, GCE PD, most NFS implementations.
var SecurityMountOptions = []string{"nodev", "noexec", "nosuid"}

// unsupportedMountOptionsProvisioners lists storage provisioners that do not support
// mount options. These are typically local/dev environment provisioners used in
// kind, minikube, or similar local Kubernetes clusters.
// When a PV uses one of these provisioners, security mount options will be skipped
// to avoid PV binding failures.
var unsupportedMountOptionsProvisioners = []string{
	"rancher.io/local-path",    // kind default local-path-provisioner
	"k8s.io/minikube-hostpath", // minikube default hostpath provisioner
}

// PersistentVolumeReconciler sets the reclaim policy, security mount options,
// ownership labels and schema-version annotation of PVs that back a product
// cluster.
type PersistentVolumeReconciler[T client.Object] struct {
	client.Client
	Adapter product.ClusterAdapter[T]
}

func (r *PersistentVolumeReconciler[T]) naming() product.NamingProfile {
	return r.Adapter.Profile().Naming
}

// Reconcile applies the desired configuration to one PersistentVolume.
func (r *PersistentVolumeReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	kind := r.naming().Kind

	// Fetch the PersistentVolume
	pv := &corev1.PersistentVolume{}
	if err := r.Get(ctx, req.NamespacedName, pv); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get PersistentVolume")
		return ctrl.Result{}, err
	}

	// Find the associated custom resource through the ownership chain:
	// PV -> PVC -> CNPG Cluster -> custom resource
	obj, found, err := r.FindOwnerForPV(ctx, pv)
	if err != nil {
		logger.Error(err, "Failed to find "+kind+" for PV")
		return ctrl.Result{}, err
	}

	if !found {
		logger.V(1).Info("PV is not associated with a "+kind+" cluster, skipping", "pv", pv.Name)
		return ctrl.Result{}, nil
	}

	// Apply desired configuration to PV
	needsUpdate := r.ApplyDesiredPVConfiguration(ctx, pv, obj)

	if needsUpdate {
		if err := r.Update(ctx, pv); err != nil {
			logger.Error(err, "Failed to update PV")
			return ctrl.Result{}, err
		}

		logger.Info("Successfully updated PV",
			"pv", pv.Name,
			"reclaimPolicy", pv.Spec.PersistentVolumeReclaimPolicy,
			"mountOptions", pv.Spec.MountOptions)
	}

	return ctrl.Result{}, nil
}

// ApplyDesiredPVConfiguration applies the desired reclaim policy, mount options, and labels to a PV.
// Returns true if any changes were made.
func (r *PersistentVolumeReconciler[T]) ApplyDesiredPVConfiguration(ctx context.Context, pv *corev1.PersistentVolume, obj T) bool {
	logger := log.FromContext(ctx)
	naming := r.naming()
	ownerKey := strings.ToLower(naming.Kind)
	clusterLabel, namespaceLabel, schemaAnnotation := naming.ClusterLabel(), naming.NamespaceLabel(), naming.SchemaVersionAnnotation()
	needsUpdate := false

	// Ensure PV is labeled with the custom resource name.
	// This label is stable across single and multi-cluster scenarios (where the
	// CNPG cluster name may differ from the custom resource name due to hashing).
	if pv.Labels == nil {
		pv.Labels = make(map[string]string)
	}
	if pv.Labels[clusterLabel] != obj.GetName() {
		logger.Info("PV cluster label needs update",
			"pv", pv.Name,
			"currentLabel", pv.Labels[clusterLabel],
			"desiredLabel", obj.GetName(),
			ownerKey, obj.GetName())
		pv.Labels[clusterLabel] = obj.GetName()
		needsUpdate = true
	}
	if pv.Labels[namespaceLabel] != obj.GetNamespace() {
		logger.Info("PV namespace label needs update",
			"pv", pv.Name,
			"currentLabel", pv.Labels[namespaceLabel],
			"desiredLabel", obj.GetNamespace(),
			ownerKey, obj.GetName())
		pv.Labels[namespaceLabel] = obj.GetNamespace()
		needsUpdate = true
	}

	// Stamp the installed schema version so a future PV restore can validate
	// binary/schema compatibility at admission time. Only set it once the cluster
	// has reported a schema version; never clear a previously stamped value (a
	// transient empty status must not erase the last-known-good version, which is
	// what a retained PV would be restored from).
	if schemaVersion := r.Adapter.Status(obj).SchemaVersion; schemaVersion != "" &&
		pv.Annotations[schemaAnnotation] != schemaVersion {
		if pv.Annotations == nil {
			pv.Annotations = make(map[string]string)
		}
		logger.Info("PV schema-version annotation needs update",
			"pv", pv.Name,
			"currentValue", pv.Annotations[schemaAnnotation],
			"desiredValue", schemaVersion,
			ownerKey, obj.GetName())
		pv.Annotations[schemaAnnotation] = schemaVersion
		needsUpdate = true
	}

	// Check if reclaim policy needs update
	desiredPolicy := DesiredReclaimPolicy(r.Adapter.Spec(obj).PersistentVolumeReclaimPolicy)
	if pv.Spec.PersistentVolumeReclaimPolicy != desiredPolicy {
		logger.Info("PV reclaim policy needs update",
			"pv", pv.Name,
			"currentPolicy", pv.Spec.PersistentVolumeReclaimPolicy,
			"desiredPolicy", desiredPolicy,
			ownerKey, obj.GetName())
		pv.Spec.PersistentVolumeReclaimPolicy = desiredPolicy
		needsUpdate = true
	}

	// Check if the storage provisioner supports mount options
	// Skip mount options for local/dev provisioners (kind, minikube, etc.)
	if r.ProvisionerSupportsMountOptions(ctx, pv) {
		// Check if mount options need update
		if !ContainsAllMountOptions(pv.Spec.MountOptions, SecurityMountOptions) {
			logger.Info("PV mount options need update",
				"pv", pv.Name,
				"currentMountOptions", pv.Spec.MountOptions,
				"desiredMountOptions", SecurityMountOptions)
			pv.Spec.MountOptions = MergeMountOptions(pv.Spec.MountOptions, SecurityMountOptions)
			needsUpdate = true
		}
	} else {
		logger.V(1).Info("Skipping mount options for PV - provisioner does not support them",
			"pv", pv.Name,
			"storageClassName", pv.Spec.StorageClassName)
	}

	return needsUpdate
}

// ProvisionerSupportsMountOptions checks if the PV's storage class provisioner supports mount options.
// Returns false for known local/dev provisioners (kind, minikube, etc.) that don't support mount options.
// Returns true for production provisioners (Azure Disk, AWS EBS, etc.) or if the provisioner cannot be determined.
func (r *PersistentVolumeReconciler[T]) ProvisionerSupportsMountOptions(ctx context.Context, pv *corev1.PersistentVolume) bool {
	logger := log.FromContext(ctx)

	// If no storage class is specified, assume mount options are supported (safer default for production)
	if pv.Spec.StorageClassName == "" {
		return true
	}

	// Fetch the StorageClass to get the provisioner
	storageClass := &storagev1.StorageClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: pv.Spec.StorageClassName}, storageClass); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("StorageClass not found, assuming mount options are supported",
				"storageClassName", pv.Spec.StorageClassName)
			return true
		}
		logger.Error(err, "Failed to get StorageClass, assuming mount options are supported",
			"storageClassName", pv.Spec.StorageClassName)
		return true
	}

	// Check if the provisioner is in the unsupported list
	for _, unsupportedProvisioner := range unsupportedMountOptionsProvisioners {
		if storageClass.Provisioner == unsupportedProvisioner {
			logger.V(1).Info("Provisioner does not support mount options",
				"provisioner", storageClass.Provisioner,
				"storageClassName", pv.Spec.StorageClassName)
			return false
		}
	}

	return true
}

// ContainsAllMountOptions checks if all desired mount options are present in current options
func ContainsAllMountOptions(current, desired []string) bool {
	for _, opt := range desired {
		if !slices.Contains(current, opt) {
			return false
		}
	}
	return true
}

// MergeMountOptions merges desired mount options into current, avoiding duplicates.
func MergeMountOptions(current, desired []string) []string {
	optSet := make(map[string]struct{}, len(current)+len(desired))
	for _, opt := range current {
		optSet[opt] = struct{}{}
	}
	for _, opt := range desired {
		optSet[opt] = struct{}{}
	}

	result := make([]string, 0, len(optSet))
	for opt := range optSet {
		result = append(result, opt)
	}
	return result
}

// FindOwnerForPV traverses the ownership chain to find the custom resource
// associated with a PersistentVolume:
// PV.claimRef -> PVC -> (ownerRef) CNPG Cluster -> (ownerRef) custom resource
func (r *PersistentVolumeReconciler[T]) FindOwnerForPV(ctx context.Context, pv *corev1.PersistentVolume) (T, bool, error) {
	logger := log.FromContext(ctx)
	kind := r.naming().Kind
	var none T

	// Step 1: Get the PVC from PV's claimRef
	if pv.Spec.ClaimRef == nil {
		return none, false, nil
	}

	pvc := &corev1.PersistentVolumeClaim{}
	pvcKey := types.NamespacedName{
		Name:      pv.Spec.ClaimRef.Name,
		Namespace: pv.Spec.ClaimRef.Namespace,
	}
	if err := r.Get(ctx, pvcKey, pvc); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("PVC not found for PV", "pvc", pvcKey, "pv", pv.Name)
			return none, false, nil
		}
		return none, false, err
	}

	// Step 2: Find CNPG Cluster that owns the PVC
	cnpgCluster := r.FindCNPGClusterOwner(ctx, pvc)
	if cnpgCluster == nil {
		logger.V(1).Info("No CNPG Cluster owner found for PVC", "pvc", pvc.Name)
		return none, false, nil
	}

	// Step 3: Find the custom resource that owns the CNPG Cluster
	obj, found := r.FindClusterOwner(ctx, cnpgCluster)
	if !found {
		logger.V(1).Info("No "+kind+" owner found for CNPG Cluster", "cluster", cnpgCluster.Name)
		return none, false, nil
	}

	logger.V(1).Info("Found "+kind+" for PV",
		"pv", pv.Name,
		"pvc", pvc.Name,
		"cluster", cnpgCluster.Name,
		strings.ToLower(kind), obj.GetName())

	return obj, true, nil
}

// FindCNPGClusterOwner finds the CNPG Cluster that owns the given PVC
func (r *PersistentVolumeReconciler[T]) FindCNPGClusterOwner(ctx context.Context, pvc *corev1.PersistentVolumeClaim) *cnpgv1.Cluster {
	logger := log.FromContext(ctx)

	for _, ownerRef := range pvc.OwnerReferences {
		if !IsCNPGClusterOwnerRef(ownerRef) {
			continue
		}

		cluster := &cnpgv1.Cluster{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      ownerRef.Name,
			Namespace: pvc.Namespace,
		}, cluster); err != nil {
			if !errors.IsNotFound(err) {
				logger.Error(err, "Failed to get CNPG Cluster", "name", ownerRef.Name)
			}
			continue
		}
		return cluster
	}

	return nil
}

// FindClusterOwner finds the product custom resource that owns the given CNPG Cluster.
func (r *PersistentVolumeReconciler[T]) FindClusterOwner(ctx context.Context, cluster *cnpgv1.Cluster) (T, bool) {
	logger := log.FromContext(ctx)
	kind := r.naming().Kind
	var none T

	for _, ownerRef := range cluster.OwnerReferences {
		if ownerRef.Kind != kind {
			continue
		}

		obj := r.Adapter.New()
		if err := r.Get(ctx, types.NamespacedName{
			Name:      ownerRef.Name,
			Namespace: cluster.Namespace,
		}, obj); err != nil {
			if !errors.IsNotFound(err) {
				logger.Error(err, "Failed to get "+kind, "name", ownerRef.Name)
			}
			continue
		}
		return obj, true
	}

	return none, false
}

// IsCNPGClusterOwnerRef checks if an owner reference refers to a CNPG Cluster
func IsCNPGClusterOwnerRef(ownerRef metav1.OwnerReference) bool {
	return ownerRef.Kind == ownerRefKindCluster && strings.Contains(ownerRef.APIVersion, cnpgAPIVersionPrefix)
}

// DesiredReclaimPolicy maps the spec reclaim policy to the PV reclaim policy;
// anything but "Delete" is Retain, the safer default for database workloads.
func DesiredReclaimPolicy(policy string) corev1.PersistentVolumeReclaimPolicy {
	switch policy {
	case "Retain":
		return corev1.PersistentVolumeReclaimRetain
	case "Delete":
		return corev1.PersistentVolumeReclaimDelete
	default:
		// Default to Retain if not specified - safer for database workloads
		return corev1.PersistentVolumeReclaimRetain
	}
}

// BoundPVPredicate filters PV events to only process bound PVs
func BoundPVPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pv, ok := e.Object.(*corev1.PersistentVolume)
			if !ok {
				return false
			}
			// Only process PVs that are bound and have a claimRef
			return pv.Status.Phase == corev1.VolumeBound && pv.Spec.ClaimRef != nil
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newPV, ok := e.ObjectNew.(*corev1.PersistentVolume)
			if !ok {
				return false
			}
			// Process when PV becomes bound or when claimRef changes
			return newPV.Status.Phase == corev1.VolumeBound && newPV.Spec.ClaimRef != nil
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// No need to reconcile deleted PVs
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			pv, ok := e.Object.(*corev1.PersistentVolume)
			if !ok {
				return false
			}
			return pv.Status.Phase == corev1.VolumeBound && pv.Spec.ClaimRef != nil
		},
	}
}

// PVRelevantPredicate triggers PV reconciliation when a custom resource change
// affects PV-level state: the reclaim policy (mutates the PV spec) or the
// installed schema version (stamped onto the PV as an annotation for restore
// compatibility validation).
func (r *PersistentVolumeReconciler[T]) PVRelevantPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldObj, ok := e.ObjectOld.(T)
			if !ok {
				return false
			}
			newObj, ok := e.ObjectNew.(T)
			if !ok {
				return false
			}
			return r.Adapter.Spec(oldObj).PersistentVolumeReclaimPolicy != r.Adapter.Spec(newObj).PersistentVolumeReclaimPolicy ||
				r.Adapter.Status(oldObj).SchemaVersion != r.Adapter.Status(newObj).SchemaVersion
		},
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// MapClusterToPVs returns reconcile requests for all PVs labeled with the
// custom resource. The cluster/namespace labels are set by this reconciler and
// work in both single and multi-cluster scenarios where CNPG cluster names may
// differ from the custom resource name.
func (r *PersistentVolumeReconciler[T]) MapClusterToPVs(ctx context.Context, o client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	naming := r.naming()
	obj, ok := o.(T)
	if !ok {
		return nil
	}

	pvList := &corev1.PersistentVolumeList{}
	if err := r.List(ctx, pvList,
		client.MatchingLabels{
			naming.ClusterLabel():   obj.GetName(),
			naming.NamespaceLabel(): obj.GetNamespace(),
		},
	); err != nil {
		logger.Error(err, "Failed to list PVs for "+naming.Kind)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(pvList.Items))
	for _, pv := range pvList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: pv.Name,
			},
		})
	}

	logger.Info("Found PVs to reconcile for "+naming.Kind+" update",
		strings.ToLower(naming.Kind), obj.GetName(),
		"pvCount", len(requests))

	return requests
}

// SetupWithManager registers the reconciler as controller name.
func (r *PersistentVolumeReconciler[T]) SetupWithManager(mgr ctrl.Manager, name string) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Apply BoundPVPredicate only to PersistentVolume events, not globally
		For(&corev1.PersistentVolume{}, builder.WithPredicates(BoundPVPredicate())).
		// Watch custom resource changes and trigger reconciliation of associated PVs
		// when the reclaim policy or the installed schema version changes.
		Watches(
			r.Adapter.New(),
			handler.EnqueueRequestsFromMapFunc(r.MapClusterToPVs),
			builder.WithPredicates(r.PVRelevantPredicate()),
		).
		Named(name).
		Complete(r)
}
