// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

// IsPVRecoveryConfigured checks if PV recovery is configured for the DocumentDB instance.
func (d *DocumentDB) IsPVRecoveryConfigured() bool {
	return d.Spec.Bootstrap != nil &&
		d.Spec.Bootstrap.Recovery != nil &&
		d.Spec.Bootstrap.Recovery.PersistentVolume != nil &&
		d.Spec.Bootstrap.Recovery.PersistentVolume.Name != ""
}

// GetPVNameForRecovery returns the PV name configured for recovery, or empty string if not configured.
func (d *DocumentDB) GetPVNameForRecovery() string {
	if !d.IsPVRecoveryConfigured() {
		return ""
	}
	return d.Spec.Bootstrap.Recovery.PersistentVolume.Name
}

// ShouldWarnAboutRetainedPVs returns true if the reclaim policy is Retain (explicitly or by default).
// Default is Retain, so warn unless explicitly set to Delete.
func (d *DocumentDB) ShouldWarnAboutRetainedPVs() bool {
	policy := d.Spec.Resource.Storage.PersistentVolumeReclaimPolicy
	return policy == "" || policy == "Retain"
}
