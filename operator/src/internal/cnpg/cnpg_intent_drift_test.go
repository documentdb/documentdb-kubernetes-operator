// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"cmp"
	"fmt"
	"reflect"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var cnpgCertsForTest = cnpgv1.CertificatesConfiguration{ServerCASecret: "pg-ca", ServerTLSSecret: "pg-tls"}

// TestRenderIntentSeamNoDrift is the Phase 0 drift guard: it proves that routing
// the builder inputs through ClusterIntent produces exactly the same rendered
// Cluster as the retained *DocumentDB computations, across a spec matrix.
func TestRenderIntentSeamNoDrift(t *testing.T) {
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
					Postgres: &cnpgCertsForTest,
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
			spec := GetCnpgClusterSpec(req, db, "", "test-sa", "", true, log).Spec

			// Parameters: intent path must equal the direct MergeParameters over the
			// same memory-aware split.
			wantMem := ComputeResourceSplit(db, DefaultSplitConfig()).PostgresMemoryBytes
			wantParams := MergeParameters(db, wantMem)
			if !reflect.DeepEqual(spec.PostgresConfiguration.Parameters, wantParams) {
				t.Errorf("parameters drift:\n got  %v\n want %v", spec.PostgresConfiguration.Parameters, wantParams)
			}

			// LogLevel
			wantLog := cmp.Or(db.Spec.LogLevel, "info")
			if spec.LogLevel != wantLog {
				t.Errorf("logLevel drift: got %q want %q", spec.LogLevel, wantLog)
			}

			// MaxStopDelay
			wantStop := int32(util.CNPG_DEFAULT_STOP_DELAY)
			if db.Spec.Timeouts.StopDelay != 0 {
				wantStop = db.Spec.Timeouts.StopDelay
			}
			if spec.MaxStopDelay != wantStop {
				t.Errorf("maxStopDelay drift: got %d want %d", spec.MaxStopDelay, wantStop)
			}

			// Postgres certificates
			var wantCerts interface{}
			if db.Spec.TLS != nil {
				wantCerts = db.Spec.TLS.Postgres
			}
			if !reflect.DeepEqual(spec.Certificates, wantCerts) && !(spec.Certificates == nil && wantCerts == nil) {
				t.Errorf("certificates drift: got %v want %v", spec.Certificates, wantCerts)
			}

			// Gateway TLS secret plugin param
			gotTLS := spec.Plugins[0].Parameters["gatewayTLSSecret"]
			wantTLS := ""
			if db.Status.TLS != nil && db.Status.TLS.Ready && db.Status.TLS.SecretName != "" {
				wantTLS = db.Status.TLS.SecretName
			}
			if gotTLS != wantTLS {
				t.Errorf("gatewayTLSSecret drift: got %q want %q", gotTLS, wantTLS)
			}

			// Gateway resource params reflect the resource split.
			split := ComputeResourceSplit(db, DefaultSplitConfig())
			assertParamEq(t, spec.Plugins[0].Parameters, util.PLUGIN_PARAM_GATEWAY_MEMORY_REQUEST, split.Gateway.MemoryRequest)
			assertParamEq(t, spec.Plugins[0].Parameters, util.PLUGIN_PARAM_GATEWAY_MEMORY_LIMIT, split.Gateway.MemoryLimit)
			assertParamEq(t, spec.Plugins[0].Parameters, util.PLUGIN_PARAM_GATEWAY_CPU_REQUEST, split.Gateway.CPURequest)
			assertParamEq(t, spec.Plugins[0].Parameters, util.PLUGIN_PARAM_GATEWAY_CPU_LIMIT, split.Gateway.CPULimit)

			// OTel plugin params: the intent-driven path must equal the direct
			// otel computation from the monitoring spec (config map name, prometheus
			// port, and — critically — the config hash that drives pod restarts).
			mon := product.MonitoringConfigFromSpec(db.Spec.Monitoring)
			wantCM, wantPort, wantHash := "", "", ""
			if mon.Enabled {
				wantCM = otelcfg.ConfigMapName(db.Name)
				if p := otelcfg.ResolvePrometheusPort(mon); p > 0 {
					wantPort = fmt.Sprintf("%d", p)
				}
				if data, err := otelcfg.GenerateConfigMapData(db.Name, db.Namespace, mon); err == nil {
					wantHash = otelcfg.HashConfigMapData(data)
				}
			}
			assertParamEq(t, spec.Plugins[0].Parameters, "otelConfigMapName", wantCM)
			assertParamEq(t, spec.Plugins[0].Parameters, "prometheusPort", wantPort)
			assertParamEq(t, spec.Plugins[0].Parameters, "otelConfigHash", wantHash)

			// OTel monitor role: present (EnsurePresent) only when monitoring is on.
			gotRolePresent := false
			if spec.Managed != nil {
				for _, r := range spec.Managed.Roles {
					if r.Name == otelcfg.MonitorRoleName && r.Ensure == cnpgv1.EnsurePresent {
						gotRolePresent = true
					}
				}
			}
			if gotRolePresent != mon.Enabled {
				t.Errorf("otel monitor role presence drift: got %v want %v", gotRolePresent, mon.Enabled)
			}
		})
	}
}

func assertParamEq(t *testing.T, params map[string]string, key, want string) {
	t.Helper()
	got, present := params[key]
	if want == "" {
		if present {
			t.Errorf("param %q unexpectedly set to %q", key, got)
		}
		return
	}
	if got != want {
		t.Errorf("param %q drift: got %q want %q", key, got, want)
	}
}
