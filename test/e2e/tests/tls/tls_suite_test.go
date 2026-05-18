// Package tls hosts the DocumentDB E2E tls area. See
// docs/designs/e2e-test-suite.md for the spec catalog. This file is
// the Ginkgo root for the area binary and shares bootstrap with the
// other area binaries via the exported helpers in package e2e.
package tls

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

func TestTLS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - TLS", Label(e2e.TLSLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("tls bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("tls worker bootstrap: %v", err))
		}
	},
)

// SynchronizedAfterSuite: per Ginkgo v2, the FIRST callback runs on
// every parallel process as soon as that process's specs finish; the
// SECOND callback runs only on process #1 after every other process
// has exited. Shared fixture teardown (shared-ro / shared-scale) must
// live in the second callback so a fast process cannot delete a
// fixture another process is still exercising.
var _ = SynchronizedAfterSuite(
	func(_ SpecContext) {},
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "tls teardown: %v\n", err)
		}
	},
)

// BeforeEach in this area aborts the spec if the operator pod has
// drifted since SetupSuite (UID/name/restart-count change). Area
// tests/upgrade/ intentionally omits this hook because operator
// restarts are part of its scenario.
var _ = BeforeEach(func() {
Expect(e2e.CheckOperatorUnchanged()).To(Succeed(),
"operator health check failed — a previous spec or reconciler likely restarted the operator")
})
