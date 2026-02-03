// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

const (
	// DefaultBatchInterval is the default interval for batching telemetry events.
	DefaultBatchInterval = 30 * time.Second
	// DefaultMaxBatchSize is the maximum number of events to batch before sending.
	DefaultMaxBatchSize = 100
	// DefaultMaxRetries is the maximum number of retries for failed telemetry submissions.
	DefaultMaxRetries = 3
	// DefaultRetryBaseDelay is the base delay for exponential backoff retries.
	DefaultRetryBaseDelay = 1 * time.Second
	// DefaultBufferSize is the size of the local buffer for events when AppInsights is unreachable.
	DefaultBufferSize = 1000

	// EnvAppInsightsKey is the environment variable for the Application Insights instrumentation key.
	EnvAppInsightsKey = "APPINSIGHTS_INSTRUMENTATIONKEY"
	// EnvAppInsightsConnectionString is the environment variable for the Application Insights connection string.
	EnvAppInsightsConnectionString = "APPLICATIONINSIGHTS_CONNECTION_STRING"
	// EnvTelemetryEnabled is the environment variable to enable/disable telemetry.
	EnvTelemetryEnabled = "DOCUMENTDB_TELEMETRY_ENABLED"

	// AppInsightsTrackEndpoint is the Application Insights ingestion endpoint.
	AppInsightsTrackEndpoint = "https://dc.services.visualstudio.com/v2/track"
)

// TelemetryClient handles sending telemetry to Application Insights.
type TelemetryClient struct {
	instrumentationKey string
	ingestionEndpoint  string
	enabled            bool
	operatorContext    *OperatorContext
	logger             logr.Logger

	// Batching
	eventBuffer   []telemetryEnvelope
	bufferMutex   sync.Mutex
	batchInterval time.Duration
	maxBatchSize  int

	// Retry and buffering
	maxRetries     int
	retryBaseDelay time.Duration
	localBuffer    []telemetryEnvelope
	localMutex     sync.Mutex
	maxBufferSize  int

	// HTTP client
	httpClient *http.Client

	// Shutdown
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// telemetryEnvelope wraps events for Application Insights API.
type telemetryEnvelope struct {
	Name string                 `json:"name"`
	Time string                 `json:"time"`
	IKey string                 `json:"iKey"`
	Tags map[string]string      `json:"tags"`
	Data telemetryData          `json:"data"`
}

type telemetryData struct {
	BaseType string                 `json:"baseType"`
	BaseData map[string]interface{} `json:"baseData"`
}

// ClientOption configures the TelemetryClient.
type ClientOption func(*TelemetryClient)

// WithBatchInterval sets the batch interval for sending telemetry.
func WithBatchInterval(interval time.Duration) ClientOption {
	return func(c *TelemetryClient) {
		c.batchInterval = interval
	}
}

// WithMaxBatchSize sets the maximum batch size.
func WithMaxBatchSize(size int) ClientOption {
	return func(c *TelemetryClient) {
		c.maxBatchSize = size
	}
}

// WithLogger sets the logger for the telemetry client.
func WithLogger(logger logr.Logger) ClientOption {
	return func(c *TelemetryClient) {
		c.logger = logger
	}
}

// NewTelemetryClient creates a new TelemetryClient.
func NewTelemetryClient(ctx *OperatorContext, opts ...ClientOption) *TelemetryClient {
	client := &TelemetryClient{
		operatorContext:   ctx,
		enabled:           true,
		batchInterval:     DefaultBatchInterval,
		maxBatchSize:      DefaultMaxBatchSize,
		maxRetries:        DefaultMaxRetries,
		retryBaseDelay:    DefaultRetryBaseDelay,
		maxBufferSize:     DefaultBufferSize,
		eventBuffer:       make([]telemetryEnvelope, 0),
		localBuffer:       make([]telemetryEnvelope, 0),
		ingestionEndpoint: AppInsightsTrackEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		stopChan: make(chan struct{}),
	}

	// Apply options
	for _, opt := range opts {
		opt(client)
	}

	// Check if telemetry is enabled
	if enabled := os.Getenv(EnvTelemetryEnabled); enabled == "false" {
		client.enabled = false
		if client.logger.GetSink() != nil {
			client.logger.Info("Telemetry collection is disabled via environment variable")
		}
		return client
	}

	// Get instrumentation key from environment
	client.instrumentationKey = os.Getenv(EnvAppInsightsKey)
	if client.instrumentationKey == "" {
		// Try connection string
		connStr := os.Getenv(EnvAppInsightsConnectionString)
		client.instrumentationKey = parseInstrumentationKeyFromConnectionString(connStr)
	}

	if client.instrumentationKey == "" {
		client.enabled = false
		if client.logger.GetSink() != nil {
			client.logger.Info("No Application Insights instrumentation key found, telemetry disabled")
		}
		return client
	}

	return client
}

// Start begins the background batch processing goroutine.
func (c *TelemetryClient) Start() {
	if !c.enabled {
		return
	}

	c.wg.Add(1)
	go c.batchProcessor()
}

// Stop gracefully stops the telemetry client and flushes remaining events.
func (c *TelemetryClient) Stop() {
	if !c.enabled {
		return
	}

	close(c.stopChan)
	c.wg.Wait()

	// Flush any remaining events
	c.flush()
}

// IsEnabled returns whether telemetry collection is enabled.
func (c *TelemetryClient) IsEnabled() bool {
	return c.enabled
}

// TrackEvent sends a custom event to Application Insights.
func (c *TelemetryClient) TrackEvent(eventName string, properties map[string]interface{}) {
	if !c.enabled {
		return
	}

	envelope := c.createEnvelope("Microsoft.ApplicationInsights.Event", map[string]interface{}{
		"name":       eventName,
		"properties": c.addContextProperties(properties),
	})

	c.bufferMutex.Lock()
	c.eventBuffer = append(c.eventBuffer, envelope)
	shouldFlush := len(c.eventBuffer) >= c.maxBatchSize
	c.bufferMutex.Unlock()

	if shouldFlush {
		go c.flush()
	}
}

// TrackMetric sends a metric to Application Insights.
func (c *TelemetryClient) TrackMetric(metricName string, value float64, properties map[string]interface{}) {
	if !c.enabled {
		return
	}

	envelope := c.createEnvelope("Microsoft.ApplicationInsights.Metric", map[string]interface{}{
		"metrics": []map[string]interface{}{
			{
				"name":  metricName,
				"value": value,
			},
		},
		"properties": c.addContextProperties(properties),
	})

	c.bufferMutex.Lock()
	c.eventBuffer = append(c.eventBuffer, envelope)
	c.bufferMutex.Unlock()
}

// TrackException sends an exception/error to Application Insights.
func (c *TelemetryClient) TrackException(err error, properties map[string]interface{}) {
	if !c.enabled {
		return
	}

	// Sanitize error message to remove potential PII
	sanitizedMessage := sanitizeErrorMessage(err.Error())

	envelope := c.createEnvelope("Microsoft.ApplicationInsights.Exception", map[string]interface{}{
		"exceptions": []map[string]interface{}{
			{
				"message": sanitizedMessage,
			},
		},
		"properties": c.addContextProperties(properties),
	})

	c.bufferMutex.Lock()
	c.eventBuffer = append(c.eventBuffer, envelope)
	c.bufferMutex.Unlock()
}

// createEnvelope creates a telemetry envelope for Application Insights.
func (c *TelemetryClient) createEnvelope(baseType string, baseData map[string]interface{}) telemetryEnvelope {
	return telemetryEnvelope{
		Name: baseType,
		Time: time.Now().UTC().Format(time.RFC3339Nano),
		IKey: c.instrumentationKey,
		Tags: map[string]string{
			"ai.cloud.role":         "documentdb-operator",
			"ai.cloud.roleInstance": c.operatorContext.OperatorNamespaceHash,
			"ai.application.ver":    c.operatorContext.OperatorVersion,
		},
		Data: telemetryData{
			BaseType: baseType,
			BaseData: baseData,
		},
	}
}

// addContextProperties adds operator context to event properties.
func (c *TelemetryClient) addContextProperties(properties map[string]interface{}) map[string]interface{} {
	if properties == nil {
		properties = make(map[string]interface{})
	}

	// Add operator context (these are added to all events as per spec)
	properties["kubernetes_distribution"] = string(c.operatorContext.KubernetesDistribution)
	properties["kubernetes_version"] = c.operatorContext.KubernetesVersion
	properties["operator_version"] = c.operatorContext.OperatorVersion

	if c.operatorContext.Region != "" {
		properties["region"] = c.operatorContext.Region
	}

	return properties
}

// batchProcessor runs in the background to periodically send batched events.
func (c *TelemetryClient) batchProcessor() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.flush()
		case <-c.stopChan:
			return
		}
	}
}

// flush sends all buffered events to Application Insights.
func (c *TelemetryClient) flush() {
	c.bufferMutex.Lock()
	if len(c.eventBuffer) == 0 {
		c.bufferMutex.Unlock()
		// Also try to send locally buffered events
		c.flushLocalBuffer()
		return
	}

	events := c.eventBuffer
	c.eventBuffer = make([]telemetryEnvelope, 0)
	c.bufferMutex.Unlock()

	// Send events with retry
	if err := c.sendWithRetry(events); err != nil {
		// Store in local buffer if send fails
		c.localMutex.Lock()
		c.localBuffer = append(c.localBuffer, events...)
		// Trim buffer if it exceeds max size
		if len(c.localBuffer) > c.maxBufferSize {
			c.localBuffer = c.localBuffer[len(c.localBuffer)-c.maxBufferSize:]
		}
		c.localMutex.Unlock()

		if c.logger.GetSink() != nil {
			c.logger.Error(err, "Failed to send telemetry, buffered locally", "eventCount", len(events))
		}
	}
}

// flushLocalBuffer attempts to send locally buffered events.
func (c *TelemetryClient) flushLocalBuffer() {
	c.localMutex.Lock()
	if len(c.localBuffer) == 0 {
		c.localMutex.Unlock()
		return
	}

	events := c.localBuffer
	c.localBuffer = make([]telemetryEnvelope, 0)
	c.localMutex.Unlock()

	if err := c.sendWithRetry(events); err != nil {
		// Put back in buffer
		c.localMutex.Lock()
		c.localBuffer = append(events, c.localBuffer...)
		if len(c.localBuffer) > c.maxBufferSize {
			c.localBuffer = c.localBuffer[:c.maxBufferSize]
		}
		c.localMutex.Unlock()
	}
}

// sendWithRetry sends events to Application Insights with exponential backoff retry.
func (c *TelemetryClient) sendWithRetry(events []telemetryEnvelope) error {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.retryBaseDelay * time.Duration(1<<uint(attempt-1))
			time.Sleep(delay)
		}

		if err := c.send(events); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	return lastErr
}

// send sends events to Application Insights.
func (c *TelemetryClient) send(events []telemetryEnvelope) error {
	if len(events) == 0 {
		return nil
	}

	// Serialize events as newline-delimited JSON
	var buf bytes.Buffer
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	req, err := http.NewRequest(http.MethodPost, c.ingestionEndpoint, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-json-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send telemetry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telemetry submission failed with status: %d", resp.StatusCode)
	}

	return nil
}

// parseInstrumentationKeyFromConnectionString extracts the instrumentation key from a connection string.
func parseInstrumentationKeyFromConnectionString(connStr string) string {
	if connStr == "" {
		return ""
	}

	// Connection string format: InstrumentationKey=xxx;IngestionEndpoint=xxx;...
	for _, part := range bytes.Split([]byte(connStr), []byte(";")) {
		if bytes.HasPrefix(part, []byte("InstrumentationKey=")) {
			return string(bytes.TrimPrefix(part, []byte("InstrumentationKey=")))
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
