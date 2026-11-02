package upgrade

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
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

	// Old/new DocumentDB *versions* (e.g. "0.116.0" / "0.117.0") for the
	// schema-upgrade spec. These drive spec.documentDBVersion — the
	// user-facing knob that sets both the extension and gateway images
	// together — rather than raw image overrides. The extension's
	// installed schema semver matches the version string, so the same
	// values are used to assert status.schemaVersion progression across
	// the two-phase upgrade.
	envOldDocumentDBVersion = "E2E_UPGRADE_OLD_DOCUMENTDB_VERSION"
	envNewDocumentDBVersion = "E2E_UPGRADE_NEW_DOCUMENTDB_VERSION"

	// Default old/new versions for the schema-upgrade spec, applied when
	// the env vars above are unset. Chosen as the last released pair so
	// the spec exercises a real two-phase migration on every e2e PR
	// without depending on repo-level CI vars. Both images are published
	// on the public GHCR documentdb/gateway repos, so documentDBVersion
	// resolves to pullable tags in kind.
	//
	// This pair is the single source of truth for the CI default and is
	// bumped automatically by .github/workflows/release_documentdb_images.yml
	// on each DocumentDB release (old <- previous default, new <- released
	// version). Do not duplicate it into workflow env; test-e2e.yml only
	// passes an optional workflow_dispatch override.
	defaultOldDocumentDBVersion = "0.117.0"
	defaultNewDocumentDBVersion = "1.0.0"

	// Optional gateway image overrides for the image-upgrade spec.
	// When unset the spec patches only spec.image.documentDB and leaves
	// spec.image.gateway as-is (operator uses its default gateway). The
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

// replicaInstalledSchemaVersion execs psql on every replica pod of the
// CNPG cluster backing the DocumentDB and returns their agreed installed
// documentdb extension version, normalized to semver (e.g. "0.110.0").
//
// This exists because the operator computes status.schemaVersion by
// querying the PRIMARY only (see executeSQLCommand in the controller),
// so the CR status does not independently prove that a schema migration
// propagated to replicas. The extension schema (an ALTER EXTENSION
// catalog change) reaches replicas via WAL streaming replication; this
// helper reads pg_extension.extversion directly on each replica to
// confirm that convergence across all of them.
//
// The extension reports its version in "Major.Minor-Patch" form (e.g.
// "0.110-0"); replacing the final "-" with "." yields the semver used
// throughout the upgrade specs. clusterName is the CNPG cluster name,
// which for a single-cluster DocumentDB equals the DocumentDB name.
//
// wantReplicas is the number of replica pods the caller expects (instances
// minus the primary). The helper errors until exactly that many replicas
// are present AND they all report the same installed version, so a lagging
// or not-yet-rolled replica keeps an Eventually polling rather than passing
// on the first replica alone.
func replicaInstalledSchemaVersion(
	ctx context.Context,
	env *environment.TestingEnvironment,
	ns, clusterName string,
	wantReplicas int,
) (string, error) {
	var pods corev1.PodList
	if err := env.Client.List(ctx, &pods,
		client.InNamespace(ns),
		client.MatchingLabels{
			"cnpg.io/cluster":      clusterName,
			"cnpg.io/instanceRole": "replica",
		},
	); err != nil {
		return "", fmt.Errorf("list replica pods for cluster %s/%s: %w", ns, clusterName, err)
	}
	if len(pods.Items) != wantReplicas {
		return "", fmt.Errorf("expected %d replica pods for cluster %s/%s, found %d",
			wantReplicas, ns, clusterName, len(pods.Items))
	}

	var agreed string
	for i := range pods.Items {
		pod := pods.Items[i]
		v, err := podInstalledSchemaVersion(ctx, env, pod)
		if err != nil {
			return "", err
		}
		switch {
		case agreed == "":
			agreed = v
		case agreed != v:
			return "", fmt.Errorf("replicas disagree on installed schema version: %s vs %s (%s)",
				agreed, v, pod.Name)
		}
	}
	return agreed, nil
}

// podInstalledSchemaVersion execs psql on a single pod and returns the
// installed documentdb extension version normalized to semver.
func podInstalledSchemaVersion(
	ctx context.Context,
	env *environment.TestingEnvironment,
	pod corev1.Pod,
) (string, error) {
	timeout := time.Minute
	stdout, stderr, err := env.EventuallyExecCommand(ctx, pod, "postgres", &timeout,
		"psql", "-U", "postgres", "-d", "postgres", "-tAc",
		"SELECT extversion FROM pg_extension WHERE extname='documentdb'")
	if err != nil {
		return "", fmt.Errorf("exec psql on pod %s: %w (stderr: %s)", pod.Name, err, stderr)
	}

	raw := strings.TrimSpace(stdout)
	if raw == "" {
		return "", fmt.Errorf("documentdb extension not installed on pod %s", pod.Name)
	}
	// "0.110-0" -> "0.110.0"; a value already in semver form is unchanged.
	if i := strings.LastIndex(raw, "-"); i >= 0 {
		raw = raw[:i] + "." + raw[i+1:]
	}
	return raw, nil
}

// unpullableVersionAbove returns "<major>.999.0" for a semver, which no release
// publishes and which stays above v so the image-rollback webhook admits it.
func unpullableVersionAbove(v string) string {
	major, _, _ := strings.Cut(v, ".")
	return fmt.Sprintf("%s.999.0", major)
}
