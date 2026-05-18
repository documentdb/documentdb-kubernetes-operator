package lifecycle

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB lifecycle — delete with Retain reclaim",
	Label(e2e.LifecycleLabel, e2e.DestructiveLabel), e2e.MediumLevelLabel,
	func() {
		const name = "lifecycle-delete-retain"
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

		It("preserves the underlying PersistentVolume after the CR is deleted", func() {
			vars := baseVars("1Gi")
			dd, err := documentdb.Create(ctx, c, ns, name, documentdb.CreateOptions{
				Base:   "documentdb",
				Mixins: []string{"reclaim_retain"},
				Vars:   vars,
			})
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{Namespace: ns, Name: name}
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			// Capture the PV names currently bound to this
			// namespace's PVCs so we can verify they survive
			// DocumentDB deletion.
			var pvcs corev1.PersistentVolumeClaimList
			Expect(c.List(ctx, &pvcs, client.InNamespace(ns))).To(Succeed())
			Expect(pvcs.Items).ToNot(BeEmpty(), "expected at least one PVC after Ready")
			var pvNames []string
			for i := range pvcs.Items {
				if v := pvcs.Items[i].Spec.VolumeName; v != "" {
					pvNames = append(pvNames, v)
				}
			}
			Expect(pvNames).ToNot(BeEmpty(), "expected bound PVs; got only pending PVCs")

			// Delete the DocumentDB and wait for it to disappear.
			Expect(documentdb.Delete(ctx, c, dd, 3*time.Minute)).To(Succeed())

			// Retained PVs should remain in the API server; their
			// phase transitions to Released (or stays Bound briefly)
			// but the object itself must not be collected.
			for _, pvName := range pvNames {
				var pv corev1.PersistentVolume
				Eventually(func() error {
					return c.Get(ctx, types.NamespacedName{Name: pvName}, &pv)
				}, 2*time.Minute, 5*time.Second).Should(Succeed(),
					"PV %s should still exist under Retain policy", pvName)
				Expect(pv.Spec.PersistentVolumeReclaimPolicy).To(
					Equal(corev1.PersistentVolumeReclaimRetain),
					"PV %s must have reclaimPolicy=Retain", pvName)
			}

			// Manual cleanup: retained PVs will otherwise leak across
			// test runs. Deleting them releases the underlying
			// provisioner storage in kind's local-path driver.
			DeferCleanup(func(ctx SpecContext) {
				for _, pvName := range pvNames {
					_ = c.Delete(ctx, &corev1.PersistentVolume{
						ObjectMeta: metav1.ObjectMeta{Name: pvName},
					})
				}
			})
		})
	})
