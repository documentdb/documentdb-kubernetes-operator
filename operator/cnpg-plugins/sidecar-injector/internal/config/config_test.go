// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"testing"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	corev1 "k8s.io/api/core/v1"
)

func TestParsePullPolicy(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected corev1.PullPolicy
	}{
		{"Always", "Always", corev1.PullAlways},
		{"Never", "Never", corev1.PullNever},
		{"IfNotPresent", "IfNotPresent", corev1.PullIfNotPresent},
		{"empty string returns empty", "", ""},
		{"invalid value returns empty", "invalid", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePullPolicy(tt.input)
			if result != tt.expected {
				t.Errorf("parsePullPolicy(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("sets default gateway image when empty", func(t *testing.T) {
		config := &Configuration{}
		config.applyDefaults()
		expected := "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway:0.110.0"
		if config.GatewayImage != expected {
			t.Errorf("expected %q, got %q", expected, config.GatewayImage)
		}
	})

	t.Run("sets IfNotPresent when pull policy is empty", func(t *testing.T) {
		config := &Configuration{}
		config.applyDefaults()
		if config.GatewayImagePullPolicy != corev1.PullIfNotPresent {
			t.Errorf("expected IfNotPresent, got %q", config.GatewayImagePullPolicy)
		}
	})

	t.Run("preserves explicit pull policy", func(t *testing.T) {
		config := &Configuration{GatewayImagePullPolicy: corev1.PullNever}
		config.applyDefaults()
		if config.GatewayImagePullPolicy != corev1.PullNever {
			t.Errorf("expected Never, got %q", config.GatewayImagePullPolicy)
		}
	})
}

func TestFromParameters(t *testing.T) {
	t.Run("pull policy from parameters", func(t *testing.T) {
		helper := &common.Plugin{Parameters: map[string]string{
			"gatewayImagePullPolicy": "Never",
		}}
		config, errs := FromParameters(helper)
		if len(errs) != 0 {
			t.Fatalf("unexpected validation errors: %v", errs)
		}
		if config.GatewayImagePullPolicy != corev1.PullNever {
			t.Errorf("GatewayImagePullPolicy = %q, want Never", config.GatewayImagePullPolicy)
		}
	})

	t.Run("defaults to IfNotPresent when not set", func(t *testing.T) {
		helper := &common.Plugin{Parameters: map[string]string{}}
		config, errs := FromParameters(helper)
		if len(errs) != 0 {
			t.Fatalf("unexpected validation errors: %v", errs)
		}
		if config.GatewayImagePullPolicy != corev1.PullIfNotPresent {
			t.Errorf("GatewayImagePullPolicy = %q, want IfNotPresent", config.GatewayImagePullPolicy)
		}
	})

	t.Run("parses OTel monitoring parameters", func(t *testing.T) {
		helper := &common.Plugin{Parameters: map[string]string{
			"otelCollectorImage": "otel/opentelemetry-collector-contrib:test",
			"otelConfigMapName":  "demo-otel-config",
			"otelConfigHash":     "abc123",
		}}
		config, errs := FromParameters(helper)
		if len(errs) != 0 {
			t.Fatalf("unexpected validation errors: %v", errs)
		}
		if config.OtelCollectorImage != "otel/opentelemetry-collector-contrib:test" {
			t.Errorf("OtelCollectorImage = %q", config.OtelCollectorImage)
		}
		if config.OtelConfigMapName != "demo-otel-config" {
			t.Errorf("OtelConfigMapName = %q", config.OtelConfigMapName)
		}
		if config.OtelConfigHash != "abc123" {
			t.Errorf("OtelConfigHash = %q", config.OtelConfigHash)
		}
	})

	for _, tt := range []struct {
		name       string
		parameters map[string]string
		wantErrors int
	}{
		{
			name: "rejects collector image without config map",
			parameters: map[string]string{
				"otelCollectorImage": "otel/opentelemetry-collector-contrib:test",
			},
			wantErrors: 1,
		},
		{
			name: "rejects config map without collector image",
			parameters: map[string]string{
				"otelConfigMapName": "demo-otel-config",
			},
			wantErrors: 1,
		},
		{
			name: "rejects optional OTel parameter without required parameters",
			parameters: map[string]string{
				"prometheusPort": "8888",
			},
			wantErrors: 2,
		},
		{
			name: "rejects config hash without required parameters",
			parameters: map[string]string{
				"otelConfigHash": "abc123",
			},
			wantErrors: 2,
		},
		{
			name: "rejects memory request without required parameters",
			parameters: map[string]string{
				"otelMemoryRequest": "64Mi",
			},
			wantErrors: 2,
		},
		{
			name: "rejects memory limit without required parameters",
			parameters: map[string]string{
				"otelMemoryLimit": "128Mi",
			},
			wantErrors: 2,
		},
		{
			name: "rejects CPU request without required parameters",
			parameters: map[string]string{
				"otelCpuRequest": "100m",
			},
			wantErrors: 2,
		},
		{
			name: "rejects CPU limit without required parameters",
			parameters: map[string]string{
				"otelCpuLimit": "300m",
			},
			wantErrors: 2,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := FromParameters(&common.Plugin{Parameters: tt.parameters})
			if len(errs) != tt.wantErrors {
				t.Fatalf("validation errors = %d, want %d: %v", len(errs), tt.wantErrors, errs)
			}
		})
	}

	t.Run("resource parameters from parameters", func(t *testing.T) {
		helper := &common.Plugin{Parameters: map[string]string{
			"gatewayMemoryRequest": "768Mi",
			"gatewayMemoryLimit":   "3Gi",
			"gatewayCpuRequest":    "500m",
			"gatewayCpuLimit":      "2",
			"otelCollectorImage":   "otel:latest",
			"otelConfigMapName":    "otel-config",
			"otelMemoryRequest":    "64Mi",
			"otelMemoryLimit":      "128Mi",
			"otelCpuRequest":       "100m",
		}}
		config, errs := FromParameters(helper)
		if len(errs) != 0 {
			t.Fatalf("unexpected validation errors: %v", errs)
		}
		if config.GatewayMemoryRequest != "768Mi" {
			t.Errorf("GatewayMemoryRequest = %q, want 768Mi", config.GatewayMemoryRequest)
		}
		if config.GatewayMemoryLimit != "3Gi" {
			t.Errorf("GatewayMemoryLimit = %q, want 3Gi", config.GatewayMemoryLimit)
		}
		if config.GatewayCPURequest != "500m" {
			t.Errorf("GatewayCPURequest = %q, want 500m", config.GatewayCPURequest)
		}
		if config.GatewayCPULimit != "2" {
			t.Errorf("GatewayCPULimit = %q, want 2", config.GatewayCPULimit)
		}
		if config.OTelMemoryRequest != "64Mi" {
			t.Errorf("OTelMemoryRequest = %q, want 64Mi", config.OTelMemoryRequest)
		}
		if config.OTelMemoryLimit != "128Mi" {
			t.Errorf("OTelMemoryLimit = %q, want 128Mi", config.OTelMemoryLimit)
		}
		if config.OTelCPURequest != "100m" {
			t.Errorf("OTelCPURequest = %q, want 100m", config.OTelCPURequest)
		}
	})
}

func TestToParametersRoundTrip(t *testing.T) {
	original := &Configuration{
		GatewayImage:           "my-image:latest",
		GatewayImagePullPolicy: corev1.PullNever,
		GatewayMemoryRequest:   "768Mi",
		GatewayMemoryLimit:     "3Gi",
		GatewayCPURequest:      "500m",
		GatewayCPULimit:        "2",
		OtelCollectorImage:     "otel:latest",
		OtelConfigMapName:      "otel-config",
		OtelConfigHash:         "abc123",
		OTelMemoryRequest:      "64Mi",
		OTelMemoryLimit:        "128Mi",
		OTelCPURequest:         "100m",
	}
	original.applyDefaults()

	params, err := original.ToParameters()
	if err != nil {
		t.Fatalf("ToParameters() error: %v", err)
	}

	helper := &common.Plugin{Parameters: params}
	restored, errs := FromParameters(helper)
	if len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if restored.GatewayImagePullPolicy != original.GatewayImagePullPolicy {
		t.Errorf("round-trip pull policy = %q, want %q", restored.GatewayImagePullPolicy, original.GatewayImagePullPolicy)
	}
	if restored.GatewayImage != original.GatewayImage {
		t.Errorf("round-trip gateway image = %q, want %q", restored.GatewayImage, original.GatewayImage)
	}
	if restored.GatewayMemoryRequest != original.GatewayMemoryRequest {
		t.Errorf("round-trip gateway memory request = %q, want %q", restored.GatewayMemoryRequest, original.GatewayMemoryRequest)
	}
	if restored.GatewayMemoryLimit != original.GatewayMemoryLimit {
		t.Errorf("round-trip gateway memory limit = %q, want %q", restored.GatewayMemoryLimit, original.GatewayMemoryLimit)
	}
	if restored.GatewayCPURequest != original.GatewayCPURequest {
		t.Errorf("round-trip gateway cpu request = %q, want %q", restored.GatewayCPURequest, original.GatewayCPURequest)
	}
	if restored.GatewayCPULimit != original.GatewayCPULimit {
		t.Errorf("round-trip gateway cpu limit = %q, want %q", restored.GatewayCPULimit, original.GatewayCPULimit)
	}
	if restored.OtelCollectorImage != original.OtelCollectorImage {
		t.Errorf("round-trip OTel collector image = %q, want %q", restored.OtelCollectorImage, original.OtelCollectorImage)
	}
	if restored.OtelConfigMapName != original.OtelConfigMapName {
		t.Errorf("round-trip OTel config map = %q, want %q", restored.OtelConfigMapName, original.OtelConfigMapName)
	}
	if restored.OtelConfigHash != original.OtelConfigHash {
		t.Errorf("round-trip OTel config hash = %q, want %q", restored.OtelConfigHash, original.OtelConfigHash)
	}
	if restored.OTelMemoryRequest != original.OTelMemoryRequest {
		t.Errorf("round-trip otel memory request = %q, want %q", restored.OTelMemoryRequest, original.OTelMemoryRequest)
	}
	if restored.OTelMemoryLimit != original.OTelMemoryLimit {
		t.Errorf("round-trip otel memory limit = %q, want %q", restored.OTelMemoryLimit, original.OTelMemoryLimit)
	}
	if restored.OTelCPURequest != original.OTelCPURequest {
		t.Errorf("round-trip otel cpu request = %q, want %q", restored.OTelCPURequest, original.OTelCPURequest)
	}
}
