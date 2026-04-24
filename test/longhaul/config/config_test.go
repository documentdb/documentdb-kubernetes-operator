// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Config", func() {
	Describe("DefaultConfig", func() {
		It("returns safe defaults", func() {
			cfg := DefaultConfig()
			Expect(cfg.MaxDuration).To(Equal(30 * time.Minute))
			Expect(cfg.Namespace).To(Equal("default"))
			Expect(cfg.ClusterName).To(BeEmpty())
		})
	})

	Describe("LoadFromEnv", func() {
		It("uses defaults when no env vars set", func() {
			GinkgoT().Setenv(EnvMaxDuration, "")
			GinkgoT().Setenv(EnvNamespace, "")
			GinkgoT().Setenv(EnvClusterName, "")
			cfg, err := LoadFromEnv()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.MaxDuration).To(Equal(30 * time.Minute))
		})

		It("parses MaxDuration from env", func() {
			GinkgoT().Setenv(EnvMaxDuration, "1h")
			cfg, err := LoadFromEnv()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.MaxDuration).To(Equal(1 * time.Hour))
		})

		It("parses zero MaxDuration for infinite runs", func() {
			GinkgoT().Setenv(EnvMaxDuration, "0s")
			cfg, err := LoadFromEnv()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.MaxDuration).To(Equal(time.Duration(0)))
		})

		It("parses Namespace and ClusterName from env", func() {
			GinkgoT().Setenv(EnvNamespace, "test-ns")
			GinkgoT().Setenv(EnvClusterName, "my-cluster")
			cfg, err := LoadFromEnv()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Namespace).To(Equal("test-ns"))
			Expect(cfg.ClusterName).To(Equal("my-cluster"))
		})

		It("returns error for invalid MaxDuration", func() {
			GinkgoT().Setenv(EnvMaxDuration, "not-a-duration")
			_, err := LoadFromEnv()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(EnvMaxDuration))
		})
	})

	Describe("Validate", func() {
		It("passes for valid config", func() {
			cfg := DefaultConfig()
			cfg.ClusterName = "test-cluster"
			Expect(cfg.Validate()).To(Succeed())
		})

		It("fails when Namespace is empty", func() {
			cfg := DefaultConfig()
			cfg.ClusterName = "test"
			cfg.Namespace = ""
			Expect(cfg.Validate()).To(MatchError(ContainSubstring("namespace")))
		})

		It("fails when ClusterName is empty", func() {
			cfg := DefaultConfig()
			Expect(cfg.Validate()).To(MatchError(ContainSubstring("cluster name")))
		})

		It("fails when MaxDuration is negative", func() {
			cfg := DefaultConfig()
			cfg.ClusterName = "test"
			cfg.MaxDuration = -1 * time.Second
			Expect(cfg.Validate()).To(MatchError(ContainSubstring("max duration must not be negative")))
		})
	})

	Describe("IsEnabled", func() {
		It("returns false when env not set", func() {
			GinkgoT().Setenv(EnvEnabled, "")
			Expect(IsEnabled()).To(BeFalse())
		})

		It("returns true for 'true'", func() {
			GinkgoT().Setenv(EnvEnabled, "true")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true for '1'", func() {
			GinkgoT().Setenv(EnvEnabled, "1")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true for 'yes'", func() {
			GinkgoT().Setenv(EnvEnabled, "yes")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true case-insensitively", func() {
			GinkgoT().Setenv(EnvEnabled, "TRUE")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true for mixed case 'True'", func() {
			GinkgoT().Setenv(EnvEnabled, "True")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true for mixed case 'YES'", func() {
			GinkgoT().Setenv(EnvEnabled, "YES")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true with surrounding whitespace", func() {
			GinkgoT().Setenv(EnvEnabled, " true ")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns true for ' yes ' with whitespace", func() {
			GinkgoT().Setenv(EnvEnabled, " yes ")
			Expect(IsEnabled()).To(BeTrue())
		})

		It("returns false for whitespace-only", func() {
			GinkgoT().Setenv(EnvEnabled, "   ")
			Expect(IsEnabled()).To(BeFalse())
		})

		It("returns false for 'false'", func() {
			GinkgoT().Setenv(EnvEnabled, "false")
			Expect(IsEnabled()).To(BeFalse())
		})

		It("returns false for '0'", func() {
			GinkgoT().Setenv(EnvEnabled, "0")
			Expect(IsEnabled()).To(BeFalse())
		})

		It("returns false for 'no'", func() {
			GinkgoT().Setenv(EnvEnabled, "no")
			Expect(IsEnabled()).To(BeFalse())
		})
	})
})
