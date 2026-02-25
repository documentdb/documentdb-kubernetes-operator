// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsFeatureGateEnabled", func() {
	var documentdb *DocumentDB

	BeforeEach(func() {
		documentdb = &DocumentDB{}
	})

	Context("when featureGates is nil", func() {
		It("returns the default value (false) for ChangeStreams", func() {
			Expect(IsFeatureGateEnabled(documentdb, FeatureGateChangeStreams)).To(BeFalse())
		})
	})

	Context("when featureGates is an empty map", func() {
		BeforeEach(func() {
			documentdb.Spec.FeatureGates = map[string]bool{}
		})

		It("returns the default value (false) for ChangeStreams", func() {
			Expect(IsFeatureGateEnabled(documentdb, FeatureGateChangeStreams)).To(BeFalse())
		})
	})

	Context("when ChangeStreams is explicitly enabled", func() {
		BeforeEach(func() {
			documentdb.Spec.FeatureGates = map[string]bool{
				FeatureGateChangeStreams: true,
			}
		})

		It("returns true", func() {
			Expect(IsFeatureGateEnabled(documentdb, FeatureGateChangeStreams)).To(BeTrue())
		})
	})

	Context("when ChangeStreams is explicitly disabled", func() {
		BeforeEach(func() {
			documentdb.Spec.FeatureGates = map[string]bool{
				FeatureGateChangeStreams: false,
			}
		})

		It("returns false", func() {
			Expect(IsFeatureGateEnabled(documentdb, FeatureGateChangeStreams)).To(BeFalse())
		})
	})

	Context("when an unknown feature gate is queried", func() {
		It("returns false when featureGates is nil", func() {
			Expect(IsFeatureGateEnabled(documentdb, "UnknownFeature")).To(BeFalse())
		})

		It("returns false when featureGates has other keys", func() {
			documentdb.Spec.FeatureGates = map[string]bool{
				FeatureGateChangeStreams: true,
			}
			Expect(IsFeatureGateEnabled(documentdb, "UnknownFeature")).To(BeFalse())
		})
	})
})

var _ = Describe("DocumentDB Methods", func() {
	Describe("IsPVRecoveryConfigured", func() {
		It("returns false when bootstrap is nil", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{},
			}
			Expect(db.IsPVRecoveryConfigured()).To(BeFalse())
		})

		It("returns false when recovery is nil", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Bootstrap: &BootstrapConfiguration{},
				},
			}
			Expect(db.IsPVRecoveryConfigured()).To(BeFalse())
		})

		It("returns false when persistentVolume is nil", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Bootstrap: &BootstrapConfiguration{
						Recovery: &RecoveryConfiguration{},
					},
				},
			}
			Expect(db.IsPVRecoveryConfigured()).To(BeFalse())
		})

		It("returns false when persistentVolume name is empty", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Bootstrap: &BootstrapConfiguration{
						Recovery: &RecoveryConfiguration{
							PersistentVolume: &PVRecoveryConfiguration{
								Name: "",
							},
						},
					},
				},
			}
			Expect(db.IsPVRecoveryConfigured()).To(BeFalse())
		})

		It("returns true when persistentVolume name is set", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Bootstrap: &BootstrapConfiguration{
						Recovery: &RecoveryConfiguration{
							PersistentVolume: &PVRecoveryConfiguration{
								Name: "my-pv",
							},
						},
					},
				},
			}
			Expect(db.IsPVRecoveryConfigured()).To(BeTrue())
		})
	})

	Describe("GetPVNameForRecovery", func() {
		It("returns empty string when PV recovery is not configured", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{},
			}
			Expect(db.GetPVNameForRecovery()).To(Equal(""))
		})

		It("returns empty string when persistentVolume is nil", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Bootstrap: &BootstrapConfiguration{
						Recovery: &RecoveryConfiguration{},
					},
				},
			}
			Expect(db.GetPVNameForRecovery()).To(Equal(""))
		})

		It("returns the PV name when configured", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Bootstrap: &BootstrapConfiguration{
						Recovery: &RecoveryConfiguration{
							PersistentVolume: &PVRecoveryConfiguration{
								Name: "my-retained-pv",
							},
						},
					},
				},
			}
			Expect(db.GetPVNameForRecovery()).To(Equal("my-retained-pv"))
		})
	})

	Describe("ShouldWarnAboutRetainedPVs", func() {
		It("returns true when reclaim policy is empty (default)", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Resource: Resource{
						Storage: StorageConfiguration{
							PersistentVolumeReclaimPolicy: "",
						},
					},
				},
			}
			Expect(db.ShouldWarnAboutRetainedPVs()).To(BeTrue())
		})

		It("returns true when reclaim policy is Retain", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Resource: Resource{
						Storage: StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}
			Expect(db.ShouldWarnAboutRetainedPVs()).To(BeTrue())
		})

		It("returns false when reclaim policy is Delete", func() {
			db := &DocumentDB{
				Spec: DocumentDBSpec{
					Resource: Resource{
						Storage: StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Delete",
						},
					},
				},
			}
			Expect(db.ShouldWarnAboutRetainedPVs()).To(BeFalse())
		})
	})
})
