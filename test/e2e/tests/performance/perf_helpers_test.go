package performance

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	driver "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
)

// perfConn bundles everything a perf spec needs to drive mongo traffic
// against the shared RO fixture: a connected client, an isolated DB
// name, and a cleanup hook that drops the DB and tears down the
// port-forward.
type perfConn struct {
	Client *driver.Client
	DB     string
	Stop   func()
}

// connectSharedRO provisions the SharedRO fixture (lazily on first
// call) and returns a connected mongo client scoped to a per-spec
// database name derived from CurrentSpecReport().FullText(). The
// returned Stop drops the spec's database and tears down the
// forward/client.
//
// The mechanics (port-forward, credential resolution, retry on
// forwarder bind) are delegated to mongo.NewFromDocumentDB so that all
// suites share a single connect path — we just wrap it to preserve the
// per-spec DB-drop cleanup contract the perf specs rely on.
func connectSharedRO(ctx context.Context) *perfConn {
	GinkgoHelper()
	env := e2e.SuiteEnv()
	Expect(env).NotTo(BeNil(), "SuiteEnv must be initialized")

	handle, err := fixtures.GetOrCreateSharedRO(ctx, env.Client)
	Expect(err).NotTo(HaveOccurred(), "provision SharedRO fixture")
	Expect(handle).NotTo(BeNil())

	h, err := mongo.NewFromDocumentDB(ctx, env, handle.Namespace(), handle.Name())
	Expect(err).NotTo(HaveOccurred(), "open mongo connection to SharedRO")

	db := fixtures.DBNameFor(CurrentSpecReport().FullText())
	c := h.Client()

	stop := func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		_ = mongo.DropDatabase(dropCtx, c, db)
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer closeCancel()
		_ = h.Close(closeCtx)
	}
	return &perfConn{Client: c, DB: db, Stop: stop}
}

// logLatency is a small convenience so every spec reports its measured
// duration in a uniform format that CI log scrapers can grep.
func logLatency(op string, elapsed time.Duration) {
	fmt.Fprintf(GinkgoWriter, "perf[%s]: %s\n", op, elapsed)
}
