// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package otel

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

var _ = Describe("MonitorRoleName", func() {
	It("is the dedicated least-privilege monitoring role", func() {
		Expect(MonitorRoleName).To(Equal("otel_monitor"))
	})
})

// parseCfg is a helper to unmarshal YAML into a collectorConfig struct.
func parseCfg(yamlStr string) collectorConfig {
	var cfg collectorConfig
	ExpectWithOffset(1, yaml.Unmarshal([]byte(yamlStr), &cfg)).To(Succeed())
	return cfg
}

// defaultStaticYAML is base_config.yaml rendered with the DocumentDB names.
func defaultStaticYAML() []byte {
	out, err := renderStaticConfig(staticConfigData{HealthMetricName: defaultHealthMetricName})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return []byte(out)
}

// documentDBOptions enable the DocumentDB SQL metrics, as the DocumentDB
// product profile does.
var documentDBOptions = Options{DocumentDBMetrics: true}

// documentDBStaticYAML is base_config.yaml rendered for DocumentDB: the
// DocumentDB names plus the DocumentDB SQL metrics.
func documentDBStaticYAML() []byte {
	out, err := renderStaticConfig(staticConfigData{HealthMetricName: defaultHealthMetricName, DocumentDBMetrics: true})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return []byte(out)
}

var _ = Describe("base_config.yaml embed", func() {
	It("can be parsed as valid YAML with expected static components", func() {
		var cfg collectorConfig
		Expect(yaml.Unmarshal(defaultStaticYAML(), &cfg)).To(Succeed())
		Expect(cfg.Receivers).To(HaveKey("sqlquery"))
		Expect(cfg.Receivers).To(HaveKey("otlp"))
		Expect(cfg.Processors).To(HaveKey("memory_limiter"))
		Expect(cfg.Processors).To(HaveKey("batch"))
		// Static config should NOT have exporters or service (those are dynamic)
		Expect(cfg.Exporters).To(BeEmpty())
	})

	It("does not configure a password while PostgreSQL uses trust authentication", func() {
		Expect(string(defaultStaticYAML())).To(ContainSubstring("user=${env:PGUSER}"))
		Expect(string(defaultStaticYAML())).NotTo(ContainSubstring("PGPASSWORD"))
	})

	It("declares a cgroup-aware memory_limiter processor", func() {
		var cfg collectorConfig
		Expect(yaml.Unmarshal(defaultStaticYAML(), &cfg)).To(Succeed())

		memLimiter, ok := cfg.Processors["memory_limiter"].(map[string]any)
		Expect(ok).To(BeTrue(), "memory_limiter processor must be a map")
		Expect(memLimiter["check_interval"]).To(Equal("1s"))
		Expect(memLimiter["limit_percentage"]).To(Equal(80))
		Expect(memLimiter["spike_limit_percentage"]).To(Equal(25))
	})

	It("declares an OTLP gRPC receiver on port 4317", func() {
		var cfg collectorConfig
		Expect(yaml.Unmarshal(defaultStaticYAML(), &cfg)).To(Succeed())

		otlp, ok := cfg.Receivers["otlp"].(map[string]any)
		Expect(ok).To(BeTrue(), "otlp receiver must be a map")
		protocols, ok := otlp["protocols"].(map[string]any)
		Expect(ok).To(BeTrue(), "otlp.protocols must be a map")
		grpc, ok := protocols["grpc"].(map[string]any)
		Expect(ok).To(BeTrue(), "otlp.protocols.grpc must be a map")
		Expect(grpc["endpoint"]).To(Equal("127.0.0.1:4317"))
	})

	DescribeTable("wires every static receiver into the metrics pipeline",
		func(opts Options) {
			spec := &dbpreview.MonitoringSpec{
				Enabled: true,
				Exporter: &dbpreview.ExporterSpec{
					Prometheus: &dbpreview.PrometheusExporterSpec{},
				},
			}
			data, err := GenerateConfigMapDataWithOptions(opts, "cluster", "ns", spec)
			Expect(err).NotTo(HaveOccurred())
			staticCfg := parseCfg(data["static.yaml"])
			dynCfg := parseCfg(data["dynamic.yaml"])

			receivers := make([]string, 0, len(staticCfg.Receivers))
			for name := range staticCfg.Receivers {
				receivers = append(receivers, name)
			}
			Expect(dynCfg.Service.Pipelines["metrics"].Receivers).To(ConsistOf(receivers))
		},
		Entry("with the DocumentDB SQL metrics", documentDBOptions),
		Entry("without the DocumentDB SQL metrics", Options{}),
	)

	It("renders the DocumentDB SQL metrics only when enabled", func() {
		spec := &dbpreview.MonitoringSpec{Enabled: true}
		without, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())
		staticCfg := parseCfg(without["static.yaml"])
		Expect(staticCfg.Receivers).To(HaveKey("hostmetrics"))
		Expect(staticCfg.Receivers).NotTo(HaveKey("sqlquery/catalog"))
		sqlquery, ok := staticCfg.Receivers["sqlquery"].(map[string]any)
		Expect(ok).To(BeTrue(), "sqlquery receiver must be a map")
		Expect(sqlquery["queries"]).To(HaveLen(1), "only the health query")

		with, err := GenerateConfigMapDataWithOptions(documentDBOptions, "cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())
		Expect(parseCfg(with["static.yaml"]).Receivers).To(HaveKey("sqlquery/catalog"))
		Expect(with["static.yaml"]).To(ContainSubstring("metric_name: documentdb.postgres.uptime\n"))
	})

	It("declares the per-instance PostgreSQL metrics with parseable value types", func() {
		queries := receiverQueries("sqlquery")
		metrics := metricsByName(queries)
		Expect(metrics).To(HaveKey("documentdb.postgres.up"))

		// Float columns must be parsed as doubles; the default int type would
		// fail every scrape.
		for _, name := range []string{"documentdb.postgres.uptime", "documentdb.postgres.replication.lag"} {
			Expect(metrics).To(HaveKey(name))
			Expect(metrics[name]["value_type"]).To(Equal("double"), name)
			Expect(metrics[name]["data_type"]).To(Equal("gauge"), name)
			Expect(metrics[name]["unit"]).To(Equal("s"), name)
		}
		Expect(metrics).To(HaveKey("documentdb.postgres.replication.receiver.up"))

		// Block counters are cumulative monotonic sums so backends can compute rates.
		for _, name := range []string{"documentdb.postgres.blocks.hit", "documentdb.postgres.blocks.read"} {
			Expect(metrics).To(HaveKey(name))
			Expect(metrics[name]["data_type"]).To(Equal("sum"), name)
			Expect(metrics[name]["monotonic"]).To(BeTrue(), name)
			Expect(metrics[name]["aggregation"]).To(Equal("cumulative"), name)
		}

		// Replication metrics come only from standbys.
		Expect(querySQLFor(queries, "documentdb.postgres.replication.lag")).To(ContainSubstring("WHERE pg_is_in_recovery()"))
	})

	It("declares primary-only catalog metrics on a 5 minute interval", func() {
		var cfg collectorConfig
		Expect(yaml.Unmarshal(documentDBStaticYAML(), &cfg)).To(Succeed())
		catalog, ok := cfg.Receivers["sqlquery/catalog"].(map[string]any)
		Expect(ok).To(BeTrue(), "sqlquery/catalog receiver must be a map")
		Expect(catalog["collection_interval"]).To(Equal("5m"))

		queries := receiverQueries("sqlquery/catalog")
		for _, q := range queries {
			Expect(q["sql"]).To(ContainSubstring("NOT pg_is_in_recovery()"), "catalog queries must return no rows on standbys")
			Expect(q["sql"]).To(ContainSubstring("NOT IN ('admin', 'config', 'local')"))
			Expect(q["sql"]).To(ContainSubstring("NOT LIKE 'system.%'"))
		}

		metrics := metricsByName(queries)
		for _, name := range []string{
			"documentdb.database.storage.size",
			"documentdb.database.collection.count",
			"documentdb.database.view.count",
			"documentdb.database.index.count",
		} {
			Expect(metrics).To(HaveKey(name))
			Expect(metrics[name]["attribute_columns"]).To(ConsistOf("db.namespace"), name)
		}
		Expect(metrics).To(HaveKey("documentdb.database.count"))
		Expect(metrics["documentdb.database.count"]).NotTo(HaveKey("attribute_columns"))
	})

	It("scrapes only the data volume mount and non-loopback interfaces", func() {
		var cfg collectorConfig
		Expect(yaml.Unmarshal(defaultStaticYAML(), &cfg)).To(Succeed())
		hostmetrics, ok := cfg.Receivers["hostmetrics"].(map[string]any)
		Expect(ok).To(BeTrue(), "hostmetrics receiver must be a map")
		scrapers, ok := hostmetrics["scrapers"].(map[string]any)
		Expect(ok).To(BeTrue(), "hostmetrics.scrapers must be a map")
		Expect(scrapers).To(HaveLen(2))

		filesystem, ok := scrapers["filesystem"].(map[string]any)
		Expect(ok).To(BeTrue(), "filesystem scraper must be a map")
		// The subPath mount is a bind mount; without this the scraper drops it.
		Expect(filesystem["include_virtual_filesystems"]).To(BeTrue())
		Expect(filesystem["include_mount_points"]).To(Equal(map[string]any{
			"mount_points": []any{"/var/lib/otel/pgdata"},
			"match_type":   "strict",
		}))

		network, ok := scrapers["network"].(map[string]any)
		Expect(ok).To(BeTrue(), "network scraper must be a map")
		Expect(network["exclude"]).To(Equal(map[string]any{
			"interfaces": []any{"lo"},
			"match_type": "strict",
		}))
		Expect(network["metrics"]).To(Equal(map[string]any{
			"system.network.connections": map[string]any{"enabled": false},
		}))
	})
})

// receiverQueries returns the queries declared by a sqlquery receiver in the
// DocumentDB static config.
func receiverQueries(receiver string) []map[string]any {
	var cfg collectorConfig
	ExpectWithOffset(1, yaml.Unmarshal(documentDBStaticYAML(), &cfg)).To(Succeed())
	r, ok := cfg.Receivers[receiver].(map[string]any)
	ExpectWithOffset(1, ok).To(BeTrue(), "%s receiver must be a map", receiver)
	raw, ok := r["queries"].([]any)
	ExpectWithOffset(1, ok).To(BeTrue(), "%s.queries must be a list", receiver)
	queries := make([]map[string]any, 0, len(raw))
	for _, q := range raw {
		query, ok := q.(map[string]any)
		ExpectWithOffset(1, ok).To(BeTrue(), "%s query must be a map", receiver)
		queries = append(queries, query)
	}
	return queries
}

// metricsByName indexes the metric definitions of the given queries by metric_name.
func metricsByName(queries []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, q := range queries {
		metrics, ok := q["metrics"].([]any)
		ExpectWithOffset(1, ok).To(BeTrue(), "query metrics must be a list")
		for _, m := range metrics {
			metric, ok := m.(map[string]any)
			ExpectWithOffset(1, ok).To(BeTrue(), "metric must be a map")
			name, _ := metric["metric_name"].(string)
			ExpectWithOffset(1, out).NotTo(HaveKey(name), "duplicate metric %s", name)
			out[name] = metric
		}
	}
	return out
}

// querySQLFor returns the SQL of the query that emits the named metric.
func querySQLFor(queries []map[string]any, metricName string) string {
	for _, q := range queries {
		metrics, _ := q["metrics"].([]any)
		for _, m := range metrics {
			if metric, ok := m.(map[string]any); ok && metric["metric_name"] == metricName {
				sql, _ := q["sql"].(string)
				return sql
			}
		}
	}
	return ""
}

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
		Expect(staticCfg.Processors).To(HaveKey("memory_limiter"))
		Expect(staticCfg.Processors).To(HaveKey("batch"))
	})

	It("generates dynamic.yaml with resource processor and exporters", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapDataWithOptions(documentDBOptions, "test-cluster", "test-ns", spec)
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
		Expect(dynCfg.Service.Pipelines["metrics"].Receivers).To(ConsistOf("sqlquery", "sqlquery/catalog", "hostmetrics", "otlp"))
		Expect(dynCfg.Service.Pipelines["metrics"].Processors).To(Equal([]string{"memory_limiter", "resource", "batch"}))
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

	// Regression guards — see comments in config.go for the why behind each.
	It("uses 'insert' (not 'upsert') on the resource processor so per-datapoint k8s attrs survive", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		// Must not contain 'upsert' — that would clobber per-datapoint resource
		// attrs from any receiver that emits its own k8s.* identity.
		Expect(data["dynamic.yaml"]).NotTo(ContainSubstring("upsert"))
		// Should use 'insert' for all three resource attrs.
		Expect(strings.Count(data["dynamic.yaml"], "action: insert")).To(Equal(3))
	})

	It("enables resource_to_telemetry_conversion on the prometheus exporter so resource attrs become labels", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{Port: 9090},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())
		// Without this option the prometheus exporter writes resource attrs only
		// to target_info, hiding documentdb.cluster / k8s.* labels.
		Expect(data["dynamic.yaml"]).To(ContainSubstring("resource_to_telemetry_conversion"))
		Expect(data["dynamic.yaml"]).To(MatchRegexp(`resource_to_telemetry_conversion:\s*\n\s+enabled:\s*true`))
	})

	It("keeps prometheus series longer than the slowest receiver collection interval", func() {
		spec := &dbpreview.MonitoringSpec{
			Enabled: true,
			Exporter: &dbpreview.ExporterSpec{
				Prometheus: &dbpreview.PrometheusExporterSpec{},
			},
		}
		data, err := GenerateConfigMapData("cluster", "ns", spec)
		Expect(err).NotTo(HaveOccurred())

		promCfg, ok := parseCfg(data["dynamic.yaml"]).Exporters["prometheus"].(map[string]any)
		Expect(ok).To(BeTrue())
		expiration, err := time.ParseDuration(fmt.Sprint(promCfg["metric_expiration"]))
		Expect(err).NotTo(HaveOccurred())

		var slowest time.Duration
		for name, receiver := range parseCfg(string(documentDBStaticYAML())).Receivers {
			cfg, _ := receiver.(map[string]any)
			raw, ok := cfg["collection_interval"]
			if !ok {
				continue
			}
			interval, err := time.ParseDuration(fmt.Sprint(raw))
			Expect(err).NotTo(HaveOccurred(), "receiver %s", name)
			slowest = max(slowest, interval)
		}
		Expect(slowest).To(BeNumerically(">", 0))
		Expect(expiration).To(BeNumerically(">", slowest))
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
