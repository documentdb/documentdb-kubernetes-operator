package status

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
)

// DocumentDB persistent volume — CRD discrepancy note.
//
// The task brief asked the spec to inspect `status.persistentVolumeClaim`
// but the `DocumentDBStatus` type in operator/src/api/preview/documentdb_types.go
// does not expose such a field. The authoritative ownership of a
// DocumentDB's data volumes sits on the backing CloudNative-PG Cluster,
// which labels each PVC with `cnpg.io/cluster=<name>`.
//
// We therefore verify the operator's observable contract by listing
// PersistentVolumeClaims in the DocumentDB's namespace filtered by that
// CNPG label and asserting:
//   - at least one PVC exists (one per Postgres instance);
//   - every returned PVC has reached phase Bound.
//
// If `status.persistentVolumeClaim` is added to the CRD in the future,
// this spec should grow an additional assertion that correlates the
// status field with the live PVC list.
var _ = Describe("DocumentDB persistent volume — CNPG PVC discovery",
	Label(e2e.StatusLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("provisions Bound PVCs labelled with cnpg.io/cluster", func() {
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil())
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			DeferCleanup(cancel)

			handle, err := fixtures.GetOrCreateSharedRO(ctx, c)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() error {
				pvcList := &corev1.PersistentVolumeClaimList{}
				if err := c.List(ctx, pvcList,
					client.InNamespace(handle.Namespace()),
					client.MatchingLabels{"cnpg.io/cluster": handle.Name()},
				); err != nil {
					return err
				}
				if len(pvcList.Items) == 0 {
					return &noPVCErr{namespace: handle.Namespace(), name: handle.Name()}
				}
				for i := range pvcList.Items {
					p := &pvcList.Items[i]
					if p.Status.Phase != corev1.ClaimBound {
						return &pvcNotBoundErr{name: p.Name, phase: string(p.Status.Phase)}
					}
				}
				return nil
			}, 3*time.Minute, 5*time.Second).Should(Succeed())
		})
	})

type noPVCErr struct {
	namespace, name string
}

func (e *noPVCErr) Error() string {
	return "no PVCs labelled cnpg.io/cluster=" + e.name + " in " + e.namespace
}

type pvcNotBoundErr struct {
	name, phase string
}

func (e *pvcNotBoundErr) Error() string {
	return "PVC " + e.name + " is not Bound (phase=" + e.phase + ")"
}
