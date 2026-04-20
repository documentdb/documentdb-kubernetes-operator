// Package data hosts DocumentDB E2E data-area specs. This file provides
// a small connectSharedRO helper shared across the spec files in this
// package so each spec does not repeat the fixture-get /
// port-forward / client-connect plumbing. It is a test-only helper
// (package data) and is not exported to other areas.
package data

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e2e "github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	emongo "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
)

// connectSharedRO returns a Handle against the session-wide SharedRO
// DocumentDB cluster and a DB name unique to the calling spec. The
// returned Handle MUST be closed by the caller (typically from
// AfterAll). dbName is derived from CurrentSpecReport().FullText() so
// Ginkgo parallel processes running the same file against the same
// cluster do not collide on collection state.
func connectSharedRO(ctx context.Context) (*emongo.Handle, string) {
	roHandle, err := fixtures.GetOrCreateSharedRO(ctx, e2e.SuiteEnv().Client)
	Expect(err).NotTo(HaveOccurred(), "get-or-create shared-ro fixture")
	h, err := emongo.NewFromDocumentDB(ctx, e2e.SuiteEnv(), roHandle.Namespace(), roHandle.Name())
	Expect(err).NotTo(HaveOccurred(), "connect to shared-ro gateway")
	return h, fixtures.DBNameFor(CurrentSpecReport().FullText())
}
