// Package resources hosts the DocumentDB E2E resources area, validating the
// pod memory carve-out between the PostgreSQL, gateway, and OTel collector
// containers (sidecar resource isolation). This file is the Ginkgo root for
// the area binary and shares bootstrap with the other area binaries via the
// exported helpers in package e2e.
package resources

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

func TestResources(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - Resources", Label(e2e.ResourcesLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("resources bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("resources worker bootstrap: %v", err))
		}
	},
)

var _ = SynchronizedAfterSuite(
	func(_ SpecContext) {},
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "resources teardown: %v\n", err)
		}
	},
)

var _ = BeforeEach(func() {
	Expect(e2e.CheckOperatorUnchanged()).To(Succeed(),
		"operator health check failed — a previous spec or reconciler likely restarted the operator")
})
