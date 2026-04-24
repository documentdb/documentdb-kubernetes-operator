// Package feature_gates hosts the DocumentDB E2E featuregates area. See
// docs/designs/e2e-test-suite.md for the spec catalog. This file is
// the Ginkgo root for the area binary and shares bootstrap with the
// other area binaries via the exported helpers in package e2e.
package feature_gates

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

const operatorReadyTimeout = 2 * time.Minute

func TestFeatureGates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - FeatureGates", Label(e2e.FeatureLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("featuregates bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("featuregates worker bootstrap: %v", err))
		}
	},
)

var _ = SynchronizedAfterSuite(
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "featuregates teardown: %v\n", err)
		}
	},
	func(_ SpecContext) {},
)

// BeforeEach in this area aborts the spec if the operator pod has
// drifted since SetupSuite (UID/name/restart-count change). Area
// tests/upgrade/ intentionally omits this hook because operator
// restarts are part of its scenario.
var _ = BeforeEach(func() {
Expect(e2e.CheckOperatorUnchanged()).To(Succeed(),
"operator health check failed — a previous spec or reconciler likely restarted the operator")
})

// TODO(e2e/feature-gates): add a ChangeStreams spec here once the
// suite standardises on a change-stream-capable DocumentDB image.
//
// Status: experimental feature. The operator already translates
// `spec.featureGates.ChangeStreams=true` into `wal_level=logical` on
// the underlying CNPG Cluster (see operator/src/internal/cnpg/
// cnpg_cluster.go), but end-to-end validation of the Mongo-wire
// `watch()` call requires the `-changestream` DocumentDB image
// variant, which is not part of the default e2e image set.
//
// Previously this area carried a tests/feature_gates/changestreams_
// test.go that asserted the wal_level translation via the CNPG spec.
// It was removed together with manifests/mixins/feature_changestreams.
// yaml.template and the fixtures_test render check so the default
// pipeline does not imply the feature is supported in the shipped
// image.
//
// When re-enabling:
//  1. Restore manifests/mixins/feature_changestreams.yaml.template
//     (single key: spec.featureGates.ChangeStreams: true).
//  2. Gate the spec behind a `needs-changestream-image` capability
//     label (mirrors `needs-cert-manager`) and a preflight check that
//     skips when the current documentDBImage cannot handle it.
//  3. Layer a best-effort mongo-driver `Watch` smoke on top of the
//     existing wal_level assertion so both the operator and extension
//     contracts are covered.
