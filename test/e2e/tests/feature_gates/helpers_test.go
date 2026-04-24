package feature_gates

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	documentdbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// Shared credential name for fresh per-spec clusters. Username and
// password values are now sourced from fixtures.DefaultCredentialUsername
// and fixtures.DefaultCredentialPassword so mongo helpers that already
// know those values work against both shared fixtures and per-spec CRs.
const credSecretName = fixtures.DefaultCredentialSecretName

// defaultDocDBImage / defaultGatewayImage are empty by default so the
// operator picks the correct layered defaults (CNPG pg18 base +
// DocumentDB extension image + gateway sidecar). Env vars still
// override for CI pinning.
const (
	defaultDocDBImage   = ""
	defaultGatewayImage = ""
)

// baseVars builds the envsubst map the base/documentdb.yaml.template
// expects. Callers override individual entries for per-spec tweaks.
func baseVars(ns, name string) map[string]string {
	docdbImg := defaultDocDBImage
	if v := os.Getenv("DOCUMENTDB_IMAGE"); v != "" {
		docdbImg = v
	}
	gwImg := defaultGatewayImage
	if v := os.Getenv("GATEWAY_IMAGE"); v != "" {
		gwImg = v
	}
	sSize := "1Gi"
	if v := os.Getenv("E2E_STORAGE_SIZE"); v != "" {
		sSize = v
	}
	sClass := "standard"
	if v := os.Getenv("E2E_STORAGE_CLASS"); v != "" {
		sClass = v
	}
	return map[string]string{
		"NAMESPACE":         ns,
		"NAME":              name,
		"INSTANCES":         "1",
		"STORAGE_SIZE":      sSize,
		"STORAGE_CLASS":     sClass,
		"DOCUMENTDB_IMAGE":  docdbImg,
		"GATEWAY_IMAGE":     gwImg,
		"CREDENTIAL_SECRET": credSecretName,
		"EXPOSURE_TYPE":     "ClusterIP",
		"LOG_LEVEL":         "info",
	}
}

// manifestsRoot returns the absolute path to test/e2e/manifests so the
// per-spec clusters can read the mixin templates without depending on
// the caller's working directory.
func manifestsRoot() string {
	// tests/feature_gates/<file> → ../../manifests
	return "../../manifests"
}

// setupFreshCluster creates a namespace, credential secret, and a
// DocumentDB CR composed of the base template plus mixins, then waits
// for it to become healthy. It returns the live CR plus a cleanup func
// that deletes the namespace. Namespace + secret creation delegate to
// the fixtures helpers so ownership labels match the rest of the suite.
func setupFreshCluster(
	ctx context.Context,
	c client.Client,
	name string,
	mixins []string,
	extraVars map[string]string,
) (*previewv1.DocumentDB, func()) {
	ns := namespaces.NamespaceForSpec(e2e.FeatureLabel)
	Expect(fixtures.CreateLabeledNamespace(ctx, c, ns, e2e.FeatureLabel)).To(Succeed())
	Expect(fixtures.CreateLabeledCredentialSecret(ctx, c, ns)).To(Succeed())
	vars := baseVars(ns, name)
	for k, v := range extraVars {
		vars[k] = v
	}
	_, err := documentdbutil.Create(ctx, c, ns, name, documentdbutil.CreateOptions{
		Base:          "documentdb",
		Mixins:        mixins,
		Vars:          vars,
		ManifestsRoot: manifestsRoot(),
	})
	Expect(err).ToNot(HaveOccurred(), "create DocumentDB")

	Eventually(func() error {
		return documentdbutil.WaitHealthy(ctx, c,
			types.NamespacedName{Namespace: ns, Name: name},
			timeouts.For(timeouts.DocumentDBReady))
	}, timeouts.For(timeouts.DocumentDBReady)+30*time.Second, 10*time.Second).
		Should(Succeed(), "DocumentDB %s/%s did not become healthy", ns, name)

	// Re-fetch to return the populated object.
	live, err := documentdbutil.Get(ctx, c, client.ObjectKey{Namespace: ns, Name: name})
	Expect(err).ToNot(HaveOccurred())

	cleanup := func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = c.Delete(delCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	return live, cleanup
}