// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package documentdb

import (
	"context"
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
	if IsHealthy(nil) {
		t.Error("nil should not be healthy")
	}
	if IsHealthy(&previewv1.DocumentDB{}) {
		t.Error("empty should not be healthy")
	}
	dd := &previewv1.DocumentDB{Status: previewv1.DocumentDBStatus{Status: ReadyStatus}}
	if !IsHealthy(dd) {
		t.Errorf("%q should be healthy", ReadyStatus)
	}
	notReady := &previewv1.DocumentDB{Status: previewv1.DocumentDBStatus{Status: "Running"}}
	if IsHealthy(notReady) {
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
