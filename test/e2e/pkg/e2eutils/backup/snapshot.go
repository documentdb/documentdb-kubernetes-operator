// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IsSnapshotReady returns true when the given VolumeSnapshot has a
// non-nil ReadyToUse == true status flag.
func IsSnapshotReady(s *snapshotv1.VolumeSnapshot) bool {
	if s == nil || s.Status == nil || s.Status.ReadyToUse == nil {
		return false
	}
	return *s.Status.ReadyToUse
}

// ListSnapshotsForBackup returns every VolumeSnapshot in ns carrying
// the CNPG cnpg.io/backupName=<backupName> label.
func ListSnapshotsForBackup(ctx context.Context, c client.Client, ns, backupName string) ([]snapshotv1.VolumeSnapshot, error) {
	var list snapshotv1.VolumeSnapshotList
	opts := []client.ListOption{client.InNamespace(ns), BackupNameLabelSelector(backupName)}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, fmt.Errorf("list VolumeSnapshots in %s for backup %s: %w", ns, backupName, err)
	}
	return list.Items, nil
}

// ListSnapshotsForCluster returns every VolumeSnapshot in ns carrying
// the CNPG cnpg.io/cluster=<clusterName> label. Useful when the caller
// only knows the cluster and wants to observe any snapshot produced by
// any Backup (direct or scheduled) against that cluster.
func ListSnapshotsForCluster(ctx context.Context, c client.Client, ns, clusterName string) ([]snapshotv1.VolumeSnapshot, error) {
	var list snapshotv1.VolumeSnapshotList
	opts := []client.ListOption{client.InNamespace(ns), ClusterLabelSelector(clusterName)}
	if err := c.List(ctx, &list, opts...); err != nil {
		return nil, fmt.Errorf("list VolumeSnapshots in %s for cluster %s: %w", ns, clusterName, err)
	}
	return list.Items, nil
}

// WaitForSnapshotForBackup polls until at least one VolumeSnapshot
// labeled cnpg.io/backupName=<backupName> reports ReadyToUse=true, or
// timeout elapses. Returns the first such snapshot as observed.
func WaitForSnapshotForBackup(ctx context.Context, c client.Client, ns, backupName string, timeout time.Duration) (*snapshotv1.VolumeSnapshot, error) {
	if c == nil {
		return nil, errors.New("backup.WaitForSnapshotForBackup: client must not be nil")
	}
	deadline := time.Now().Add(timeout)
	var lastObserved int
	for {
		snaps, err := ListSnapshotsForBackup(ctx, c, ns, backupName)
		if err != nil {
			return nil, err
		}
		lastObserved = len(snaps)
		for i := range snaps {
			if IsSnapshotReady(&snaps[i]) {
				return &snaps[i], nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for a ready VolumeSnapshot for backup %s/%s (observed %d snapshots)",
				timeout, ns, backupName, lastObserved)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}

// FindRetainedPV returns the first PersistentVolume whose claimRef
// points at a PVC in ns for clusterName. The helper prefers PVs that
// are in the Released phase (the post-delete state when the reclaim
// policy is Retain) but will also return a Bound PV — useful when the
// caller hasn't deleted the source DocumentDB yet.
//
// The DocumentDB backup-and-restore workflow sets reclaim=Retain on
// every PV belonging to the cluster, so after the source DocumentDB is
// deleted the PVs linger for the "restore from PV" scenario.
//
// Single-instance requirement. This helper is deterministic only when
// the source DocumentDB ran with spec.instancesPerNode=1. A
// multi-instance cluster produces several retained PVs (one per CNPG
// instance); the "first match wins" semantics above give no guarantee
// about which PV is returned, and in particular the returned PV may
// not be the one that backed the primary. Callers that need support
// for multi-instance restore should extend this helper to filter by
// the cnpg.io/instanceRole=primary label on the PVC or the owner
// reference of the PV, and to fail loudly when more than one
// candidate remains. Until then, keep the caller-side invariant of
// InstancesPerNode=1 to avoid flaky restore tests.
func FindRetainedPV(ctx context.Context, c client.Client, ns, clusterName string) (*corev1.PersistentVolume, error) {
	var pvs corev1.PersistentVolumeList
	if err := c.List(ctx, &pvs); err != nil {
		return nil, fmt.Errorf("list PersistentVolumes: %w", err)
	}
	// Prefer Released PVs first, then fall back to Bound (test may
	// still be holding the source DocumentDB alive).
	var bound *corev1.PersistentVolume
	for i := range pvs.Items {
		pv := &pvs.Items[i]
		if pv.Spec.ClaimRef == nil {
			continue
		}
		if pv.Spec.ClaimRef.Namespace != ns {
			continue
		}
		// The CNPG PVC naming convention is <cluster>-<n> for the
		// primary and replicas; match by prefix plus the cnpg.io/cluster
		// label if set on the PV.
		if !pvBelongsToCluster(pv, clusterName) {
			continue
		}
		switch pv.Status.Phase {
		case corev1.VolumeReleased, corev1.VolumeAvailable:
			out := *pv
			return &out, nil
		case corev1.VolumeBound:
			cp := *pv
			bound = &cp
		}
	}
	if bound != nil {
		return bound, nil
	}
	return nil, fmt.Errorf("no retained PersistentVolume found for cluster %s/%s", ns, clusterName)
}

// pvBelongsToCluster matches a PV to a DocumentDB/CNPG cluster by
// either: (a) the CNPG cnpg.io/cluster label; (b) the DocumentDB
// operator's documentdb.io/cluster label; or (c) the claimRef.name
// prefix falling back on the CNPG convention <cluster>-<n>.
func pvBelongsToCluster(pv *corev1.PersistentVolume, clusterName string) bool {
	if pv == nil {
		return false
	}
	ls := labels.Set(pv.Labels)
	if ls.Get("cnpg.io/cluster") == clusterName {
		return true
	}
	if ls.Get("documentdb.io/cluster") == clusterName {
		return true
	}
	if pv.Spec.ClaimRef == nil {
		return false
	}
	claim := pv.Spec.ClaimRef.Name
	return claim == clusterName ||
		(len(claim) > len(clusterName)+1 && claim[:len(clusterName)+1] == clusterName+"-")
}

// WaitForPVCDeleted polls until the named PVC no longer exists in ns
// or timeout elapses. Used to assert that the operator cleans up the
// temporary <cluster>-pv-recovery-temp PVC after a PV-based restore
// finishes.
func WaitForPVCDeleted(ctx context.Context, c client.Client, ns, name string, timeout time.Duration) error {
	if c == nil {
		return errors.New("backup.WaitForPVCDeleted: client must not be nil")
	}
	deadline := time.Now().Add(timeout)
	for {
		var pvc corev1.PersistentVolumeClaim
		err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &pvc)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("polling PVC %s/%s: %w", ns, name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for PVC %s/%s to be deleted", timeout, ns, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}
