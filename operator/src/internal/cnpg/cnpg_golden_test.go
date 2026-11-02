// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// renderEnv pins every operator environment variable that influences the
// rendered Cluster so golden output is independent of the developer machine.
var renderEnv = []string{
	util.DOCUMENTDB_VERSION_ENV,
	util.GATEWAY_IMAGE_PULL_POLICY_ENV,
	util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV,
	util.DOCUMENTDB_EXTENSION_IMAGE_REPO_ENV,
	util.GATEWAY_IMAGE_REPO_ENV,
	util.OTEL_COLLECTOR_IMAGE_ENV,
	util.POSTGRES_IMAGE_ENV,
	util.IOURING_SECCOMP_PROFILE_ENV,
	util.GATEWAY_MEMORY_FRACTION_ENV,
	util.GATEWAY_MEMORY_CAP_ENV,
	util.GATEWAY_CPU_LIMIT_ENV,
	util.OTEL_MEMORY_REQUEST_ENV,
	util.OTEL_MEMORY_LIMIT_ENV,
	util.OTEL_CPU_REQUEST_ENV,
	util.OTEL_CPU_LIMIT_ENV,
}

func goldenBaseDocumentDB() *dbpreview.DocumentDB {
	return &dbpreview.DocumentDB{
		TypeMeta: metav1.TypeMeta{APIVersion: "documentdb.io/preview", Kind: "DocumentDB"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "golden",
			Namespace: "golden-ns",
			UID:       "00000000-0000-0000-0000-000000000001",
		},
		Spec: dbpreview.DocumentDBSpec{
			NodeCount:        1,
			InstancesPerNode: 1,
			Resource: dbpreview.Resource{
				Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
			},
		},
	}
}

type goldenCase struct {
	name         string
	env          map[string]string
	mutate       func(*dbpreview.DocumentDB)
	extImage     string
	storageClass string
	primary      bool
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{name: "defaults", primary: true},
		{
			name:         "full",
			primary:      true,
			storageClass: "premium",
			mutate: func(d *dbpreview.DocumentDB) {
				d.Spec.InstancesPerNode = 3
				d.Spec.Resource.Memory = "16Gi"
				d.Spec.Resource.CPU = "4"
				d.Spec.Image = &dbpreview.ImageSpec{
					DocumentDB: "reg.example/ext:9.9.9",
					Gateway:    "reg.example/gw:9.9.9",
					Postgres:   "reg.example/pg:18",
				}
				d.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "pull"}}
				d.Spec.DocumentDbCredentialSecret = "creds"
				d.Spec.Plugins = &dbpreview.PluginsSpec{SidecarInjectorName: "custom.injector.io"}
				d.Spec.Postgres = &dbpreview.PostgresSpec{
					UID:         ptr.To[int64](26),
					GID:         ptr.To[int64](26),
					PostInitSQL: []string{"SELECT 1"},
					Parameters:  map[string]string{"work_mem": "64MB", "max_wal_senders": "99"},
				}
				d.Spec.FeatureGates = map[string]bool{
					dbpreview.FeatureGateChangeStreams: true,
					dbpreview.FeatureGateIOUring:       true,
				}
				d.Spec.LogLevel = "debug"
				d.Spec.Timeouts.StopDelay = 120
				d.Spec.TLS = &dbpreview.TLSConfiguration{
					Postgres: &cnpgv1.CertificatesConfiguration{ServerTLSSecret: "pg-server-tls", ServerCASecret: "pg-ca"},
				}
				d.Status.TLS = &dbpreview.TLSStatus{Ready: true, SecretName: "gw-tls"}
				d.Spec.Monitoring = &dbpreview.MonitoringSpec{
					Enabled: true,
					Exporter: &dbpreview.ExporterSpec{
						OTLP:       &dbpreview.OTLPExporterSpec{Endpoint: "otel:4317"},
						Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9187},
					},
				}
				d.Spec.Affinity = cnpgv1.AffinityConfiguration{
					EnablePodAntiAffinity: ptr.To(true),
					TopologyKey:           "kubernetes.io/hostname",
				}
			},
		},
		{
			name:    "explicit-component-resources",
			primary: true,
			mutate: func(d *dbpreview.DocumentDB) {
				d.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "4Gi", CPU: "2"}
				d.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "1Gi", CPU: "500m"}
				d.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: true}
				d.Spec.Resource.OTel = &dbpreview.ComponentResources{Memory: "256Mi", CPU: "100m"}
			},
		},
		{
			name:    "recovery-backup",
			primary: true,
			mutate: func(d *dbpreview.DocumentDB) {
				d.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{Backup: cnpgv1.LocalObjectReference{Name: "bk"}},
				}
			},
		},
		{
			name:    "recovery-pv",
			primary: true,
			mutate: func(d *dbpreview.DocumentDB) {
				d.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "pv-1"},
					},
				}
			},
		},
		{
			name:    "replica-region-ignores-recovery",
			primary: false,
			mutate: func(d *dbpreview.DocumentDB) {
				d.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{Backup: cnpgv1.LocalObjectReference{Name: "bk"}},
				}
			},
		},
		{
			name:     "env-overrides",
			primary:  true,
			extImage: "override/ext:1",
			env: map[string]string{
				util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV: "Always",
				util.GATEWAY_IMAGE_PULL_POLICY_ENV:    "Never",
				util.POSTGRES_IMAGE_ENV:               "env/pg:17",
				util.GATEWAY_CPU_LIMIT_ENV:            "1",
				util.IOURING_SECCOMP_PROFILE_ENV:      "profiles/custom.json",
				util.OTEL_COLLECTOR_IMAGE_ENV:         "env/otel:1",
			},
			mutate: func(d *dbpreview.DocumentDB) {
				d.Spec.Resource.Memory = "8Gi"
				d.Spec.FeatureGates = map[string]bool{dbpreview.FeatureGateIOUring: true}
				d.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: true}
			},
		},
	}
}

// TestGetCnpgClusterSpecGolden locks the rendered CNPG Cluster for a matrix of
// DocumentDB inputs. Regenerate with UPDATE_GOLDEN=1 only for intentional
// rendering changes.
func TestGetCnpgClusterSpecGolden(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range renderEnv {
				t.Setenv(key, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			d := goldenBaseDocumentDB()
			if tc.mutate != nil {
				tc.mutate(d)
			}
			req := ctrl.Request{}
			req.Name = d.Name
			req.Namespace = d.Namespace

			cluster := GetCnpgClusterSpec(req, d, tc.extImage, d.Name, tc.storageClass, tc.primary, zap.New(zap.WriteTo(os.Stderr)))
			got, err := json.MarshalIndent(cluster, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')

			path := filepath.Join("testdata", "golden", tc.name+".json")
			if update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden file %s (run with UPDATE_GOLDEN=1): %v", path, err)
			}
			if string(want) != string(got) {
				t.Errorf("rendered cluster differs from %s\n--- want\n%s\n--- got\n%s", path, want, got)
			}
		})
	}
}
