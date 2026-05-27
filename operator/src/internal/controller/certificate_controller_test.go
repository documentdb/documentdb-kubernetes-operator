// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"testing"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	v1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// helper to build TLS reconciler with objects
func buildCertificateReconciler(t *testing.T, objs ...runtime.Object) *CertificateReconciler {
	scheme := runtime.NewScheme()
	require.NoError(t, dbpreview.AddToScheme(scheme))
	require.NoError(t, cmapi.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithRuntimeObjects(objs...)
		clientObjs := make([]client.Object, 0, len(objs))
		for _, obj := range objs {
			if co, ok := obj.(client.Object); ok {
				clientObjs = append(clientObjs, co)
			}
		}
		if len(clientObjs) > 0 {
			builder = builder.WithStatusSubresource(clientObjs...)
		}
	}
	c := builder.Build()
	return &CertificateReconciler{Client: c, Scheme: scheme}
}

func baseDocumentDB(name, ns string) *dbpreview.DocumentDB {
	return &dbpreview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: dbpreview.DocumentDBSpec{
			NodeCount:        1,
			InstancesPerNode: 1,
			Resource:         dbpreview.Resource{Storage: dbpreview.StorageConfiguration{PvcSize: "1Gi"}},
			Image:            &dbpreview.ImageSpec{DocumentDB: "test-image"},
			ExposeViaService: dbpreview.ExposeViaService{ServiceType: "ClusterIP"},
		},
	}
}

func TestEnsureProvidedSecret(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-prov", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: "Provided", Provided: &dbpreview.ProvidedTLS{SecretName: "mycert"}}}
	// Secret missing first
	r := buildCertificateReconciler(t, ddb)
	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)
	require.False(t, ddb.Status.TLS.Ready, "Should not be ready until secret exists")

	// Create secret with required keys then reconcile again
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "mycert", Namespace: "default"}, Data: map[string][]byte{"tls.crt": []byte("crt"), "tls.key": []byte("key")}}
	require.NoError(t, r.Client.Create(ctx, secret))
	res, err = r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Zero(t, res.RequeueAfter)
	require.True(t, ddb.Status.TLS.Ready, "Provided secret should mark TLS ready")
	require.Equal(t, "mycert", ddb.Status.TLS.SecretName)
}

func TestEnsureCertManagerManagedCert(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-cm", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: "CertManager", CertManager: &dbpreview.CertManagerTLS{IssuerRef: dbpreview.IssuerRef{Name: "test-issuer", Kind: "Issuer"}, DNSNames: []string{"custom.example"}}}}
	ddb.Status.TLS = &dbpreview.TLSStatus{}
	issuer := &cmapi.Issuer{ObjectMeta: metav1.ObjectMeta{Name: "test-issuer", Namespace: "default"}, Spec: cmapi.IssuerSpec{IssuerConfig: cmapi.IssuerConfig{SelfSigned: &cmapi.SelfSignedIssuer{}}}}
	r := buildCertificateReconciler(t, ddb, issuer)

	// Call certificate ensure twice to mimic reconcile loops
	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)
	res, err = r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	cert := &cmapi.Certificate{}
	// fetch certificate (self-created by reconcile). If not found, run reconcile again once.
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-cm-gateway-cert", Namespace: "default"}, cert))
	// Debug: list all certificates to ensure store functioning
	certList := &cmapi.CertificateList{}
	_ = r.Client.List(ctx, certList)
	for _, c := range certList.Items {
		t.Logf("Found certificate: %s/%s secret=%s", c.Namespace, c.Name, c.Spec.SecretName)
	}
	require.Contains(t, cert.Spec.DNSNames, "custom.example")
	// Should include service DNS names
	serviceBase := util.DOCUMENTDB_SERVICE_PREFIX + ddb.Name
	require.Contains(t, cert.Spec.DNSNames, serviceBase)

	// Simulate readiness condition then invoke ensure again (mimic reconcile loop)
	cert.Status.Conditions = append(cert.Status.Conditions, cmapi.CertificateCondition{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue, LastTransitionTime: &metav1.Time{Time: time.Now()}})
	require.NoError(t, r.Client.Update(ctx, cert))
	res, err = r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Zero(t, res.RequeueAfter)
	require.True(t, ddb.Status.TLS.Ready, "Cert-manager managed cert should mark ready after condition true")
	require.NotEmpty(t, ddb.Status.TLS.SecretName)
}

func TestEnsureSelfSignedCert(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-ss", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: "SelfSigned"}}
	ddb.Status.TLS = &dbpreview.TLSStatus{}
	r := buildCertificateReconciler(t, ddb)

	// First call should create issuer and certificate
	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	// Certificate should exist
	cert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-ss-gateway-cert", Namespace: "default"}, cert))

	// Simulate ready condition and call again
	cert.Status.Conditions = append(cert.Status.Conditions, cmapi.CertificateCondition{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue, LastTransitionTime: &metav1.Time{Time: time.Now()}})
	require.NoError(t, r.Client.Update(ctx, cert))
	res, err = r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Zero(t, res.RequeueAfter)
	require.True(t, ddb.Status.TLS.Ready)
	require.NotEmpty(t, ddb.Status.TLS.SecretName)
}

func TestEnsurePostgresCertificatesDefaults(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-pg", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{
		Postgres: &v1.CertificatesConfiguration{},
	}
	r := buildCertificateReconciler(t, ddb)

	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	issuer := &cmapi.Issuer{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-postgres-selfsigned", Namespace: "default"}, issuer))
	require.NotNil(t, issuer.Spec.SelfSigned)

	serverIssuer := &cmapi.Issuer{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-postgres-server-issuer", Namespace: "default"}, serverIssuer))
	require.NotNil(t, serverIssuer.Spec.CA)
	require.Equal(t, "ddb-pg-postgres-ca", serverIssuer.Spec.CA.SecretName)

	clientIssuer := &cmapi.Issuer{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-postgres-client-issuer", Namespace: "default"}, clientIssuer))
	require.NotNil(t, clientIssuer.Spec.CA)
	require.Equal(t, "ddb-pg-postgres-ca", clientIssuer.Spec.CA.SecretName)

	caCert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-postgres-ca", Namespace: "default"}, caCert))
	require.Equal(t, "ddb-pg-postgres-ca", caCert.Spec.SecretName)
	require.True(t, caCert.Spec.IsCA)
	require.Contains(t, caCert.Spec.Usages, cmapi.UsageCertSign)
	require.Contains(t, caCert.Spec.Usages, cmapi.UsageCRLSign)

	serverCert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-postgres-server", Namespace: "default"}, serverCert))
	require.Equal(t, "ddb-pg-postgres-server", serverCert.Spec.SecretName)
	require.Equal(t, "ddb-pg-postgres-server-issuer", serverCert.Spec.IssuerRef.Name)
	require.Contains(t, serverCert.Spec.DNSNames, "ddb-pg-rw.default.svc")
	require.Contains(t, serverCert.Spec.Usages, cmapi.UsageServerAuth)

	replicationCert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-postgres-replication", Namespace: "default"}, replicationCert))
	require.Equal(t, "ddb-pg-postgres-replication", replicationCert.Spec.SecretName)
	require.Equal(t, "ddb-pg-postgres-client-issuer", replicationCert.Spec.IssuerRef.Name)
	require.Equal(t, "streaming_replica", replicationCert.Spec.CommonName)
	require.Contains(t, replicationCert.Spec.Usages, cmapi.UsageClientAuth)
}

func TestEnsurePostgresCertificatesWhenPostgresTLSIsPresent(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-pg-provided", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{
		Postgres: &v1.CertificatesConfiguration{},
	}
	r := buildCertificateReconciler(t, ddb)

	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	replicationCert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-provided-postgres-replication", Namespace: "default"}, replicationCert))
	require.Equal(t, "ddb-pg-provided-postgres-replication", replicationCert.Spec.SecretName)
}

func TestEnsurePostgresCertificatesOmittedSkipsPostgresResources(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-pg-omitted", "default")
	r := buildCertificateReconciler(t, ddb)

	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	postgresIssuer := &cmapi.Issuer{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-omitted-postgres-selfsigned", Namespace: "default"}, postgresIssuer)
	require.True(t, errors.IsNotFound(err))

	postgresCert := &cmapi.Certificate{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-omitted-postgres-ca", Namespace: "default"}, postgresCert)
	require.True(t, errors.IsNotFound(err))

	gatewayIssuer := &cmapi.Issuer{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-pg-omitted-gateway-selfsigned", Namespace: "default"}, gatewayIssuer))
}

// TestEmptyModeDefaultsToSelfSigned verifies that when mode is empty,
// the controller treats it as SelfSigned to ensure TLS is always enabled.
// This is a security fix - see https://github.com/documentdb/documentdb-kubernetes-operator/issues/356
func TestEmptyModeDefaultsToSelfSigned(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-empty-mode", "default")
	// Empty mode should default to SelfSigned behavior
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: ""}}
	ddb.Status.TLS = &dbpreview.TLSStatus{}
	r := buildCertificateReconciler(t, ddb)

	// First call should create issuer and certificate (SelfSigned behavior)
	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	// Certificate should exist, proving SelfSigned was used as default
	cert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-empty-mode-gateway-cert", Namespace: "default"}, cert))

	// Simulate ready condition and verify TLS becomes ready
	cert.Status.Conditions = append(cert.Status.Conditions, cmapi.CertificateCondition{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue, LastTransitionTime: &metav1.Time{Time: time.Now()}})
	require.NoError(t, r.Client.Update(ctx, cert))
	res, err = r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Zero(t, res.RequeueAfter)
	require.True(t, ddb.Status.TLS.Ready, "TLS should be ready with empty mode defaulting to SelfSigned")
	require.NotEmpty(t, ddb.Status.TLS.SecretName)
}

// TestEmptyModeWithNilStatus verifies empty mode defaults to SelfSigned
// even when Status.TLS is nil (fresh resource).
func TestEmptyModeWithNilStatus(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-empty-nil", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: ""}}
	// Status.TLS is nil - fresh resource
	ddb.Status.TLS = nil
	r := buildCertificateReconciler(t, ddb)

	// Should default to SelfSigned and create certificate
	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	// Certificate should exist
	cert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-empty-nil-gateway-cert", Namespace: "default"}, cert))
}

// TestNilTLSDefaultsToSelfSigned verifies that when the entire spec.tls block
// is omitted, the controller still provisions a SelfSigned cert so the gateway
// never serves plaintext (issue #356).
func TestNilTLSDefaultsToSelfSigned(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-nil-tls", "default")
	ddb.Spec.TLS = nil
	ddb.Status.TLS = nil
	r := buildCertificateReconciler(t, ddb)

	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	cert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-nil-tls-gateway-cert", Namespace: "default"}, cert))
}

// TestNilGatewayDefaultsToSelfSigned verifies that when spec.tls is set but
// spec.tls.gateway is nil, the controller still provisions a SelfSigned cert.
func TestNilGatewayDefaultsToSelfSigned(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-nil-gateway", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: nil}
	ddb.Status.TLS = nil
	r := buildCertificateReconciler(t, ddb)

	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	cert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-nil-gateway-gateway-cert", Namespace: "default"}, cert))
}

// TestDisabledModeFailsClosed verifies that legacy "Disabled" mode (which can
// linger in etcd from pre-#357 releases) fails closed by provisioning a
// SelfSigned cert, preserving the no-plaintext invariant from issue #356.
// CRD enum validation rejects "Disabled" on new applies; this guards stored objects.
func TestDisabledModeFailsClosed(t *testing.T) {
	ctx := context.Background()
	ddb := baseDocumentDB("ddb-disabled", "default")
	ddb.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: "Disabled"}}
	ddb.Status.TLS = &dbpreview.TLSStatus{}
	r := buildCertificateReconciler(t, ddb)

	// Even for invalid legacy values, the controller should fail closed and
	// provision certificate material rather than taking no action.
	res, err := r.reconcileCertificates(ctx, ddb)
	require.NoError(t, err)
	require.Equal(t, RequeueAfterShort, res.RequeueAfter)

	cert := &cmapi.Certificate{}
	require.NoError(t, r.Client.Get(ctx, types.NamespacedName{Name: "ddb-disabled-gateway-cert", Namespace: "default"}, cert))
}
