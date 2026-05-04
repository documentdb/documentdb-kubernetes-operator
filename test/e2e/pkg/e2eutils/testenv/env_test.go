// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testenv

import (
	"context"
	"errors"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
)

func TestDefaultDocumentDBSchemeRegistersExpectedGroups(t *testing.T) {
	s, err := DefaultDocumentDBScheme()
	if err != nil {
		t.Fatalf("DefaultDocumentDBScheme: %v", err)
	}

	if !s.Recognizes(cnpgv1.SchemeGroupVersion.WithKind("Cluster")) {
		t.Errorf("expected scheme to recognize cnpg apiv1 Cluster")
	}
	if !s.Recognizes(previewv1.GroupVersion.WithKind("DocumentDB")) {
		t.Errorf("expected scheme to recognize DocumentDB preview group")
	}
	if !s.Recognizes(corev1.SchemeGroupVersion.WithKind("Pod")) {
		t.Errorf("expected scheme to recognize core/v1 Pod")
	}
}

func TestDefaultConstants(t *testing.T) {
	if DefaultOperatorNamespace == "" {
		t.Fatal("DefaultOperatorNamespace must not be empty")
	}
	if DefaultPostgresImage == "" {
		t.Fatal("DefaultPostgresImage must not be empty")
	}
}

// TestDetachEnvCtxSurvivesParentCancel pins the contract used by
// NewDocumentDBTestingEnvironment when storing the long-lived env.Ctx:
// the stored context MUST keep working after the parent is cancelled,
// because the parent in production is Ginkgo's SynchronizedBeforeSuite
// SpecContext, which is cancelled the moment BeforeSuite returns. A
// regression here surfaces in CI as "context canceled" at the first
// k8s call from any BeforeAll that reads env.Ctx (see the scale specs).
func TestDetachEnvCtxSurvivesParentCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	envCtx := detachEnvCtx(parent)

	cancel()

	if err := envCtx.Err(); err != nil {
		t.Fatalf("env ctx should be unaffected by parent cancellation, got %v", err)
	}
	select {
	case <-envCtx.Done():
		t.Fatal("env ctx Done() channel should not fire when parent is cancelled")
	default:
	}
}

// TestDetachEnvCtxIgnoresParentDeadline mirrors the cancellation case
// for deadline propagation. Ginkgo's SpecContext can carry both a
// cancel and a per-spec deadline; the long-lived env.Ctx must survive
// either, otherwise specs that out-run the per-spec deadline will see
// "context deadline exceeded" instead of their own timeout error.
func TestDetachEnvCtxIgnoresParentDeadline(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(1*time.Millisecond))
	defer cancel()

	envCtx := detachEnvCtx(parent)

	time.Sleep(20 * time.Millisecond)

	if _, ok := envCtx.Deadline(); ok {
		t.Fatal("env ctx must not inherit the parent deadline")
	}
	if err := envCtx.Err(); err != nil {
		t.Fatalf("env ctx should not have an Err after parent deadline elapses, got %v", err)
	}
	if errors.Is(parent.Err(), context.DeadlineExceeded) != true {
		t.Fatalf("parent should be deadline-exceeded for this test to be meaningful, got %v", parent.Err())
	}
}

// TestDetachEnvCtxPropagatesValues guards the second half of the
// detachEnvCtx contract: values placed on the parent (e.g. the things
// Ginkgo SpecContext may carry) must still be reachable through
// env.Ctx, even though cancellation is severed.
func TestDetachEnvCtxPropagatesValues(t *testing.T) {
	type k struct{}
	parent := context.WithValue(context.Background(), k{}, "v")
	envCtx := detachEnvCtx(parent)

	if got, _ := envCtx.Value(k{}).(string); got != "v" {
		t.Fatalf("env ctx must propagate parent values, got %q", got)
	}
}
