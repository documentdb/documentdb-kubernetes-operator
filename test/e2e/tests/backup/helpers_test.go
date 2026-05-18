package backup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	bkp "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/backup"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/clusterprobe"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
)

// credentialSecretName is the secret the backup area seeds in every
// source and recovery namespace. Aliased to the fixtures default so
// future credential-name changes stay a single-edit concern.
const credentialSecretName = fixtures.DefaultCredentialSecretName

// baseVars returns the envsubst variable map shared by the
// backup-area specs. It mirrors the lifecycle helper but substitutes
// a CSI-backed StorageClass by default (backups require volume
// snapshots, which stock "standard" classes in CI do not produce).
func baseVars(name, ns, size string) map[string]string {
	// Empty defaults → operator composes CNPG pg18 + extension + gateway.
	// Do NOT fall back GATEWAY_IMAGE to DOCUMENTDB_IMAGE: the gateway is
	// an independent sidecar image, not a monolithic build.
	ddImage := os.Getenv("DOCUMENTDB_IMAGE")
	gwImage := os.Getenv("GATEWAY_IMAGE")
	sc := "csi-hostpath-sc"
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

// createNamespace creates ns via fixtures.CreateLabeledNamespace so
// ownership labels are stamped, and registers a DeferCleanup to remove
// it at spec teardown.
func createNamespace(ctx context.Context, c client.Client, ns string) {
	if err := fixtures.CreateLabeledNamespace(ctx, c, ns, "backup"); err != nil {
		Fail("create namespace " + ns + ": " + err.Error())
	}
	DeferCleanup(func(ctx SpecContext) {
		_ = c.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})
}

// createCredentialSecret seeds the default DocumentDB credential
// secret used by both source and recovery clusters. Delegates to
// fixtures.CreateLabeledCredentialSecret so the secret picks up the
// same ownership labels as the namespace.
func createCredentialSecret(ctx context.Context, c client.Client, ns string) {
	if err := fixtures.CreateLabeledCredentialSecret(ctx, c, ns); err != nil {
		Fail("create credential secret " + ns + "/" + credentialSecretName + ": " + err.Error())
	}
}

// manifestsRoot resolves the absolute path to test/e2e/manifests so
// recovery-template renders and documentdb.Create calls work
// regardless of the current working directory.
func manifestsRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		Fail("runtime.Caller failed — cannot locate test/e2e/manifests")
	}
	// this file: test/e2e/tests/backup/helpers_test.go
	// manifests: test/e2e/manifests/
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "manifests")
}

// createRecoveryDocumentDB renders a flat recovery_* template under
// manifests/backup/ and applies the resulting DocumentDB CR.
func createRecoveryDocumentDB(
	ctx context.Context, c client.Client,
	ns, name, templateName string, extra map[string]string,
) *previewv1.DocumentDB {
	vars := baseVars(name, ns, "")
	for k, v := range extra {
		vars[k] = v
	}
	path := filepath.Join(manifestsRoot(), "backup", templateName)
	raw, err := bkp.RenderTemplateFrom(path, vars)
	Expect(err).NotTo(HaveOccurred(), "render %s", templateName)
	dd := &previewv1.DocumentDB{}
	Expect(yaml.Unmarshal(raw, dd)).To(Succeed(), "unmarshal rendered %s", templateName)
	if dd.Namespace == "" {
		dd.Namespace = ns
	}
	if dd.Name == "" {
		dd.Name = name
	}
	Expect(c.Create(ctx, dd)).To(Succeed(), "create recovery DocumentDB %s/%s", ns, name)
	return dd
}

// skipUnlessCSISnapshotsUsable is the pre-flight for every backup
// spec. The Ginkgo NeedsCSISnapshotsLabel only filters invocations;
// this probe additionally verifies that the VolumeSnapshot CRD is
// installed and that at least one VolumeSnapshotClass exists at spec
// runtime. It Skips — rather than Fails — so running the full suite
// on a cluster without CSI snapshot support produces a clear,
// actionable message instead of deep backup-controller errors.
func skipUnlessCSISnapshotsUsable(ctx context.Context, c client.Client) {
	hasCRD, err := clusterprobe.HasVolumeSnapshotCRD(ctx, c)
	Expect(err).NotTo(HaveOccurred(), "probe VolumeSnapshot CRD")
	if !hasCRD {
		Skip("VolumeSnapshot CRD not installed — backup specs require the external snapshotter")
	}
	hasClass, err := clusterprobe.HasUsableSnapshotClass(ctx, c)
	Expect(err).NotTo(HaveOccurred(), "probe VolumeSnapshotClass")
	if !hasClass {
		Skip("no VolumeSnapshotClass found — backup specs require at least one CSI snapshot class")
	}
}
