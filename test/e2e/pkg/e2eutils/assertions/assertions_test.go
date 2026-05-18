package assertions

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	preview "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1.AddToScheme: %v", err)
	}
	if err := preview.AddToScheme(s); err != nil {
		t.Fatalf("preview.AddToScheme: %v", err)
	}
	if err := cnpgv1.AddToScheme(s); err != nil {
		t.Fatalf("cnpgv1.AddToScheme: %v", err)
	}
	return s
}

func TestAssertDocumentDBReady(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	dd := &preview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "ns"},
		Status:     preview.DocumentDBStatus{Status: "Cluster in healthy state"},
	}
	notReady := &preview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "db2", Namespace: "ns"},
		Status:     preview.DocumentDBStatus{Status: "Setting up primary"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dd, notReady).Build()

	if err := AssertDocumentDBReady(context.Background(), c, client.ObjectKey{Namespace: "ns", Name: "db1"})(); err != nil {
		t.Fatalf("expected ready, got err=%v", err)
	}
	if err := AssertDocumentDBReady(context.Background(), c, client.ObjectKey{Namespace: "ns", Name: "db2"})(); err == nil {
		t.Fatalf("expected not-ready error")
	}
	if err := AssertDocumentDBReady(context.Background(), c, client.ObjectKey{Namespace: "ns", Name: "missing"})(); err == nil {
		t.Fatalf("expected error for missing object")
	}
}

func TestAssertInstanceCount(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	dd := &preview.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"}}
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"},
		Status:     cnpgv1.ClusterStatus{ReadyInstances: 3},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dd, cluster).Build()
	key := client.ObjectKey{Namespace: "ns", Name: "db"}

	if err := AssertInstanceCount(context.Background(), c, key, 3)(); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
	if err := AssertInstanceCount(context.Background(), c, key, 2)(); err == nil {
		t.Fatalf("want mismatch error")
	}
}

func TestAssertPVCCount(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	pvcs := []client.Object{
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "p1", Namespace: "ns", Labels: map[string]string{"app": "dd"}}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "p2", Namespace: "ns", Labels: map[string]string{"app": "dd"}}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "p3", Namespace: "ns", Labels: map[string]string{"app": "other"}}},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pvcs...).Build()

	if err := AssertPVCCount(context.Background(), c, "ns", "app=dd", 2)(); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
	if err := AssertPVCCount(context.Background(), c, "ns", "app=dd", 3)(); err == nil {
		t.Fatalf("want mismatch error")
	}
	// Malformed selector surfaces on every call.
	if err := AssertPVCCount(context.Background(), c, "ns", "!!bad!!", 0)(); err == nil {
		t.Fatalf("want parse error")
	}
}

func TestAssertTLSSecretReady(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	good := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "ns"},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("c"), corev1.TLSPrivateKeyKey: []byte("k")},
	}
	missingKey := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("c")},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(good, missingKey).Build()
	if err := AssertTLSSecretReady(context.Background(), c, "ns", "g")(); err != nil {
		t.Fatalf("good: %v", err)
	}
	if err := AssertTLSSecretReady(context.Background(), c, "ns", "b")(); err == nil {
		t.Fatalf("want error for missing key")
	}
	err := AssertTLSSecretReady(context.Background(), c, "ns", "none")()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want not-found error, got %v", err)
	}
}

func TestAssertServiceType(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(svc).Build()
	if err := AssertServiceType(context.Background(), c, "ns", "svc", corev1.ServiceTypeLoadBalancer)(); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
	if err := AssertServiceType(context.Background(), c, "ns", "svc", corev1.ServiceTypeClusterIP)(); err == nil {
		t.Fatalf("want mismatch")
	}
}

func TestAssertConnectionStringMatches(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	dd := &preview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"},
		Status:     preview.DocumentDBStatus{ConnectionString: "mongodb://user:pw@svc:10260/?tls=true"},
	}
	empty := &preview.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "ns"}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(dd, empty).Build()
	k := client.ObjectKey{Namespace: "ns", Name: "db"}

	if err := AssertConnectionStringMatches(context.Background(), c, k, `^mongodb://.*tls=true`)(); err != nil {
		t.Fatalf("want ok, got %v", err)
	}
	if err := AssertConnectionStringMatches(context.Background(), c, k, `tls=false`)(); err == nil {
		t.Fatalf("want mismatch")
	}
	if err := AssertConnectionStringMatches(context.Background(), c,
		client.ObjectKey{Namespace: "ns", Name: "empty"}, `.*`)(); err == nil {
		t.Fatalf("want empty-string error")
	}
	// Bad regex must surface.
	if err := AssertConnectionStringMatches(context.Background(), c, k, `[unclosed`)(); err == nil {
		t.Fatalf("want regex compile error")
	}
}
