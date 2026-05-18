package lifecycle

import (
	"context"
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
)

var (
	// errPendingPVCs signals that no PVCs have been created yet.
	errPendingPVCs = errors.New("waiting for PVCs to appear")
	// errNotExpanded signals that at least one PVC has not reached
	// the requested capacity yet.
	errNotExpanded = errors.New("waiting for PVC expansion")
)

// baseVars returns the envsubst variables used by the lifecycle base
// template. Image overrides honour the same E2E-wide env vars the
// shared fixtures do; tests that need to mutate specific fields
// override the returned map before calling Create.
func baseVars(size string) map[string]string {
	// Leave DOCUMENTDB_IMAGE / GATEWAY_IMAGE empty by default so the
	// operator picks its own defaults — the DocumentDB extension is
	// mounted onto the CNPG pg18 base via the image-library mechanism
	// and the gateway is a separate sidecar image. Setting a monolithic
	// override here (e.g. documentdb-local:16) would point the CNPG
	// cluster at a non-postgres image and break initdb.
	ddImage := os.Getenv("DOCUMENTDB_IMAGE")
	gwImage := os.Getenv("GATEWAY_IMAGE")
	storageClass := "standard"
	if v := os.Getenv("E2E_STORAGE_CLASS"); v != "" {
		storageClass = v
	}
	if size == "" {
		size = "1Gi"
	}
	return map[string]string{
		"INSTANCES":         "1",
		"STORAGE_SIZE":      size,
		"STORAGE_CLASS":     storageClass,
		"DOCUMENTDB_IMAGE":  ddImage,
		"GATEWAY_IMAGE":     gwImage,
		"CREDENTIAL_SECRET": fixtures.DefaultCredentialSecretName,
		"EXPOSURE_TYPE":     "ClusterIP",
		"LOG_LEVEL":         "info",
	}
}

// createNamespace creates ns (via fixtures.CreateLabeledNamespace so the
// ownership labels are stamped) and registers a DeferCleanup to remove
// it. The signature is preserved so update_storage_test.go — which is
// out of scope for this pass — continues to compile.
func createNamespace(ctx context.Context, c client.Client, ns string) {
	if err := fixtures.CreateLabeledNamespace(ctx, c, ns, "lifecycle"); err != nil {
		Fail("create namespace " + ns + ": " + err.Error())
	}
	DeferCleanup(func(ctx SpecContext) {
		_ = c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})
}

// createCredentialSecret seeds the default DocumentDB credential secret
// so the operator can finish the bootstrap bring-up. Name is accepted
// for signature compatibility with update_storage_test.go; when it
// matches DefaultCredentialSecretName the fixtures helper is used so
// ownership labels are stamped.
func createCredentialSecret(ctx context.Context, c client.Client, ns, name string) {
	if name == fixtures.DefaultCredentialSecretName || name == "" {
		if err := fixtures.CreateLabeledCredentialSecret(ctx, c, ns); err != nil {
			Fail("create credential secret " + ns + "/" + name + ": " + err.Error())
		}
		return
	}
	// Non-default secret name — fall back to an inline Create so callers
	// can seed multiple named secrets in the same namespace.
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": fixtures.DefaultCredentialUsername,
			"password": fixtures.DefaultCredentialPassword,
		},
	}
	if err := c.Create(ctx, sec); err != nil {
		Fail("create credential secret " + ns + "/" + name + ": " + err.Error())
	}
}

// getDD is a convenience shortcut around documentdb.Get used by specs
// that need to refetch the CR after a patch.
func getDD(ctx context.Context, ns, name string) *previewv1.DocumentDB {
	c := e2e.SuiteEnv().Client
	dd, err := documentdb.Get(ctx, c, types.NamespacedName{Namespace: ns, Name: name})
	Expect(err).ToNot(HaveOccurred())
	return dd
}
