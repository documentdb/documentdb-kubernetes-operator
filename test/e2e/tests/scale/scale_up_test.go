// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package scale

import (
	"context"

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

var _ = Describe("DocumentDB scale — up",
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

		It("scales 2 → 3 instances while keeping the primary pod stable", func() {
			e2e.SkipUnlessLevel(e2e.Medium)

			primary, err := cnpgclusterutils.GetPrimary(ctx, c, key.Namespace, key.Name)
			Expect(err).NotTo(HaveOccurred(), "fetch initial primary")
			Expect(primary).NotTo(BeNil())
			initialPrimary := primary.Name

			Expect(documentdb.PatchInstances(ctx, c, key.Namespace, key.Name, 3)).To(Succeed())

			Eventually(assertions.AssertInstanceCount(ctx, c, key, 3),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should report readyInstances=3")

			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "DocumentDB status should be Ready")

			Expect(assertions.AssertPrimaryUnchanged(ctx, c, key, initialPrimary)()).
				To(Succeed(), "scaling up must not change the primary")
		})

		It("scales 1 → 2 instances after first scaling down to 1", func() {
			e2e.SkipUnlessLevel(e2e.Medium)

			Expect(documentdb.PatchInstances(ctx, c, key.Namespace, key.Name, 1)).To(Succeed())
			Eventually(assertions.AssertInstanceCount(ctx, c, key, 1),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should converge to readyInstances=1")
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "DocumentDB should be Ready at 1 instance")

			primary, err := cnpgclusterutils.GetPrimary(ctx, c, key.Namespace, key.Name)
			Expect(err).NotTo(HaveOccurred(), "fetch primary before scale-up")
			Expect(primary).NotTo(BeNil())
			initialPrimary := primary.Name

			Expect(documentdb.PatchInstances(ctx, c, key.Namespace, key.Name, 2)).To(Succeed())

			Eventually(assertions.AssertInstanceCount(ctx, c, key, 2),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "CNPG Cluster should converge to readyInstances=2")
			Eventually(assertions.AssertDocumentDBReady(ctx, c, key),
				timeouts.For(timeouts.InstanceScale),
				timeouts.PollInterval(timeouts.InstanceScale)).
				Should(Succeed(), "DocumentDB should be Ready at 2 instances")

			Expect(assertions.AssertPrimaryUnchanged(ctx, c, key, initialPrimary)()).
				To(Succeed(), "scaling up 1→2 must not change the primary")
		})
	})
