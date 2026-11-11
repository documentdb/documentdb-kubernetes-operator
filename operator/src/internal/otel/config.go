// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package otel

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
)

//go:embed base_config.yaml
var baseConfigYAML []byte

// baseConfigTemplate renders static.yaml: base_config.yaml is a text/template
// over staticConfigData, so product-specific metric names stay structured.
var baseConfigTemplate = template.Must(template.New("base_config.yaml").Option("missingkey=error").Parse(string(baseConfigYAML)))

// staticConfigData holds the product-specific names base_config.yaml uses.
type staticConfigData struct {
	// HealthMetricName is the PostgreSQL liveness gauge.
	HealthMetricName string
}

// renderStaticConfig renders static.yaml from base_config.yaml.
func renderStaticConfig(data staticConfigData) (string, error) {
	metricName, err := yaml.Marshal(data.HealthMetricName)
	if err != nil {
		return "", fmt.Errorf("failed to encode health metric name: %w", err)
	}
	data.HealthMetricName = strings.TrimSuffix(string(metricName), "\n")
	var out bytes.Buffer
	if err := baseConfigTemplate.Execute(&out, data); err != nil {
		return "", fmt.Errorf("failed to render static OTel config: %w", err)
	}
	return out.String(), nil
}

const defaultPrometheusPort = 8888

// MonitorRoleName is the dedicated PostgreSQL identity the OTel Collector
// sidecar uses for its health-check query.
const MonitorRoleName = "otel_monitor"

// collectorConfig represents the OTel Collector configuration structure.
type collectorConfig struct {
	Receivers  map[string]any `yaml:"receivers,omitempty"`
	Processors map[string]any `yaml:"processors,omitempty"`
	Exporters  map[string]any `yaml:"exporters,omitempty"`
	Service    *serviceConfig `yaml:"service,omitempty"`
}

type serviceConfig struct {
	Telemetry *telemetryConfig          `yaml:"telemetry,omitempty"`
	Pipelines map[string]pipelineConfig `yaml:"pipelines,omitempty"`
}

type telemetryConfig struct {
	Metrics *telemetryMetricsConfig `yaml:"metrics,omitempty"`
}

type telemetryMetricsConfig struct {
	Level   string `yaml:"level,omitempty"`
	Address string `yaml:"address,omitempty"`
}

type pipelineConfig struct {
	Receivers  []string `yaml:"receivers"`
	Processors []string `yaml:"processors,omitempty"`
	Exporters  []string `yaml:"exporters"`
}

// ConfigMapName returns the OTel ConfigMap name for a given DocumentDB cluster.
func ConfigMapName(clusterName string) string {
	return fmt.Sprintf("%s-otel-config", clusterName)
}

// GenerateConfigMapData returns the ConfigMap data entries for the OTel Collector.
// The config is split into two files that the collector merges at startup:
//   - static.yaml: receivers and static processors rendered from the embedded
//     base_config.yaml template. Add new SQL metric queries to base_config.yaml —
//     no Go code changes needed unless a query needs a new product-specific name.
//   - dynamic.yaml: per-cluster resource attributes, exporters, and pipeline wiring.
//
// This function is only called when monitoring is enabled. When monitoring is
// disabled, the operator deletes the ConfigMap and removes sidecar parameters,
// then triggers a rolling restart (via restart annotation) so that CNPG
// recreates pods without the sidecar.
func GenerateConfigMapData(clusterName, namespace string, spec *dbv1.MonitoringSpec) (map[string]string, error) {
	return GenerateConfigMapDataWithOptions(Options{}, clusterName, namespace, spec)
}

// Options carries the product naming used in the generated collector config.
// Empty fields fall back to the DocumentDB defaults.
type Options struct {
	// ClusterAttribute is the resource attribute key holding the cluster name.
	ClusterAttribute string
	// Generator is the operator name written in the generated-file header.
	Generator string
	// HealthMetricName is the gauge the sqlquery receiver reports PostgreSQL
	// liveness as.
	HealthMetricName string
}

const (
	defaultClusterAttribute = "documentdb.cluster"
	defaultGenerator        = "documentdb-operator"
	// defaultHealthMetricName is the DocumentDB PostgreSQL liveness gauge.
	defaultHealthMetricName = "documentdb.postgres.up"
)

// GenerateConfigMapDataWithOptions is GenerateConfigMapData with product naming.
func GenerateConfigMapDataWithOptions(opts Options, clusterName, namespace string, spec *dbv1.MonitoringSpec) (map[string]string, error) {
	if opts.ClusterAttribute == "" {
		opts.ClusterAttribute = defaultClusterAttribute
	}
	if opts.Generator == "" {
		opts.Generator = defaultGenerator
	}
	if opts.HealthMetricName == "" {
		opts.HealthMetricName = defaultHealthMetricName
	}
	staticYAML, err := renderStaticConfig(staticConfigData{HealthMetricName: opts.HealthMetricName})
	if err != nil {
		return nil, err
	}
	dynamicYAML, err := generateDynamicConfig(opts.ClusterAttribute, clusterName, namespace, spec)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"static.yaml":  staticYAML,
		"dynamic.yaml": "# Auto-generated by " + opts.Generator + ". Do not edit.\n" + dynamicYAML,
	}, nil
}

// generateDynamicConfig builds the per-cluster dynamic config (resource
// processor, exporters, pipeline wiring) that the collector deep-merges
// with the embedded base_config.yaml.
func generateDynamicConfig(clusterAttribute, clusterName, namespace string, spec *dbv1.MonitoringSpec) (string, error) {
	cfg := collectorConfig{
		Processors: map[string]any{
			// `insert` (not `upsert`) so receivers that already emit their
			// own k8s.* resource attrs keep them; `insert` only fills the
			// gap for receivers that don't (e.g. sqlquery).
			"resource": map[string]any{
				"attributes": []map[string]any{
					{"key": clusterAttribute, "value": clusterName, "action": "insert"},
					{"key": "k8s.namespace.name", "value": namespace, "action": "insert"},
					{"key": "k8s.pod.name", "value": "${POD_NAME}", "action": "insert"},
				},
			},
		},
	}

	// sqlquery (Postgres health) + otlp (gateway-pushed metrics) come from
	// base_config.yaml. Container/node resource metrics are served by a
	// node-level collector such as the playground reference DaemonSet, not
	// this sidecar.
	receiverNames := []string{"sqlquery", "otlp"}

	exporterNames := []string{}

	if spec.Exporter != nil {
		if otlp := spec.Exporter.OTLP; otlp != nil && otlp.Endpoint != "" {
			if cfg.Exporters == nil {
				cfg.Exporters = map[string]any{}
			}
			cfg.Exporters["otlp"] = map[string]any{
				"endpoint": otlp.Endpoint,
				"tls": map[string]any{
					// TODO: Support TLS for OTLP exporter. Currently hardcoded to
					// insecure for in-cluster communication. When TLS is needed,
					// add TLS config fields to OTLPExporterSpec (certSecret, etc.).
					"insecure": true,
				},
			}
			exporterNames = append(exporterNames, "otlp")
		}

		if prom := spec.Exporter.Prometheus; prom != nil {
			if cfg.Exporters == nil {
				cfg.Exporters = map[string]any{}
			}
			port := prom.Port
			if port == 0 {
				port = defaultPrometheusPort
			}
			cfg.Exporters["prometheus"] = map[string]any{
				"endpoint": fmt.Sprintf("0.0.0.0:%d", port),
				// Surface resource attributes as Prometheus labels (instead
				// of burying them in target_info) so dashboards can filter
				// by pod/container/cluster.
				"resource_to_telemetry_conversion": map[string]any{
					"enabled": true,
				},
			}
			exporterNames = append(exporterNames, "prometheus")
		}
	}

	// Wire pipeline: receivers + memory_limiter/batch from static.yaml,
	// resource from this dynamic config.
	// Disable the collector's internal telemetry to avoid port conflicts with the
	// Prometheus exporter (both default to 8888).
	if len(exporterNames) > 0 {
		cfg.Service = &serviceConfig{
			Telemetry: &telemetryConfig{
				Metrics: &telemetryMetricsConfig{
					Level: "none",
				},
			},
			Pipelines: map[string]pipelineConfig{
				"metrics": {
					Receivers:  receiverNames,
					Processors: []string{"memory_limiter", "resource", "batch"},
					Exporters:  exporterNames,
				},
			},
		}
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal dynamic OTel config: %w", err)
	}

	return string(out), nil
}

// HashConfigMapData computes a truncated SHA-256 hash of ConfigMap data.
// The hash is stored as a CNPG plugin parameter so that the operator detects
// config changes (via parameter diff in SyncCnpgCluster) and triggers a
// rolling restart via a restart annotation
// (e.g., operator upgrade adds new metrics, user changes exporter settings).
func HashConfigMapData(data map[string]string) string {
	h := sha256.New()
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Use length-prefixed format to avoid ambiguity between key/value boundaries.
		fmt.Fprintf(h, "%d:%s%d:%s;", len(k), k, len(data[k]), data[k])
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// ResolvePrometheusPort returns the effective Prometheus port from the spec,
// or 0 if Prometheus exporter is not configured.
func ResolvePrometheusPort(spec *dbv1.MonitoringSpec) int32 {
	if spec == nil || spec.Exporter == nil || spec.Exporter.Prometheus == nil {
		return 0
	}
	if spec.Exporter.Prometheus.Port == 0 {
		return defaultPrometheusPort
	}
	return spec.Exporter.Prometheus.Port
}
