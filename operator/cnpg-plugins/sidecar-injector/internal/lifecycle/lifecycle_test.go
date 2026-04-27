// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lifecycle

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func gatewayContainer(env ...corev1.EnvVar) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "postgres"},
				{Name: gatewayContainerName, Env: env},
				{Name: "otel-collector"},
			},
		},
	}
}

func envNames(env []corev1.EnvVar) []string {
	out := make([]string, len(env))
	for i, e := range env {
		out[i] = e.Name
	}
	return out
}

// TestInjectGatewayOTelEnv_AllAppended covers the CREATE case: empty gateway
// env, both OTel env vars appended in declaration order. Per-pod attribution
// (k8s.pod.name) is added by the collector's resource processor downstream,
// so we don't need POD_NAME / OTEL_RESOURCE_ATTRIBUTES on the gateway.
func TestInjectGatewayOTelEnv_AllAppended(t *testing.T) {
	pod := gatewayContainer()
	injectGatewayOTelEnv(pod)

	got := envNames(pod.Spec.Containers[1].Env)
	want := []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_METRICS_ENABLED"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("envs got %v, want %v", got, want)
	}
}

// TestInjectGatewayOTelEnv_MetricsEnabledPresent guards against future
// removal of OTEL_METRICS_ENABLED — without this env, the pgmongo gateway
// silently disables OTLP metrics export.
func TestInjectGatewayOTelEnv_MetricsEnabledPresent(t *testing.T) {
	pod := gatewayContainer()
	injectGatewayOTelEnv(pod)
	for _, e := range pod.Spec.Containers[1].Env {
		if e.Name == "OTEL_METRICS_ENABLED" {
			if e.Value != "true" {
				t.Errorf("OTEL_METRICS_ENABLED = %q, want %q", e.Value, "true")
			}
			return
		}
	}
	t.Fatal("OTEL_METRICS_ENABLED env var missing — pgmongo gateway will not initialize OTel")
}

// TestInjectGatewayOTelEnv_PreservesExisting verifies the dedup logic: a
// pre-existing env var with the same name as one of the OTel envs is left
// untouched (we don't overwrite), and the others are still appended.
func TestInjectGatewayOTelEnv_PreservesExisting(t *testing.T) {
	pod := gatewayContainer(corev1.EnvVar{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://custom:4317"})
	injectGatewayOTelEnv(pod)

	env := pod.Spec.Containers[1].Env
	if len(env) != 2 {
		t.Errorf("expected 2 env vars, got %d (%v)", len(env), envNames(env))
	}
	// Pre-existing endpoint must be unchanged.
	for _, e := range env {
		if e.Name == "OTEL_EXPORTER_OTLP_ENDPOINT" && e.Value != "http://custom:4317" {
			t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT was overwritten: got %q, want %q", e.Value, "http://custom:4317")
		}
	}
}

// TestInjectGatewayOTelEnv_Idempotent is the actual CNPG-PATCH scenario: a
// second invocation on the output of the first must produce a byte-equal Env
// slice. Otherwise CNPG's reconciler trips on "spec: Forbidden: pod updates
// may not change fields other than ...".
func TestInjectGatewayOTelEnv_Idempotent(t *testing.T) {
	pod := gatewayContainer()
	injectGatewayOTelEnv(pod)
	first := append([]corev1.EnvVar(nil), pod.Spec.Containers[1].Env...)

	injectGatewayOTelEnv(pod)
	second := pod.Spec.Containers[1].Env

	if !reflect.DeepEqual(first, second) {
		t.Errorf("second invocation changed Env slice:\nfirst:  %v\nsecond: %v", envNames(first), envNames(second))
	}
}

// TestInjectGatewayOTelEnv_NoGatewayContainer is a no-op safety check.
func TestInjectGatewayOTelEnv_NoGatewayContainer(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "postgres"}},
		},
	}
	injectGatewayOTelEnv(pod)
	if len(pod.Spec.Containers[0].Env) != 0 {
		t.Errorf("expected no envs on non-gateway container, got %v", envNames(pod.Spec.Containers[0].Env))
	}
}
