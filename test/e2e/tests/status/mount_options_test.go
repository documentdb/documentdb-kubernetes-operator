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

// DocumentDB mount options — CRD discrepancy note.
//
// The task brief asked the spec to inspect `status.mountOptions` but no
// such field exists on `DocumentDBStatus`. Mount configuration for a
// DocumentDB cluster lives on the backing CloudNative-PG Postgres pods
// — concretely, CNPG mounts the PGDATA volume at
// `/var/lib/postgresql/data` (see CNPG's pkg/specs/volumes.go).
//
// We therefore verify the observable contract by listing the pods CNPG
// owns (label `cnpg.io/cluster=<name>`) and asserting that at least one
// container mounts `/var/lib/postgresql/data`.
const pgdataMountPath = "/var/lib/postgresql/data"

var _ = Describe("DocumentDB mount options — PGDATA volume mount",
	Label(e2e.StatusLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("mounts the PGDATA volume at /var/lib/postgresql/data", func() {
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil())
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			DeferCleanup(cancel)

			handle, err := fixtures.GetOrCreateSharedRO(ctx, c)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() error {
				pods := &corev1.PodList{}
				if err := c.List(ctx, pods,
					client.InNamespace(handle.Namespace()),
					client.MatchingLabels{"cnpg.io/cluster": handle.Name()},
				); err != nil {
					return err
				}
				if len(pods.Items) == 0 {
					return &noCNPGPodsErr{
						namespace: handle.Namespace(), name: handle.Name(),
					}
				}
				for i := range pods.Items {
					if hasPGDATAMount(&pods.Items[i]) {
						return nil
					}
				}
				return &noPGDATAMountErr{namespace: handle.Namespace(), name: handle.Name()}
			}, 3*time.Minute, 5*time.Second).Should(Succeed())
		})
	})

func hasPGDATAMount(pod *corev1.Pod) bool {
	for i := range pod.Spec.Containers {
		for _, vm := range pod.Spec.Containers[i].VolumeMounts {
			if vm.MountPath == pgdataMountPath {
				return true
			}
		}
	}
	return false
}

type noCNPGPodsErr struct {
	namespace, name string
}

func (e *noCNPGPodsErr) Error() string {
	return "no CNPG pods labelled cnpg.io/cluster=" + e.name + " in " + e.namespace
}

type noPGDATAMountErr struct {
	namespace, name string
}

func (e *noPGDATAMountErr) Error() string {
	return "no CNPG pod in " + e.namespace + "/" + e.name +
		" mounts " + pgdataMountPath
}
