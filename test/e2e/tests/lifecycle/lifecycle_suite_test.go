// Package lifecycle hosts the DocumentDB E2E lifecycle area. See
// docs/designs/e2e-test-suite.md for the spec catalog. This file is
// the Ginkgo root for the area binary and shares bootstrap with the
// other area binaries via the exported helpers in package e2e.
package lifecycle

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

func TestLifecycle(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - Lifecycle", Label(e2e.LifecycleLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("lifecycle bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("lifecycle worker bootstrap: %v", err))
		}
	},
)

var _ = SynchronizedAfterSuite(
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "lifecycle teardown: %v\n", err)
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
