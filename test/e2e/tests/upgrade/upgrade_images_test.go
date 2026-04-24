package upgrade

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.mongodb.org/mongo-driver/v2/bson"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	e2emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/seed"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// DocumentDB upgrade — images: with the operator already running at
// the current version, patches the DocumentDB spec.documentDBImage
// (and spec.gatewayImage) from an old image tag to a new one and
// verifies the rollout completes + the seeded dataset is retained.
// Unlike upgrade_control_plane_test.go this does not touch the Helm
// release; it only exercises the CR-driven data-plane image upgrade
// path.
//
// Residual risk: the spec needs two image references (old/new). They
// come from E2E_UPGRADE_OLD_DOCUMENTDB_IMAGE /
// E2E_UPGRADE_NEW_DOCUMENTDB_IMAGE — there is no pinned default
// because the set of valid old→new pairs depends on the release being
// validated.
var _ = Describe("DocumentDB upgrade — images",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighLevelLabel,
	Serial, Ordered, func() {
		const (
			ddName   = "upgrade-img"
			dbName   = "upgrade_img"
			collName = "seed"
		)
		var (
			oldImage    string
			newImage    string
			oldGwImage  string
			newGwImage  string
			ctx         context.Context
			cancel      context.CancelFunc
		)

		BeforeAll(func() {
			skipUnlessUpgradeEnabled()
			oldImage = requireEnv(envOldDocumentDBImage,
				"DocumentDB image tag to start from (e.g. ghcr.io/microsoft/documentdb/documentdb:0.108.0)")
			newImage = requireEnv(envNewDocumentDBImage,
				"DocumentDB image tag to upgrade to (must be different from the old tag)")
			if oldImage == newImage {
				Skip("E2E_UPGRADE_OLD_DOCUMENTDB_IMAGE and E2E_UPGRADE_NEW_DOCUMENTDB_IMAGE are identical; nothing to upgrade")
			}
			// The gateway is an independent sidecar image; specs may
			// exercise a gateway upgrade alongside the extension
			// upgrade, or leave the gateway untouched. Both env vars
			// must either be set together or both left empty.
			oldGwImage = os.Getenv(envOldGatewayImage)
			newGwImage = os.Getenv(envNewGatewayImage)
			if (oldGwImage == "") != (newGwImage == "") {
				Fail(fmt.Sprintf("%s and %s must be set together (or both unset)",
					envOldGatewayImage, envNewGatewayImage))
			}
			if oldGwImage != "" && oldGwImage == newGwImage {
				Skip(envOldGatewayImage + " and " + envNewGatewayImage + " are identical; nothing to upgrade")
			}
		})

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.High)
			ctx, cancel = context.WithTimeout(context.Background(), imageRolloutTimeout)
			DeferCleanup(func() { cancel() })
		})

		It("rolls DocumentDB pods to a new image and retains data", func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized by SetupSuite")
			Expect(ctx).NotTo(BeNil(), "BeforeEach must have populated the spec context")
			c := env.Client

			By("creating a DocumentDB pinned to the old image")
			ns := namespaces.NamespaceForSpec(e2e.UpgradeLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns)

			vars := baseVars(ddName, ns, "2Gi")
			vars["DOCUMENTDB_IMAGE"] = oldImage
			if oldGwImage != "" {
				vars["GATEWAY_IMAGE"] = oldGwImage
			}

			dd, err := documentdb.Create(ctx, c, ns, ddName, documentdb.CreateOptions{
				Base:          "documentdb",
				Vars:          vars,
				ManifestsRoot: manifestsRoot(),
			})
			Expect(err).NotTo(HaveOccurred(), "create DocumentDB %s/%s", ns, ddName)
			DeferCleanup(func(ctx SpecContext) {
				_ = documentdb.Delete(ctx, c, dd, 3*time.Minute)
			})

			key := types.NamespacedName{Namespace: ns, Name: ddName}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed(), "DocumentDB did not reach Ready on oldImage=%s", oldImage)

			By("seeding data on the old image")
			docs := seed.SmallDataset()
			handle, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "connect to DocumentDB gateway on oldImage")
			inserted, err := e2emongo.Seed(ctx, handle.Client(), dbName, collName, docs)
			Expect(err).NotTo(HaveOccurred(), "seed %s.%s", dbName, collName)
			Expect(inserted).To(Equal(seed.SmallDatasetSize))
			Expect(handle.Close(ctx)).To(Succeed())

			By("patching spec.documentDBImage (and optionally gatewayImage) to the new image")
			fresh, err := documentdb.Get(ctx, c, key)
			Expect(err).NotTo(HaveOccurred(), "re-fetch DocumentDB before patch")
			Expect(documentdb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.DocumentDBImage = newImage
				if newGwImage != "" {
					s.GatewayImage = newGwImage
				}
			})).To(Succeed(), "patch DocumentDB image from %s to %s", oldImage, newImage)

			By("waiting for the CNPG-backed rollout to settle on the new image")
			// Poll the CR's backing pods until every container image
			// matches newImage. A transient all-pods-gone window is
			// acceptable during rollout, so we require at least one
			// pod AND zero pods still on oldImage.
			Eventually(func() error {
				return allPodsOnImage(ctx, c, ns, ddName, newImage)
			}, timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "pods did not roll to %s", newImage)

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBUpgrade),
				timeouts.PollInterval(timeouts.DocumentDBUpgrade),
			).Should(Succeed(), "DocumentDB did not reach Ready on newImage=%s", newImage)

			By("verifying data seeded before the upgrade is still reachable")
			handle2, err := e2emongo.NewFromDocumentDB(ctx, env, ns, ddName)
			Expect(err).NotTo(HaveOccurred(), "reconnect to DocumentDB gateway on newImage")
			DeferCleanup(func(ctx SpecContext) { _ = handle2.Close(ctx) })
			n, err := e2emongo.Count(ctx, handle2.Client(), dbName, collName, bson.M{})
			Expect(err).NotTo(HaveOccurred(), "count %s.%s on newImage", dbName, collName)
			Expect(n).To(Equal(int64(seed.SmallDatasetSize)),
				"seeded document count changed across image upgrade")
		})
	})

// allPodsOnImage returns nil when there is at least one Pod owned by
// the CNPG Cluster backing ddName and every container in every such
// Pod reports an image equal to want. The helper intentionally errs
// on the side of "not yet done" — missing pods, empty status, or any
// mismatch returns a non-nil error so Eventually keeps polling.
func allPodsOnImage(ctx context.Context, c client.Client, ns, ddName, want string) error {
	var pods corev1.PodList
	sel := labels.SelectorFromSet(labels.Set{"cnpg.io/cluster": ddName})
	if err := c.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods yet for cluster %s/%s", ns, ddName)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if len(p.Status.ContainerStatuses) == 0 {
			return fmt.Errorf("pod %s has no container statuses yet", p.Name)
		}
		for j := range p.Status.ContainerStatuses {
			got := p.Status.ContainerStatuses[j].Image
			// Container image strings can be reported by the kubelet in
			// resolved form (digest appended). Accept any image whose
			// reported tag contains the requested ref; this matches the
			// upgrade-verification semantics used in other areas.
			if got != want && !containsImageRef(got, want) {
				return fmt.Errorf("pod %s container %s image=%q, want %q",
					p.Name, p.Status.ContainerStatuses[j].Name, got, want)
			}
		}
	}
	return nil
}

// containsImageRef returns true when got references want either
// verbatim or as the repository:tag prefix of a digest-resolved form
// (e.g. "repo:tag@sha256:..."). Keeps the image-rollout assertion
// resilient to kubelets that report resolved digests.
func containsImageRef(got, want string) bool {
	if got == want {
		return true
	}
	if len(got) < len(want) {
		return false
	}
	return got[:len(want)] == want && (len(got) == len(want) || got[len(want)] == '@')
}
