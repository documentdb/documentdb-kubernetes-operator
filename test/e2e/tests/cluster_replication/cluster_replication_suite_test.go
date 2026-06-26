// Package clusterreplication hosts the DocumentDB E2E cluster-replication area.
// It tests cross-cluster replication semantics within a single Kind
// cluster by deploying two DocumentDB CRs (primary + replica) that
// reference each other via spec.clusterReplication with
// crossCloudNetworkingStrategy=None.
package clusterreplication

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

func TestClusterReplication(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - Cluster Replication", Label(e2e.ClusterReplicationLabel))
}

var _ = SynchronizedBeforeSuite(
	func(ctx SpecContext) []byte {
		if err := e2e.SetupSuite(ctx, operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("cluster-replication bootstrap: %v", err))
		}
		return []byte{}
	},
	func(_ SpecContext, _ []byte) {
		if err := e2e.SetupSuite(context.Background(), operatorReadyTimeout); err != nil {
			Fail(fmt.Sprintf("cluster-replication worker bootstrap: %v", err))
		}
	},
)

var _ = SynchronizedAfterSuite(
	func(_ SpecContext) {},
	func(ctx SpecContext) {
		if err := e2e.TeardownSuite(ctx); err != nil {
			fmt.Fprintf(GinkgoWriter, "cluster-replication teardown: %v\n", err)
		}
	},
)

var _ = BeforeEach(func() {
	Expect(e2e.CheckOperatorUnchanged()).To(Succeed(),
		"operator health check failed — a previous spec or reconciler likely restarted the operator")
})
