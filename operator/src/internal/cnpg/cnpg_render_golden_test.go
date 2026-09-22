// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite golden render fixtures")

var goldenCerts = cnpgv1.CertificatesConfiguration{ServerCASecret: "pg-ca", ServerTLSSecret: "pg-tls"}

// TestRenderGolden renders the full CNPG Cluster for a DocumentDB spec matrix and
// compares it against committed golden fixtures. It is the render regression
// guard for the DocumentDB -> ClusterIntent -> CNPG pipeline: any change that
// alters a rendered Cluster must be reflected in testdata/render/*.golden.yaml.
// Regenerate with: go test ./internal/cnpg -run TestRenderGolden -update-golden
func TestRenderGolden(t *testing.T) {
	log := zap.New()
	req := ctrl.Request{}
	req.Name = "drift"
	req.Namespace = "default"

	ptr := func(v int64) *int64 { return &v }

	cases := map[string]*dbpreview.DocumentDB{
		"minimal": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
			},
		},
		"custom-loglevel-and-stopdelay": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				LogLevel:         "debug",
				Timeouts:         dbpreview.Timeouts{StopDelay: 120},
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
			},
		},
		"user-params": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
				Postgres: &dbpreview.PostgresSpec{
					Parameters: map[string]string{"work_mem": "64MB", "max_connections": "200"},
				},
			},
		},
		"resource-envelope": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
					Memory:  "8Gi",
					CPU:     "4",
				},
			},
		},
		"resource-overrides": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage:  dbpreview.StorageConfiguration{PvcSize: "10Gi"},
					Gateway:  &dbpreview.ComponentResources{Memory: "512Mi", CPU: "500m"},
					Database: &dbpreview.ComponentResources{Memory: "4Gi", CPU: "2"},
				},
			},
		},
		"process-identity": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
				Postgres:         &dbpreview.PostgresSpec{UID: ptr(26), GID: ptr(26)},
			},
		},
		"iouring-and-changestreams": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
				FeatureGates: map[string]bool{
					string(dbpreview.FeatureGateIOUring):       true,
					string(dbpreview.FeatureGateChangeStreams): true,
				},
			},
		},
		"postgres-tls": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
				TLS: &dbpreview.TLSConfiguration{
					Postgres: &goldenCerts,
				},
			},
		},
		"gateway-tls-ready": {
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
			},
			Status: dbpreview.DocumentDBStatus{
				TLS: &dbpreview.TLSStatus{Ready: true, SecretName: "gw-tls-secret"},
			},
		},
		"monitoring-prometheus": {
			ObjectMeta: metav1.ObjectMeta{Name: "mon-prom"},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
				Monitoring: &dbpreview.MonitoringSpec{
					Enabled: true,
					Exporter: &dbpreview.ExporterSpec{
						Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
					},
				},
			},
		},
		"monitoring-otlp-and-prometheus": {
			ObjectMeta: metav1.ObjectMeta{Name: "mon-both"},
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"}},
				Monitoring: &dbpreview.MonitoringSpec{
					Enabled: true,
					Exporter: &dbpreview.ExporterSpec{
						OTLP:       &dbpreview.OTLPExporterSpec{Endpoint: "otel-collector:4317"},
						Prometheus: &dbpreview.PrometheusExporterSpec{},
					},
				},
			},
		},
	}

	for name, db := range cases {
		t.Run(name, func(t *testing.T) {
			cluster := GetCnpgClusterSpec(req, db, "", "test-sa", "", true, log)
			got, err := yaml.Marshal(cluster)
			if err != nil {
				t.Fatalf("marshal cluster: %v", err)
			}
			path := filepath.Join("testdata", "render", name+".golden.yaml")
			if *updateGolden {
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
				t.Fatalf("read golden (regenerate with -update-golden): %v", err)
			}
			if diff := cmp.Diff(string(want), string(got)); diff != "" {
				t.Errorf("render drift (-want +got):\n%s", diff)
			}
		})
	}
}
