// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	apiv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	cnpglifecycle "github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pluginmetadata "github.com/documentdb/cnpg-i-sidecar-injector/pkg/metadata"
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

func TestLifecycleHookInjectsContainerResourcesAndGoMemLimit(t *testing.T) {
	enabled := true
	cluster := &apiv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiv1.SchemeGroupVersion.String(),
			Kind:       apiv1.ClusterKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster",
			Namespace: "default",
		},
		Spec: apiv1.ClusterSpec{
			Plugins: []apiv1.PluginConfiguration{
				{
					Name:    pluginmetadata.PluginName,
					Enabled: &enabled,
					Parameters: map[string]string{
						"gatewayImage":               "gateway:latest",
						"gatewayMemoryRequest":       "768Mi",
						"gatewayMemoryLimit":         "3Gi",
						"gatewayCpuRequest":          "500m",
						"gatewayCpuLimit":            "2",
						"otelCollectorImage":         "otel:latest",
						"otelConfigMapName":          "otel-config",
						"otelMemoryRequest":          "64Mi",
						"otelMemoryLimit":            "128Mi",
						"otelCpuRequest":             "100m",
						"documentDbCredentialSecret": "documentdb-credentials",
					},
				},
			},
		},
		Status: apiv1.ClusterStatus{
			TargetPrimary: "cluster-1",
		},
	}
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "cluster-1",
			Namespace:   "default",
			Labels:      map[string]string{"cnpg.io/cluster": "cluster"},
			Annotations: map[string]string{"cnpg.io/operatorVersion": "test"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "postgres"},
			},
		},
	}

	response, err := Implementation{}.LifecycleHook(context.Background(), &cnpglifecycle.OperatorLifecycleRequest{
		OperationType: &cnpglifecycle.OperatorOperationType{
			Type: cnpglifecycle.OperatorOperationType_TYPE_CREATE,
		},
		ClusterDefinition: mustMarshal(t, cluster),
		ObjectDefinition:  mustMarshal(t, pod),
	})
	if err != nil {
		t.Fatalf("LifecycleHook() error: %v", err)
	}

	containers := addedContainersFromPatch(t, response.JsonPatch)
	gateway, ok := containers[gatewayContainerName]
	if !ok {
		t.Fatalf("gateway container missing from patch; patch=%s", string(response.JsonPatch))
	}
	assertResourceQuantity(t, gateway.Resources.Requests, corev1.ResourceCPU, "500m")
	assertResourceQuantity(t, gateway.Resources.Requests, corev1.ResourceMemory, "768Mi")
	assertResourceQuantity(t, gateway.Resources.Limits, corev1.ResourceCPU, "2")
	assertResourceQuantity(t, gateway.Resources.Limits, corev1.ResourceMemory, "3Gi")

	otel, ok := containers["otel-collector"]
	if !ok {
		t.Fatalf("otel container missing from patch; patch=%s", string(response.JsonPatch))
	}
	assertResourceQuantity(t, otel.Resources.Requests, corev1.ResourceCPU, "100m")
	assertResourceQuantity(t, otel.Resources.Requests, corev1.ResourceMemory, "64Mi")
	assertResourceQuantity(t, otel.Resources.Limits, corev1.ResourceMemory, "128Mi")
	if _, ok := otel.Resources.Limits[corev1.ResourceCPU]; ok {
		t.Errorf("otel Resources.Limits[%s] set, want unset", corev1.ResourceCPU)
	}
	assertEnvValue(t, otel.Env, "GOMEMLIMIT", "107374182")
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	return data
}

func addedContainersFromPatch(t *testing.T, patch []byte) map[string]corev1.Container {
	t.Helper()
	var operations []struct {
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(patch, &operations); err != nil {
		t.Fatalf("json.Unmarshal(patch) error: %v", err)
	}

	containers := map[string]corev1.Container{}
	for _, operation := range operations {
		if operation.Path == "/spec/containers" {
			var added []corev1.Container
			if err := json.Unmarshal(operation.Value, &added); err != nil {
				continue
			}
			for _, container := range added {
				containers[container.Name] = container
			}
			continue
		}
		if !strings.HasPrefix(operation.Path, "/spec/containers/") {
			continue
		}
		var container corev1.Container
		if err := json.Unmarshal(operation.Value, &container); err != nil {
			continue
		}
		if container.Name != "" {
			containers[container.Name] = container
		}
	}
	return containers
}

func assertResourceQuantity(t *testing.T, resources corev1.ResourceList, name corev1.ResourceName, want string) {
	t.Helper()
	got, ok := resources[name]
	if !ok {
		t.Fatalf("resource %s missing, want %s", name, want)
	}
	wantQuantity := resource.MustParse(want)
	if got.Cmp(wantQuantity) != 0 {
		t.Errorf("resource %s = %s, want %s", name, got.String(), want)
	}
}

func assertEnvValue(t *testing.T, env []corev1.EnvVar, name, want string) {
	t.Helper()
	for _, envVar := range env {
		if envVar.Name == name {
			if envVar.Value != want {
				t.Errorf("%s = %q, want %q", name, envVar.Value, want)
			}
			return
		}
	}
	t.Fatalf("%s env var missing", name)
}
