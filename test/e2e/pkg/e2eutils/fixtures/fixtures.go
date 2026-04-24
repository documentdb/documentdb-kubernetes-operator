// Package fixtures provides session-scoped test fixtures shared across
// DocumentDB e2e test areas. Two cluster fixtures are supported:
//
//   - SharedRO: a 1-instance read-only DocumentDB reused by data/,
//     performance/ and status/ specs. Specs isolate via per-spec Mongo
//     database names (see DBNameFor).
//   - SharedScale: a 2-instance mutable DocumentDB reused by scale/
//     specs. Callers must call ResetToTwoInstances in AfterEach.
//
// Both fixtures are created lazily via sync.Once guards and torn down
// explicitly from the area suite_test.go AfterSuite.
//
// Ownership labels (LabelRunID, LabelFixture, LabelArea) are stamped on
// every namespace and CR fixtures create so TeardownSharedRO /
// TeardownSharedScale can list-by-label instead of delete-by-name —
// that avoids cross-binary teardown collisions described in the Phase 1
// rubber-duck review.
package fixtures

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/envsubst"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"

	documentdbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
)

// Ownership label keys stamped on every fixture-created namespace and
// DocumentDB CR. Exported so parallel tooling (CI cleanup scripts,
// kubectl one-liners) can use the same selectors.
const (
	LabelRunID   = "e2e.documentdb.io/run-id"
	LabelFixture = "e2e.documentdb.io/fixture"
	LabelArea    = "e2e.documentdb.io/area"
)

// Fixture kind label values.
const (
	FixtureSharedRO    = "shared-ro"
	FixtureSharedScale = "shared-scale"
	// FixturePerSpec is stamped on namespaces and secrets created per
	// individual spec (i.e. not shared across specs). Area-specific
	// helpers_test.go files use this value via CreateLabeledNamespace.
	FixturePerSpec = "per-spec"
)

// DefaultCredentialPassword / DefaultCredentialUsername expose the seed
// credentials used by both shared and per-spec fixture secrets. Area
// helpers_test.go files import these instead of re-declaring string
// literals; that way a credential change ripples out in one edit.
const (
	DefaultCredentialPassword = defaultCredentialPassword
	DefaultCredentialUsername = defaultCredentialUsername
)

// procID returns the Ginkgo parallel process identifier as a string,
// falling back to "1" when unset. This lets per-process fixtures coexist
// safely in a single kind cluster during ginkgo -p runs.
func procID() string {
	if v, ok := os.LookupEnv("GINKGO_PARALLEL_PROCESS"); ok && v != "" {
		return v
	}
	return "1"
}

// runIDMu guards runIDVal. fixtures cannot import the parent e2e
// package (it would create an import cycle); instead the root suite
// calls SetRunID once during SetupSuite.
var (
	runIDMu  sync.RWMutex
	runIDVal string
)

// SetRunID records the suite-wide run identifier. Call exactly once
// from the root suite.go after resolving the identifier from the
// environment. Subsequent calls with the same value are no-ops; calls
// with a different non-empty value are ignored (first-writer-wins) to
// keep fixture naming stable if a worker races with the primary node.
func SetRunID(id string) {
	if id == "" {
		return
	}
	runIDMu.Lock()
	defer runIDMu.Unlock()
	if runIDVal == "" {
		runIDVal = id
	}
}

// RunID returns the identifier previously recorded by SetRunID, or
// "unset" if SetRunID was never called. The fallback exists so unit
// tests that exercise fixture helpers directly still produce valid
// Kubernetes names; production code paths always call SetRunID first.
func RunID() string {
	runIDMu.RLock()
	defer runIDMu.RUnlock()
	if runIDVal == "" {
		return "unset"
	}
	return runIDVal
}

// resetRunIDForTest clears the cached run id for unit tests.
func resetRunIDForTest() {
	runIDMu.Lock()
	defer runIDMu.Unlock()
	runIDVal = ""
}

// defaultCredentialSecretName is the credential secret created alongside
// every shared fixture cluster. Tests read these credentials through
// pkg/e2eutils/mongo helpers.
const defaultCredentialSecretName = "documentdb-credentials"

// DefaultCredentialSecretName is the exported alias of the credential
// secret name created by the shared fixtures. Exported so cross-package
// helpers (e.g., pkg/e2eutils/mongo) can discover the secret without
// duplicating the string literal.
const DefaultCredentialSecretName = defaultCredentialSecretName

// defaultCredentialUsername / defaultCredentialPassword are the seed
// credentials stamped into the per-fixture credential secret.
const (
	defaultCredentialUsername = "e2e_admin"
	defaultCredentialPassword = "E2eAdmin100" //nolint:gosec // fixture-only
)

// defaultDocumentDBImage / defaultGatewayImage are empty by default so
// the operator composes the cluster itself: CNPG pg18 base image +
// DocumentDB extension via the image-library mechanism + gateway as a
// separate sidecar image. Setting a single monolithic image here would
// make CNPG run the wrong container for postgres. CI pins real images
// via DOCUMENTDB_IMAGE / GATEWAY_IMAGE environment variables.
const (
	defaultDocumentDBImage = ""
	defaultGatewayImage    = ""
)

// defaultStorageSize / defaultStorageClass are conservative defaults
// used by both shared fixtures. Override via E2E_STORAGE_SIZE /
// E2E_STORAGE_CLASS environment variables when targeting non-kind
// clusters.
const (
	defaultStorageSize  = "1Gi"
	defaultStorageClass = "standard"
)

// defaultFixtureCreateTimeout / defaultFixtureDeleteTimeout / defaultPollInterval
// bound waits performed inside this package. They intentionally do not
// depend on the sibling timeouts package so that fixture setup is not
// delayed by a missing helper.
const (
	defaultFixtureCreateTimeout = 10 * time.Minute
	defaultFixtureDeleteTimeout = 5 * time.Minute
	defaultPollInterval         = 5 * time.Second
)

// manifestsDir returns the absolute path to the test/e2e/manifests
// directory regardless of the caller's working directory. It relies on
// runtime.Caller to anchor off this source file.
func manifestsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed while locating manifests")
	}
	// this file lives at test/e2e/pkg/e2eutils/fixtures/<file>.go — walk up
	// four dirs to reach test/e2e, then descend into manifests/.
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "manifests"), nil
}

// renderTemplate applies envsubst to the template at path relative to
// manifestsDir() and unmarshals the result into a DocumentDB CR.
func renderDocumentDB(relPath string, vars map[string]string) (*previewv1.DocumentDB, error) {
	root, err := manifestsDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		return nil, fmt.Errorf("reading template %s: %w", relPath, err)
	}
	rendered, err := envsubst.Envsubst(vars, documentdbutil.DropEmptyVarLines(data, vars))
	if err != nil {
		return nil, fmt.Errorf("envsubst on %s: %w", relPath, err)
	}
	out := &previewv1.DocumentDB{}
	if err := yaml.Unmarshal(rendered, out); err != nil {
		return nil, fmt.Errorf("unmarshal rendered %s: %w", relPath, err)
	}
	return out, nil
}

// ownershipLabels returns the canonical ownership labels applied to
// every fixture-created object. area may be empty when the caller is a
// cross-area helper.
func ownershipLabels(fixture, area string) map[string]string {
	l := map[string]string{
		LabelRunID:   RunID(),
		LabelFixture: fixture,
	}
	if area != "" {
		l[LabelArea] = area
	}
	return l
}

// ensureNamespace creates the namespace if it is missing and stamps the
// ownership labels onto it. If the namespace already exists its labels
// are validated: a mismatched LabelRunID returns a collision error.
func ensureNamespace(ctx context.Context, c client.Client, name, fixture string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: ownershipLabels(fixture, ""),
		},
	}
	err := c.Create(ctx, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %s: %w", name, err)
	}
	existing := &corev1.Namespace{}
	if getErr := c.Get(ctx, types.NamespacedName{Name: name}, existing); getErr != nil {
		return fmt.Errorf("get existing namespace %s: %w", name, getErr)
	}
	if got := existing.Labels[LabelRunID]; got != RunID() {
		return fmt.Errorf("fixture collision: namespace %s exists with run-id=%q (current run-id=%q)",
			name, got, RunID())
	}
	if got := existing.Labels[LabelFixture]; got != "" && got != fixture {
		return fmt.Errorf("fixture collision: namespace %s exists with fixture=%q (want %q)",
			name, got, fixture)
	}
	return nil
}

// ensureCredentialSecret creates the fixture credential secret if it is
// missing. The secret schema matches the DocumentDB operator's contract
// (keys "username" and "password").
func ensureCredentialSecret(ctx context.Context, c client.Client, namespace, name, fixture string) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    ownershipLabels(fixture, ""),
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": defaultCredentialUsername,
			"password": defaultCredentialPassword,
		},
	}
	if err := c.Create(ctx, sec); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create credential secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// CreateLabeledNamespace creates a per-spec namespace stamped with the
// suite run-id, fixture=per-spec, and the caller-supplied area label.
// It is the exported entry point that area helpers_test.go files call
// in BeforeEach; the labels let CI cleanup scripts reap orphaned
// namespaces by selector even when a spec panics before AfterEach.
//
// Semantics on AlreadyExists mirror ensureNamespace: an existing
// namespace with the current run-id (or no run-id label) is adopted; a
// mismatched run-id is a collision and returns an error.
func CreateLabeledNamespace(ctx context.Context, c client.Client, name, area string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: ownershipLabels(FixturePerSpec, area),
		},
	}
	err := c.Create(ctx, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %s: %w", name, err)
	}
	existing := &corev1.Namespace{}
	if getErr := c.Get(ctx, types.NamespacedName{Name: name}, existing); getErr != nil {
		return fmt.Errorf("get existing namespace %s: %w", name, getErr)
	}
	if got := existing.Labels[LabelRunID]; got != "" && got != RunID() {
		return fmt.Errorf("fixture collision: namespace %s exists with run-id=%q (current run-id=%q)",
			name, got, RunID())
	}
	return nil
}

// CreateLabeledCredentialSecret creates the default DocumentDB
// credential secret (DefaultCredentialSecretName) in namespace with the
// same labels CreateLabeledNamespace stamps. Idempotent: an existing
// secret is treated as success regardless of label state, matching the
// contract of ensureCredentialSecret used by shared fixtures.
func CreateLabeledCredentialSecret(ctx context.Context, c client.Client, namespace string) error {
	return ensureCredentialSecret(ctx, c, namespace, defaultCredentialSecretName, FixturePerSpec)
}

// baseVars returns the envsubst variable map shared by both fixtures.
func baseVars(namespace, name, instances string) map[string]string {
	documentdbImage := defaultDocumentDBImage
	if v := os.Getenv("DOCUMENTDB_IMAGE"); v != "" {
		documentdbImage = v
	}
	gatewayImage := defaultGatewayImage
	if v := os.Getenv("GATEWAY_IMAGE"); v != "" {
		gatewayImage = v
	}
	storageSize := defaultStorageSize
	if v := os.Getenv("E2E_STORAGE_SIZE"); v != "" {
		storageSize = v
	}
	storageClass := defaultStorageClass
	if v := os.Getenv("E2E_STORAGE_CLASS"); v != "" {
		storageClass = v
	}
	return map[string]string{
		"NAMESPACE":         namespace,
		"NAME":              name,
		"INSTANCES":         instances,
		"STORAGE_SIZE":      storageSize,
		"STORAGE_CLASS":     storageClass,
		"DOCUMENTDB_IMAGE":  documentdbImage,
		"GATEWAY_IMAGE":     gatewayImage,
		"CREDENTIAL_SECRET": defaultCredentialSecretName,
		"EXPOSURE_TYPE":     "ClusterIP",
		"LOG_LEVEL":         "info",
	}
}

// createDocumentDB creates the supplied CR if absent, stamping the
// ownership labels onto it. On AlreadyExists it validates the existing
// CR's run-id label matches the current RunID(); a mismatch returns an
// explicit collision error so the caller can abort rather than adopt a
// foreign fixture.
func createDocumentDB(ctx context.Context, c client.Client, dd *previewv1.DocumentDB, fixture string) error {
	if dd.Labels == nil {
		dd.Labels = map[string]string{}
	}
	for k, v := range ownershipLabels(fixture, "") {
		if _, present := dd.Labels[k]; !present {
			dd.Labels[k] = v
		}
	}
	err := c.Create(ctx, dd)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create documentdb %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	existing := &previewv1.DocumentDB{}
	key := types.NamespacedName{Namespace: dd.Namespace, Name: dd.Name}
	if getErr := c.Get(ctx, key, existing); getErr != nil {
		return fmt.Errorf("get existing documentdb %s: %w", key, getErr)
	}
	if got := existing.Labels[LabelRunID]; got != RunID() {
		return fmt.Errorf("fixture collision: existing CR %s/%s belongs to run %q (current %q)",
			dd.Namespace, dd.Name, got, RunID())
	}
	if got := existing.Labels[LabelFixture]; got != "" && got != fixture {
		return fmt.Errorf("fixture collision: existing CR %s/%s has fixture=%q (want %q)",
			dd.Namespace, dd.Name, got, fixture)
	}
	return nil
}

// waitDocumentDBHealthy polls the DocumentDB CR until its status
// reports the canonical healthy string used by the operator and CI.
func waitDocumentDBHealthy(ctx context.Context, c client.Client, namespace, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, defaultPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		dd := &previewv1.DocumentDB{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, dd); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return dd.Status.Status == documentdbutil.ReadyStatus, nil
	})
}

// deleteDocumentDB deletes the DocumentDB CR and waits for it to be
// fully removed.
func deleteDocumentDB(ctx context.Context, c client.Client, namespace, name string, timeout time.Duration) error {
	dd := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := c.Delete(ctx, dd); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete documentdb %s/%s: %w", namespace, name, err)
	}
	return wait.PollUntilContextTimeout(ctx, defaultPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &previewv1.DocumentDB{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

// deleteNamespace deletes the namespace and waits for termination. Used
// from fixture teardown.
func deleteNamespace(ctx context.Context, c client.Client, name string, timeout time.Duration) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete namespace %s: %w", name, err)
	}
	return wait.PollUntilContextTimeout(ctx, defaultPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, types.NamespacedName{Name: name}, &corev1.Namespace{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

// teardownFixtureByLabels performs a label-selector-driven teardown of
// all resources owned by the current RunID() for the given fixture. It
// first deletes any matching DocumentDB CRs (waiting for finalizers),
// then deletes matching namespaces. Callers must pass the same fixture
// constant they used when creating the resources.
func teardownFixtureByLabels(ctx context.Context, c client.Client, fixture string) error {
	sel := client.MatchingLabels{
		LabelRunID:   RunID(),
		LabelFixture: fixture,
	}
	// Step 1: delete DocumentDB CRs cluster-wide.
	dds := &previewv1.DocumentDBList{}
	if err := c.List(ctx, dds, sel); err != nil {
		return fmt.Errorf("list %s DocumentDB CRs: %w", fixture, err)
	}
	for i := range dds.Items {
		dd := &dds.Items[i]
		if err := deleteDocumentDB(ctx, c, dd.Namespace, dd.Name, defaultFixtureDeleteTimeout); err != nil {
			return fmt.Errorf("delete %s DocumentDB %s/%s: %w", fixture, dd.Namespace, dd.Name, err)
		}
	}
	// Step 2: delete namespaces.
	nss := &corev1.NamespaceList{}
	if err := c.List(ctx, nss, sel); err != nil {
		return fmt.Errorf("list %s namespaces: %w", fixture, err)
	}
	for i := range nss.Items {
		ns := &nss.Items[i]
		if err := deleteNamespace(ctx, c, ns.Name, defaultFixtureDeleteTimeout); err != nil {
			return fmt.Errorf("delete %s namespace %s: %w", fixture, ns.Name, err)
		}
	}
	return nil
}

// DBNameFor returns a deterministic Mongo database name derived from
// the supplied spec text (typically ginkgo's CurrentSpecReport().FullText()).
// The returned string matches "db_<hex12>" and is safe for Mongo.
func DBNameFor(specText string) string {
	sum := sha256.Sum256([]byte(specText))
	return "db_" + hex.EncodeToString(sum[:])[:12]
}
