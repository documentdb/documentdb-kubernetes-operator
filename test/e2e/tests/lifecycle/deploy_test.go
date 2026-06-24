package lifecycle

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	mongohelper "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	sharedmongo "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

var _ = Describe("DocumentDB lifecycle — deploy",
	Label(e2e.LifecycleLabel, e2e.BasicLabel, e2e.SmokeLabel), e2e.MediumLevelLabel,
	func() {
		const name = "lifecycle-deploy"
		var (
			ctx context.Context
			ns  string
			c   client.Client
		)

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			ns = namespaces.NamespaceForSpec(e2e.LifecycleLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns, "documentdb-credentials")
		})

		It("brings a 1-instance cluster to Ready and wires owner refs on the backing CNPG Cluster", func() {
			dd, err := documentdb.Create(ctx, c, ns, name, documentdb.CreateOptions{
				Base: "documentdb",
				Vars: baseVars("1Gi"),
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = shareddb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: name}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			// CNPG Cluster backing this DocumentDB exists and has an
			// owner reference back to the DocumentDB CR — mirrors
			// what docs/designs/e2e-test-suite.md calls for. The
			// Cluster name equals the DocumentDB name for single-
			// cluster deployments (see assertions.clusterNameFor).
			var cluster cnpgv1.Cluster
			Eventually(func() error {
				return c.Get(ctx, key, &cluster)
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			current := getDD(ctx, ns, name)
			Expect(cluster.OwnerReferences).ToNot(BeEmpty(),
				"CNPG Cluster should be owned by the DocumentDB CR")
			var found bool
			for _, o := range cluster.OwnerReferences {
				if o.UID == current.UID && o.Kind == "DocumentDB" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(),
				"expected owner reference with UID=%s on CNPG Cluster %s", current.UID, key)

			// Data-plane smoke: opening a mongo-driver connection
			// against the freshly-deployed CR proves the gateway
			// actually answers on the wire. Without this step,
			// "Ready=true" alone can mask a broken gateway sidecar
			// (e.g. wrong image, misconfigured credentials secret).
			// NewFromDocumentDB pings internally before returning,
			// so the explicit Ping below is belt-and-braces at the
			// test boundary — keeping it here makes the failure
			// narrative clear without readers chasing helper code.
			h, err := mongohelper.NewFromDocumentDB(ctx, e2e.SuiteEnv(), ns, name)
			Expect(err).ToNot(HaveOccurred(), "connect mongo to freshly-deployed DocumentDB")
			DeferCleanup(func(ctx SpecContext) { _ = h.Close(ctx) })
			Expect(sharedmongo.Ping(ctx, h.Client())).To(Succeed(),
				"ping freshly-deployed DocumentDB gateway")

			// PSA "restricted" guard (regression test for #387): the
			// namespace is labeled pod-security.kubernetes.io/enforce=
			// restricted by the fixture, so reaching Ready already
			// proves the injected sidecars passed admission. This
			// explicit assertion turns an otherwise opaque CNPG
			// pod-creation failure into a precise message naming the
			// offending container and field.
			var pods corev1.PodList
			Expect(c.List(ctx, &pods,
				client.InNamespace(ns),
				client.MatchingLabels{"cnpg.io/cluster": name})).To(Succeed())
			Expect(pods.Items).ToNot(BeEmpty(), "expected CNPG pods for cluster %s", name)
			for i := range pods.Items {
				for j := range pods.Items[i].Spec.Containers {
					ctr := pods.Items[i].Spec.Containers[j]
					if ctr.Name != "documentdb-gateway" && ctr.Name != "otel-collector" {
						continue
					}
					sc := ctr.SecurityContext
					Expect(sc).ToNot(BeNil(),
						"container %q in pod %q must set a securityContext", ctr.Name, pods.Items[i].Name)
					Expect(sc.RunAsNonRoot).To(HaveValue(BeTrue()),
						"container %q must set runAsNonRoot=true", ctr.Name)
					Expect(sc.AllowPrivilegeEscalation).To(HaveValue(BeFalse()),
						"container %q must set allowPrivilegeEscalation=false", ctr.Name)
					Expect(sc.Capabilities).ToNot(BeNil(),
						"container %q must drop ALL capabilities", ctr.Name)
					Expect(sc.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")),
						"container %q must drop ALL capabilities", ctr.Name)
					Expect(sc.SeccompProfile).ToNot(BeNil(),
						"container %q must set a seccompProfile", ctr.Name)
					Expect(sc.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault),
						"container %q must set seccompProfile=RuntimeDefault", ctr.Name)
				}
			}
		})
	})
