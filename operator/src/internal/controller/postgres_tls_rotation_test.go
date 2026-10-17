package controller

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("PostgreSQL TLS rotation", func() {
	It("changes the connection generation only when referenced public TLS material changes", func() {
		ctx := context.Background()
		cert := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "client", Namespace: "db"},
			Data:       map[string][]byte{"tls.crt": []byte("cert-1"), "tls.key": []byte("key-1")},
		}
		ca := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "ca", Namespace: "db"},
			Data:       map[string][]byte{"ca.crt": []byte("ca-1")},
		}
		r := buildDocumentDBReconciler(cert, ca)
		first, err := r.postgresTLSGeneration(ctx, "db", "client", "ca")
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(HavePrefix("documentdb-tls-"))
		cert.Labels = map[string]string{"unrelated": "change"}
		cert.Data["tls.key"] = []byte("key-2")
		Expect(r.Update(ctx, cert)).To(Succeed())
		same, err := r.postgresTLSGeneration(ctx, "db", "client", "ca")
		Expect(err).NotTo(HaveOccurred())
		Expect(same).To(Equal(first), "private key material and metadata must not enter the public connection token")
		cert.Data["tls.crt"] = []byte("cert-2")
		Expect(r.Update(ctx, cert)).To(Succeed())
		rotated, err := r.postgresTLSGeneration(ctx, "db", "client", "ca")
		Expect(err).NotTo(HaveOccurred())
		Expect(rotated).NotTo(Equal(first))
		ca.Data["ca.crt"] = []byte("ca-2")
		Expect(r.Update(ctx, ca)).To(Succeed())
		newTrust, err := r.postgresTLSGeneration(ctx, "db", "client", "ca")
		Expect(err).NotTo(HaveOccurred())
		Expect(newTrust).NotTo(Equal(rotated))
		withoutCA, err := r.postgresTLSGeneration(ctx, "db", "client", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(withoutCA).NotTo(Equal(newTrust))
		_, err = r.postgresTLSGeneration(ctx, "another-namespace", "client", "ca")
		Expect(err).To(HaveOccurred())
		delete(cert.Data, "tls.crt")
		Expect(r.Update(ctx, cert)).To(Succeed())
		_, err = r.postgresTLSGeneration(ctx, "db", "client", "ca")
		Expect(err).To(MatchError(ContainSubstring("missing tls.crt")))
	})

	It("reconciles only replicated DocumentDBs that reference the changed Secret in its namespace", func() {
		ctx := context.Background()
		dd := baseDocumentDB("replicated", "db")
		dd.Spec.ClusterReplication = &dbpreview.ClusterReplication{Primary: "replicated"}
		dd.Spec.TLS = &dbpreview.TLSConfiguration{Postgres: &cnpgv1.CertificatesConfiguration{
			ReplicationTLSSecret: "client", ServerCASecret: "server-ca", ClientCASecret: "client-ca",
		}}
		otherNamespace := dd.DeepCopy()
		otherNamespace.Namespace = "other"
		single := dd.DeepCopy()
		single.Name = "standalone"
		single.Spec.ClusterReplication = nil
		noPostgres := dd.DeepCopy()
		noPostgres.Name = "gateway-only"
		noPostgres.Spec.TLS.Postgres = nil
		noTLS := dd.DeepCopy()
		noTLS.Name = "no-tls"
		noTLS.Spec.TLS = nil
		r := buildDocumentDBReconciler(dd, otherNamespace, single, noPostgres, noTLS)
		expected := []reconcile.Request{{NamespacedName: types.NamespacedName{Name: dd.Name, Namespace: dd.Namespace}}}
		for _, name := range []string{"client", "server-ca"} {
			Expect(r.documentDBsForPostgresTLSSecret(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "db"},
			})).To(Equal(expected))
		}
		for _, name := range []string{"unrelated", "client-ca"} {
			Expect(r.documentDBsForPostgresTLSSecret(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "db"},
			})).To(BeEmpty())
		}
	})
})
