// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

var _ = Describe("formatMB", func() {
	It("formats plain megabytes", func() {
		Expect(formatMB(512)).To(Equal("512MB"))
	})

	It("formats exactly 1 GB", func() {
		Expect(formatMB(1024)).To(Equal("1GB"))
	})

	It("formats multiple GB", func() {
		Expect(formatMB(2048)).To(Equal("2GB"))
	})

	It("keeps MB when not evenly divisible by 1024", func() {
		Expect(formatMB(1536)).To(Equal("1536MB"))
	})

	It("formats zero", func() {
		Expect(formatMB(0)).To(Equal("0MB"))
	})
})

var _ = Describe("ComputeMemoryAwareDefaults", func() {
	Context("with zero or negative memory", func() {
		It("returns static fallbacks for zero", func() {
			result := ComputeMemoryAwareDefaults(0)
			Expect(result).To(Equal(map[string]string{
				"shared_buffers":       "256MB",
				"effective_cache_size": "512MB",
				"work_mem":             "16MB",
				"maintenance_work_mem": "128MB",
			}))
		})

		It("returns static fallbacks for negative value", func() {
			result := ComputeMemoryAwareDefaults(-1)
			Expect(result).To(Equal(map[string]string{
				"shared_buffers":       "256MB",
				"effective_cache_size": "512MB",
				"work_mem":             "16MB",
				"maintenance_work_mem": "128MB",
			}))
		})
	})

	Context("with 2 GiB memory", func() {
		var result map[string]string

		BeforeEach(func() {
			result = ComputeMemoryAwareDefaults(2 * 1024 * 1024 * 1024) // 2147483648
		})

		It("sets shared_buffers to 25% (512MB)", func() {
			Expect(result["shared_buffers"]).To(Equal("512MB"))
		})

		It("sets effective_cache_size to 75% (1536MB)", func() {
			Expect(result["effective_cache_size"]).To(Equal("1536MB"))
		})

		It("floors work_mem to 4MB minimum", func() {
			Expect(result["work_mem"]).To(Equal("4MB"))
		})

		It("sets maintenance_work_mem to 10% (204MB)", func() {
			Expect(result["maintenance_work_mem"]).To(Equal("204MB"))
		})
	})

	Context("with 8 GiB memory", func() {
		var result map[string]string

		BeforeEach(func() {
			result = ComputeMemoryAwareDefaults(8 * 1024 * 1024 * 1024) // 8589934592
		})

		It("sets shared_buffers to 25% (2GB)", func() {
			Expect(result["shared_buffers"]).To(Equal("2GB"))
		})

		It("sets effective_cache_size to 75% (6GB)", func() {
			Expect(result["effective_cache_size"]).To(Equal("6GB"))
		})

		It("computes work_mem as 6MB", func() {
			// 8192 / (300*4) = 6
			Expect(result["work_mem"]).To(Equal("6MB"))
		})

		It("sets maintenance_work_mem to 10% (819MB)", func() {
			Expect(result["maintenance_work_mem"]).To(Equal("819MB"))
		})
	})

	Context("with 32 GiB memory", func() {
		var result map[string]string

		BeforeEach(func() {
			result = ComputeMemoryAwareDefaults(32 * 1024 * 1024 * 1024) // 34359738368
		})

		It("sets shared_buffers to 25% (8GB)", func() {
			Expect(result["shared_buffers"]).To(Equal("8GB"))
		})

		It("sets effective_cache_size to 75% (24GB)", func() {
			Expect(result["effective_cache_size"]).To(Equal("24GB"))
		})

		It("computes work_mem as 27MB", func() {
			// 32768 / (300*4) = 27
			Expect(result["work_mem"]).To(Equal("27MB"))
		})

		It("caps maintenance_work_mem at 2GB", func() {
			// 10% = 3276MB > 2048MB cap → 2GB
			Expect(result["maintenance_work_mem"]).To(Equal("2GB"))
		})
	})
})

var _ = Describe("StaticDefaults", func() {
	var result map[string]string

	BeforeEach(func() {
		result = StaticDefaults()
	})

	It("returns all expected keys", func() {
		expectedKeys := []string{
			"max_connections",
			"random_page_cost",
			"effective_io_concurrency",
			"autovacuum_vacuum_scale_factor",
			"autovacuum_analyze_scale_factor",
			"autovacuum_vacuum_cost_delay",
			"autovacuum_max_workers",
			"checkpoint_completion_target",
			"wal_buffers",
			"min_wal_size",
			"max_wal_size",
		}
		for _, key := range expectedKeys {
			Expect(result).To(HaveKey(key))
		}
	})

	It("sets max_connections to 300", func() {
		Expect(result["max_connections"]).To(Equal("300"))
	})

	It("sets wal_buffers to 16MB", func() {
		Expect(result["wal_buffers"]).To(Equal("16MB"))
	})
})

var _ = Describe("ProtectedParameters", func() {
	Context("without ChangeStreams", func() {
		var result map[string]string

		BeforeEach(func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			}
			result = ProtectedParameters(documentdb)
		})

		It("contains cron.database_name", func() {
			Expect(result["cron.database_name"]).To(Equal("postgres"))
		})

		It("contains max_replication_slots", func() {
			Expect(result["max_replication_slots"]).To(Equal("10"))
		})

		It("contains max_wal_senders", func() {
			Expect(result["max_wal_senders"]).To(Equal("10"))
		})

		It("contains max_prepared_transactions", func() {
			Expect(result["max_prepared_transactions"]).To(Equal("100"))
		})

		It("does not contain wal_level", func() {
			Expect(result).NotTo(HaveKey("wal_level"))
		})
	})

	Context("with ChangeStreams enabled", func() {
		var result map[string]string

		BeforeEach(func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					FeatureGates: map[string]bool{
						dbpreview.FeatureGateChangeStreams: true,
					},
				},
			}
			result = ProtectedParameters(documentdb)
		})

		It("sets wal_level to logical", func() {
			Expect(result["wal_level"]).To(Equal("logical"))
		})

		It("still contains other protected params", func() {
			Expect(result["cron.database_name"]).To(Equal("postgres"))
		})
	})
})

var _ = Describe("MergeParameters", func() {
	Context("user override takes precedence over defaults", func() {
		It("uses user-specified max_connections over static default", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					PostgresParameters: map[string]string{
						"max_connections": "500",
					},
				},
			}
			result := MergeParameters(documentdb, 0)
			Expect(result["max_connections"]).To(Equal("500"))
		})
	})

	Context("protected params always win", func() {
		It("overrides user-specified cron.database_name", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					PostgresParameters: map[string]string{
						"cron.database_name": "mydb",
					},
				},
			}
			result := MergeParameters(documentdb, 0)
			Expect(result["cron.database_name"]).To(Equal("postgres"))
		})
	})

	Context("memory-aware defaults override static", func() {
		It("computes shared_buffers from memory instead of using a static value", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			}
			result := MergeParameters(documentdb, 8*1024*1024*1024)
			Expect(result["shared_buffers"]).To(Equal("2GB"))
		})
	})

	Context("all layers present", func() {
		It("merges feature gates, user overrides, and memory-aware defaults", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					FeatureGates: map[string]bool{
						dbpreview.FeatureGateChangeStreams: true,
					},
					PostgresParameters: map[string]string{
						"max_connections":  "500",
						"random_page_cost": "1.5",
					},
				},
			}
			result := MergeParameters(documentdb, 8*1024*1024*1024)

			// User overrides win for non-protected params
			Expect(result["max_connections"]).To(Equal("500"))
			Expect(result["random_page_cost"]).To(Equal("1.5"))
			// Memory-aware defaults
			Expect(result["shared_buffers"]).To(Equal("2GB"))
			Expect(result["effective_cache_size"]).To(Equal("6GB"))
			// Protected params always win
			Expect(result["cron.database_name"]).To(Equal("postgres"))
			// Feature gate
			Expect(result["wal_level"]).To(Equal("logical"))
			// Static defaults still present
			Expect(result["wal_buffers"]).To(Equal("16MB"))
		})
	})

	Context("empty user params", func() {
		It("uses defaults when PostgresParameters is nil", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			}
			result := MergeParameters(documentdb, 8*1024*1024*1024)

			Expect(result["max_connections"]).To(Equal("300"))
			Expect(result["shared_buffers"]).To(Equal("2GB"))
			Expect(result["cron.database_name"]).To(Equal("postgres"))
		})
	})

	Context("zero memory", func() {
		It("uses fallback defaults for memory-aware parameters", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			}
			result := MergeParameters(documentdb, 0)

			Expect(result["shared_buffers"]).To(Equal("256MB"))
			Expect(result["effective_cache_size"]).To(Equal("512MB"))
			Expect(result["work_mem"]).To(Equal("16MB"))
			Expect(result["maintenance_work_mem"]).To(Equal("128MB"))
			// Static defaults still present
			Expect(result["max_connections"]).To(Equal("300"))
		})
	})
})
