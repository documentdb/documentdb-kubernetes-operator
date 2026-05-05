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
}

func TestToParametersRoundTrip(t *testing.T) {
	original := &Configuration{
		GatewayImage:           "my-image:latest",
		GatewayImagePullPolicy: corev1.PullNever,
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
}
