package tls

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"

	"github.com/documentdb/documentdb-operator/test/e2e"
	ddbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/portforward"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// Shared per-spec setup for the TLS area. Each TLS spec uses the same
// base DocumentDB template plus a single mixin describing the TLS
// mode under test.
//
// tlsCredentialSecret is intentionally distinct from
// fixtures.DefaultCredentialSecretName so specs can exercise a custom
// secret name on the CR spec path; the credentials themselves reuse
// fixtures.DefaultCredentialUsername / DefaultCredentialPassword so a
// future rotation stays a one-file edit.
const (
	tlsCredentialSecret    = "tls-e2e-credentials"
	tlsCredentialUser      = fixtures.DefaultCredentialUsername
	tlsCredentialPassword  = fixtures.DefaultCredentialPassword //nolint:gosec // fixture-only
	tlsDocumentDBName      = "tls-e2e"
	tlsDefaultStorageSize  = "1Gi"
	tlsDefaultStorageCls   = "standard"
	tlsDefaultDDBImage     = ""
	tlsDefaultGatewayImage = ""
)

// clusterSetup holds the artefacts returned by provisionCluster.
type clusterSetup struct {
	NamespaceName string
	DD            *previewv1.DocumentDB
}

// provisionCluster builds a TLS-configured DocumentDB from the base
// template + supplied mixin, waits for it to become healthy, and
// registers DeferCleanup hooks to tear it down. extraVars are merged
// on top of the baseline variable map so specs can inject
// mode-specific values (e.g., TLS_SECRET_NAME for Provided mode).
func provisionCluster(
	ctx context.Context,
	c client.Client,
	area, mixin string,
	extraVars map[string]string,
) *clusterSetup {
	GinkgoHelper()

	nsName := namespaces.NamespaceForSpec(area)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	Expect(createIdempotent(ctx, c, ns)).To(Succeed(), "create namespace %s", nsName)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: tlsCredentialSecret, Namespace: nsName},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": fixtures.DefaultCredentialUsername,
			"password": fixtures.DefaultCredentialPassword,
		},
	}
	Expect(createIdempotent(ctx, c, sec)).To(Succeed(), "create credential secret")

	vars := map[string]string{
		"STORAGE_SIZE":      envDefault("E2E_STORAGE_SIZE", tlsDefaultStorageSize),
		"STORAGE_CLASS":     envDefault("E2E_STORAGE_CLASS", tlsDefaultStorageCls),
		"DOCUMENTDB_IMAGE":  envDefault("DOCUMENTDB_IMAGE", tlsDefaultDDBImage),
		"GATEWAY_IMAGE":     envDefault("GATEWAY_IMAGE", tlsDefaultGatewayImage),
		"CREDENTIAL_SECRET": tlsCredentialSecret,
		"INSTANCES":         "1",
		"EXPOSURE_TYPE":     "ClusterIP",
		"LOG_LEVEL":         "info",
	}
	for k, v := range extraVars {
		vars[k] = v
	}

	dd, err := ddbutil.Create(ctx, c, nsName, tlsDocumentDBName, ddbutil.CreateOptions{
		Base:          "documentdb",
		Mixins:        []string{mixin},
		Vars:          vars,
		ManifestsRoot: manifestsRoot(),
	})
	Expect(err).NotTo(HaveOccurred(), "render/create documentdb with mixin %q", mixin)

	DeferCleanup(func(ctx SpecContext) {
		// Best-effort namespace deletion — this also garbage-collects
		// the DocumentDB CR and any child objects via ownerRefs.
		_ = c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}})
	})

	key := types.NamespacedName{Namespace: nsName, Name: tlsDocumentDBName}
	Expect(ddbutil.WaitHealthy(ctx, c, key, timeouts.For(timeouts.DocumentDBReady))).
		To(Succeed(), "documentdb did not become healthy within %s", timeouts.For(timeouts.DocumentDBReady))

	return &clusterSetup{NamespaceName: nsName, DD: dd}
}

// openGatewayForward opens a port-forward to the gateway Service of
// dd on a locally-reserved port and returns (host, port, stop). The
// caller defers stop; the host is always "127.0.0.1".
func openGatewayForward(ctx context.Context, dd *previewv1.DocumentDB) (string, string, func()) {
	GinkgoHelper()
	port := pickFreeLocalPort()
	stop, err := portforward.Open(ctx, e2e.SuiteEnv(), dd, port)
	Expect(err).NotTo(HaveOccurred(), "open port-forward to gateway service")
	// Give the forwarder a beat to bind the local listener before
	// the first connect attempt on slow CI nodes.
	time.Sleep(250 * time.Millisecond)
	return "127.0.0.1", fmt.Sprintf("%d", port), stop
}

// pickFreeLocalPort binds :0 to discover an unused TCP port, closes
// the listener, and returns the port. A narrow race exists between
// close and the forwarder's bind; it matches how controller-runtime
// envtest picks its local API server port and is benign on CI hosts
// without adversarial workloads.
func pickFreeLocalPort() int {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred(), "reserve a free local TCP port")
	addr := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return addr
}

// createIdempotent wraps c.Create so tests that re-enter on retry
// don't trip over AlreadyExists.
func createIdempotent(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// envDefault returns os.Getenv(k) when set, otherwise def.
func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// manifestsRoot returns the absolute path of the shared manifests
// directory. Uses runtime.Caller so go test invocations from any
// working directory still find the templates.
func manifestsRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join(".", "..", "..", "manifests")
	}
	// test/e2e/tests/tls/<file> -> test/e2e/manifests
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "manifests")
}
