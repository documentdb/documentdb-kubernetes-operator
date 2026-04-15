// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package otel

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

func TestOtel(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Otel Suite")
}

var _ = Describe("ConfigMapName", func() {
	It("returns the expected ConfigMap name", func() {
		Expect(ConfigMapName("my-cluster")).To(Equal("my-cluster-otel-config"))
	})
})

// parseCfg is a helper to unmarshal the generated YAML into a collectorConfig struct.
func parseCfg(yamlStr string) collectorConfig {
	var cfg collectorConfig
	// Strip the auto-generated comment header before parsing
	ExpectWithOffset(1, yaml.Unmarshal([]byte(yamlStr), &cfg)).To(Succeed())
	return cfg
}

var _ = Describe("GenerateBaseYAML", func() {
	It("generates YAML with debug exporter only when no exporter is configured", func() {
		spec := &dbpreview.MonitoringSpec{Enabled: true}
		result, err := GenerateBaseYAML("test-cluster", "test-ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)

		// Receivers: otlp only
		Expect(cfg.Receivers).To(HaveKey("otlp"))

		// Processors: batch and resource
		Expect(cfg.Processors).To(HaveKey("batch"))
		Expect(cfg.Processors).To(HaveKey("resource"))

		// Exporters: debug only
		Expect(cfg.Exporters).To(HaveKey("debug"))
		Expect(cfg.Exporters).NotTo(HaveKey("otlp"))
		Expect(cfg.Exporters).NotTo(HaveKey("prometheus"))

		// Pipeline exporters
		Expect(cfg.Service.Pipelines).To(HaveKey("metrics"))
		Expect(cfg.Service.Pipelines["metrics"].Exporters).To(ConsistOf("debug"))
	})

	It("includes OTLP exporter when configured", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				OTLP: &dbpreview.OTLPExporterSpec{
					Endpoint: "otel-collector.monitoring:4317",
				},
			},
		}
		result, err := GenerateBaseYAML("prod-cluster", "prod-ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)

		Expect(cfg.Exporters).To(HaveKey("otlp"))
		Expect(cfg.Service.Pipelines["metrics"].Exporters).To(ContainElements("debug", "otlp"))
	})

	It("skips OTLP exporter when endpoint is empty", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				OTLP: &dbpreview.OTLPExporterSpec{Endpoint: ""},
			},
		}
		result, err := GenerateBaseYAML("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)
		Expect(cfg.Exporters).NotTo(HaveKey("otlp"))
		Expect(cfg.Service.Pipelines["metrics"].Exporters).To(ConsistOf("debug"))
	})

	It("handles nil Exporter spec", func() {
		spec := &dbpreview.MonitoringSpec{Enabled: true, Exporter: nil}
		result, err := GenerateBaseYAML("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)
		Expect(cfg.Exporters).To(HaveKey("debug"))
		Expect(cfg.Exporters).NotTo(HaveKey("otlp"))
		Expect(cfg.Exporters).NotTo(HaveKey("prometheus"))
	})

	It("includes Prometheus exporter with default port", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{},
			},
		}
		result, err := GenerateBaseYAML("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)
		Expect(cfg.Exporters).To(HaveKey("prometheus"))

		promCfg, ok := cfg.Exporters["prometheus"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(promCfg["endpoint"]).To(Equal("0.0.0.0:8888"))

		Expect(cfg.Service.Pipelines["metrics"].Exporters).To(ContainElement("prometheus"))
	})

	It("includes Prometheus exporter with custom port", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		result, err := GenerateBaseYAML("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)
		promCfg, ok := cfg.Exporters["prometheus"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(promCfg["endpoint"]).To(Equal("0.0.0.0:9090"))
	})

	It("includes both OTLP and Prometheus exporters when both configured", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				OTLP:       &dbpreview.OTLPExporterSpec{Endpoint: "otel-collector:4317"},
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		result, err := GenerateBaseYAML("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		cfg := parseCfg(result)
		Expect(cfg.Exporters).To(HaveKey("otlp"))
		Expect(cfg.Exporters).To(HaveKey("prometheus"))
		Expect(cfg.Exporters).To(HaveKey("debug"))
		Expect(cfg.Service.Pipelines["metrics"].Exporters).To(ContainElements("debug", "otlp", "prometheus"))
	})

	It("injects resource attributes for cluster, namespace, and pod", func() {
		spec := &dbpreview.MonitoringSpec{Enabled: true}
		result, err := GenerateBaseYAML("my-cluster", "my-ns", spec)
		Expect(err).NotTo(HaveOccurred())

		// Check the raw YAML contains resource attributes
		Expect(result).To(ContainSubstring("documentdb.cluster"))
		Expect(result).To(ContainSubstring("my-cluster"))
		Expect(result).To(ContainSubstring("k8s.namespace.name"))
		Expect(result).To(ContainSubstring("my-ns"))
		Expect(result).To(ContainSubstring("k8s.pod.name"))
		Expect(result).To(ContainSubstring("${POD_NAME}"))
	})
})

var _ = Describe("ResolvePrometheusPort", func() {
	It("returns 0 when spec is nil", func() {
		Expect(ResolvePrometheusPort(nil)).To(Equal(int32(0)))
	})

	It("returns 0 when Prometheus is not configured", func() {
		spec := &dbpreview.MonitoringSpec{Enabled: true}
		Expect(ResolvePrometheusPort(spec)).To(Equal(int32(0)))
	})

	It("returns default port when Port is 0", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{},
			},
		}
		Expect(ResolvePrometheusPort(spec)).To(Equal(int32(8888)))
	})

	It("returns custom port when set", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		Expect(ResolvePrometheusPort(spec)).To(Equal(int32(9090)))
	})
})
