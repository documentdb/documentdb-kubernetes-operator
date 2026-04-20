package lifecycle

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB lifecycle — update logLevel",
	Label(e2e.LifecycleLabel, e2e.BasicLabel), e2e.MediumLevelLabel,
	func() {
		const name = "lifecycle-update-loglevel"
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

		It("propagates a spec.logLevel patch to the live CR", func() {
			vars := baseVars("1Gi")
			vars["LOG_LEVEL"] = "info"

			dd, err := documentdb.Create(ctx, c, ns, name, documentdb.CreateOptions{
				Base: "documentdb",
				Vars: vars,
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: name}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			// Patch spec.logLevel; field is exported verbatim as
			// LogLevel in api/preview/documentdb_types.go.
			fresh := getDD(ctx, ns, name)
			Expect(fresh.Spec.LogLevel).To(Equal("info"))
			Expect(documentdb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.LogLevel = "debug"
			})).To(Succeed())

			Eventually(func() string {
				current := getDD(ctx, ns, name)
				return current.Spec.LogLevel
			}, 1*time.Minute, 2*time.Second).Should(Equal("debug"),
				"patched spec.logLevel should reach the API server")

			// Reconciliation should not disrupt readiness while the
			// only change is a log-level string.
			Consistently(assertions.AssertDocumentDBReady(ctx, c, key),
				30*time.Second, 5*time.Second,
			).Should(Succeed())
		})
	})
