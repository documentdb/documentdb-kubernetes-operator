// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"sync"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestBufferMetric(t *testing.T) {
	client := newDisabledClient()
	mgr := &Manager{
		Client:    client,
		Events:    NewEventTracker(client),
		Metrics:   NewMetricsTracker(client),
		stopCh:    make(chan struct{}),
		bufferCap: 500,
	}

	mgr.bufferMetric("test.metric", 1.0, map[string]interface{}{"key": "value"})

	mgr.bufferMu.Lock()
	if len(mgr.metricsBuffer) != 1 {
		t.Errorf("expected 1 buffered metric, got %d", len(mgr.metricsBuffer))
	}
	if mgr.metricsBuffer[0].name != "test.metric" {
		t.Errorf("expected metric name 'test.metric', got '%s'", mgr.metricsBuffer[0].name)
	}
	mgr.bufferMu.Unlock()
}

func TestBufferMetric_AutoFlush(t *testing.T) {
	client := newDisabledClient()
	mgr := &Manager{
		Client:    client,
		Events:    NewEventTracker(client),
		Metrics:   NewMetricsTracker(client),
		stopCh:    make(chan struct{}),
		bufferCap: 3, // Low cap for testing
		logger:    ctrl.Log,
	}

	// Fill buffer to cap
	mgr.bufferMetric("m1", 1.0, nil)
	mgr.bufferMetric("m2", 2.0, nil)
	mgr.bufferMetric("m3", 3.0, nil) // This triggers auto-flush

	// Buffer should be empty after auto-flush
	mgr.bufferMu.Lock()
	count := len(mgr.metricsBuffer)
	mgr.bufferMu.Unlock()
	if count != 0 {
		t.Errorf("expected buffer to be empty after auto-flush, got %d items", count)
	}
}

func TestFlushMetricsBuffer_Empty(t *testing.T) {
	client := newDisabledClient()
	mgr := &Manager{
		Client:    client,
		Events:    NewEventTracker(client),
		Metrics:   NewMetricsTracker(client),
		stopCh:    make(chan struct{}),
		bufferCap: 500,
		logger:    ctrl.Log,
	}

	// Flushing empty buffer should not panic
	mgr.flushMetricsBuffer()
}

func TestFlushMetricsBuffer_WithItems(t *testing.T) {
	client := newDisabledClient()
	mgr := &Manager{
		Client:    client,
		Events:    NewEventTracker(client),
		Metrics:   NewMetricsTracker(client),
		stopCh:    make(chan struct{}),
		bufferCap: 500,
		logger:    ctrl.Log,
	}

	mgr.bufferMetric("m1", 1.0, nil)
	mgr.bufferMetric("m2", 2.0, map[string]interface{}{"key": "val"})

	mgr.flushMetricsBuffer()

	mgr.bufferMu.Lock()
	count := len(mgr.metricsBuffer)
	mgr.bufferMu.Unlock()
	if count != 0 {
		t.Errorf("expected buffer to be empty after flush, got %d", count)
	}
}

func TestBufferMetric_ConcurrentSafe(t *testing.T) {
	client := newDisabledClient()
	mgr := &Manager{
		Client:    client,
		Events:    NewEventTracker(client),
		Metrics:   NewMetricsTracker(client),
		stopCh:    make(chan struct{}),
		bufferCap: 1000,
		logger:    ctrl.Log,
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mgr.bufferMetric("concurrent.metric", float64(i), nil)
		}(i)
	}
	wg.Wait()

	mgr.bufferMu.Lock()
	count := len(mgr.metricsBuffer)
	mgr.bufferMu.Unlock()
	if count != 100 {
		t.Errorf("expected 100 buffered metrics, got %d", count)
	}
}

func TestManagerIsEnabled(t *testing.T) {
	client := newDisabledClient()
	mgr := &Manager{
		Client: client,
	}
	if mgr.IsEnabled() {
		t.Error("expected disabled when no instrumentation key")
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("azure://something", "azure") {
		t.Error("expected true for azure")
	}
	if containsAny("gce://something", "azure", "aws") {
		t.Error("expected false for gce with azure/aws")
	}
	if containsAny("", "azure") {
		t.Error("expected false for empty string")
	}
	if containsAny("test", "") {
		t.Error("expected false for empty substring")
	}
}

func TestDetectInstallationMethod(t *testing.T) {
	// Default should be kubectl
	method := detectInstallationMethod()
	if method != "kubectl" && method != "helm" && method != "operator-sdk" {
		t.Errorf("unexpected installation method: %s", method)
	}
}

func TestGetRestartCount(t *testing.T) {
	count := getRestartCount()
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}
