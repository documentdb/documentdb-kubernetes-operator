// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"slices"
	"testing"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/product"
)

type certReconciler = CertificateReconciler[*dbv1.DocumentDB]

var certKey = ctrl.Request{NamespacedName: types.NamespacedName{Name: "db", Namespace: testNamespace}}

func newCertReconciler(c client.Client, s *runtime.Scheme) *certReconciler {
	return &certReconciler{Client: c, Scheme: s, Adapter: product.DocumentDBAdapter{}}
}

func clusterWithTLS(gw *dbv1.GatewayTLS) *dbv1.DocumentDB {
	db := testCluster()
	if gw != nil {
		db.Spec.TLS = &dbv1.TLSConfiguration{Gateway: gw}
	}
	return db
}

func getTLSStatus(t *testing.T, c client.Client) *dbv1.TLSStatus {
	t.Helper()
	db := &dbv1.DocumentDB{}
	if err := c.Get(context.Background(), certKey.NamespacedName, db); err != nil {
		t.Fatal(err)
	}
	if db.Status.TLS == nil {
		t.Fatal("expected TLS status to be initialized")
	}
	return db.Status.TLS
}

func readyCert(name, secret string) *cmapi.Certificate {
	return &cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       cmapi.CertificateSpec{SecretName: secret},
		Status: cmapi.CertificateStatus{Conditions: []cmapi.CertificateCondition{
			{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue},
		}},
	}
}

func TestCertificateReconcileNotFoundAndGetError(t *testing.T) {
	s := newTestScheme(t)
	r := newCertReconciler(newFakeClient(t, s, nil), s)
	if res, err := r.Reconcile(context.Background(), certKey); err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected no-op for missing cluster, got %v, %v", res, err)
	}
	c := newFakeClient(t, s, &interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return errInjected
	}})
	if _, err := newCertReconciler(c, s).Reconcile(context.Background(), certKey); err != errInjected {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestCertificateSelfSignedLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name string
		gw   *dbv1.GatewayTLS
	}{
		{name: "tls unset defaults to self-signed"},
		{name: "empty mode defaults to self-signed", gw: &dbv1.GatewayTLS{}},
		{name: "legacy mode falls back to self-signed", gw: &dbv1.GatewayTLS{Mode: "Disabled"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			c := newFakeClient(t, s, nil, clusterWithTLS(tt.gw))
			r := newCertReconciler(c, s)

			res, err := r.Reconcile(context.Background(), certKey)
			if err != nil || res.RequeueAfter != RequeueAfterShort {
				t.Fatalf("expected short requeue while creating, got %v, %v", res, err)
			}
			issuer := &cmapi.Issuer{}
			if err := c.Get(context.Background(), types.NamespacedName{Name: "db-gateway-selfsigned", Namespace: testNamespace}, issuer); err != nil {
				t.Fatalf("expected self-signed issuer: %v", err)
			}
			if issuer.Spec.SelfSigned == nil || metav1.GetControllerOf(issuer) == nil {
				t.Fatalf("expected owned self-signed issuer, got %+v", issuer)
			}
			cert := &cmapi.Certificate{}
			if err := c.Get(context.Background(), types.NamespacedName{Name: "db-gateway-cert", Namespace: testNamespace}, cert); err != nil {
				t.Fatalf("expected certificate: %v", err)
			}
			svc := product.DocumentDBProfile().Naming.ServicePrefix + "db"
			if cert.Spec.SecretName != "db-gateway-cert-tls" || cert.Spec.IssuerRef.Name != "db-gateway-selfsigned" ||
				!slices.Equal(cert.Spec.DNSNames, []string{svc, svc + ".default", svc + ".default.svc"}) {
				t.Fatalf("unexpected certificate spec: %+v", cert.Spec)
			}
			if st := getTLSStatus(t, c); st.Ready || st.Message != "Creating self-signed certificate" || st.SecretName != "db-gateway-cert-tls" {
				t.Fatalf("unexpected TLS status: %+v", st)
			}

			// Not ready yet: waiting.
			if res, err := r.Reconcile(context.Background(), certKey); err != nil || res.RequeueAfter != RequeueAfterShort {
				t.Fatalf("expected requeue while waiting, got %v, %v", res, err)
			}
			if st := getTLSStatus(t, c); st.Ready || st.Message != "Waiting for gateway TLS certificate to become ready" {
				t.Fatalf("unexpected TLS status: %+v", st)
			}

			// Ready: status flips to ready and no requeue.
			cert.Status.Conditions = []cmapi.CertificateCondition{{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue}}
			if err := c.Update(context.Background(), cert); err != nil {
				t.Fatal(err)
			}
			if res, err := r.Reconcile(context.Background(), certKey); err != nil || res != (ctrl.Result{}) {
				t.Fatalf("expected no requeue when ready, got %v, %v", res, err)
			}
			if st := getTLSStatus(t, c); !st.Ready || st.Message != "Gateway TLS certificate ready" {
				t.Fatalf("unexpected TLS status: %+v", st)
			}
			// Already ready: idempotent.
			if res, err := r.Reconcile(context.Background(), certKey); err != nil || res != (ctrl.Result{}) {
				t.Fatalf("expected idempotent ready reconcile, got %v, %v", res, err)
			}
		})
	}
}

func TestCertificateProvidedSecret(t *testing.T) {
	secret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: testNamespace}, Data: data}
	}
	provided := &dbv1.GatewayTLS{Mode: "Provided", Provided: &dbv1.ProvidedTLS{SecretName: "my-tls"}}
	tests := []struct {
		name        string
		gw          *dbv1.GatewayTLS
		secret      *corev1.Secret
		wantRequeue bool
		wantReady   bool
		wantMessage string
	}{
		{name: "secret name missing", gw: &dbv1.GatewayTLS{Mode: "Provided"}, wantMessage: "Provided TLS secret name missing"},
		{name: "secret absent", gw: provided, wantRequeue: true, wantMessage: "Waiting for provided TLS secret"},
		{name: "missing tls.crt", gw: provided, secret: secret(map[string][]byte{"tls.key": []byte("k")}), wantRequeue: true, wantMessage: "Provided secret missing tls.crt"},
		{name: "missing tls.key", gw: provided, secret: secret(map[string][]byte{"tls.crt": []byte("c")}), wantRequeue: true, wantMessage: "Provided secret missing tls.key"},
		{name: "complete secret", gw: provided, secret: secret(map[string][]byte{"tls.crt": []byte("c"), "tls.key": []byte("k")}), wantReady: true, wantMessage: "Using provided TLS secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			objs := []client.Object{clusterWithTLS(tt.gw)}
			if tt.secret != nil {
				objs = append(objs, tt.secret)
			}
			c := newFakeClient(t, s, nil, objs...)
			res, err := newCertReconciler(c, s).Reconcile(context.Background(), certKey)
			if err != nil {
				t.Fatal(err)
			}
			if (res.RequeueAfter == RequeueAfterShort) != tt.wantRequeue {
				t.Fatalf("unexpected requeue %v", res)
			}
			st := getTLSStatus(t, c)
			if st.Ready != tt.wantReady || st.Message != tt.wantMessage {
				t.Fatalf("unexpected TLS status: %+v", st)
			}
			if tt.gw.Provided != nil && st.SecretName != "my-tls" {
				t.Fatalf("expected provided secret name in status, got %q", st.SecretName)
			}
		})
	}
}

func TestCertificateCertManagerMode(t *testing.T) {
	t.Run("configuration missing", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, clusterWithTLS(&dbv1.GatewayTLS{Mode: "CertManager"}))
		if res, err := newCertReconciler(c, s).Reconcile(context.Background(), certKey); err != nil || res != (ctrl.Result{}) {
			t.Fatalf("expected no requeue, got %v, %v", res, err)
		}
		if st := getTLSStatus(t, c); st.Ready || st.Message != "CertManager configuration missing" {
			t.Fatalf("unexpected TLS status: %+v", st)
		}
	})

	t.Run("defaults issuer kind, group and secret and merges DNS names", func(t *testing.T) {
		s := newTestScheme(t)
		gw := &dbv1.GatewayTLS{Mode: "CertManager", CertManager: &dbv1.CertManagerTLS{
			IssuerRef: dbv1.IssuerRef{Name: "ca"},
			DNSNames:  []string{"db.example.com", "", "db.example.com"},
		}}
		c := newFakeClient(t, s, nil, clusterWithTLS(gw))
		r := newCertReconciler(c, s)
		if res, err := r.Reconcile(context.Background(), certKey); err != nil || res.RequeueAfter != RequeueAfterShort {
			t.Fatalf("expected requeue after creating, got %v, %v", res, err)
		}
		cert := &cmapi.Certificate{}
		if err := c.Get(context.Background(), types.NamespacedName{Name: "db-gateway-cert", Namespace: testNamespace}, cert); err != nil {
			t.Fatal(err)
		}
		svc := product.DocumentDBProfile().Naming.ServicePrefix + "db"
		if cert.Spec.IssuerRef != (cmmeta.ObjectReference{Name: "ca", Kind: "Issuer", Group: "cert-manager.io"}) ||
			cert.Spec.SecretName != "db-gateway-cert-tls" ||
			!slices.Equal(cert.Spec.DNSNames, []string{"db.example.com", svc, svc + ".default", svc + ".default.svc"}) {
			t.Fatalf("unexpected certificate spec: %+v", cert.Spec)
		}
		if st := getTLSStatus(t, c); st.Ready || st.Message != "Creating cert-manager certificate" {
			t.Fatalf("unexpected TLS status: %+v", st)
		}
		if _, err := r.Reconcile(context.Background(), certKey); err != nil {
			t.Fatal(err)
		}
		if st := getTLSStatus(t, c); st.Ready || st.Message != "Waiting for cert-manager certificate to become ready" {
			t.Fatalf("unexpected TLS status: %+v", st)
		}
	})

	t.Run("honors explicit issuer and reports readiness", func(t *testing.T) {
		s := newTestScheme(t)
		gw := &dbv1.GatewayTLS{Mode: "CertManager", CertManager: &dbv1.CertManagerTLS{
			IssuerRef:  dbv1.IssuerRef{Name: "ca", Kind: "ClusterIssuer", Group: "example.io"},
			SecretName: "custom-tls",
		}}
		c := newFakeClient(t, s, nil, clusterWithTLS(gw))
		r := newCertReconciler(c, s)
		if _, err := r.Reconcile(context.Background(), certKey); err != nil {
			t.Fatal(err)
		}
		cert := &cmapi.Certificate{}
		if err := c.Get(context.Background(), types.NamespacedName{Name: "db-gateway-cert", Namespace: testNamespace}, cert); err != nil {
			t.Fatal(err)
		}
		if cert.Spec.IssuerRef.Kind != "ClusterIssuer" || cert.Spec.IssuerRef.Group != "example.io" || cert.Spec.SecretName != "custom-tls" {
			t.Fatalf("explicit issuer settings not honored: %+v", cert.Spec)
		}
		cert.Status.Conditions = []cmapi.CertificateCondition{{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue}}
		if err := c.Update(context.Background(), cert); err != nil {
			t.Fatal(err)
		}
		if res, err := r.Reconcile(context.Background(), certKey); err != nil || res != (ctrl.Result{}) {
			t.Fatalf("expected ready without requeue, got %v, %v", res, err)
		}
		if st := getTLSStatus(t, c); !st.Ready || st.SecretName != "custom-tls" || st.Message != "Gateway TLS certificate ready (cert-manager)" {
			t.Fatalf("unexpected TLS status: %+v", st)
		}
	})
}

func TestCertificateClientErrors(t *testing.T) {
	isKind := func(obj client.Object, kind string) bool {
		switch obj.(type) {
		case *cmapi.Certificate:
			return kind == "cert"
		case *cmapi.Issuer:
			return kind == "issuer"
		case *corev1.Secret:
			return kind == "secret"
		}
		return false
	}
	getErr := func(kind string) interceptor.Funcs {
		return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if isKind(obj, kind) {
				return errInjected
			}
			return c.Get(ctx, key, obj, opts...)
		}}
	}
	createErr := func(kind string) interceptor.Funcs {
		return interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if isKind(obj, kind) {
				return errInjected
			}
			return c.Create(ctx, obj, opts...)
		}}
	}
	statusErr := interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
		return errInjected
	}}
	certManager := &dbv1.GatewayTLS{Mode: "CertManager", CertManager: &dbv1.CertManagerTLS{IssuerRef: dbv1.IssuerRef{Name: "ca"}}}
	provided := &dbv1.GatewayTLS{Mode: "Provided", Provided: &dbv1.ProvidedTLS{SecretName: "my-tls"}}
	initialized := &dbv1.TLSStatus{}

	tests := []struct {
		name   string
		gw     *dbv1.GatewayTLS
		status *dbv1.TLSStatus
		extra  []client.Object
		funcs  interceptor.Funcs
	}{
		{name: "initial status update", funcs: statusErr},
		{name: "self-signed issuer get", funcs: getErr("issuer")},
		{name: "self-signed issuer create", funcs: createErr("issuer")},
		{name: "self-signed cert get", funcs: getErr("cert")},
		{name: "self-signed cert create", funcs: createErr("cert")},
		{name: "self-signed creating status", status: initialized, funcs: statusErr},
		{name: "self-signed waiting status", status: initialized, funcs: statusErr, extra: []client.Object{&cmapi.Certificate{ObjectMeta: metav1.ObjectMeta{Name: "db-gateway-cert", Namespace: testNamespace}}}},
		{name: "self-signed ready status", status: initialized, funcs: statusErr, extra: []client.Object{readyCert("db-gateway-cert", "s")}},
		{name: "cert-manager missing config status", gw: &dbv1.GatewayTLS{Mode: "CertManager"}, status: initialized, funcs: statusErr},
		{name: "cert-manager cert get", gw: certManager, funcs: getErr("cert")},
		{name: "cert-manager cert create", gw: certManager, funcs: createErr("cert")},
		{name: "cert-manager creating status", gw: certManager, status: initialized, funcs: statusErr},
		{name: "cert-manager waiting status", gw: certManager, status: initialized, funcs: statusErr, extra: []client.Object{&cmapi.Certificate{ObjectMeta: metav1.ObjectMeta{Name: "db-gateway-cert", Namespace: testNamespace}}}},
		{name: "cert-manager ready status", gw: certManager, status: initialized, funcs: statusErr, extra: []client.Object{readyCert("db-gateway-cert", "s")}},
		{name: "provided missing name status", gw: &dbv1.GatewayTLS{Mode: "Provided"}, status: initialized, funcs: statusErr},
		{name: "provided secret get", gw: provided, funcs: getErr("secret")},
		{name: "provided secret absent status", gw: provided, status: initialized, funcs: statusErr},
		{name: "provided missing crt status", gw: provided, status: initialized, funcs: statusErr, extra: []client.Object{&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: testNamespace}}}},
		{name: "provided missing key status", gw: provided, status: initialized, funcs: statusErr, extra: []client.Object{&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: testNamespace}, Data: map[string][]byte{"tls.crt": nil}}}},
		{name: "provided ready status", gw: provided, status: initialized, funcs: statusErr, extra: []client.Object{&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: testNamespace}, Data: map[string][]byte{"tls.crt": nil, "tls.key": nil}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := clusterWithTLS(tt.gw)
			db.Status.TLS = tt.status
			c := newFakeClient(t, s, &tt.funcs, append(tt.extra, db)...)
			res, err := newCertReconciler(c, s).Reconcile(context.Background(), certKey)
			if err != errInjected {
				t.Fatalf("expected injected error, got %v (result %v)", err, res)
			}
		})
	}
}

func TestCertificateOwnerReferenceErrors(t *testing.T) {
	for _, tt := range []struct {
		name  string
		gw    *dbv1.GatewayTLS
		extra []client.Object
	}{
		{name: "self-signed issuer"},
		{name: "self-signed certificate", extra: []client.Object{&cmapi.Issuer{ObjectMeta: metav1.ObjectMeta{Name: "db-gateway-selfsigned", Namespace: testNamespace}}}},
		{name: "cert-manager certificate", gw: &dbv1.GatewayTLS{Mode: "CertManager", CertManager: &dbv1.CertManagerTLS{IssuerRef: dbv1.IssuerRef{Name: "ca"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			c := newFakeClient(t, s, nil, append(tt.extra, clusterWithTLS(tt.gw))...)
			// The reconciler scheme lacks the DocumentDB kind, so owner
			// references cannot be set and nothing is created.
			r := newCertReconciler(c, runtime.NewScheme())
			if _, err := r.Reconcile(context.Background(), certKey); err == nil {
				t.Fatal("expected owner reference error")
			}
			err := c.Get(context.Background(), types.NamespacedName{Name: "db-gateway-cert", Namespace: testNamespace}, &cmapi.Certificate{})
			if !apierrors.IsNotFound(err) {
				t.Fatalf("certificate must not be created without an owner, got %v", err)
			}
		})
	}
}
