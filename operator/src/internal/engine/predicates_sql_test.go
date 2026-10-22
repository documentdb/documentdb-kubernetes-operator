// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestClusterInstanceStatusChangedPredicate(t *testing.T) {
	p := ClusterInstanceStatusChangedPredicate()
	cluster := func(phase string, healthy ...string) *cnpgv1.Cluster {
		return &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{
			Phase:           phase,
			InstancesStatus: map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: healthy},
		}}
	}
	tests := []struct {
		name     string
		old, new client.Object
		want     bool
	}{
		{name: "unchanged", old: cluster("ok", "a"), new: cluster("ok", "a"), want: false},
		{name: "healthy instances changed", old: cluster("ok", "a"), new: cluster("ok", "a", "b"), want: true},
		{name: "phase changed", old: cluster("ok", "a"), new: cluster("failover", "a"), want: true},
		{name: "old is not a CNPG cluster", old: &corev1.Pod{}, new: cluster("ok"), want: true},
		{name: "new is not a CNPG cluster", old: cluster("ok"), new: &corev1.Pod{}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new}
			if got := p.Update(e); got != tt.want {
				t.Fatalf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServicePrefixPredicate(t *testing.T) {
	p := ServicePrefixPredicate("documentdb-service-")
	match := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "documentdb-service-db"}}
	other := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes"}}
	for _, tc := range []struct {
		svc  *corev1.Service
		want bool
	}{{match, true}, {other, false}} {
		if got := p.Create(event.CreateEvent{Object: tc.svc}); got != tc.want {
			t.Errorf("Create(%s) = %v", tc.svc.Name, got)
		}
		if got := p.Update(event.UpdateEvent{ObjectOld: other, ObjectNew: tc.svc}); got != tc.want {
			t.Errorf("Update(%s) = %v", tc.svc.Name, got)
		}
		if got := p.Delete(event.DeleteEvent{Object: tc.svc}); got != tc.want {
			t.Errorf("Delete(%s) = %v", tc.svc.Name, got)
		}
		if got := p.Generic(event.GenericEvent{Object: tc.svc}); got != tc.want {
			t.Errorf("Generic(%s) = %v", tc.svc.Name, got)
		}
	}
}

func TestPodSQLExecutorMissingPrimaryPod(t *testing.T) {
	s := newTestScheme(t)
	exec := NewPodSQLExecutor(newFakeClient(t, s, nil), nil, nil)
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: testNamespace}, Status: cnpgv1.ClusterStatus{CurrentPrimary: "db-1"}}
	if _, err := exec(context.Background(), cluster, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "failed to get primary pod") {
		t.Fatalf("expected primary pod lookup error, got %v", err)
	}
}

func TestPodSQLExecutorExecFailureTargetsPrimaryPod(t *testing.T) {
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		http.Error(w, "exec forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	config := &rest.Config{Host: server.URL}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	s := newTestScheme(t)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db-1", Namespace: testNamespace}}
	exec := NewPodSQLExecutor(newFakeClient(t, s, nil, pod), clientset, config)
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: testNamespace}, Status: cnpgv1.ClusterStatus{CurrentPrimary: "db-1"}}

	if _, err := exec(context.Background(), cluster, "SELECT 1"); err == nil || !strings.Contains(err.Error(), "failed to execute command") {
		t.Fatalf("expected exec failure, got %v", err)
	}
	if gotPath != "/api/v1/namespaces/default/pods/db-1/exec" {
		t.Fatalf("expected exec on the primary pod, got %q", gotPath)
	}
	for _, want := range []string{"container=postgres", "command=psql", "command=SELECT+1"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("expected %q in exec query %q", want, gotQuery)
		}
	}
}
