package tls

import (
	"bytes"
	"context"
	"crypto/x509"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/documentdb/documentdb-operator/test/e2e"
	ddbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	mongohelper "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// CertManager mode delegates certificate issuance to cert-manager via
// an IssuerRef on the DocumentDB CR. This spec creates a minimal
// self-signed Issuer in the test namespace, points the CR at it, and
// verifies the gateway serves a TLS connection that validates against
// the CA material cert-manager stored in the issued Secret. This
// matters because InsecureSkipVerify would mask missing CA wiring; the
// real invariant the operator promises is "the secret named in
// status.tls.secretName contains a chain that the gateway serves".
//
// The spec is skipped automatically when cert-manager is not installed
// on the target cluster, detected by the absence of the Issuer CRD.
var _ = Describe("DocumentDB TLS — cert-manager",
	Label(e2e.TLSLabel, e2e.NeedsCertManagerLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("provisions certificates through a cert-manager Issuer", func(sctx SpecContext) {
			ctx, cancel := context.WithTimeout(sctx, 10*time.Minute)
			defer cancel()

			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "suite env not initialised")

			skipIfCertManagerMissing(ctx)

			// Pre-create the namespace and a self-signed Issuer in it
			// so the gateway reconcile can resolve the IssuerRef on
			// its first pass. provisionCluster treats the namespace
			// as idempotent and reuses it.
			nsName := namespaces.NamespaceForSpec(e2e.TLSLabel)
			Expect(createIdempotent(ctx, env.Client,
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}})).
				To(Succeed(), "create namespace %s", nsName)

			issuerName := "tls-e2e-selfsigned"
			issuer := &unstructured.Unstructured{}
			issuer.SetGroupVersionKind(schema.GroupVersionKind{
				Group: "cert-manager.io", Version: "v1", Kind: "Issuer",
			})
			issuer.SetName(issuerName)
			issuer.SetNamespace(nsName)
			// spec.selfSigned is an empty object per cert-manager schema.
			Expect(unstructured.SetNestedMap(issuer.Object, map[string]any{},
				"spec", "selfSigned")).To(Succeed(), "set spec.selfSigned")
			Expect(createIdempotent(ctx, env.Client, issuer)).
				To(Succeed(), "create selfSigned Issuer")

			cluster := provisionCluster(ctx, env.Client, e2e.TLSLabel,
				"tls_certmanager", map[string]string{
					"ISSUER_NAME": issuerName,
					"ISSUER_KIND": "Issuer",
				})
			Expect(cluster.NamespaceName).To(Equal(nsName),
				"provisionCluster must reuse the pre-created namespace")

			key := types.NamespacedName{Namespace: cluster.NamespaceName, Name: cluster.DD.Name}
			var tlsSecretName string
			Eventually(func(g Gomega) bool {
				dd, err := ddbutil.Get(ctx, env.Client, key)
				g.Expect(err).NotTo(HaveOccurred())
				if dd.Status.TLS == nil {
					return false
				}
				tlsSecretName = dd.Status.TLS.SecretName
				return dd.Status.TLS.Ready
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).
				Should(BeTrue(), "status.tls.ready did not flip true with cert-manager issuer")
			Expect(tlsSecretName).NotTo(BeEmpty(),
				"status.tls.secretName must be populated once ready")

			// Read the cert-manager-issued secret and extract the CA
			// (ca.crt for self-signed issuer; fall back to tls.crt
			// when the issuer didn't populate ca.crt because the
			// self-signed issuer doubles as its own CA).
			caPEM := readCAFromSecret(ctx, cluster.NamespaceName, tlsSecretName)

			host, port, stop := openGatewayForward(ctx, cluster.DD)
			defer stop()

			connectCtx, cancelConnect := context.WithTimeout(ctx, timeouts.For(timeouts.MongoConnect))
			defer cancelConnect()

			pool := x509.NewCertPool()
			Expect(pool.AppendCertsFromPEM(caPEM)).
				To(BeTrue(), "parse CA PEM from cert-manager secret")

			// The gateway certificate is issued for the Service DNS
			// name; override SNI to match one of its SANs so
			// hostname-verification through the 127.0.0.1 forward
			// succeeds. Keep the primary Service FQDN matching
			// mixins/tls_certmanager issue.
			sni := "documentdb-service-" + tlsDocumentDBName + "." + cluster.NamespaceName + ".svc"

			client, err := mongohelper.NewClient(connectCtx, mongohelper.ClientOptions{
				Host:       host,
				Port:       port,
				User:       tlsCredentialUser,
				Password:   tlsCredentialPassword,
				TLS:        true,
				RootCAs:    pool,
				ServerName: sni,
			})
			Expect(err).NotTo(HaveOccurred(), "TLS connect via cert-manager issuer")
			defer func() { _ = client.Disconnect(ctx) }()

			Eventually(func() error {
				return mongohelper.Ping(connectCtx, client)
			}, timeouts.For(timeouts.MongoConnect), timeouts.PollInterval(timeouts.MongoConnect)).
				Should(Succeed(), "ping via cert-manager-issued cert should succeed with CA verification")

			// --- Renewal check ---
			// Force cert-manager to re-issue the Certificate by
			// deleting the generated Secret; cert-manager recreates
			// it with a fresh tls.crt. With the self-signed Issuer
			// used here, a new leaf + new CA are produced on every
			// issuance, so the old CA pool will NOT validate the
			// new leaf — proving the gateway actually picked up the
			// reissued material. If the gateway pinned the initial
			// cert in memory, this ping would fail with a bad
			// certificate error.
			By("forcing cert-manager to reissue the gateway Secret")
			origSec := &corev1.Secret{}
			Expect(env.Client.Get(ctx, types.NamespacedName{
				Namespace: cluster.NamespaceName, Name: tlsSecretName,
			}, origSec)).To(Succeed(), "read TLS secret before deletion")
			origCrt := bytes.Clone(origSec.Data[corev1.TLSCertKey])
			Expect(env.Client.Delete(ctx, origSec)).To(Succeed(),
				"delete TLS secret to trigger cert-manager renewal")

			// Wait for cert-manager to recreate the secret with a
			// different tls.crt. Using the DocumentDBReady budget
			// here because cert-manager reissue latency is dominated
			// by issuer controller scheduling, not mongo connect.
			By("waiting for cert-manager to reissue the TLS Secret with a new tls.crt")
			Eventually(func(g Gomega) {
				sec := &corev1.Secret{}
				g.Expect(env.Client.Get(ctx, types.NamespacedName{
					Namespace: cluster.NamespaceName, Name: tlsSecretName,
				}, sec)).To(Succeed())
				g.Expect(sec.Data[corev1.TLSCertKey]).NotTo(BeEmpty(),
					"reissued secret must carry tls.crt")
				g.Expect(sec.Data[corev1.TLSCertKey]).NotTo(Equal(origCrt),
					"tls.crt must differ after reissue")
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).
				Should(Succeed(), "cert-manager did not reissue TLS secret")

			// Reconnect with the NEW CA and ping; Eventually gives
			// the gateway a window to notice the remounted cert.
			// Each Eventually attempt gets its own bounded context so
			// the per-attempt budget does not collapse across retries
			// — otherwise the first iteration's NewClient could burn
			// the whole MongoConnect window, leaving no time for the
			// gateway to actually pick up the reissued material.
			By("reconnecting via the renewed CA and pinging through the gateway")
			newCA := readCAFromSecret(ctx, cluster.NamespaceName, tlsSecretName)
			newPool := x509.NewCertPool()
			Expect(newPool.AppendCertsFromPEM(newCA)).
				To(BeTrue(), "parse renewed CA PEM")

			Eventually(func(g Gomega) {
				attemptCtx, cancelAttempt := context.WithTimeout(ctx, timeouts.For(timeouts.MongoConnect))
				defer cancelAttempt()
				client2, err := mongohelper.NewClient(attemptCtx, mongohelper.ClientOptions{
					Host:       host,
					Port:       port,
					User:       tlsCredentialUser,
					Password:   tlsCredentialPassword,
					TLS:        true,
					RootCAs:    newPool,
					ServerName: sni,
				})
				g.Expect(err).NotTo(HaveOccurred(), "reconnect with renewed CA")
				defer func() { _ = client2.Disconnect(attemptCtx) }()
				g.Expect(mongohelper.Ping(attemptCtx, client2)).To(Succeed(),
					"ping via renewed cert should succeed")
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.MongoConnect)).
				Should(Succeed(), "gateway did not start serving the renewed cert (or reconnect kept failing)")
		})
	},
)

// readCAFromSecret fetches the issued TLS secret and returns the CA
// bundle bytes. Cert-manager's self-signed Issuer populates ca.crt;
// some issuer types leave it empty and rely on tls.crt being a
// self-contained self-signed leaf, so we fall back to tls.crt when
// ca.crt is missing or empty.
func readCAFromSecret(ctx context.Context, ns, name string) []byte {
	GinkgoHelper()
	env := e2e.SuiteEnv()
	sec := &corev1.Secret{}
	Expect(env.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, sec)).
		To(Succeed(), "get issued TLS secret %s/%s", ns, name)
	if ca := sec.Data[corev1.ServiceAccountRootCAKey]; len(ca) > 0 {
		return ca
	}
	if crt := sec.Data[corev1.TLSCertKey]; len(crt) > 0 {
		return crt
	}
	Fail("issued TLS secret " + ns + "/" + name + " contains neither ca.crt nor tls.crt")
	return nil
}

// skipIfCertManagerMissing probes for the cert-manager Issuer CRD via
// a no-op List on the v1 kind and calls Skip when the resource is not
// registered. Using a discovery-driven List avoids pulling in the
// apiextensions client for a single check.
func skipIfCertManagerMissing(ctx context.Context) {
	GinkgoHelper()
	env := e2e.SuiteEnv()
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "cert-manager.io", Version: "v1", Kind: "IssuerList",
	})
	err := env.Client.List(ctx, list)
	if err == nil {
		return
	}
	// apimeta.IsNoMatchError matches the REST-mapper error when the
	// CRD is not registered; NotFound covers servers that return 404
	// on the discovery round-trip.
	if apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		Skip("cert-manager is not installed on the target cluster")
	}
	Expect(err).NotTo(HaveOccurred(), "unexpected error probing for cert-manager")
}
