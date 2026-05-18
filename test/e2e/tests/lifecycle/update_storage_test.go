package lifecycle

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/clusterprobe"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// The CRD nests storage as Spec.Resource.Storage.PvcSize (see
// operator/src/api/preview/documentdb_types.go). The design doc wording
// "spec.resource or spec.persistentVolumeClaim" is ambiguous — the real
// field is `spec.resource.storage.pvcSize`, patched below.
var _ = Describe("DocumentDB lifecycle — update storage.pvcSize",
	Label(e2e.LifecycleLabel, e2e.DisruptiveLabel, e2e.NeedsCSIResizeLabel),
	e2e.MediumLevelLabel,
	func() {
		const name = "lifecycle-update-storage"
		var (
			ctx context.Context
			ns  string
			c   client.Client
		)

		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			ctx = context.Background()
			c = e2e.SuiteEnv().Client
			// Runtime capability probe: PVC resize silently falls over
			// on StorageClasses without AllowVolumeExpansion=true. The
			// NeedsCSIResizeLabel only gates invocation; this probe
			// gives a clear Skip when the backing class cannot expand.
			scName := baseVars("1Gi")["STORAGE_CLASS"]
			canExpand, err := clusterprobe.StorageClassAllowsExpansion(ctx, c, scName)
			Expect(err).NotTo(HaveOccurred(), "probe StorageClass %q expansion", scName)
			if !canExpand {
				Skip("StorageClass " + scName + " does not allow volume expansion — skipping PVC resize spec")
			}
			ns = namespaces.NamespaceForSpec(e2e.LifecycleLabel)
			createNamespace(ctx, c, ns)
			createCredentialSecret(ctx, c, ns, "documentdb-credentials")
		})

		It("expands PVCs from 1Gi to 2Gi without rotating the primary", func() {
			dd, err := documentdb.Create(ctx, c, ns, name, documentdb.CreateOptions{
				Base: "documentdb",
				Vars: baseVars("1Gi"),
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

			// Patch the storage size.
			fresh := getDD(ctx, ns, name)
			Expect(documentdb.PatchSpec(ctx, c, fresh, func(s *previewv1.DocumentDBSpec) {
				s.Resource.Storage.PvcSize = "2Gi"
			})).To(Succeed())

			// PVC capacity should eventually be updated across all
			// backing claims. List PVCs in the namespace; in kind
			// with a single-instance cluster there is one data PVC.
			want := resource.MustParse("2Gi")
			Eventually(func() error {
				var pvcs corev1.PersistentVolumeClaimList
				if err := c.List(ctx, &pvcs, client.InNamespace(ns)); err != nil {
					return err
				}
				if len(pvcs.Items) == 0 {
					return errPendingPVCs
				}
				for i := range pvcs.Items {
					got := pvcs.Items[i].Status.Capacity[corev1.ResourceStorage]
					if got.Cmp(want) < 0 {
						return errNotExpanded
					}
				}
				return nil
			}, timeouts.For(timeouts.PVCResize),
				timeouts.PollInterval(timeouts.PVCResize),
			).Should(Succeed())

			// Cluster still healthy after the resize.
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				1*time.Minute, 5*time.Second,
			).Should(Succeed())
		})
	})
