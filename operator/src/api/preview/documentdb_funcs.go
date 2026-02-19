// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

// featureGateDefaults defines the default enabled/disabled state for each feature gate
// when the user does not explicitly specify a value. To enable a feature gate by default
// in a future version, simply change its value here — no CRD schema change is needed.
var featureGateDefaults = map[string]bool{
	FeatureGateChangeStreams: false,
}

// IsFeatureGateEnabled checks whether a named feature gate is enabled for the given DocumentDB instance.
// If the feature gate is not explicitly set in spec.featureGates, the default from featureGateDefaults is used.
func IsFeatureGateEnabled(documentdb *DocumentDB, featureGate string) bool {
	if documentdb.Spec.FeatureGates != nil {
		if val, ok := documentdb.Spec.FeatureGates[featureGate]; ok {
			return val
		}
	}
	return featureGateDefaults[featureGate]
}

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
