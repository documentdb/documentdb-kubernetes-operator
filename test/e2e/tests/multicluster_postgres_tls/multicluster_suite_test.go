package multiclusterpostgrestls

import (
	"testing"

	"github.com/documentdb/documentdb-operator/test/e2e"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMultiClusterPostgresTLS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DocumentDB E2E - Multi-cluster PostgreSQL TLS",
		Label(e2e.TLSLabel, e2e.MultiClusterTLSLabel))
}
