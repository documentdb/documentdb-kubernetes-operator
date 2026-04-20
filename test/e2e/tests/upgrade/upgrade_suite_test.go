// Package upgrade hosts the DocumentDB E2E upgrade area. See
// docs/designs/e2e-test-suite.md for the spec catalog. This file is
// the Ginkgo root for the area binary and shares bootstrap with the
// other area binaries via the exported helpers in package e2e.
//
// This area is DISRUPTIVE — its specs install/upgrade the operator
// itself. They are gated behind the E2E_UPGRADE=1 environment variable
// to prevent accidental local runs. They require the `helm` v3 CLI on
// PATH and must run with `ginkgo -procs=1` because they mutate the
// cluster-wide operator Deployment.
//
// Unlike every other area, tests/upgrade/ does NOT install the
// [e2e.CheckOperatorUnchanged] BeforeEach hook — operator restarts are
// part of the scenario here, not a failure mode. This exemption is
// acknowledged in pkg e2e's suite.go header comment.
package upgrade

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

func TestUpgrade(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - Upgrade", Label(e2e.UpgradeLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("upgrade bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("upgrade worker bootstrap: %v", err))
		}
	},
)

var _ = SynchronizedAfterSuite(
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "upgrade teardown: %v\n", err)
		}
	},
	func(_ SpecContext) {},
)
