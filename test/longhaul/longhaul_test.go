// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package longhaul

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/config"
)

var testConfig config.Config

var _ = BeforeSuite(func() {
	if !config.IsEnabled() {
		Skip("Long haul tests are disabled. Set LONGHAUL_ENABLED=true to run.")
	}

	var err error
	testConfig, err = config.LoadFromEnv()
	Expect(err).NotTo(HaveOccurred(), "Failed to load long haul config from environment")

	err = testConfig.Validate()
	Expect(err).NotTo(HaveOccurred(), "Invalid long haul config")

	GinkgoWriter.Printf("Long haul test config:\n")
	GinkgoWriter.Printf("  MaxDuration:  %s\n", testConfig.MaxDuration)
	GinkgoWriter.Printf("  Namespace:    %s\n", testConfig.Namespace)
	GinkgoWriter.Printf("  ClusterName:  %s\n", testConfig.ClusterName)
})

var _ = Describe("Long Haul Test", func() {
	It("should run the long haul canary", func() {
		// Phase 1b+ will implement the actual workload, operations, and monitoring.
		// For now, verify the skeleton is wired up correctly.
		GinkgoWriter.Println("Long haul test skeleton is running")
		Expect(testConfig.ClusterName).NotTo(BeEmpty())
	})
})
