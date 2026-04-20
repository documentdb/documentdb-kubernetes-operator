package exposure

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

// Credential constants now alias the fixtures exports so every area
// lands on the same values. credUser / credPassword are retained as
// package-level constants because clusterip_test.go (a spec under this
// pass's do-not-touch list) references them directly.
const (
	credSecretName = fixtures.DefaultCredentialSecretName
	credUser       = fixtures.DefaultCredentialUsername
	credPassword   = fixtures.DefaultCredentialPassword //nolint:gosec // fixture-only

	// DOCUMENTDB_IMAGE / GATEWAY_IMAGE default to empty strings so the
	// operator selects the correct components itself: CNPG pg18 base +
	// DocumentDB extension via image-library + gateway as a separate
	// sidecar. A pinned env-var override is still honoured for CI.
	defaultDocDBImage   = ""
	defaultGatewayImage = ""
)

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

// tests/exposure/<file> → ../../manifests
func manifestsRoot() string { return "../../manifests" }

// setupFreshCluster is the exposure-area analogue of the feature_gates
// helper: namespace + secret + DocumentDB, waits for healthy. Returns
// the live CR plus a namespace-deleting cleanup.
func setupFreshCluster(
	ctx context.Context,
	c client.Client,
	name string,
	mixins []string,
	extraVars map[string]string,
) (*previewv1.DocumentDB, func()) {
	ns := namespaces.NamespaceForSpec(e2e.ExposureLabel)
	Expect(fixtures.CreateLabeledNamespace(ctx, c, ns, e2e.ExposureLabel)).To(Succeed())
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

	live, err := documentdbutil.Get(ctx, c, client.ObjectKey{Namespace: ns, Name: name})
	Expect(err).ToNot(HaveOccurred())

	cleanup := func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = c.Delete(delCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	return live, cleanup
}
