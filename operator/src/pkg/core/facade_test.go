// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	utils "github.com/documentdb/documentdb-operator/internal/utils"
	"github.com/documentdb/documentdb-operator/pkg/core"
)

// A second product's admission webhook validates its custom resource through
// the facade alone.
func TestFacadeAdmissionValidation(t *testing.T) {
	terms := core.ValidationTerms{
		Product:                   "Widget",
		VersionField:              "spec.widgetVersion",
		ImageField:                "spec.image.widget",
		SchemaWithoutBinaryFields: "spec.widgetVersion",
	}

	if got := core.ImageTagSemver("example.com/widget:1.2.3"); got != "1.2.3" {
		t.Fatalf("ImageTagSemver = %q", got)
	}
	if got := core.ResolveBinaryVersion("example.com/widget@sha256:abc", "0.110.0"); got != "0.110.0" {
		t.Fatalf("digest-only image should fall back to the version field, got %q", got)
	}

	if errs := core.ValidateSchemaVersion("0.111.0", "0.110.0", terms); len(errs) != 1 {
		t.Fatalf("schema above binary should be rejected, got %v", errs)
	}
	if errs := core.ValidateSchemaVersion("0.110.0", "", terms); len(errs) != 1 || !strings.Contains(errs[0].Detail, "spec.widgetVersion") {
		t.Fatalf("schema without a binary should name the product's fields, got %v", errs)
	}
	if errs := core.ValidateImageRollback("0.111.0", "0.110.0"); len(errs) != 1 {
		t.Fatalf("binary below installed schema should be rejected, got %v", errs)
	}
	if errs := core.ValidateImageRollback("0.110.0", "0.111.0"); len(errs) != 0 {
		t.Fatalf("upgrade should be allowed, got %v", errs)
	}
	sizePath := field.NewPath("spec", "storage", "size")
	if errs := core.ValidateStorageResize(sizePath, "10Gi", "5Gi"); len(errs) != 1 || errs[0].Field != "spec.storage.size" {
		t.Fatalf("shrink should be rejected at the product path, got %v", errs)
	}

	base := field.NewPath("spec", "bootstrap", "recovery")
	backupSrc := core.BackupRestoreSource(base, "nightly")
	if backupSrc.FieldPath.String() != "spec.bootstrap.recovery.backup" || backupSrc.Name != "nightly" {
		t.Fatalf("unexpected backup source %+v", backupSrc)
	}
	schema, warnings := core.RestoreSourceSchemaVersion(context.Background(), nil, backupSrc, core.DocumentDBProfile().Naming,
		func() (string, error) { return "0.111.0", nil })
	if schema != "0.111.0" || len(warnings) != 0 {
		t.Fatalf("backup schema = %q, warnings %v", schema, warnings)
	}
	if _, errs := core.ValidateRestoreCompatibility("0.110.0", schema, backupSrc, terms); len(errs) != 1 {
		t.Fatalf("restoring a newer schema should be rejected, got %v", errs)
	}

	naming := core.DocumentDBProfile().Naming
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{
		Name:        "pv-1",
		Annotations: map[string]string{naming.SchemaVersionAnnotation(): "0.109.0"},
	}}
	c := fake.NewClientBuilder().WithObjects(pv).Build()
	pvSrc := core.PVRestoreSource(base, "pv-1")
	schema, warnings = core.RestoreSourceSchemaVersion(context.Background(), c, pvSrc, naming,
		func() (string, error) { return "", errors.New("unused") })
	if schema != "0.109.0" || len(warnings) != 0 {
		t.Fatalf("pv schema = %q, warnings %v", schema, warnings)
	}
	if warnings, errs := core.ValidateRestoreCompatibility("0.110.0", schema, pvSrc, terms); len(errs) != 0 || len(warnings) != 0 {
		t.Fatalf("restoring an older schema should be allowed, got %v %v", warnings, errs)
	}
}

func TestFacadeParametersAndResources(t *testing.T) {
	intent := core.ClusterIntent{
		Product: core.ProductProfile{Postgres: core.PostgresProfile{
			ProtectedParameters: map[string]string{"widget.mode": "strict"},
		}},
		Postgres:     core.Postgres{Parameters: map[string]string{"widget.mode": "loose", "work_mem": "8MB"}},
		FeatureGates: core.FeatureGates{ChangeStreams: true},
	}
	protected := core.ProtectedParameters(intent)
	if protected["widget.mode"] != "strict" || protected["wal_level"] != "logical" {
		t.Fatalf("unexpected protected parameters %v", protected)
	}
	merged := core.MergeParameters(intent, 4<<30)
	if merged["widget.mode"] != "strict" || merged["work_mem"] != "8MB" || merged["wal_level"] != "logical" {
		t.Fatalf("user overrides must not beat protected parameters: %v", merged)
	}

	cfg := core.DefaultSplitConfig()
	split := core.ComputeResourceSplit(core.Resources{Memory: "4Gi"}, false, cfg)
	if split.MonitoringEnabled || split.PostgresMemoryBytes <= 0 || split.PostgresMemoryBytes >= 4<<30 || split.Gateway.MemoryLimit == "" {
		t.Fatalf("expected the gateway to be carved out of the envelope, got %+v", split)
	}
	base := field.NewPath("spec", "resources")
	if errs := core.ValidateResources(core.Resources{Memory: "4Gi"}, false, base, cfg); len(errs) != 0 {
		t.Fatalf("valid envelope rejected: %v", errs)
	}
	errs := core.ValidateResources(core.Resources{Memory: "1Gi", Gateway: &core.ComponentResources{Memory: "2Gi"}}, false, base, cfg)
	if len(errs) == 0 || !strings.HasPrefix(errs[0].Field, "spec.resources") {
		t.Fatalf("over-committed envelope should be rejected under the product path, got %v", errs)
	}
}

func TestFacadeExtensionLifecycleHelpers(t *testing.T) {
	if got := core.ExtensionVersionSQL("widget"); !strings.Contains(got, "name = 'widget'") {
		t.Fatalf("ExtensionVersionSQL = %q", got)
	}
	if got := core.ExtensionUpdateSQL("widget", "1.2-3"); got != `ALTER EXTENSION "widget" UPDATE TO '1.2-3'` {
		t.Fatalf("ExtensionUpdateSQL = %q", got)
	}
	if got := core.ReplicationRoleGrantSQL("widget_admin"); got != `GRANT "widget_admin" TO streaming_replica;` {
		t.Fatalf("ReplicationRoleGrantSQL = %q", got)
	}

	cluster := &cnpgv1.Cluster{}
	if core.ExtensionImage(cluster, "widget") != "" || core.GatewayImage(cluster) != "" {
		t.Fatal("expected no images on an empty cluster")
	}
	cluster.Spec.PostgresConfiguration.Extensions = []cnpgv1.ExtensionConfiguration{{
		Name: "widget", ImageVolumeSource: corev1.ImageVolumeSource{Reference: "example.com/widget:1.0"},
	}}
	cluster.Spec.Plugins = []cnpgv1.PluginConfiguration{{
		Name: "sidecar", Parameters: map[string]string{utils.PLUGIN_PARAM_GATEWAY_IMAGE: "example.com/gw:1.0"},
	}}
	if got := core.ExtensionImage(cluster, "widget"); got != "example.com/widget:1.0" {
		t.Fatalf("ExtensionImage = %q", got)
	}
	if got := core.GatewayImage(cluster); got != "example.com/gw:1.0" {
		t.Fatalf("GatewayImage = %q", got)
	}

	if core.ParsePullPolicy("Always") != corev1.PullAlways || core.ParsePullPolicy("sometimes") != "" {
		t.Fatal("ParsePullPolicy should accept only Kubernetes pull policies")
	}
}

func TestFacadeSyncClusterPatchesImages(t *testing.T) {
	current := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default"},
		Spec: cnpgv1.ClusterSpec{Plugins: []cnpgv1.PluginConfiguration{{
			Name: "sidecar", Parameters: map[string]string{utils.PLUGIN_PARAM_GATEWAY_IMAGE: "example.com/gw:1.0"},
		}}},
	}
	desired := current.DeepCopy()
	desired.Spec.Plugins[0].Parameters[utils.PLUGIN_PARAM_GATEWAY_IMAGE] = "example.com/gw:2.0"

	s := runtime.NewScheme()
	if err := cnpgv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(current.DeepCopy()).Build()
	if err := core.SyncCluster(context.Background(), c, current, desired, nil); err != nil {
		t.Fatal(err)
	}
	got := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(current), got); err != nil {
		t.Fatal(err)
	}
	if core.GatewayImage(got) != "example.com/gw:2.0" {
		t.Fatalf("expected the gateway image to be patched, got %q", core.GatewayImage(got))
	}
}
