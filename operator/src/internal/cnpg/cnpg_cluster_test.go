// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var _ = Describe("getBootstrapConfiguration", func() {
	var log = zap.New(zap.WriteTo(GinkgoWriter))

	It("returns default bootstrap when no bootstrap is configured", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{},
		}

		result := getBootstrapConfiguration(documentdb, true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.InitDB).ToNot(BeNil())
		Expect(result.InitDB.PostInitSQL).To(HaveLen(3))
		Expect(result.InitDB.PostInitSQL[0]).To(Equal("CREATE EXTENSION documentdb CASCADE"))
		Expect(result.Recovery).To(BeNil())
	})

	It("returns default bootstrap when not primary region", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "my-backup",
						},
					},
				},
			},
		}

		result := getBootstrapConfiguration(documentdb, false, log)
		Expect(result).ToNot(BeNil())
		Expect(result.InitDB).ToNot(BeNil())
		Expect(result.Recovery).To(BeNil())
	})

	It("returns default bootstrap when recovery is not configured", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{},
			},
		}

		result := getBootstrapConfiguration(documentdb, true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.InitDB).ToNot(BeNil())
		Expect(result.Recovery).To(BeNil())
	})

	It("returns backup recovery when backup name is specified", func() {
		backupName := "my-backup"
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: backupName,
						},
					},
				},
			},
		}

		result := getBootstrapConfiguration(documentdb, true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Recovery).ToNot(BeNil())
		Expect(result.Recovery.Backup).ToNot(BeNil())
		Expect(result.Recovery.Backup.LocalObjectReference.Name).To(Equal(backupName))
		Expect(result.Recovery.VolumeSnapshots).To(BeNil())
		Expect(result.InitDB).To(BeNil())
	})

	It("returns PV recovery when PV name is specified", func() {
		pvName := "my-pv"
		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-cluster",
			},
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						PersistentVolume: &dbpreview.PVRecoveryConfiguration{
							Name: pvName,
						},
					},
				},
			},
		}

		result := getBootstrapConfiguration(documentdb, true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Recovery).ToNot(BeNil())
		Expect(result.Recovery.VolumeSnapshots).ToNot(BeNil())
		// Temp PVC name is based on documentdb name
		Expect(result.Recovery.VolumeSnapshots.Storage.Name).To(Equal("test-cluster-pv-recovery-temp"))
		Expect(result.Recovery.VolumeSnapshots.Storage.Kind).To(Equal("PersistentVolumeClaim"))
		Expect(result.Recovery.VolumeSnapshots.Storage.APIGroup).To(Equal(ptr.To("")))
		Expect(result.Recovery.Backup).To(BeNil())
		Expect(result.InitDB).To(BeNil())
	})

	It("returns default bootstrap when backup name is empty", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "",
						},
					},
				},
			},
		}

		result := getBootstrapConfiguration(documentdb, true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.InitDB).ToNot(BeNil())
		Expect(result.Recovery).To(BeNil())
	})

	It("returns default bootstrap when PV name is empty", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						PersistentVolume: &dbpreview.PVRecoveryConfiguration{
							Name: "",
						},
					},
				},
			},
		}

		result := getBootstrapConfiguration(documentdb, true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.InitDB).ToNot(BeNil())
		Expect(result.Recovery).To(BeNil())
	})
})

var _ = Describe("getDefaultBootstrapConfiguration", func() {
	It("returns a bootstrap configuration with InitDB", func() {
		result := getDefaultBootstrapConfiguration(&dbpreview.DocumentDB{})
		Expect(result).ToNot(BeNil())
		Expect(result.InitDB).ToNot(BeNil())
		Expect(result.Recovery).To(BeNil())
	})

	It("includes required PostInitSQL statements by default", func() {
		result := getDefaultBootstrapConfiguration(&dbpreview.DocumentDB{})
		Expect(result.InitDB.PostInitSQL).To(HaveLen(3))
		Expect(result.InitDB.PostInitSQL).To(ContainElement("CREATE EXTENSION documentdb CASCADE"))
		Expect(result.InitDB.PostInitSQL).To(ContainElement("CREATE ROLE documentdb WITH LOGIN PASSWORD 'Admin100'"))
		Expect(result.InitDB.PostInitSQL).To(ContainElement("ALTER ROLE documentdb WITH SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS"))
	})

	It("appends spec.postgres.postInitSQL after operator-required statements", func() {
		db := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Postgres: &dbpreview.PostgresSpec{
					PostInitSQL: []string{"SELECT 1", "SELECT 2"},
				},
			},
		}
		result := getDefaultBootstrapConfiguration(db)
		Expect(result.InitDB.PostInitSQL).To(HaveLen(5))
		Expect(result.InitDB.PostInitSQL[0]).To(Equal("CREATE EXTENSION documentdb CASCADE"))
		Expect(result.InitDB.PostInitSQL[3]).To(Equal("SELECT 1"))
		Expect(result.InitDB.PostInitSQL[4]).To(Equal("SELECT 2"))
	})
})

var _ = Describe("Postgres certificate configuration", func() {
	It("omits Postgres certificate configuration", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "ext:1.0", "test-sa", "", true, zap.New(zap.WriteTo(GinkgoWriter)))

		Expect(result.Spec.Certificates).To(BeNil())
	})

	It("includes Postgres certificate configuration when TLS is set", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		// Create a certificates configuration
		certificatesConfig := &cnpgv1.CertificatesConfiguration{
			ServerTLSSecret:      "server-tls-secret",
			ServerCASecret:       "server-ca-secret",
			ReplicationTLSSecret: "replication-tls-secret",
			ClientCASecret:       "client-ca-secret",
		}

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
				TLS: &dbpreview.TLSConfiguration{
					Postgres: certificatesConfig,
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "ext:1.0", "test-sa", "", true, zap.New(zap.WriteTo(GinkgoWriter)))

		Expect(result.Spec.Certificates).ToNot(BeNil())
		Expect(result.Spec.Certificates).To(Equal(certificatesConfig))
		Expect(result.Spec.Certificates.ServerTLSSecret).To(Equal("server-tls-secret"))
		Expect(result.Spec.Certificates.ClientCASecret).To(Equal("client-ca-secret"))
	})
})

var _ = Describe("GetCnpgClusterSpec", func() {
	var log = zap.New(zap.WriteTo(GinkgoWriter))

	setProdSplitEnv := func() {
		GinkgoT().Setenv(util.GATEWAY_MEMORY_FRACTION_ENV, "0.1875")
		GinkgoT().Setenv(util.GATEWAY_MEMORY_CAP_ENV, "32Gi")
		GinkgoT().Setenv(util.GATEWAY_CPU_LIMIT_ENV, "")
		GinkgoT().Setenv(util.OTEL_MEMORY_REQUEST_ENV, "48Mi")
		GinkgoT().Setenv(util.OTEL_MEMORY_LIMIT_ENV, "128Mi")
		GinkgoT().Setenv(util.OTEL_CPU_REQUEST_ENV, "50m")
	}

	It("creates a CNPG cluster spec with default bootstrap", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Image: &dbpreview.ImageSpec{
					Postgres: "ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie",
				},
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "documentdb-oss:1.0", "test-sa", "standard", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Name).To(Equal("test-cluster"))
		Expect(result.Namespace).To(Equal("default"))
		Expect(int(result.Spec.Instances)).To(Equal(3))
		Expect(result.Spec.Bootstrap).ToNot(BeNil())
		Expect(result.Spec.Bootstrap.InitDB).ToNot(BeNil())

		// ImageVolume mode: PostgresImage as ImageName, extension via ImageVolumeSource
		Expect(result.Spec.ImageName).To(Equal("ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie"))
		Expect(result.Spec.PostgresConfiguration.Extensions).To(HaveLen(1))
		Expect(result.Spec.PostgresConfiguration.Extensions[0].Name).To(Equal("documentdb"))
		Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb-oss:1.0"))
		Expect(result.Spec.PostgresConfiguration.Extensions[0].DynamicLibraryPath).To(Equal([]string{"lib"}))
		Expect(result.Spec.PostgresConfiguration.Extensions[0].ExtensionControlPath).To(Equal([]string{"share"}))
		Expect(result.Spec.PostgresConfiguration.Extensions[0].LdLibraryPath).To(Equal([]string{"lib", "system"}))
		Expect(result.Spec.PostgresConfiguration.AdditionalLibraries).To(ConsistOf("pg_cron", "pg_documentdb_core", "pg_documentdb"))
		Expect(result.Spec.PostgresConfiguration.Parameters).To(HaveKeyWithValue("cron.database_name", "postgres"))
		Expect(result.Spec.PostgresConfiguration.PgHBA).To(HaveLen(2))
		Expect(result.Spec.PostgresConfiguration.PgHBA[0]).To(Equal("host all all localhost trust"))
		Expect(result.Spec.PostgresConfiguration.PgHBA[1]).To(Equal("hostssl replication streaming_replica all cert"))
		Expect(result.Spec.PostgresUID).To(Equal(int64(0)))
		Expect(result.Spec.PostgresGID).To(Equal(int64(0)))
	})

	It("creates a CNPG cluster spec with backup recovery", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "test-backup",
						},
					},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "standard", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.Bootstrap).ToNot(BeNil())
		Expect(result.Spec.Bootstrap.Recovery).ToNot(BeNil())
		Expect(result.Spec.Bootstrap.Recovery.Backup).ToNot(BeNil())
		Expect(result.Spec.Bootstrap.Recovery.Backup.LocalObjectReference.Name).To(Equal("test-backup"))
	})

	It("creates a CNPG cluster spec with PV recovery", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-cluster",
			},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						PersistentVolume: &dbpreview.PVRecoveryConfiguration{
							Name: "test-pv",
						},
					},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "standard", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.Bootstrap).ToNot(BeNil())
		Expect(result.Spec.Bootstrap.Recovery).ToNot(BeNil())
		Expect(result.Spec.Bootstrap.Recovery.VolumeSnapshots).ToNot(BeNil())
		// Temp PVC name is based on documentdb name
		Expect(result.Spec.Bootstrap.Recovery.VolumeSnapshots.Storage.Name).To(Equal("test-cluster-pv-recovery-temp"))
		Expect(result.Spec.Bootstrap.Recovery.VolumeSnapshots.Storage.Kind).To(Equal("PersistentVolumeClaim"))
	})

	It("uses specified storage class", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "premium-storage", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.StorageConfiguration.StorageClass).ToNot(BeNil())
		Expect(*result.Spec.StorageConfiguration.StorageClass).To(Equal("premium-storage"))
	})

	It("uses nil storage class when empty string is provided", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.StorageConfiguration.StorageClass).To(BeNil())
	})

	It("includes TLS secret in plugin parameters when TLS is ready", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
			},
			Status: dbpreview.DocumentDBStatus{
				TLS: &dbpreview.TLSStatus{
					Ready:      true,
					SecretName: "my-tls-secret",
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.Plugins).To(HaveLen(1))
		Expect(result.Spec.Plugins[0].Parameters).To(HaveKey("gatewayTLSSecret"))
		Expect(result.Spec.Plugins[0].Parameters["gatewayTLSSecret"]).To(Equal("my-tls-secret"))
	})

	It("uses custom SidecarInjectorName when specified", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Plugins: &dbpreview.PluginsSpec{
					SidecarInjectorName: "custom-injector",
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.Plugins).To(HaveLen(1))
		Expect(result.Spec.Plugins[0].Name).To(Equal("custom-injector"))
	})

	It("applies TLS and certificate configuration together", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		certificatesConfig := &cnpgv1.CertificatesConfiguration{
			ServerTLSSecret:      "server-tls-secret",
			ServerCASecret:       "server-ca-secret",
			ReplicationTLSSecret: "replication-tls-secret",
			ClientCASecret:       "client-ca-secret",
		}

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 3,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				TLS: &dbpreview.TLSConfiguration{
					Postgres: certificatesConfig,
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.Certificates).ToNot(BeNil())
		Expect(result.Spec.Certificates).To(Equal(certificatesConfig))
	})

	It("handles nil plugins and nil TLS gracefully", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Plugins: nil,
				TLS:     nil,
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "", true, log)
		Expect(result).ToNot(BeNil())
		Expect(result.Spec.Plugins).To(HaveLen(1))
		Expect(result.Spec.Plugins[0].Name).To(Equal(util.DEFAULT_SIDECAR_INJECTOR_PLUGIN))
		Expect(result.Spec.Certificates).To(BeNil())
	})

	It("passes gatewayImagePullPolicy to plugin params when env var is set", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		GinkgoT().Setenv(util.GATEWAY_IMAGE_PULL_POLICY_ENV, "Never")
		result := GetCnpgClusterSpec(req, documentdb, "ext:1.0", "test-sa", "", true, log)
		Expect(result.Spec.Plugins[0].Parameters).To(HaveKeyWithValue("gatewayImagePullPolicy", "Never"))
	})

	It("omits gatewayImagePullPolicy when env var is not set", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "ext:1.0", "test-sa", "", true, log)
		Expect(result.Spec.Plugins[0].Parameters).ToNot(HaveKey("gatewayImagePullPolicy"))
	})

	It("sets extension image pull policy from env var", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		GinkgoT().Setenv(util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV, "Never")
		result := GetCnpgClusterSpec(req, documentdb, "ext:1.0", "test-sa", "", true, log)
		Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.PullPolicy).To(Equal(corev1.PullNever))
	})

	It("leaves extension image pull policy empty when env var is not set", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		result := GetCnpgClusterSpec(req, documentdb, "ext:1.0", "test-sa", "", true, log)
		Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.PullPolicy).To(BeEmpty())
	})

	Context("wal_level parameter", func() {
		It("does not include wal_level when featureGates is nil", func() {
			req := ctrl.Request{}
			req.Name = "test-cluster"
			req.Namespace = "default"

			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "10Gi",
						},
					},
				},
			}

			cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
			_, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
			Expect(exists).To(BeFalse())
		})

		It("sets wal_level to logical when ChangeStreams feature gate is enabled", func() {
			req := ctrl.Request{}
			req.Name = "test-cluster"
			req.Namespace = "default"

			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "10Gi",
						},
					},
					FeatureGates: map[string]bool{
						dbpreview.FeatureGateChangeStreams: true,
					},
				},
			}

			cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
			walLevel, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
			Expect(exists).To(BeTrue())
			Expect(walLevel).To(Equal("logical"))
		})

		It("does not include wal_level when ChangeStreams feature gate is explicitly disabled", func() {
			req := ctrl.Request{}
			req.Name = "test-cluster"
			req.Namespace = "default"

			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "10Gi",
						},
					},
					FeatureGates: map[string]bool{
						dbpreview.FeatureGateChangeStreams: false,
					},
				},
			}

			cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
			_, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
			Expect(exists).To(BeFalse())
		})
	})

	Context("IOUring seccomp profile", func() {
		var req ctrl.Request

		BeforeEach(func() {
			req = ctrl.Request{}
			req.Name = "test-cluster"
			req.Namespace = "default"
		})

		createDocumentDB := func(featureGateEnabled bool) *dbpreview.DocumentDB {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "10Gi",
						},
					},
				},
			}
			if featureGateEnabled {
				documentdb.Spec.FeatureGates = map[string]bool{
					dbpreview.FeatureGateIOUring: true,
				}
			}
			return documentdb
		}

		It("does not set seccomp profile or io_method when IOUring is disabled", func() {
			cluster := GetCnpgClusterSpec(req, createDocumentDB(false), "test-image:latest", "test-sa", "", true, log)

			Expect(cluster.Spec.SeccompProfile).To(BeNil())
			Expect(cluster.Spec.PostgresConfiguration.Parameters).NotTo(HaveKey("io_method"))
		})

		It("uses the default Localhost seccomp profile when IOUring is enabled and env is unset", func() {
			GinkgoT().Setenv(util.IOURING_SECCOMP_PROFILE_ENV, "")

			cluster := GetCnpgClusterSpec(req, createDocumentDB(true), "test-image:latest", "test-sa", "", true, log)

			Expect(cluster.Spec.SeccompProfile).ToNot(BeNil())
			Expect(cluster.Spec.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeLocalhost))
			Expect(cluster.Spec.SeccompProfile.LocalhostProfile).ToNot(BeNil())
			Expect(*cluster.Spec.SeccompProfile.LocalhostProfile).To(Equal(util.DEFAULT_IOURING_SECCOMP_PROFILE))
			Expect(cluster.Spec.PostgresConfiguration.Parameters).To(HaveKeyWithValue("io_method", "io_uring"))
		})

		It("uses the custom Localhost seccomp profile when configured", func() {
			GinkgoT().Setenv(util.IOURING_SECCOMP_PROFILE_ENV, "profiles/custom-iouring.json")

			cluster := GetCnpgClusterSpec(req, createDocumentDB(true), "test-image:latest", "test-sa", "", true, log)

			Expect(cluster.Spec.SeccompProfile).ToNot(BeNil())
			Expect(cluster.Spec.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeLocalhost))
			Expect(cluster.Spec.SeccompProfile.LocalhostProfile).ToNot(BeNil())
			Expect(*cluster.Spec.SeccompProfile.LocalhostProfile).To(Equal("profiles/custom-iouring.json"))
			Expect(cluster.Spec.PostgresConfiguration.Parameters).To(HaveKeyWithValue("io_method", "io_uring"))
		})
	})

	It("always includes default PostgreSQL parameters", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
		params := cluster.Spec.PostgresConfiguration.Parameters
		Expect(params).To(HaveKeyWithValue("cron.database_name", "postgres"))
		Expect(params).To(HaveKeyWithValue("max_replication_slots", "10"))
		Expect(params).To(HaveKeyWithValue("max_wal_senders", "10"))
	})

	It("uses carved postgres resources and gateway plugin params when monitoring is disabled", func() {
		setProdSplitEnv()
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
					Memory: "16Gi",
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
		expectedPostgresMemory := resource.MustParse("13Gi")
		Expect(cluster.Spec.Resources.Limits[corev1.ResourceMemory]).To(Equal(expectedPostgresMemory))
		Expect(cluster.Spec.Resources.Requests[corev1.ResourceMemory]).To(Equal(expectedPostgresMemory))
		Expect(cluster.Spec.Plugins[0].Parameters).To(HaveKeyWithValue(util.PLUGIN_PARAM_GATEWAY_MEMORY_REQUEST, "3Gi"))
		Expect(cluster.Spec.Plugins[0].Parameters).To(HaveKeyWithValue(util.PLUGIN_PARAM_GATEWAY_MEMORY_LIMIT, "3Gi"))
		Expect(cluster.Spec.Plugins[0].Parameters).NotTo(HaveKey(util.PLUGIN_PARAM_OTEL_MEMORY_REQUEST))
		Expect(cluster.Spec.Plugins[0].Parameters).NotTo(HaveKey(util.PLUGIN_PARAM_OTEL_MEMORY_LIMIT))
		Expect(cluster.Spec.PostgresConfiguration.Parameters).To(HaveKeyWithValue("shared_buffers", "3328MB"))
	})

	It("passes OTel resource plugin params and carves OTel memory when monitoring is enabled", func() {
		setProdSplitEnv()
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
					Memory: "16Gi",
				},
				Monitoring: &dbpreview.MonitoringSpec{
					Enabled: true,
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
		expectedPostgresMemory := resource.MustParse("13184Mi")
		Expect(cluster.Spec.Resources.Limits[corev1.ResourceMemory]).To(Equal(expectedPostgresMemory))
		Expect(cluster.Spec.Resources.Requests[corev1.ResourceMemory]).To(Equal(expectedPostgresMemory))
		Expect(cluster.Spec.Plugins[0].Parameters).To(HaveKeyWithValue(util.PLUGIN_PARAM_GATEWAY_MEMORY_LIMIT, "3Gi"))
		Expect(cluster.Spec.Plugins[0].Parameters).To(HaveKeyWithValue(util.PLUGIN_PARAM_OTEL_MEMORY_REQUEST, "48Mi"))
		Expect(cluster.Spec.Plugins[0].Parameters).To(HaveKeyWithValue(util.PLUGIN_PARAM_OTEL_MEMORY_LIMIT, "128Mi"))
		Expect(cluster.Spec.Plugins[0].Parameters).To(HaveKeyWithValue(util.PLUGIN_PARAM_OTEL_CPU_REQUEST, "50m"))
	})

	It("passes monitoring parameters to plugin when monitoring is enabled", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Monitoring: &dbpreview.MonitoringSpec{
					Enabled: true,
					Exporter: &dbpreview.ExporterSpec{
						OTLP: &dbpreview.OTLPExporterSpec{
							Endpoint: "otel-collector.monitoring:4317",
						},
					},
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
		Expect(cluster.Spec.Plugins).To(HaveLen(1))
		pluginParams := cluster.Spec.Plugins[0].Parameters
		Expect(pluginParams).NotTo(HaveKey("monitoringEnabled"))
		Expect(pluginParams).To(HaveKey("otelCollectorImage"))
		Expect(pluginParams).To(HaveKeyWithValue("otelConfigMapName", "test-cluster-otel-config"))
		Expect(pluginParams).NotTo(HaveKey("prometheusPort"))
	})

	It("passes prometheusPort parameter when Prometheus exporter is configured", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Monitoring: &dbpreview.MonitoringSpec{
					Enabled: true,
					Exporter: &dbpreview.ExporterSpec{
						Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
					},
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
		pluginParams := cluster.Spec.Plugins[0].Parameters
		Expect(pluginParams).NotTo(HaveKey("monitoringEnabled"))
		Expect(pluginParams).To(HaveKeyWithValue("prometheusPort", "9090"))
	})

	It("does not pass monitoring parameters when monitoring is nil", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)
		Expect(cluster.Spec.Plugins).To(HaveLen(1))
		pluginParams := cluster.Spec.Plugins[0].Parameters
		Expect(pluginParams).NotTo(HaveKey("monitoringEnabled"))
		Expect(pluginParams).NotTo(HaveKey("otelCollectorImage"))
		Expect(pluginParams).NotTo(HaveKey("otelConfigMapName"))
	})

	It("declares a password-disabled otel_monitor role when monitoring is enabled", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				Monitoring: &dbpreview.MonitoringSpec{Enabled: true},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)

		Expect(cluster.Spec.Managed).NotTo(BeNil())
		Expect(cluster.Spec.Managed.Roles).To(HaveLen(1))
		role := cluster.Spec.Managed.Roles[0]
		Expect(role.Name).To(Equal("otel_monitor"))
		Expect(role.Login).To(BeTrue())
		Expect(role.Ensure).To(Equal(cnpgv1.EnsurePresent))
		Expect(role.InRoles).To(BeEmpty())
		Expect(role.PasswordSecret).To(BeNil())
		Expect(role.DisablePassword).To(BeTrue())
		Expect(role.Superuser).To(BeFalse())
		Expect(role.CreateDB).To(BeFalse())
		Expect(role.CreateRole).To(BeFalse())
	})

	It("declares the otel_monitor role absent when monitoring is disabled", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"

		documentdb := &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
				Monitoring: &dbpreview.MonitoringSpec{Enabled: false},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "test-image:latest", "test-sa", "", true, log)

		Expect(cluster.Spec.Managed).NotTo(BeNil())
		Expect(cluster.Spec.Managed.Roles).To(Equal([]cnpgv1.RoleConfiguration{absentOtelMonitorRole()}))
	})

	It("propagates spec.imagePullSecrets to the CNPG cluster spec", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				ImagePullSecrets: []corev1.LocalObjectReference{
					{Name: "registry-creds"},
					{Name: ""},
					{Name: "private-pull"},
				},
				Image: &dbpreview.ImageSpec{
					Postgres: "ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie",
				},
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "documentdb-oss:1.0", "test-sa", "", true, log)
		Expect(cluster.Spec.ImagePullSecrets).To(HaveLen(2))
		Expect(cluster.Spec.ImagePullSecrets[0].Name).To(Equal("registry-creds"))
		Expect(cluster.Spec.ImagePullSecrets[1].Name).To(Equal("private-pull"))
	})

	It("propagates spec.postgres.uid and gid to PostgresUID/PostgresGID", func() {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Image: &dbpreview.ImageSpec{
					Postgres: "ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie",
				},
				Postgres: &dbpreview.PostgresSpec{
					UID: ptr.To(int64(1001)),
					GID: ptr.To(int64(1002)),
				},
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}

		cluster := GetCnpgClusterSpec(req, documentdb, "documentdb-oss:1.0", "test-sa", "", true, log)
		Expect(cluster.Spec.PostgresUID).To(Equal(int64(1001)))
		Expect(cluster.Spec.PostgresGID).To(Equal(int64(1002)))
	})
})

// Standard Go tests for additional coverage

func TestGetInheritedMetadataLabels(t *testing.T) {
	tests := []struct {
		name     string
		appName  string
		expected map[string]string
	}{
		{
			name:    "standard app name",
			appName: "my-documentdb",
			expected: map[string]string{
				util.LABEL_APP:          "my-documentdb",
				util.LABEL_REPLICA_TYPE: "primary",
			},
		},
		{
			name:    "app name with special characters",
			appName: "test-db-123",
			expected: map[string]string{
				util.LABEL_APP:          "test-db-123",
				util.LABEL_REPLICA_TYPE: "primary",
			},
		},
		{
			name:    "empty app name",
			appName: "",
			expected: map[string]string{
				util.LABEL_APP:          "",
				util.LABEL_REPLICA_TYPE: "primary",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInheritedMetadataLabels(tt.appName)

			if result == nil {
				t.Fatal("Expected non-nil result")
			}

			if result.Labels == nil {
				t.Fatal("Expected non-nil labels map")
			}

			for key, expectedValue := range tt.expected {
				if actualValue, exists := result.Labels[key]; !exists {
					t.Errorf("Expected label %q to exist", key)
				} else if actualValue != expectedValue {
					t.Errorf("Expected label %q = %q, got %q", key, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestGetMaxStopDelayOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		documentdb *dbpreview.DocumentDB
		expected   int32
	}{
		{
			name: "returns default when StopDelay is 0",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Timeouts: dbpreview.Timeouts{
						StopDelay: 0,
					},
				},
			},
			expected: util.CNPG_DEFAULT_STOP_DELAY,
		},
		{
			name: "returns custom StopDelay when set",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Timeouts: dbpreview.Timeouts{
						StopDelay: 60,
					},
				},
			},
			expected: 60,
		},
		{
			name: "returns max StopDelay",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Timeouts: dbpreview.Timeouts{
						StopDelay: 1800,
					},
				},
			},
			expected: 1800,
		},
		{
			name: "returns default when Timeouts is empty",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			},
			expected: util.CNPG_DEFAULT_STOP_DELAY,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMaxStopDelayOrDefault(tt.documentdb)

			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

var _ = Describe("parseMemoryToBytes", func() {
	It("returns 0 for empty string", func() {
		Expect(parseMemoryToBytes("")).To(Equal(int64(0)))
	})

	It("returns 0 for '0'", func() {
		Expect(parseMemoryToBytes("0")).To(Equal(int64(0)))
	})

	It("returns 0 for invalid quantity", func() {
		Expect(parseMemoryToBytes("notavalue")).To(Equal(int64(0)))
	})

	It("parses Gi values correctly", func() {
		Expect(parseMemoryToBytes("2Gi")).To(Equal(int64(2 * 1024 * 1024 * 1024)))
	})

	It("parses Mi values correctly", func() {
		Expect(parseMemoryToBytes("512Mi")).To(Equal(int64(512 * 1024 * 1024)))
	})
})

var _ = Describe("buildResourceRequirements", func() {
	It("returns empty requirements when both memory and cpu are empty", func() {
		result := buildResourceRequirements(ComponentResource{})
		Expect(result.Limits).To(BeNil())
		Expect(result.Requests).To(BeNil())
	})

	It("returns empty requirements when both memory and cpu are '0'", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "0",
			MemoryLimit:   "0",
			CPURequest:    "0",
			CPULimit:      "0",
		})
		Expect(result.Limits).To(BeNil())
		Expect(result.Requests).To(BeNil())
	})

	It("sets memory limits and requests with Guaranteed QoS", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "4Gi",
			MemoryLimit:   "4Gi",
		})
		expectedMem := resource.MustParse("4Gi")
		Expect(result.Limits[corev1.ResourceMemory]).To(Equal(expectedMem))
		Expect(result.Requests[corev1.ResourceMemory]).To(Equal(expectedMem))
	})

	It("sets cpu limits and requests with Guaranteed QoS", func() {
		result := buildResourceRequirements(ComponentResource{
			CPURequest: "2",
			CPULimit:   "2",
		})
		expectedCPU := resource.MustParse("2")
		Expect(result.Limits[corev1.ResourceCPU]).To(Equal(expectedCPU))
		Expect(result.Requests[corev1.ResourceCPU]).To(Equal(expectedCPU))
	})

	It("sets both memory and cpu", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "8Gi",
			MemoryLimit:   "8Gi",
			CPURequest:    "4",
			CPULimit:      "4",
		})
		Expect(result.Limits[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
		Expect(result.Limits[corev1.ResourceCPU]).To(Equal(resource.MustParse("4")))
		Expect(result.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
		Expect(result.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("4")))
	})

	It("sets requests and limits independently", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "6Gi",
			MemoryLimit:   "8Gi",
			CPURequest:    "1500m",
			CPULimit:      "2",
		})
		Expect(result.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse("6Gi")))
		Expect(result.Limits[corev1.ResourceMemory]).To(Equal(resource.MustParse("8Gi")))
		Expect(result.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("1500m")))
		Expect(result.Limits[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
	})

	It("ignores invalid memory values gracefully", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "notvalid",
			MemoryLimit:   "notvalid",
			CPURequest:    "2",
			CPULimit:      "2",
		})
		_, hasMem := result.Limits[corev1.ResourceMemory]
		Expect(hasMem).To(BeFalse())
		Expect(result.Limits[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
		Expect(result.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("2")))
	})

	It("ignores invalid cpu values gracefully", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "4Gi",
			MemoryLimit:   "4Gi",
			CPURequest:    "notvalid",
			CPULimit:      "notvalid",
		})
		_, hasCPU := result.Limits[corev1.ResourceCPU]
		Expect(hasCPU).To(BeFalse())
		Expect(result.Limits[corev1.ResourceMemory]).To(Equal(resource.MustParse("4Gi")))
		Expect(result.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse("4Gi")))
	})

	It("returns empty requirements when all values are invalid", func() {
		result := buildResourceRequirements(ComponentResource{
			MemoryRequest: "notvalid",
			MemoryLimit:   "notvalid",
			CPURequest:    "alsonotvalid",
			CPULimit:      "alsonotvalid",
		})
		Expect(result.Limits).To(BeNil())
		Expect(result.Requests).To(BeNil())
	})
})
