// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package documentdb

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := previewv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func TestRenderCRConcatenatesBaseAndMixins(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, baseSubdir, "ddb"+templateExt),
		"apiVersion: documentdb.io/preview\nkind: DocumentDB\nmetadata:\n  name: ${NAME}\n  namespace: ${NAMESPACE}\n")
	mustWrite(t, filepath.Join(dir, mixinSubdir, "tls"+templateExt),
		"# tls mixin for ${NAME}\n")

	got, err := RenderCR("ddb", "my-dd", "ns1", []string{"tls"}, nil, dir)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "name: my-dd") {
		t.Errorf("expected NAME substitution; got:\n%s", s)
	}
	if !strings.Contains(s, "namespace: ns1") {
		t.Errorf("expected NAMESPACE substitution; got:\n%s", s)
	}
	if !strings.Contains(s, "---\n") {
		t.Errorf("expected YAML separator between base and mixin; got:\n%s", s)
	}
	if !strings.Contains(s, "tls mixin for my-dd") {
		t.Errorf("expected mixin body; got:\n%s", s)
	}
}

func TestRenderCRMissingBaseReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := RenderCR("nope", "n", "ns", nil, nil, dir)
	if err == nil {
		t.Fatal("expected error for missing base template")
	}
}

func TestRenderCRUserVarsOverrideNameAndNamespace(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, baseSubdir, "b"+templateExt), "x: ${NAME}-${EXTRA}\n")
	got, err := RenderCR("b", "n", "ns", nil, map[string]string{"EXTRA": "z"}, dir)
	if err != nil {
		t.Fatalf("RenderCR: %v", err)
	}
	if !strings.Contains(string(got), "x: n-z") {
		t.Errorf("expected substituted extra var; got: %s", got)
	}
}

func TestGetAndList(t *testing.T) {
	s := newScheme(t)
	objs := []client.Object{
		&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}},
		&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns1"}},
		&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns2"}},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	ctx := context.Background()

	got, err := Get(ctx, c, types.NamespacedName{Name: "a", Namespace: "ns1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "a" {
		t.Errorf("got name %q want a", got.Name)
	}

	items, err := List(ctx, c, "ns1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("got %d items want 2", len(items))
	}

	all, err := List(ctx, c, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("got %d items want 3", len(all))
	}
}

func TestPatchSpec(t *testing.T) {
	s := newScheme(t)
	dd := &previewv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"},
		Spec:       previewv1.DocumentDBSpec{NodeCount: 1, InstancesPerNode: 1},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
	ctx := context.Background()

	fresh, err := Get(ctx, c, client.ObjectKeyFromObject(dd))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := PatchSpec(ctx, c, fresh, func(spec *previewv1.DocumentDBSpec) {
		spec.LogLevel = "debug"
	}); err != nil {
		t.Fatalf("PatchSpec: %v", err)
	}
	after, err := Get(ctx, c, client.ObjectKeyFromObject(dd))
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if after.Spec.LogLevel != "debug" {
		t.Errorf("expected LogLevel=debug, got %q", after.Spec.LogLevel)
	}
}

func TestIsHealthyMatchesRunningStatus(t *testing.T) {
	if isHealthy(nil) {
		t.Error("nil should not be healthy")
	}
	if isHealthy(&previewv1.DocumentDB{}) {
		t.Error("empty should not be healthy")
	}
	dd := &previewv1.DocumentDB{Status: previewv1.DocumentDBStatus{Status: ReadyStatus}}
	if !isHealthy(dd) {
		t.Errorf("%q should be healthy", ReadyStatus)
	}
	notReady := &previewv1.DocumentDB{Status: previewv1.DocumentDBStatus{Status: "Running"}}
	if isHealthy(notReady) {
		t.Error(`"Running" should not be healthy (ReadyStatus mismatch)`)
	}
}

func TestWaitHealthyTimeout(t *testing.T) {
	s := newScheme(t)
	dd := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := WaitHealthy(ctx, c, client.ObjectKeyFromObject(dd), 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestDeleteRemovesObject(t *testing.T) {
	s := newScheme(t)
	dd := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
	ctx := context.Background()
	if err := Delete(ctx, c, dd, 2*time.Second); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Get(ctx, c, client.ObjectKeyFromObject(dd)); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}

func TestPatchInstances_UpdatesSpec(t *testing.T) {
	s := newScheme(t)
	dd := &previewv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "dd", Namespace: "ns1"},
		Spec:       previewv1.DocumentDBSpec{NodeCount: 1, InstancesPerNode: 2},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
	ctx := context.Background()

	if err := PatchInstances(ctx, c, "ns1", "dd", 3); err != nil {
		t.Fatalf("PatchInstances: %v", err)
	}
	got, err := Get(ctx, c, types.NamespacedName{Namespace: "ns1", Name: "dd"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.InstancesPerNode != 3 {
		t.Fatalf("InstancesPerNode=%d, want 3", got.Spec.InstancesPerNode)
	}
}

func TestPatchInstances_NoopWhenEqual(t *testing.T) {
	s := newScheme(t)
	dd := &previewv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "dd", Namespace: "ns1", ResourceVersion: "7"},
		Spec:       previewv1.DocumentDBSpec{NodeCount: 1, InstancesPerNode: 2},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
	if err := PatchInstances(context.Background(), c, "ns1", "dd", 2); err != nil {
		t.Fatalf("PatchInstances no-op: %v", err)
	}
}

func TestPatchInstances_RejectsOutOfRange(t *testing.T) {
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()
	for _, n := range []int{0, 4, -1} {
		if err := PatchInstances(context.Background(), c, "ns1", "dd", n); err == nil {
			t.Errorf("PatchInstances(%d) expected error, got nil", n)
		}
	}
}

func TestPatchInstances_NotFound(t *testing.T) {
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()
	if err := PatchInstances(context.Background(), c, "ns1", "missing", 2); err == nil {
		t.Fatal("expected error for missing DocumentDB")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestCreateAppliesTLSDisabledMixin uses the real base + tls_disabled
// mixin shipped under test/e2e/manifests/ to prove the multi-document
// merge in Create is no longer a silent drop: the mixin's
// Spec.TLS.Gateway.Mode must round-trip to the created object.
func TestCreateAppliesTLSDisabledMixin(t *testing.T) {
	root := realManifestsRoot(t)
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()

	obj, err := Create(context.Background(), c, "ns1", "dd1", CreateOptions{
		Base:          "documentdb",
		Mixins:        []string{"tls_disabled"},
		ManifestsRoot: root,
		Vars: map[string]string{
			"INSTANCES":         "1",
			"STORAGE_SIZE":      "1Gi",
			"STORAGE_CLASS":     "standard",
			"DOCUMENTDB_IMAGE":  "ghcr.io/example/ddb:test",
			"GATEWAY_IMAGE":     "ghcr.io/example/gw:test",
			"CREDENTIAL_SECRET": "documentdb-credentials",
			"EXPOSURE_TYPE":     "ClusterIP",
			"LOG_LEVEL":         "info",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Assert against the returned object and re-Get it from the fake
	// client; both paths must reflect the merged mixin.
	if obj.Spec.TLS == nil || obj.Spec.TLS.Gateway == nil {
		t.Fatalf("returned object missing Spec.TLS.Gateway; got %+v", obj.Spec)
	}
	if obj.Spec.TLS.Gateway.Mode != "Disabled" {
		t.Fatalf("returned Spec.TLS.Gateway.Mode=%q, want Disabled", obj.Spec.TLS.Gateway.Mode)
	}

	got, err := Get(context.Background(), c, types.NamespacedName{Namespace: "ns1", Name: "dd1"})
	if err != nil {
		t.Fatalf("Get back: %v", err)
	}
	if got.Spec.TLS == nil || got.Spec.TLS.Gateway == nil {
		t.Fatalf("stored object missing Spec.TLS.Gateway; got %+v", got.Spec)
	}
	if got.Spec.TLS.Gateway.Mode != "Disabled" {
		t.Fatalf("stored Spec.TLS.Gateway.Mode=%q, want Disabled", got.Spec.TLS.Gateway.Mode)
	}
	// Base fields must still be present after the merge.
	if got.Spec.InstancesPerNode != 1 {
		t.Errorf("Spec.InstancesPerNode=%d, want 1", got.Spec.InstancesPerNode)
	}
	if got.Spec.Resource.Storage.PvcSize != "1Gi" {
		t.Errorf("Spec.Resource.Storage.PvcSize=%q, want 1Gi", got.Spec.Resource.Storage.PvcSize)
	}
}

// TestCreateAppliesReclaimRetainMixin exercises the same multi-doc
// merge path with a mixin that nests Spec.Resource.Storage — verifying
// the deep-merge preserves sibling keys (PvcSize, StorageClass) while
// adding PersistentVolumeReclaimPolicy from the mixin.
func TestCreateAppliesReclaimRetainMixin(t *testing.T) {
	root := realManifestsRoot(t)
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()

	obj, err := Create(context.Background(), c, "ns1", "dd2", CreateOptions{
		Base:          "documentdb",
		Mixins:        []string{"reclaim_retain"},
		ManifestsRoot: root,
		Vars: map[string]string{
			"INSTANCES":         "1",
			"STORAGE_SIZE":      "2Gi",
			"STORAGE_CLASS":     "standard",
			"DOCUMENTDB_IMAGE":  "ghcr.io/example/ddb:test",
			"GATEWAY_IMAGE":     "ghcr.io/example/gw:test",
			"CREDENTIAL_SECRET": "documentdb-credentials",
			"EXPOSURE_TYPE":     "ClusterIP",
			"LOG_LEVEL":         "info",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if obj.Spec.Resource.Storage.PersistentVolumeReclaimPolicy != "Retain" {
		t.Fatalf("Spec.Resource.Storage.PersistentVolumeReclaimPolicy=%q, want Retain",
			obj.Spec.Resource.Storage.PersistentVolumeReclaimPolicy)
	}
	if obj.Spec.Resource.Storage.PvcSize != "2Gi" {
		t.Errorf("Spec.Resource.Storage.PvcSize=%q, want 2Gi (base preserved after merge)",
			obj.Spec.Resource.Storage.PvcSize)
	}
}

// realManifestsRoot returns the absolute path to test/e2e/manifests so
// the round-trip tests exercise the real templates rather than the
// synthetic fixtures that the RenderCR-only tests build with t.TempDir.
// Anchored off runtime.Caller so `go test` from any directory works.
func realManifestsRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed — cannot locate test/e2e/manifests")
	}
	// this file: test/e2e/pkg/e2eutils/documentdb/documentdb_test.go
	// walk up to test/e2e, then into manifests.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "manifests")
	if _, err := os.Stat(filepath.Join(root, "base", "documentdb"+templateExt)); err != nil {
		t.Fatalf("manifests root not found at %s: %v", root, err)
	}
	return root
}
