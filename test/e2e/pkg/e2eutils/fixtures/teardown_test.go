package fixtures

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

func newFakeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1: %v", err)
	}
	if err := previewv1.AddToScheme(s); err != nil {
		t.Fatalf("preview: %v", err)
	}
	return s
}

// TestTeardownFixtureByLabels_SelectsOnlyMatchingRun creates two sets
// of fixture objects belonging to different run ids and asserts
// teardownFixtureByLabels only removes those tagged with the current
// run id.
func TestTeardownFixtureByLabels_SelectsOnlyMatchingRun(t *testing.T) {
	resetRunIDForTest()
	SetRunID("runA")

	mineLabels := map[string]string{
		LabelRunID:   "runA",
		LabelFixture: FixtureSharedRO,
	}
	theirsLabels := map[string]string{
		LabelRunID:   "runB",
		LabelFixture: FixtureSharedRO,
	}

	mineNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "e2e-mine", Labels: mineLabels}}
	theirsNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "e2e-theirs", Labels: theirsLabels}}
	mineDD := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{
		Name: "dd-mine", Namespace: "e2e-mine", Labels: mineLabels,
	}}
	theirsDD := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{
		Name: "dd-theirs", Namespace: "e2e-theirs", Labels: theirsLabels,
	}}

	c := fake.NewClientBuilder().
		WithScheme(newFakeScheme(t)).
		WithObjects(mineNS, theirsNS, mineDD, theirsDD).
		Build()

	ctx := context.Background()
	if err := teardownFixtureByLabels(ctx, c, FixtureSharedRO); err != nil {
		t.Fatalf("teardown: %v", err)
	}

	// Mine should be gone.
	if err := c.Get(ctx, types.NamespacedName{Name: "e2e-mine"}, &corev1.Namespace{}); err == nil {
		t.Fatalf("expected mine namespace to be deleted")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "e2e-mine", Name: "dd-mine"}, &previewv1.DocumentDB{}); err == nil {
		t.Fatalf("expected mine documentdb to be deleted")
	}

	// Theirs must survive.
	if err := c.Get(ctx, types.NamespacedName{Name: "e2e-theirs"}, &corev1.Namespace{}); err != nil {
		t.Fatalf("theirs namespace should still exist: %v", err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "e2e-theirs", Name: "dd-theirs"}, &previewv1.DocumentDB{}); err != nil {
		t.Fatalf("theirs documentdb should still exist: %v", err)
	}
}

// TestCreateDocumentDB_RunIDMismatchIsExplicitError exercises the
// adoption-refusal path: when an existing CR has a different run-id
// label the helper must return a descriptive error instead of silently
// adopting a foreign fixture.
func TestCreateDocumentDB_RunIDMismatchIsExplicitError(t *testing.T) {
	resetRunIDForTest()
	SetRunID("newrun")

	existing := &previewv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared",
			Namespace: "ns",
			Labels: map[string]string{
				LabelRunID:   "oldrun",
				LabelFixture: FixtureSharedRO,
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(newFakeScheme(t)).
		WithObjects(existing).
		Build()

	attempt := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns"}}
	err := createDocumentDB(context.Background(), c, attempt, FixtureSharedRO)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "fixture collision") {
		t.Fatalf("expected 'fixture collision' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "oldrun") || !strings.Contains(err.Error(), "newrun") {
		t.Fatalf("error should name both run ids: %v", err)
	}
}

// TestCreateDocumentDB_AdoptsMatchingRun ensures that an AlreadyExists
// result with a matching run-id label is treated as idempotent success
// (this is the lazy-fixture re-entry path).
func TestCreateDocumentDB_AdoptsMatchingRun(t *testing.T) {
	resetRunIDForTest()
	SetRunID("runX")

	existing := &previewv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shared",
			Namespace: "ns",
			Labels: map[string]string{
				LabelRunID:   "runX",
				LabelFixture: FixtureSharedRO,
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(newFakeScheme(t)).
		WithObjects(existing).
		Build()

	attempt := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "ns"}}
	if err := createDocumentDB(context.Background(), c, attempt, FixtureSharedRO); err != nil {
		t.Fatalf("expected idempotent success, got %v", err)
	}
}

// TestEnsureNamespace_RunIDMismatchIsExplicitError mirrors the CR test
// for namespace-level collisions.
func TestEnsureNamespace_RunIDMismatchIsExplicitError(t *testing.T) {
	resetRunIDForTest()
	SetRunID("newrun")

	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ns",
			Labels: map[string]string{
				LabelRunID:   "oldrun",
				LabelFixture: FixtureSharedRO,
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(newFakeScheme(t)).
		WithObjects(existing).
		Build()

	err := ensureNamespace(context.Background(), c, "ns", FixtureSharedRO)
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "fixture collision") {
		t.Fatalf("want fixture collision, got: %v", err)
	}
}

// Silence unused-import warnings if client is otherwise unused.
var _ client.Client = (client.Client)(nil)
