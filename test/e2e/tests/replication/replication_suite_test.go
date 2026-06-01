// Package replication hosts the DocumentDB E2E replication area.
// It tests cross-cluster replication semantics within a single Kind
// cluster by deploying two DocumentDB CRs (primary + replica) that
// reference each other via spec.clusterReplication with
// crossCloudNetworkingStrategy=None.
package replication

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

func TestReplication(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - Replication", Label(e2e.ReplicationLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("replication bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("replication worker bootstrap: %v", err))
		}
	},
)

var _ = SynchronizedAfterSuite(
	func(_ SpecContext) {},
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "replication teardown: %v\n", err)
		}
	},
)

var _ = BeforeEach(func() {
	Expect(e2e.CheckOperatorUnchanged()).To(Succeed(),
		"operator health check failed — a previous spec or reconciler likely restarted the operator")
})
