package upgrade

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Environment variables that gate and parameterize the upgrade area.
const (
	envEnable            = "E2E_UPGRADE"
	envPreviousChart     = "E2E_UPGRADE_PREVIOUS_CHART"
	envPreviousVersion   = "E2E_UPGRADE_PREVIOUS_VERSION"
	envCurrentChart      = "E2E_UPGRADE_CURRENT_CHART"
	envCurrentVersion    = "E2E_UPGRADE_CURRENT_VERSION"
	envReleaseName       = "E2E_UPGRADE_RELEASE"
	envOperatorNamespace = "E2E_UPGRADE_OPERATOR_NS"

	envOldDocumentDBImage = "E2E_UPGRADE_OLD_DOCUMENTDB_IMAGE"
	envNewDocumentDBImage = "E2E_UPGRADE_NEW_DOCUMENTDB_IMAGE"

	// Optional gateway image overrides for the image-upgrade spec.
	// When unset the spec patches only spec.documentDBImage and leaves
	// spec.gatewayImage as-is (operator uses its default gateway). The
	// gateway image has an independent release cadence from the
	// extension image; setting these to the same value as the
	// documentdb env vars is INCORRECT under the layered-image
	// architecture (CNPG pg18 + extension image-library + gateway
	// sidecar).
	envOldGatewayImage = "E2E_UPGRADE_OLD_GATEWAY_IMAGE"
	envNewGatewayImage = "E2E_UPGRADE_NEW_GATEWAY_IMAGE"
)

// Defaults applied when the env vars above are not set. The chart
// references intentionally fail-closed — specs skip themselves instead
// of installing a hard-coded "latest" chart from the internet.
const (
	defaultReleaseName       = "documentdb-operator"
	defaultOperatorNamespace = "documentdb-operator"

	controlPlaneUpgradeTimeout = 15 * time.Minute
	imageRolloutTimeout        = 15 * time.Minute
)

// skipUnlessUpgradeEnabled skips the current spec when the upgrade
// area is not explicitly enabled. Called from BeforeEach in every
// spec below so Ginkgo reports a clear "skipped" message.
func skipUnlessUpgradeEnabled() {
	if os.Getenv(envEnable) != "1" {
		Skip("upgrade specs require E2E_UPGRADE=1")
	}
	if _, err := exec.LookPath("helm"); err != nil {
		Skip("upgrade specs require the `helm` CLI on PATH: " + err.Error())
	}
}

// requireEnv returns the value of name, or Skip()s the spec when the
// variable is unset. Used for chart path / image references that must
// be supplied by the CI job — specs fail-closed rather than guess.
func requireEnv(name, reason string) string {
	v := os.Getenv(name)
	if v == "" {
		Skip("upgrade spec skipped: " + name + " is required (" + reason + ")")
	}
	return v
}

// envOr returns the value of name, or fallback when unset.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// credentialSecretName is the default secret populated by createCredentialSecret
// and consumed by mongo.NewFromDocumentDB / the DocumentDB CR.
const credentialSecretName = "documentdb-credentials"

// baseVars returns the envsubst variable map for the base DocumentDB
// template. It mirrors the backup-area helper so upgrade specs share
// the same manifests/base/documentdb.yaml.template layout. The
// DOCUMENTDB_IMAGE / GATEWAY_IMAGE fields default to empty (operator
// picks layered defaults), and can be overridden via env vars —
// image-upgrade specs further override them per-call via extraVars.
func baseVars(name, ns, size string) map[string]string {
	// Empty defaults → operator composes CNPG pg18 + extension + gateway.
	// Do NOT fall back GATEWAY_IMAGE to DOCUMENTDB_IMAGE: the gateway is
	// an independent sidecar image, not a monolithic build.
	ddImage := os.Getenv("DOCUMENTDB_IMAGE")
	gwImage := os.Getenv("GATEWAY_IMAGE")
	sc := "standard"
	if v := os.Getenv("E2E_STORAGE_CLASS"); v != "" {
		sc = v
	}
	if size == "" {
		size = "2Gi"
	}
	return map[string]string{
		"NAME":              name,
		"NAMESPACE":         ns,
		"INSTANCES":         "1",
		"STORAGE_SIZE":      size,
		"STORAGE_CLASS":     sc,
		"DOCUMENTDB_IMAGE":  ddImage,
		"GATEWAY_IMAGE":     gwImage,
		"CREDENTIAL_SECRET": credentialSecretName,
		"EXPOSURE_TYPE":     "ClusterIP",
		"LOG_LEVEL":         "info",
	}
}

// manifestsRoot returns the absolute path to test/e2e/manifests, used
// as ManifestsRoot for documentdb.Create so rendering is robust to
// the current working directory.
func manifestsRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		Fail("runtime.Caller failed — cannot locate test/e2e/manifests")
	}
	// this file: test/e2e/tests/upgrade/helpers_test.go
	// manifests: test/e2e/manifests/
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "manifests")
}

// createNamespace creates ns (if missing) and registers DeferCleanup
// to delete it at spec teardown.
func createNamespace(ctx context.Context, c client.Client, ns string) {
	obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	err := c.Create(ctx, obj)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Fail("create namespace " + ns + ": " + err.Error())
	}
	DeferCleanup(func(ctx SpecContext) {
		_ = c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})
}

// createCredentialSecret seeds the DocumentDB credential secret in ns.
func createCredentialSecret(ctx context.Context, c client.Client, ns string) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credentialSecretName, Namespace: ns},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": "e2e_admin",
			"password": "E2eAdmin100",
		},
	}
	err := c.Create(ctx, sec)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Fail("create credential secret " + ns + "/" + credentialSecretName + ": " + err.Error())
	}
}
