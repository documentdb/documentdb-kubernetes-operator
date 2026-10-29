// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func TestDocumentDBNamingMatchesWireContract(t *testing.T) {
	n := DocumentDBProfile().Naming
	for got, want := range map[string]string{
		n.ClusterLabel():            util.LabelCluster,
		n.NamespaceLabel():          util.LabelNamespace,
		n.RecoveryTempLabel():       util.LabelRecoveryTemp,
		n.SchemaVersionAnnotation(): util.AnnotationSchemaVersion,
		n.Finalizer():               "documentdb.io/pv-retention-finalizer",
		n.ServicePrefix:             util.DOCUMENTDB_SERVICE_PREFIX,
		n.Kind:                      "DocumentDB",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if long := n.ServiceName(strings.Repeat("a", 80)); len(long) != 63 {
		t.Errorf("service name not truncated to 63: %d", len(long))
	}
}

func TestServiceNameKeepsLongClusterNamesDistinct(t *testing.T) {
	for _, n := range []NamingProfile{DocumentDBProfile().Naming, {ServicePrefix: "cosmos-gateway-"}} {
		short := n.ServiceName("db")
		if short != n.ServicePrefix+"db" {
			t.Fatalf("short names must be unchanged, got %q", short)
		}
		if _, ok := n.LegacyServiceName("db"); ok {
			t.Fatal("a name that fits has no legacy form")
		}
		fits := strings.Repeat("f", 63-len(n.ServicePrefix))
		if got := n.ServiceName(fits); got != n.ServicePrefix+fits {
			t.Fatalf("a 63-character name must be unchanged, got %q", got)
		}

		// The names differ only after the 63-character cut, and the cut
		// lands on a hyphen.
		base := strings.Repeat("a", 63-len(n.ServicePrefix)-1)
		east, west := n.ServiceName(base+"-east"), n.ServiceName(base+"-west")
		if east == west {
			t.Fatalf("distinct clusters share Service %q", east)
		}
		for _, got := range []string{east, west} {
			if errs := validation.IsDNS1035Label(got); len(errs) != 0 {
				t.Fatalf("invalid Service name %q: %v", got, errs)
			}
			if !strings.HasPrefix(got, n.ServicePrefix) {
				t.Fatalf("Service name %q lost prefix %q", got, n.ServicePrefix)
			}
		}
		if again := n.ServiceName(base + "-east"); again != east {
			t.Fatalf("Service name is not stable: %q vs %q", east, again)
		}
		if _, ok := n.LegacyServiceName(base + "-east"); ok {
			t.Fatal("plain truncation ending in a hyphen was never a valid Service name")
		}
		if legacy, ok := n.LegacyServiceName(base + "x-east"); !ok || legacy != (n.ServicePrefix+base+"x") {
			t.Fatalf("unexpected legacy name %q, %v", legacy, ok)
		}
	}
}

func TestDocumentDBAdapterStatusRoundTrip(t *testing.T) {
	a := DocumentDBAdapter{}
	db := &dbpreview.DocumentDB{}
	db.Status.TargetPrimary = "keep-me"
	db.Status.LocalPrimary = "keep-me-too"
	want := ClusterStatus{
		Phase: "Cluster in healthy state", ConnectionString: "conn", SchemaVersion: "0.110.0",
		ExtensionImage: "ext:1", GatewayImage: "gw:1", TLS: &dbpreview.TLSStatus{Ready: true},
	}
	a.SetStatus(db, want)
	if got := a.Status(db); got != want {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if db.Status.TargetPrimary != "keep-me" || db.Status.LocalPrimary != "keep-me-too" {
		t.Fatal("SetStatus must not touch product-only status fields")
	}
}

func TestDocumentDBAdapterSpecAndConnectionString(t *testing.T) {
	a := DocumentDBAdapter{}
	db := &dbpreview.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"}}
	db.Spec.ExposeViaService.ServiceType = "LoadBalancer"
	db.Spec.Resource.Storage.StorageClass = "fast"
	db.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: &dbpreview.GatewayTLS{Mode: "Provided"}}
	db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{Recovery: &dbpreview.RecoveryConfiguration{
		PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "pv-1"}}}

	spec := a.Spec(db)
	if spec.ServiceType != "LoadBalancer" || spec.StorageClass != "fast" ||
		spec.GatewayTLS.Mode != "Provided" || spec.RecoveryPersistentVolume != "pv-1" || !spec.RetainsPVs() {
		t.Fatalf("unexpected spec view: %+v", spec)
	}

	if got, want := a.ConnectionString(db, "1.2.3.4", util.GetPortFor(util.GATEWAY_PORT), false),
		util.GenerateConnectionString(db, "1.2.3.4", false); got != want {
		t.Fatalf("connection string drift:\n got %s\nwant %s", got, want)
	}
}
