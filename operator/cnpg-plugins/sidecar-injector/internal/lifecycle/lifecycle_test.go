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

// assertPSARestricted verifies a container SecurityContext sets every field
// required by the Kubernetes Pod Security Admission "restricted" profile.
// Pod-level inheritance does not satisfy PSA, so the injected sidecars must
// carry these per-container.
func assertPSARestricted(t *testing.T, name string, sc *corev1.SecurityContext) {
	t.Helper()
	if sc == nil {
		t.Fatalf("%s: SecurityContext is nil", name)
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Errorf("%s: RunAsNonRoot must be true", name)
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Errorf("%s: AllowPrivilegeEscalation must be false", name)
	}
	if sc.Privileged != nil && *sc.Privileged {
		t.Errorf("%s: Privileged must not be true", name)
	}
	if sc.Capabilities == nil {
		t.Errorf("%s: Capabilities must drop ALL", name)
	} else {
		dropsAll := false
		for _, c := range sc.Capabilities.Drop {
			if c == "ALL" {
				dropsAll = true
				break
			}
		}
		if !dropsAll {
			t.Errorf("%s: Capabilities.Drop must contain ALL, got %v", name, sc.Capabilities.Drop)
		}
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("%s: SeccompProfile.Type must be RuntimeDefault", name)
	}
}

// TestHardenedSecurityContext_PSARestricted guards the helper applied to both
// injected sidecars so PSA "restricted" compliance cannot regress silently.
func TestHardenedSecurityContext_PSARestricted(t *testing.T) {
	assertPSARestricted(t, "hardenedSecurityContext", hardenedSecurityContext())
}

// TestHardenedSecurityContext_NoForcedUID asserts the shared helper does not
// pin a UID/GID, so third-party images (e.g. otel-collector) keep their own
// baked-in non-root user.
func TestHardenedSecurityContext_NoForcedUID(t *testing.T) {
	sc := hardenedSecurityContext()
	if sc.RunAsUser != nil {
		t.Errorf("shared hardened context must not pin RunAsUser, got %d", *sc.RunAsUser)
	}
	if sc.RunAsGroup != nil {
		t.Errorf("shared hardened context must not pin RunAsGroup, got %d", *sc.RunAsGroup)
	}
}

// TestGatewaySecurityContext_PSARestrictedAsUID1000 asserts the gateway sidecar
// is PSA restricted and pinned to the non-root UID/GID 1000 its image expects.
func TestGatewaySecurityContext_PSARestrictedAsUID1000(t *testing.T) {
	sc := gatewaySecurityContext()
	assertPSARestricted(t, "gatewaySecurityContext", sc)
	if sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Errorf("gateway must run as UID 1000, got %v", sc.RunAsUser)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != 1000 {
		t.Errorf("gateway must run as GID 1000, got %v", sc.RunAsGroup)
	}
}

// TestNewOtelCollectorSidecar_Hardened is the injection-layer guard for the
// otel-collector sidecar (which the e2e suite does not exercise unless
// monitoring is enabled). It asserts the constructed container is PSA
// restricted and, unlike the gateway, does not force a UID so the collector
// image keeps its own non-root user (UID 10001).
func TestNewOtelCollectorSidecar_Hardened(t *testing.T) {
	c := newOtelCollectorSidecar("otel/opentelemetry-collector-contrib:test", "demo")

	if c.Name != otelCollectorContainerName {
		t.Fatalf("container name = %q, want %q", c.Name, otelCollectorContainerName)
	}
	assertPSARestricted(t, "otel-collector", c.SecurityContext)
	if c.SecurityContext.RunAsUser != nil {
		t.Errorf("otel-collector must not force a UID (keeps image's 10001), got %d", *c.SecurityContext.RunAsUser)
	}
	if c.SecurityContext.RunAsGroup != nil {
		t.Errorf("otel-collector must not force a GID, got %d", *c.SecurityContext.RunAsGroup)
	}
}
