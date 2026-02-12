// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/microsoft/ApplicationInsights-Go/appinsights"
)

const (
	// EnvAppInsightsKey is the environment variable for the Application Insights instrumentation key.
	EnvAppInsightsKey = "APPINSIGHTS_INSTRUMENTATIONKEY"
	// EnvAppInsightsConnectionString is the environment variable for the Application Insights connection string.
	EnvAppInsightsConnectionString = "APPLICATIONINSIGHTS_CONNECTION_STRING"
	// EnvTelemetryEnabled is the environment variable to enable/disable telemetry.
	EnvTelemetryEnabled = "DOCUMENTDB_TELEMETRY_ENABLED"
)

// TelemetryClient handles sending telemetry to Application Insights using the official SDK.
type TelemetryClient struct {
	client          appinsights.TelemetryClient
	enabled         bool
	operatorContext *OperatorContext
	logger          logr.Logger
}

// ClientOption configures the TelemetryClient.
type ClientOption func(*TelemetryClient)

// WithLogger sets the logger for the telemetry client.
func WithLogger(logger logr.Logger) ClientOption {
	return func(c *TelemetryClient) {
		c.logger = logger
	}
}

// NewTelemetryClient creates a new TelemetryClient using the official Application Insights SDK.
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

	// Get instrumentation key from environment
	instrumentationKey := os.Getenv(EnvAppInsightsKey)
	if instrumentationKey == "" {
		// Try connection string
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

	// Create telemetry configuration
	telemetryConfig := appinsights.NewTelemetryConfiguration(instrumentationKey)

	// Configure batching - send every 30 seconds or when batch reaches 100 items
	telemetryConfig.MaxBatchSize = 100
	telemetryConfig.MaxBatchInterval = 30 * time.Second

	// Check for custom endpoint from connection string
	connStr := os.Getenv(EnvAppInsightsConnectionString)
	if endpoint := parseIngestionEndpointFromConnectionString(connStr); endpoint != "" {
		telemetryConfig.EndpointUrl = strings.TrimSuffix(endpoint, "/") + "/v2/track"
	}

	// Create the client
	tc.client = appinsights.NewTelemetryClientFromConfig(telemetryConfig)

	// Set common context tags
	tc.client.Context().Tags.Cloud().SetRole("documentdb-operator")
	tc.client.Context().Tags.Cloud().SetRoleInstance(ctx.OperatorNamespaceHash)
	tc.client.Context().Tags.Application().SetVer(ctx.OperatorVersion)

	// Set common properties that will be added to all telemetry
	tc.client.Context().CommonProperties["kubernetes_distribution"] = string(ctx.KubernetesDistribution)
	tc.client.Context().CommonProperties["kubernetes_version"] = ctx.KubernetesVersion
	tc.client.Context().CommonProperties["operator_version"] = ctx.OperatorVersion
	if ctx.Region != "" {
		tc.client.Context().CommonProperties["region"] = ctx.Region
	}

	// Enable diagnostics logging if logger is available
	if tc.logger.GetSink() != nil {
		appinsights.NewDiagnosticsMessageListener(func(msg string) error {
			tc.logger.V(1).Info("Application Insights diagnostic", "message", msg)
			return nil
		})
	}

	return tc
}

// Start begins the telemetry client (no-op for SDK-based client as it handles this internally).
func (c *TelemetryClient) Start() {
	// The official SDK handles background processing internally
}

// Stop gracefully stops the telemetry client and flushes remaining events.
func (c *TelemetryClient) Stop() {
	if !c.enabled || c.client == nil {
		return
	}

	// Close the channel with a timeout for retries
	select {
	case <-c.client.Channel().Close(10 * time.Second):
		if c.logger.GetSink() != nil {
			c.logger.Info("Telemetry channel closed successfully")
		}
	case <-time.After(30 * time.Second):
		if c.logger.GetSink() != nil {
			c.logger.Info("Telemetry channel close timed out")
		}
	}
}

// IsEnabled returns whether telemetry collection is enabled.
func (c *TelemetryClient) IsEnabled() bool {
	return c.enabled
}

// TrackEvent sends a custom event to Application Insights.
func (c *TelemetryClient) TrackEvent(eventName string, properties map[string]interface{}) {
	if !c.enabled || c.client == nil {
		return
	}

	event := appinsights.NewEventTelemetry(eventName)

	// Add properties
	for k, v := range properties {
		event.Properties[k] = fmt.Sprintf("%v", v)
	}

	c.client.Track(event)
}

// TrackMetric sends a metric to Application Insights.
func (c *TelemetryClient) TrackMetric(metricName string, value float64, properties map[string]interface{}) {
	if !c.enabled || c.client == nil {
		return
	}

	metric := appinsights.NewMetricTelemetry(metricName, value)

	// Add properties
	for k, v := range properties {
		metric.Properties[k] = fmt.Sprintf("%v", v)
	}

	c.client.Track(metric)
}

// TrackException sends an exception/error to Application Insights.
func (c *TelemetryClient) TrackException(err error, properties map[string]interface{}) {
	if !c.enabled || c.client == nil {
		return
	}

	// Sanitize error message to remove potential PII
	sanitizedMessage := sanitizeErrorMessage(err.Error())

	exception := appinsights.NewExceptionTelemetry(sanitizedMessage)

	// Add properties
	for k, v := range properties {
		exception.Properties[k] = fmt.Sprintf("%v", v)
	}

	c.client.Track(exception)
}

// parseInstrumentationKeyFromConnectionString extracts the instrumentation key from a connection string.
func parseInstrumentationKeyFromConnectionString(connStr string) string {
	if connStr == "" {
		return ""
	}

	// Connection string format: InstrumentationKey=xxx;IngestionEndpoint=xxx;...
	for _, part := range strings.Split(connStr, ";") {
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

	// Connection string format: InstrumentationKey=xxx;IngestionEndpoint=xxx;...
	for _, part := range strings.Split(connStr, ";") {
		if strings.HasPrefix(part, "IngestionEndpoint=") {
			return strings.TrimPrefix(part, "IngestionEndpoint=")
		}
	}

	return ""
}

// sanitizeErrorMessage removes potential PII from error messages.
func sanitizeErrorMessage(msg string) string {
	// Basic sanitization - in production, this should be more comprehensive
	// Remove potential file paths, IP addresses, etc.
	// For now, truncate to reasonable length
	const maxLength = 500
	if len(msg) > maxLength {
		msg = msg[:maxLength] + "..."
	}
	return msg
}
