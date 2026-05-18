// Package clusterprobe supplies runtime capability checks for the
// DocumentDB E2E suite. Ginkgo label selectors (e.g.
// `e2e.NeedsCSISnapshotsLabel`) only gate invocation: when a caller
// forgets `--label-filter='!needs-csi-snapshots'` on a cluster that
// lacks CSI snapshot support, the spec still runs and produces
// confusing failures deep inside the Backup/Restore path.
//
// The probes below give each affected spec a deterministic pre-flight
// check that it can invoke from `BeforeEach` and fall through to a
// clear `Skip(...)` message when the capability is missing. They are
// intentionally framework-agnostic (plain errors, no Ginkgo/Gomega
// imports) so unit tests can exercise them with a controller-runtime
// fake client.
package clusterprobe

import (
	"context"
	"errors"
	"fmt"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultStorageClassAnnotation is the annotation Kubernetes uses to
// flag a StorageClass as the cluster default. Present with value "true"
// (or the legacy beta annotation) on at most one StorageClass per
// cluster.
const DefaultStorageClassAnnotation = "storageclass.kubernetes.io/is-default-class"

// legacyDefaultStorageClassAnnotation is the pre-GA annotation still
// honoured by some distributions (e.g. older OpenShift releases).
const legacyDefaultStorageClassAnnotation = "storageclass.beta.kubernetes.io/is-default-class"

// isMissingKindErr folds the two distinct "kind is not available"
// errors a controller-runtime client can return when the underlying
// CRD is absent: the apimachinery no-match error returned by a real
// cluster whose discovery lacks the type, and the runtime
// not-registered error returned by a fake client whose scheme omits
// it. Callers use it to decide "probe says missing" vs. "probe should
// propagate the error".
func isMissingKindErr(err error) bool {
	if err == nil {
		return false
	}
	if meta.IsNoMatchError(err) {
		return true
	}
	if runtime.IsNotRegisteredError(err) {
		return true
	}
	return false
}

// HasVolumeSnapshotCRD returns true when the cluster exposes the
// snapshot.storage.k8s.io/v1 VolumeSnapshot kind (i.e. the external
// snapshotter CRD is installed and its types are reachable through
// the supplied client). Other errors — RBAC denials, transient
// API-server failures — are returned to the caller as-is; the probe
// does not swallow them.
func HasVolumeSnapshotCRD(ctx context.Context, c client.Client) (bool, error) {
	if c == nil {
		return false, errors.New("clusterprobe.HasVolumeSnapshotCRD: client must not be nil")
	}
	var list snapshotv1.VolumeSnapshotList
	if err := c.List(ctx, &list); err != nil {
		if isMissingKindErr(err) {
			return false, nil
		}
		return false, fmt.Errorf("list VolumeSnapshots: %w", err)
	}
	return true, nil
}

// HasUsableSnapshotClass returns true when at least one
// VolumeSnapshotClass exists on the cluster. Callers that already
// confirmed the CRD via [HasVolumeSnapshotCRD] may still see this
// probe report false on clusters where the CRD is installed but no
// class is provisioned — a common state on stock kind nodes without
// the csi-hostpath driver add-on.
func HasUsableSnapshotClass(ctx context.Context, c client.Client) (bool, error) {
	if c == nil {
		return false, errors.New("clusterprobe.HasUsableSnapshotClass: client must not be nil")
	}
	var list snapshotv1.VolumeSnapshotClassList
	if err := c.List(ctx, &list); err != nil {
		if isMissingKindErr(err) {
			return false, nil
		}
		return false, fmt.Errorf("list VolumeSnapshotClasses: %w", err)
	}
	return len(list.Items) > 0, nil
}

// StorageClassAllowsExpansion returns true when the named StorageClass
// exists and has `allowVolumeExpansion=true`. When name is empty the
// probe looks up the cluster's default StorageClass (annotation
// storageclass.kubernetes.io/is-default-class=true, or its legacy
// beta variant). A nil AllowVolumeExpansion pointer on an otherwise
// valid StorageClass is reported as false — that is the Kubernetes
// API default meaning "expansion not allowed".
//
// Returns (false, nil) if the StorageClass (named or default) is not
// found; the caller typically translates that into a Skip() message.
// Returns (false, err) for any other API error.
func StorageClassAllowsExpansion(ctx context.Context, c client.Client, name string) (bool, error) {
	if c == nil {
		return false, errors.New("clusterprobe.StorageClassAllowsExpansion: client must not be nil")
	}
	sc, err := resolveStorageClass(ctx, c, name)
	if err != nil {
		return false, err
	}
	if sc == nil {
		return false, nil
	}
	if sc.AllowVolumeExpansion == nil {
		return false, nil
	}
	return *sc.AllowVolumeExpansion, nil
}

// resolveStorageClass returns the StorageClass named by name, or when
// name is empty the cluster default. A missing StorageClass returns
// (nil, nil) so the caller can report it as an absent capability.
func resolveStorageClass(ctx context.Context, c client.Client, name string) (*storagev1.StorageClass, error) {
	if name != "" {
		sc := &storagev1.StorageClass{}
		err := c.Get(ctx, client.ObjectKey{Name: name}, sc)
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		if isMissingKindErr(err) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("get StorageClass %s: %w", name, err)
		}
		return sc, nil
	}
	var list storagev1.StorageClassList
	if err := c.List(ctx, &list); err != nil {
		if isMissingKindErr(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list StorageClasses: %w", err)
	}
	for i := range list.Items {
		sc := &list.Items[i]
		if isDefaultStorageClass(sc) {
			return sc, nil
		}
	}
	return nil, nil
}

// isDefaultStorageClass honours both the GA and legacy beta
// "is-default-class" annotations.
func isDefaultStorageClass(sc *storagev1.StorageClass) bool {
	if sc == nil {
		return false
	}
	for _, key := range []string{DefaultStorageClassAnnotation, legacyDefaultStorageClassAnnotation} {
		if v, ok := sc.Annotations[key]; ok && v == "true" {
			return true
		}
	}
	return false
}
