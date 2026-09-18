package upgrade

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
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

	// Ascending, comma-separated list of published DocumentDB versions used by
	// the multi-version upgrade specs (multi-minor jump and sequential chain).
	// The extension only ships documentdb--<from>--<to>.sql scripts between
	// released versions, so every entry must be a real published tag on both
	// the documentdb and gateway GHCR repos — an invented version has no
	// update path and would be blocked by the operator's preflight.
	envDocumentDBVersionChain = "E2E_UPGRADE_DOCUMENTDB_VERSION_CHAIN"

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
	defaultOldDocumentDBVersion = "0.116.0"
	defaultNewDocumentDBVersion = "0.117.0"

	// Default version chain for the multi-version upgrade specs. These are the
	// published DocumentDB releases on ghcr.io/documentdb/documentdb-kubernetes-operator
	// (documentdb + gateway). The gaps are deliberate and load-bearing: the chain
	// omits 0.111.x and 0.112.x entirely, so the single-step jump spec (first entry
	// → last entry, 0.109.0 → 0.114.0) crosses several unpublished minors — exactly
	// the ">1 minor jump" case these specs exist to cover. Keep this list ascending
	// and keep every entry a real published tag; add newly released versions to the end.
	defaultDocumentDBVersionChain = "0.109.0,0.110.0,0.113.0,0.114.0"

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

// documentDBVersionChain returns the ascending list of published DocumentDB
// versions used by the multi-version upgrade specs, read from
// envDocumentDBVersionChain (comma-separated) or falling back to
// defaultDocumentDBVersionChain. Specs that need more entries than are
// configured should Skip rather than fabricate versions.
func documentDBVersionChain() []string {
	raw := envOr(envDocumentDBVersionChain, defaultDocumentDBVersionChain)
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// majorMinor splits a "Major.Minor.Patch" version string into its numeric major
// and minor components, reporting ok=false when either cannot be parsed.
func majorMinor(version string) (major, minor int, ok bool) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// compareMajorMinor compares two "Major.Minor.Patch" versions on their major and
// minor components only, returning -1, 0 or 1. Unparseable input yields 0 so
// callers gate conservatively rather than acting on a bogus ordering.
func compareMajorMinor(a, b string) int {
	aMajor, aMinor, aOK := majorMinor(a)
	bMajor, bMinor, bOK := majorMinor(b)
	if !aOK || !bOK {
		return 0
	}
	switch {
	case aMajor != bMajor:
		if aMajor < bMajor {
			return -1
		}
		return 1
	case aMinor != bMinor:
		if aMinor < bMinor {
			return -1
		}
		return 1
	default:
		return 0
	}
}

// minorDistance reports how many minors separate two versions, counting a major
// bump as unbounded distance so a chain crossing a major is never mistaken for a
// narrow jump. Returns -1 when either version cannot be parsed.
func minorDistance(from, to string) int {
	fromMajor, fromMinor, fromOK := majorMinor(from)
	toMajor, toMinor, toOK := majorMinor(to)
	if !fromOK || !toOK {
		return -1
	}
	if toMajor != fromMajor {
		return math.MaxInt32
	}
	return toMinor - fromMinor
}

// majorMinorOf returns the "Major.Minor" prefix of a "Major.Minor.Patch"
// version string (e.g. "0.109.0" → "0.109"). Returns the input unchanged when
// it has fewer than two components.
func majorMinorOf(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

// schemaUpgradeBlockedGetter returns a poll function reporting the DocumentDB's
// SchemaUpgradeBlocked condition, or nil when the condition is absent. A fetch
// error yields nil so Eventually keeps polling rather than failing outright.
func schemaUpgradeBlockedGetter(
	ctx context.Context,
	c client.Client,
	key types.NamespacedName,
) func() *metav1.Condition {
	return func() *metav1.Condition {
		dd, err := shareddb.Get(ctx, c, key)
		if err != nil {
			return nil
		}
		for i := range dd.Status.Conditions {
			if dd.Status.Conditions[i].Type == previewv1.ConditionSchemaUpgradeBlocked {
				return &dd.Status.Conditions[i]
			}
		}
		return nil
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
