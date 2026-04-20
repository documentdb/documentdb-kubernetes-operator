package upgrade

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

// DocumentDB upgrade — rollback: skeleton for the operator-rollback
// scenario. The upgrade flow is one-directional today — there is no
// formally supported `helm rollback` story for the DocumentDB operator
// or its CRDs (CRD removal/downgrade is the hard part). The spec
// below is Pending and always skipped with a clear reason so the
// area's intent is documented but the test does not flap against an
// unimplemented feature.
//
// When rollback support lands:
//  1. Drop the Skip() below.
//  2. Replace the placeholders with: install current, seed, helm
//     rollback to previous, verify CR still reads/writes.
//  3. Confirm the previous chart's CRD schema is backward-compatible
//     with the data written by the current operator, or document the
//     rollback boundary.
var _ = Describe("DocumentDB upgrade — rollback",
	Label(e2e.UpgradeLabel, e2e.DisruptiveLabel, e2e.SlowLabel),
	e2e.HighLevelLabel,
	Serial, Ordered, Pending, func() {
		BeforeEach(func() {
			// Defense in depth: even if Pending is removed by mistake,
			// keep the spec dormant until rollback is supported.
			Skip("rollback support pending")
		})

		It("rolls the operator back to the previous chart without losing data", func() {
			// Placeholder intent:
			//   1. Install current PR chart.
			//   2. Create DocumentDB + seed data.
			//   3. `helm rollback` to previously-released chart version.
			//   4. Assert operator becomes Ready on the old version.
			//   5. Assert DocumentDB CR is still accepted and data is intact.
		})
	})
