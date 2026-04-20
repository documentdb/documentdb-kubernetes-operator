package status

import (
	"context"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// DocumentDB status — ConnectionString.
//
// The operator publishes a mongo:// URI in status.connectionString once
// the gateway Service and credential secret are ready. We:
//  1. assert the string matches the expected "^mongodb://" shape
//     (scheme + auth + host segment);
//  2. parse it with net/url and sanity-check the scheme and host.
//
// This spec runs against the session-scoped shared RO fixture so it
// adds negligible time to the suite.
var _ = Describe("DocumentDB status — connectionString",
	Label(e2e.StatusLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("publishes a valid mongodb:// URI", func() {
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil())
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			DeferCleanup(cancel)

			handle, err := fixtures.GetOrCreateSharedRO(ctx, c)
			Expect(err).ToNot(HaveOccurred())

			key := client.ObjectKey{Namespace: handle.Namespace(), Name: handle.Name()}

			// 1. Regex assertion via the shared helper.
			Eventually(
				assertions.AssertConnectionStringMatches(ctx, c, key, `^mongodb://`),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			// 2. Parse + structural sanity.
			dd, err := handle.GetCR(ctx, c)
			Expect(err).ToNot(HaveOccurred())
			Expect(dd.Status.ConnectionString).ToNot(BeEmpty(),
				"status.connectionString must be populated on a Ready DocumentDB")

			u, err := url.Parse(dd.Status.ConnectionString)
			Expect(err).ToNot(HaveOccurred(),
				"status.connectionString must parse as a URL: %q", dd.Status.ConnectionString)
			Expect(u.Scheme).To(Equal("mongodb"),
				"connection string scheme must be mongodb; got %q", u.Scheme)
			Expect(u.Host).ToNot(BeEmpty(),
				"connection string must carry a host component")
		})
	})
