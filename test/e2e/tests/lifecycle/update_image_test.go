package lifecycle

import (
	"context"
	"os"
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

// The design doc calls the field `spec.documentDbVersion`; the CRD at
// operator/src/api/preview/documentdb_types.go names it DocumentDBVersion
// (JSON `documentDBVersion`) and also exposes DocumentDBImage / GatewayImage
// which take precedence when set. Because the base template provides
// DocumentDBImage (not Version), we exercise the rollout via the image
// field and assert against Status.DocumentDBImage — Phase 3 follow-up to
// parameterise this once the Version-only path is wired into manifests.
var _ = Describe("DocumentDB lifecycle — update documentDBImage",
	Label(e2e.LifecycleLabel, e2e.DisruptiveLabel), e2e.MediumLevelLabel,
	func() {
		const name = "lifecycle-update-image"
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

		It("rolls out a new image tag and reflects it in Status.DocumentDBImage", func() {
			vars := baseVars("1Gi")
			startImage := vars["DOCUMENTDB_IMAGE"]
			if startImage == "" {
				Skip("DOCUMENTDB_IMAGE env var must be set for the image-update spec — " +
					"it needs an explicit starting tag to roll off of. Set DOCUMENTDB_IMAGE " +
					"and optionally E2E_DOCUMENTDB_IMAGE_NEXT to exercise this path.")
			}
			// The target image override must be an explicit
			// different tag; without it the patch would be a no-op
			// (same image as startImage) and the Eventually below
			// would trivially pass, producing a false positive.
			targetImage := os.Getenv("E2E_DOCUMENTDB_IMAGE_NEXT")
			if targetImage == "" || targetImage == startImage {
				Skip("E2E_DOCUMENTDB_IMAGE_NEXT must be set to a different image than " +
					"DOCUMENTDB_IMAGE to exercise a real rollout — skipping to avoid a no-op.")
			}

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

			// Refetch for a fresh resourceVersion before patching.
			fresh := getDD(ctx, ns, name)
			Expect(documentdb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.DocumentDBImage = targetImage
			})).To(Succeed())

			Eventually(func() string {
				current := getDD(ctx, ns, name)
				return current.Status.DocumentDBImage
			}, timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Equal(targetImage))

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed())
		})
	})
