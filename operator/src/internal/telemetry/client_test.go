// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"errors"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestParseInstrumentationKeyFromConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"full connection string", "InstrumentationKey=abc-123;IngestionEndpoint=https://example.com/", "abc-123"},
		{"key only", "InstrumentationKey=key-only", "key-only"},
		{"empty string", "", ""},
		{"no key", "IngestionEndpoint=https://example.com/", ""},
		{"with spaces", "InstrumentationKey=abc-123 ; IngestionEndpoint=https://example.com/", "abc-123"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseInstrumentationKeyFromConnectionString(tc.input)
			if result != tc.expected {
				t.Errorf("parseInstrumentationKeyFromConnectionString(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestParseIngestionEndpointFromConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"full connection string", "InstrumentationKey=abc;IngestionEndpoint=https://westus2.in.ai.azure.com/", "https://westus2.in.ai.azure.com/"},
		{"no endpoint", "InstrumentationKey=abc", ""},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseIngestionEndpointFromConnectionString(tc.input)
			if result != tc.expected {
				t.Errorf("got %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestSanitizeErrorMessage(t *testing.T) {
	if msg := sanitizeErrorMessage("some error occurred"); msg == "" {
		t.Error("expected non-empty for real error message")
	}
	if msg := sanitizeErrorMessage(""); msg != "" {
		t.Errorf("expected empty for empty input, got %q", msg)
	}
}

func TestNewTelemetryClient_Disabled(t *testing.T) {
	// No env vars set → should be disabled
	ctx := &OperatorContext{OperatorVersion: "test"}
	client := NewTelemetryClient(ctx)
	if client.IsEnabled() {
		t.Error("expected client to be disabled when no instrumentation key is set")
	}
	// These should not panic on disabled client
	client.Start()
	client.TrackEvent("test", nil)
	client.TrackMetric("test", 1.0, nil)
	client.TrackException(errors.New("test"), nil)
	client.Stop()
}

func TestWithLogger(t *testing.T) {
	ctx := &OperatorContext{OperatorVersion: "test"}
	client := NewTelemetryClient(ctx, WithLogger(ctrl.Log))
	if client == nil {
		t.Error("expected non-nil client")
	}
}
