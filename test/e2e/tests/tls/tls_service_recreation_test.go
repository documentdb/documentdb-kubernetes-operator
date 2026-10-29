package tls

import (
	"context"
	"crypto/x509"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/test/e2e"
	ddbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/portforward"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	sharedmongo "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

var _ = Describe("DocumentDB TLS — legacy Service recovery",
	Label(e2e.TLSLabel, e2e.NeedsCertManagerLabel), e2e.MediumLevelLabel,
	func() {
		It("keeps the recorded long-name endpoint and strict TLS after Service recreation", func(sctx SpecContext) {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx, cancel := context.WithTimeout(sctx, 15*time.Minute)
			defer cancel()
			env := e2e.SuiteEnv()
			skipIfCertManagerMissing(ctx)
			ns := namespaces.NamespaceForSpec(e2e.TLSLabel)
			Expect(createIdempotent(ctx, env.Client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})).To(Succeed())
			DeferCleanup(func(ctx SpecContext) {
				_ = env.Client.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
			})
			Expect(createIdempotent(ctx, env.Client, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: tlsCredentialSecret, Namespace: ns},
				Data:       map[string][]byte{"username": []byte(tlsCredentialUser), "password": []byte(tlsCredentialPassword)},
			})).To(Succeed())
			name := strings.Repeat("a", 44) + "-east"
			raw, err := ddbutil.RenderCR("documentdb", name, ns, nil, map[string]string{
				"STORAGE_SIZE":      "1Gi",
				"STORAGE_CLASS":     envDefault("E2E_STORAGE_CLASS", tlsDefaultStorageCls),
				"DOCUMENTDB_IMAGE":  envDefault("DOCUMENTDB_IMAGE", tlsDefaultDDBImage),
				"GATEWAY_IMAGE":     envDefault("GATEWAY_IMAGE", tlsDefaultGatewayImage),
				"CREDENTIAL_SECRET": tlsCredentialSecret,
				"INSTANCES":         "1",
				"EXPOSURE_TYPE":     "ClusterIP",
				"LOG_LEVEL":         "info",
			}, "")
			Expect(err).NotTo(HaveOccurred())
			db := &dbv1.DocumentDB{}
			Expect(yaml.Unmarshal(raw, db)).To(Succeed())
			legacy := (portforward.ServiceNamePrefix + name)[:63]
			db.Annotations = map[string]string{portforward.ServiceNameAnnotation: legacy}
			Expect(env.Client.Create(ctx, db)).To(Succeed())
			key := types.NamespacedName{Name: name, Namespace: ns}
			Expect(shareddb.WaitHealthy(ctx, env.Client, key, timeouts.For(timeouts.DocumentDBReady))).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(env.Client.Get(ctx, key, db)).To(Succeed())
				g.Expect(db.Status.TLS).NotTo(BeNil())
				g.Expect(db.Status.TLS.Ready).To(BeTrue())
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).Should(Succeed())

			ping := func() {
				GinkgoHelper()
				pool := x509.NewCertPool()
				Expect(pool.AppendCertsFromPEM(readCAFromSecret(ctx, ns, db.Status.TLS.SecretName))).To(BeTrue())
				host, port, stop := openGatewayForward(ctx, db)
				defer stop()
				client, err := sharedmongo.NewClient(ctx, sharedmongo.ClientOptions{
					Host: host, Port: port, User: tlsCredentialUser, Password: tlsCredentialPassword,
					TLS: true, RootCAs: pool, ServerName: legacy + "." + ns + ".svc",
				})
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = client.Disconnect(ctx) }()
				Eventually(func() error {
					attempt, done := context.WithTimeout(ctx, 10*time.Second)
					defer done()
					return sharedmongo.Ping(attempt, client)
				}, timeouts.For(timeouts.MongoConnect), timeouts.PollInterval(timeouts.MongoConnect)).Should(Succeed())
			}
			ping()
			svc := &corev1.Service{}
			serviceKey := types.NamespacedName{Name: legacy, Namespace: ns}
			Expect(env.Client.Get(ctx, serviceKey, svc)).To(Succeed())
			oldUID := svc.UID
			Expect(env.Client.Delete(ctx, svc)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(env.Client.Get(ctx, serviceKey, svc)).To(Succeed())
				g.Expect(svc.UID).NotTo(Equal(oldUID))
				g.Expect(svc.Spec.Selector["app"]).To(Equal(name))
			}, timeouts.For(timeouts.DocumentDBReady), timeouts.PollInterval(timeouts.DocumentDBReady)).Should(Succeed())
			ping()
			Expect(env.Client.Get(ctx, key, db)).To(Succeed())
			Expect(db.Annotations[portforward.ServiceNameAnnotation]).To(Equal(legacy))
		})
	})
