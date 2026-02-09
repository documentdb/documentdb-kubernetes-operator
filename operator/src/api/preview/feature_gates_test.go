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
