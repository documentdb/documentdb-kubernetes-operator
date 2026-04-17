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

// parseCfg is a helper to unmarshal YAML into a collectorConfig struct.
func parseCfg(yamlStr string) collectorConfig {
	var cfg collectorConfig
	ExpectWithOffset(1, yaml.Unmarshal([]byte(yamlStr), &cfg)).To(Succeed())
	return cfg
}

var _ = Describe("base_config.yaml embed", func() {
	It("can be parsed as valid YAML with expected static components", func() {
		var cfg collectorConfig
		Expect(yaml.Unmarshal(baseConfigYAML, &cfg)).To(Succeed())
		Expect(cfg.Receivers).To(HaveKey("sqlquery"))
		Expect(cfg.Processors).To(HaveKey("batch"))
		// Static config should NOT have exporters or service (those are dynamic)
		Expect(cfg.Exporters).To(BeEmpty())
	})
})

var _ = Describe("GenerateConfigMapData", func() {
	It("returns static.yaml from embedded base_config.yaml", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		// static.yaml should contain the embedded base config
		staticCfg := parseCfg(data["static.yaml"])
		Expect(staticCfg.Receivers).To(HaveKey("sqlquery"))
		Expect(staticCfg.Processors).To(HaveKey("batch"))
	})

	It("generates dynamic.yaml with resource processor and exporters", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("test-cluster", "test-ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])

		// Dynamic resource processor
		Expect(dynCfg.Processors).To(HaveKey("resource"))
		Expect(data["dynamic.yaml"]).To(ContainSubstring("documentdb.cluster"))
		Expect(data["dynamic.yaml"]).To(ContainSubstring("test-cluster"))
		Expect(data["dynamic.yaml"]).To(ContainSubstring("k8s.namespace.name"))
		Expect(data["dynamic.yaml"]).To(ContainSubstring("test-ns"))
		Expect(data["dynamic.yaml"]).To(ContainSubstring("${POD_NAME}"))

		// Prometheus exporter
		Expect(dynCfg.Exporters).To(HaveKey("prometheus"))

		// Pipeline wiring references receivers from static config
		Expect(dynCfg.Service.Pipelines["metrics"].Receivers).To(ConsistOf("sqlquery"))
		Expect(dynCfg.Service.Pipelines["metrics"].Processors).To(ConsistOf("resource", "batch"))
		Expect(dynCfg.Service.Pipelines["metrics"].Exporters).To(ConsistOf("prometheus"))
	})

	It("includes OTLP exporter in dynamic.yaml when configured", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				OTLP: &dbpreview.OTLPExporterSpec{
					Endpoint: "otel-collector.monitoring:4317",
				},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])
		Expect(dynCfg.Exporters).To(HaveKey("otlp"))
		Expect(dynCfg.Service.Pipelines["metrics"].Exporters).To(ContainElement("otlp"))
	})

	It("skips OTLP exporter when endpoint is empty", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				OTLP:       &dbpreview.OTLPExporterSpec{Endpoint: ""},
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])
		Expect(dynCfg.Exporters).NotTo(HaveKey("otlp"))
	})

	It("includes Prometheus exporter with default port", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])
		Expect(dynCfg.Exporters).To(HaveKey("prometheus"))
		promCfg, ok := dynCfg.Exporters["prometheus"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(promCfg["endpoint"]).To(Equal("0.0.0.0:8888"))
	})

	It("includes Prometheus exporter with custom port", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])
		promCfg, ok := dynCfg.Exporters["prometheus"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(promCfg["endpoint"]).To(Equal("0.0.0.0:9090"))
	})

	It("includes both OTLP and Prometheus exporters", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				OTLP:       &dbpreview.OTLPExporterSpec{Endpoint: "otel-collector:4317"},
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])
		Expect(dynCfg.Exporters).To(HaveKey("otlp"))
		Expect(dynCfg.Exporters).To(HaveKey("prometheus"))
		Expect(dynCfg.Exporters).NotTo(HaveKey("debug"))
		Expect(dynCfg.Service.Pipelines["metrics"].Exporters).To(ContainElements("otlp", "prometheus"))
	})

	It("generates no pipeline when no exporters configured", func() {
		spec := &dbpreview.MonitoringSpec{Enabled: true, Exporter: nil}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		dynCfg := parseCfg(data["dynamic.yaml"])
		Expect(dynCfg.Service).To(BeNil())
		Expect(dynCfg.Exporters).To(BeEmpty())
	})
})

var _ = Describe("HashConfigMapData", func() {
	It("returns a deterministic hash", func() {
		data := map[string]string{
			"static.yaml":  "receivers:\n  sqlquery: {}\n",
			"dynamic.yaml": "exporters:\n  prometheus: {}\n",
		}
		hash1 := HashConfigMapData(data)
		hash2 := HashConfigMapData(data)
		Expect(hash1).To(Equal(hash2))
		Expect(hash1).To(HaveLen(16))
	})

	It("produces different hashes for different data", func() {
		data1 := map[string]string{"static.yaml": "v1", "dynamic.yaml": "v1"}
		data2 := map[string]string{"static.yaml": "v2", "dynamic.yaml": "v1"}
		Expect(HashConfigMapData(data1)).NotTo(Equal(HashConfigMapData(data2)))
	})

	It("is key-order independent", func() {
		data := map[string]string{"b": "2", "a": "1"}
		hash1 := HashConfigMapData(data)
		hash2 := HashConfigMapData(data)
		Expect(hash1).To(Equal(hash2))
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
