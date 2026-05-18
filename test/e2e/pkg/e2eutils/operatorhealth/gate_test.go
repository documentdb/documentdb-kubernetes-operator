// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operatorhealth

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	return s
}

func newPod(uid, name string, restarts int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: DefaultNamespace,
			Labels:    map[string]string{PodLabelKey: PodLabelValue},
			UID:       types.UID(uid),
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "manager", RestartCount: restarts},
			},
		},
	}
}

func TestNewGateCapturesInitialState(t *testing.T) {
	// Reset sentinel between tests.
	operatorChurned.Store(false)

	s := newScheme(t)
	pod := newPod("uid-1", "documentdb-operator-abc", 0)
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()

	g, err := NewGate(context.Background(), c, DefaultNamespace)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if g.initialUID != "uid-1" || g.initialRestarts != 0 || g.initialPodName != pod.Name {
		t.Errorf("unexpected captured state: %+v", g)
	}
}

func TestCheckHealthyWhenUnchanged(t *testing.T) {
	operatorChurned.Store(false)

	s := newScheme(t)
	pod := newPod("uid-1", "p1", 0)
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()

	g, err := NewGate(context.Background(), c, DefaultNamespace)
	if err != nil {
		t.Fatal(err)
	}
	healthy, reason, err := g.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !healthy {
		t.Errorf("expected healthy, got reason=%q", reason)
	}
}

func TestCheckDetectsRestart(t *testing.T) {
	operatorChurned.Store(false)

	s := newScheme(t)
	pod := newPod("uid-1", "p1", 0)
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()

	g, err := NewGate(context.Background(), c, DefaultNamespace)
	if err != nil {
		t.Fatal(err)
	}
	// Bump restart count.
	pod.Status.ContainerStatuses[0].RestartCount = 2
	if err := c.Status().Update(context.Background(), pod); err != nil {
		t.Fatalf("update pod: %v", err)
	}

	healthy, reason, err := g.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if healthy {
		t.Error("expected unhealthy after restart count bump")
	}
	if reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestCheckDetectsPodReplacement(t *testing.T) {
	operatorChurned.Store(false)

	s := newScheme(t)
	pod := newPod("uid-1", "p1", 0)
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()

	g, err := NewGate(context.Background(), c, DefaultNamespace)
	if err != nil {
		t.Fatal(err)
	}

	// Replace the pod with a new UID/name.
	if err := c.Delete(context.Background(), pod); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	replacement := newPod("uid-2", "p2", 0)
	if err := c.Create(context.Background(), replacement); err != nil {
		t.Fatalf("create replacement: %v", err)
	}

	healthy, _, err := g.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if healthy {
		t.Error("expected unhealthy after pod replacement")
	}
}

func TestSentinelMarkAndHas(t *testing.T) {
	operatorChurned.Store(false)
	if HasChurned() {
		t.Fatal("expected sentinel clear")
	}
	MarkChurned()
	if !HasChurned() {
		t.Fatal("expected sentinel set")
	}
	// Reset for other tests.
	operatorChurned.Store(false)
}

func TestNewGateNoPods(t *testing.T) {
	operatorChurned.Store(false)
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()
	if _, err := NewGate(context.Background(), c, DefaultNamespace); err == nil {
		t.Fatal("expected error when no pods match")
	}
}
