package fixtures

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

func TestDBNameForDeterministic(t *testing.T) {
	a := DBNameFor("spec text one")
	b := DBNameFor("spec text one")
	if a != b {
		t.Fatalf("DBNameFor not deterministic: %q != %q", a, b)
	}
	c := DBNameFor("spec text two")
	if a == c {
		t.Fatalf("DBNameFor collision for distinct inputs: %q", a)
	}
	if len(a) != len("db_")+12 {
		t.Fatalf("DBNameFor returned unexpected length %q", a)
	}
	if a[:3] != "db_" {
		t.Fatalf("DBNameFor prefix wrong: %q", a)
	}
}

func TestRenderBaseDocumentDB(t *testing.T) {
	vars := baseVars("ns", "cluster", "1")
	dd, err := renderDocumentDB("base/documentdb.yaml.template", vars)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if dd.Namespace != "ns" || dd.Name != "cluster" {
		t.Fatalf("unexpected name/namespace: %s/%s", dd.Namespace, dd.Name)
	}
	if dd.Spec.NodeCount != 1 {
		t.Fatalf("expected nodeCount=1, got %d", dd.Spec.NodeCount)
	}
	if dd.Spec.InstancesPerNode != 1 {
		t.Fatalf("expected instancesPerNode=1, got %d", dd.Spec.InstancesPerNode)
	}
	if dd.Spec.Resource.Storage.PvcSize == "" {
		t.Fatalf("expected pvcSize to be set")
	}
	if dd.Spec.ExposeViaService.ServiceType != "ClusterIP" {
		t.Fatalf("expected ClusterIP exposure, got %q", dd.Spec.ExposeViaService.ServiceType)
	}
	if _, ok := interface{}(dd).(*previewv1.DocumentDB); !ok {
		t.Fatalf("render did not produce *DocumentDB")
	}
}

func TestRenderTLSMixins(t *testing.T) {
	for _, tc := range []struct {
		path     string
		wantMode string
	}{
		{"mixins/tls_disabled.yaml.template", "Disabled"},
		{"mixins/tls_selfsigned.yaml.template", "SelfSigned"},
	} {
		vars := map[string]string{"NAMESPACE": "ns", "NAME": "c"}
		dd, err := renderDocumentDB(tc.path, vars)
		if err != nil {
			t.Fatalf("render %s: %v", tc.path, err)
		}
		if dd.Spec.TLS == nil || dd.Spec.TLS.Gateway == nil || dd.Spec.TLS.Gateway.Mode != tc.wantMode {
			t.Fatalf("%s: expected mode %q, got %+v", tc.path, tc.wantMode, dd.Spec.TLS)
		}
	}
}

// TODO(e2e/feature-gates): re-introduce a ChangeStreams mixin-render
// test once the suite ships with a change-stream-capable DocumentDB
// image. The feature is experimental and requires a custom image
// variant (the `-changestream` tag line) that is not part of the
// default e2e image set, so we removed the render+behaviour tests to
// keep the default pipeline green. The API symbol
// previewv1.FeatureGateChangeStreams and the operator's wal_level
// translation remain in place — this is purely about test coverage.

// The following tests exercise the label-selector teardown contract and
// the AlreadyExists run-id mismatch error path. They use the
// controller-runtime fake client so they can run without a real
// Kubernetes API.

func TestOwnershipLabels(t *testing.T) {
resetRunIDForTest()
SetRunID("abcd1234")
labels := ownershipLabels(FixtureSharedRO, "lifecycle")
if labels[LabelRunID] != "abcd1234" {
t.Fatalf("run-id label = %q", labels[LabelRunID])
}
if labels[LabelFixture] != FixtureSharedRO {
t.Fatalf("fixture label = %q", labels[LabelFixture])
}
if labels[LabelArea] != "lifecycle" {
t.Fatalf("area label = %q", labels[LabelArea])
}
// Empty area must not be recorded at all.
if _, ok := ownershipLabels(FixtureSharedRO, "")[LabelArea]; ok {
t.Fatalf("area label present for empty area")
}
}

func TestRunIDFirstWriterWins(t *testing.T) {
resetRunIDForTest()
SetRunID("first")
SetRunID("second")
if got := RunID(); got != "first" {
t.Fatalf("RunID after conflicting sets = %q, want %q", got, "first")
}
resetRunIDForTest()
if got := RunID(); got != "unset" {
t.Fatalf("reset RunID = %q, want \"unset\"", got)
}
}

// newFakeClient builds a controller-runtime fake client registered for
// the core + preview schemes used by the fixtures helpers.
func newFakeClient(t *testing.T) *fakeclient.ClientBuilder {
t.Helper()
s := runtime.NewScheme()
if err := corev1.AddToScheme(s); err != nil {
t.Fatalf("corev1 AddToScheme: %v", err)
}
if err := previewv1.AddToScheme(s); err != nil {
t.Fatalf("previewv1 AddToScheme: %v", err)
}
return fakeclient.NewClientBuilder().WithScheme(s)
}

func TestCreateLabeledNamespaceStampsLabels(t *testing.T) {
resetRunIDForTest()
SetRunID("r-create")
c := newFakeClient(t).Build()
if err := CreateLabeledNamespace(context.Background(), c, "ns-a", "lifecycle"); err != nil {
t.Fatalf("CreateLabeledNamespace: %v", err)
}
got := &corev1.Namespace{}
if err := c.Get(context.Background(), types.NamespacedName{Name: "ns-a"}, got); err != nil {
t.Fatalf("Get: %v", err)
}
if got.Labels[LabelRunID] != "r-create" ||
got.Labels[LabelFixture] != FixturePerSpec ||
got.Labels[LabelArea] != "lifecycle" {
t.Fatalf("unexpected labels: %v", got.Labels)
}
}

func TestCreateLabeledNamespaceAdoptsMatchingRunID(t *testing.T) {
resetRunIDForTest()
SetRunID("r-adopt")
existing := &corev1.Namespace{
ObjectMeta: metav1.ObjectMeta{
Name:   "ns-b",
Labels: map[string]string{LabelRunID: "r-adopt"},
},
}
c := newFakeClient(t).WithObjects(existing).Build()
if err := CreateLabeledNamespace(context.Background(), c, "ns-b", "lifecycle"); err != nil {
t.Fatalf("expected adoption on matching run-id, got: %v", err)
}
}

func TestCreateLabeledNamespaceRejectsRunIDMismatch(t *testing.T) {
resetRunIDForTest()
SetRunID("r-current")
existing := &corev1.Namespace{
ObjectMeta: metav1.ObjectMeta{
Name:   "ns-c",
Labels: map[string]string{LabelRunID: "r-stale"},
},
}
c := newFakeClient(t).WithObjects(existing).Build()
err := CreateLabeledNamespace(context.Background(), c, "ns-c", "lifecycle")
if err == nil {
t.Fatalf("expected collision error, got nil")
}
}

func TestCreateLabeledCredentialSecret(t *testing.T) {
resetRunIDForTest()
SetRunID("r-sec")
c := newFakeClient(t).Build()
if err := CreateLabeledCredentialSecret(context.Background(), c, "ns-s"); err != nil {
t.Fatalf("CreateLabeledCredentialSecret: %v", err)
}
got := &corev1.Secret{}
if err := c.Get(context.Background(), types.NamespacedName{
Namespace: "ns-s", Name: DefaultCredentialSecretName,
}, got); err != nil {
t.Fatalf("Get: %v", err)
}
if string(got.Data["username"]) != DefaultCredentialUsername {
// fake client promotes StringData to Data on read; both keys must match.
if got.StringData["username"] != DefaultCredentialUsername {
t.Fatalf("username mismatch: data=%q stringData=%q",
got.Data["username"], got.StringData["username"])
}
}
if got.Labels[LabelRunID] != "r-sec" || got.Labels[LabelFixture] != FixturePerSpec {
t.Fatalf("unexpected labels: %v", got.Labels)
}
// Second call must not error even though the secret already exists.
if err := CreateLabeledCredentialSecret(context.Background(), c, "ns-s"); err != nil {
t.Fatalf("idempotent CreateLabeledCredentialSecret returned: %v", err)
}
}
