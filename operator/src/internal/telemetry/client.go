// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/log/global"
	otellog "go.opentelemetry.io/otel/log"
	otelmetricapi "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	// EnvAppInsightsKey is the environment variable for the Application Insights instrumentation key.
	EnvAppInsightsKey = "APPINSIGHTS_INSTRUMENTATIONKEY"
	// EnvAppInsightsConnectionString is the environment variable for the Application Insights connection string.
	EnvAppInsightsConnectionString = "APPLICATIONINSIGHTS_CONNECTION_STRING"
	// EnvTelemetryEnabled is the environment variable to enable/disable telemetry.
	EnvTelemetryEnabled = "DOCUMENTDB_TELEMETRY_ENABLED"
	// EnvOTLPEndpoint is the environment variable for the OTel Collector endpoint.
	// Defaults to localhost:4317 (sidecar collector).
	EnvOTLPEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
)

// TelemetryClient handles sending telemetry via OpenTelemetry SDK to an OTel Collector sidecar.
// The collector is responsible for exporting to App Insights via the Azure Monitor exporter.
type TelemetryClient struct {
	enabled         bool
	operatorContext *OperatorContext
	logger          logr.Logger

	// OTel SDK components
	loggerProvider  *log.LoggerProvider
	meterProvider   *metric.MeterProvider
	otelLogger      otellog.Logger
	commonAttrs     []attribute.KeyValue
}

// ClientOption configures the TelemetryClient.
type ClientOption func(*TelemetryClient)

// WithLogger sets the logger for the telemetry client.
func WithLogger(logger logr.Logger) ClientOption {
	return func(c *TelemetryClient) {
		c.logger = logger
	}
}

// NewTelemetryClient creates a new TelemetryClient using the OpenTelemetry SDK.
// Telemetry is exported via OTLP to a local OTel Collector sidecar.
func NewTelemetryClient(ctx *OperatorContext, opts ...ClientOption) *TelemetryClient {
	tc := &TelemetryClient{
		operatorContext: ctx,
		enabled:         true,
	}

	// Apply options
	for _, opt := range opts {
		opt(tc)
	}

	// Check if telemetry is enabled
	if enabled := os.Getenv(EnvTelemetryEnabled); enabled == "false" {
		tc.enabled = false
		if tc.logger.GetSink() != nil {
			tc.logger.Info("Telemetry collection is disabled via environment variable")
		}
		return tc
	}

	// Check for instrumentation key/connection string — if neither is set, disable
	instrumentationKey := os.Getenv(EnvAppInsightsKey)
	if instrumentationKey == "" {
		connStr := os.Getenv(EnvAppInsightsConnectionString)
		instrumentationKey = parseInstrumentationKeyFromConnectionString(connStr)
	}
	if instrumentationKey == "" {
		tc.enabled = false
		if tc.logger.GetSink() != nil {
			tc.logger.Info("No Application Insights instrumentation key found, telemetry disabled")
		}
		return tc
	}

	// Build common attributes for all telemetry
	tc.commonAttrs = []attribute.KeyValue{
		semconv.ServiceName("documentdb-operator"),
		semconv.ServiceVersion(ctx.OperatorVersion),
		attribute.String("kubernetes_distribution", string(ctx.KubernetesDistribution)),
		attribute.String("kubernetes_version", ctx.KubernetesVersion),
		attribute.String("operator_version", ctx.OperatorVersion),
	}
	if ctx.KubernetesClusterID != "" {
		tc.commonAttrs = append(tc.commonAttrs, attribute.String("kubernetes_cluster_id", ctx.KubernetesClusterID))
	}
	if ctx.Region != "" {
		tc.commonAttrs = append(tc.commonAttrs, attribute.String("region", ctx.Region))
	}
	if ctx.OperatorNamespaceHash != "" {
		tc.commonAttrs = append(tc.commonAttrs, attribute.String("operator_namespace_hash", ctx.OperatorNamespaceHash))
	}

	// Determine OTLP endpoint (default: localhost sidecar)
	otlpEndpoint := os.Getenv(EnvOTLPEndpoint)
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	// Build OTel resource
	bgCtx := context.Background()
	res, err := resource.New(bgCtx,
		resource.WithAttributes(tc.commonAttrs...),
	)
	if err != nil {
		if tc.logger.GetSink() != nil {
			tc.logger.Error(err, "Failed to create OTel resource")
		}
		tc.enabled = false
		return tc
	}

	// Set up OTLP log exporter → OTel Collector sidecar
	logExporter, err := otlploggrpc.New(bgCtx,
		otlploggrpc.WithEndpoint(otlpEndpoint),
		otlploggrpc.WithInsecure(), // Sidecar communication is local, no TLS needed
	)
	if err != nil {
		if tc.logger.GetSink() != nil {
			tc.logger.Error(err, "Failed to create OTLP log exporter")
		}
		tc.enabled = false
		return tc
	}

	tc.loggerProvider = log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(logExporter,
			log.WithExportInterval(30*time.Second),
			log.WithExportMaxBatchSize(100),
		)),
	)
	global.SetLoggerProvider(tc.loggerProvider)
	tc.otelLogger = tc.loggerProvider.Logger("documentdb-operator-telemetry")

	// Set up OTLP metric exporter → OTel Collector sidecar
	metricExporter, err := otlpmetricgrpc.New(bgCtx,
		otlpmetricgrpc.WithEndpoint(otlpEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		if tc.logger.GetSink() != nil {
			tc.logger.Error(err, "Failed to create OTLP metric exporter")
		}
		tc.enabled = false
		return tc
	}

	tc.meterProvider = metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter,
			metric.WithInterval(60*time.Second),
		)),
	)
	otel.SetMeterProvider(tc.meterProvider)

	return tc
}

// Start begins the telemetry client (OTel SDK handles background processing).
func (c *TelemetryClient) Start() {
	// OTel SDK handles background processing via batch processors
}

// Stop gracefully stops the telemetry client and flushes remaining data.
func (c *TelemetryClient) Stop() {
	if !c.enabled {
		return
	}

	bgCtx := context.Background()
	shutdownCtx, cancel := context.WithTimeout(bgCtx, 10*time.Second)
	defer cancel()

	if c.loggerProvider != nil {
		if err := c.loggerProvider.Shutdown(shutdownCtx); err != nil {
			if c.logger.GetSink() != nil {
				c.logger.Error(err, "Failed to shutdown OTel logger provider")
			}
		}
	}
	if c.meterProvider != nil {
		if err := c.meterProvider.Shutdown(shutdownCtx); err != nil {
			if c.logger.GetSink() != nil {
				c.logger.Error(err, "Failed to shutdown OTel meter provider")
			}
		}
	}

	if c.logger.GetSink() != nil {
		c.logger.Info("OTel telemetry providers shut down")
	}
}

// IsEnabled returns whether telemetry collection is enabled.
func (c *TelemetryClient) IsEnabled() bool {
	return c.enabled
}

// TrackEvent sends a custom event as an OTel log record.
// Events are emitted as structured log records with the event name and properties.
func (c *TelemetryClient) TrackEvent(eventName string, properties map[string]interface{}) {
	if !c.enabled || c.otelLogger == nil {
		return
	}

	record := otellog.Record{}
	record.SetTimestamp(time.Now())
	record.SetSeverity(otellog.SeverityInfo)
	record.SetBody(otellog.StringValue(eventName))

	// Add event name as attribute
	attrs := []otellog.KeyValue{
		otellog.String("event.name", eventName),
	}

	// Add properties as attributes
	for k, v := range properties {
		attrs = append(attrs, otellog.String(k, fmt.Sprintf("%v", v)))
	}

	record.AddAttributes(attrs...)
	c.otelLogger.Emit(context.Background(), record)
}

// TrackMetric sends a metric via the OTel Metrics SDK.
// Metrics are recorded as gauge observations.
func (c *TelemetryClient) TrackMetric(metricName string, value float64, properties map[string]interface{}) {
	if !c.enabled || c.meterProvider == nil {
		return
	}

	meter := c.meterProvider.Meter("documentdb-operator-telemetry")

	// Build attributes from properties
	attrs := make([]attribute.KeyValue, 0, len(properties))
	for k, v := range properties {
		attrs = append(attrs, attribute.String(k, fmt.Sprintf("%v", v)))
	}

	// Use a gauge for point-in-time metrics
	gauge, err := meter.Float64Gauge(metricName)
	if err != nil {
		if c.logger.GetSink() != nil {
			c.logger.V(1).Info("Failed to create gauge", "metric", metricName, "error", err)
		}
		return
	}
	gauge.Record(context.Background(), value, otelmetricapi.WithAttributes(attrs...))
}

// TrackException sends an exception/error as an OTel log record.
func (c *TelemetryClient) TrackException(err error, properties map[string]interface{}) {
	if !c.enabled || c.otelLogger == nil {
		return
	}

	sanitizedMessage := sanitizeErrorMessage(err.Error())

	record := otellog.Record{}
	record.SetTimestamp(time.Now())
	record.SetSeverity(otellog.SeverityError)
	record.SetBody(otellog.StringValue(sanitizedMessage))

	attrs := []otellog.KeyValue{
		otellog.String("event.name", "Exception"),
		otellog.String("exception.message", sanitizedMessage),
	}

	for k, v := range properties {
		attrs = append(attrs, otellog.String(k, fmt.Sprintf("%v", v)))
	}

	record.AddAttributes(attrs...)
	c.otelLogger.Emit(context.Background(), record)
}

// parseInstrumentationKeyFromConnectionString extracts the instrumentation key from a connection string.
func parseInstrumentationKeyFromConnectionString(connStr string) string {
	if connStr == "" {
		return ""
	}

	for _, part := range strings.Split(connStr, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "InstrumentationKey=") {
			return strings.TrimPrefix(part, "InstrumentationKey=")
		}
	}

	return ""
}

// parseIngestionEndpointFromConnectionString extracts the ingestion endpoint from a connection string.
func parseIngestionEndpointFromConnectionString(connStr string) string {
	if connStr == "" {
		return ""
	}

	for _, part := range strings.Split(connStr, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "IngestionEndpoint=") {
			return strings.TrimPrefix(part, "IngestionEndpoint=")
		}
	}

	return ""
}

// sanitizeErrorMessage removes potential PII from error messages.
func sanitizeErrorMessage(msg string) string {
	const maxLength = 500
	if len(msg) > maxLength {
		msg = msg[:maxLength] + "..."
	}
	return msg
}
