// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package scale

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgclusterutils "github.com/cloudnative-pg/cloudnative-pg/tests/utils/clusterutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

var _ = Describe("DocumentDB scale — down",
	Ordered,
	Label(e2e.ScaleLabel, e2e.BasicLabel),
	e2e.MediumLevelLabel,
	func() {
		var (
			handle *fixtures.SharedScaleHandle
			c      client.Client
			ctx    context.Context
			key    client.ObjectKey
		)

		BeforeAll(func() {
			env := e2e.SuiteEnv()
			Expect(env).NotTo(BeNil(), "SuiteEnv not initialized")
			ctx = env.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			c = env.Client

			h, err := fixtures.GetOrCreateSharedScale(ctx, c)
			Expect(err).NotTo(HaveOccurred(), "get-or-create shared-scale fixture")
			handle = h
			key = client.ObjectKey{Namespace: handle.Namespace(), Name: handle.Name()}
		})

		AfterEach(func() {
			Expect(handle.ResetToTwoInstances(ctx, c)).To(Succeed(),
				"reset shared-scale fixture to 2 instances")
		})

		It("scales 3 → 2 instances", func() {
			e2e.SkipUnlessLevel(e2e.Medium)

			// Grow to 3 first so we can assert a genuine 3→2 scale-down.
			Expect(documentdb.PatchInstances(ctx, c, key.Namespace, key.Name, 3)).To(Succeed())
			Eventually(assertions.AssertInstanceCount(ctx, c, key, 3),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should converge to readyInstances=3 before scale-down")
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed())

			Expect(documentdb.PatchInstances(ctx, c, key.Namespace, key.Name, 2)).To(Succeed())

			Eventually(assertions.AssertInstanceCount(ctx, c, key, 2),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should converge to readyInstances=2")
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "DocumentDB should be Ready at 2 instances")
		})

		It("scales 2 → 1 instance and stays healthy after primary re-election", func() {
			e2e.SkipUnlessLevel(e2e.Medium)

			primary, err := cnpgclusterutils.GetPrimary(ctx, c, key.Namespace, key.Name)
			Expect(err).NotTo(HaveOccurred(), "fetch initial primary")
			Expect(primary).NotTo(BeNil())
			GinkgoLogr.Info("initial primary before 2→1 scale-down", "pod", primary.Name)

			Expect(documentdb.PatchInstances(ctx, c, key.Namespace, key.Name, 1)).To(Succeed())

			Eventually(assertions.AssertInstanceCount(ctx, c, key, 1),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should converge to readyInstances=1")

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "DocumentDB should be Ready after scaling to 1 instance")

			// After scale-down, a primary must still exist — but its
			// identity may legitimately have changed via re-election,
			// so we do not assert pod-name equality here.
			Eventually(func() error {
				cl := &cnpgv1.Cluster{}
				if err := c.Get(ctx, key, cl); err != nil {
					return fmt.Errorf("get CNPG cluster: %w", err)
				}
				if cl.Status.CurrentPrimary == "" {
					return fmt.Errorf("CNPG cluster %s has no currentPrimary", key)
				}
				return nil
			}, timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should report a currentPrimary after re-election")
		})
	})
