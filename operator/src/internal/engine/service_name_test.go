// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// sharedPrefix makes "documentdb-service-"+name exceed 63 characters only in
// the trailing "-east"/"-west", which plain truncation used to discard.
var sharedPrefix = strings.Repeat("a", 44)

func longCluster(suffix string, uid types.UID) *dbpreview.DocumentDB {
	db := reconcilableCluster()
	db.Name = sharedPrefix + suffix
	db.UID = uid
	db.Spec.ExposeViaService.ServiceType = "ClusterIP"
	return db
}

func ownedService(name, ip string, owner *dbpreview.DocumentDB) *corev1.Service {
	svc := gatewayService(ip)
	svc.Name = name
	svc.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: "documentdb.io/preview", Kind: "DocumentDB", Name: owner.Name, UID: owner.UID,
		Controller: &[]bool{true}[0],
	}}
	return svc
}

func legacyCert(owner *dbpreview.DocumentDB, dnsNames ...string) *cmapi.Certificate {
	return &cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name: owner.Name + "-gateway-cert", Namespace: testNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "documentdb.io/preview", Kind: "DocumentDB", Name: owner.Name, UID: owner.UID,
				Controller: &[]bool{true}[0],
			}},
		},
		Spec: cmapi.CertificateSpec{SecretName: owner.Name + "-gateway-cert-tls", DNSNames: dnsNames},
	}
}

func TestGatewayServiceNameChoosesOnceAndRecordsIt(t *testing.T) {
	naming := product.DocumentDBProfile().Naming
	key := naming.ServiceNameAnnotation()
	east := longCluster("-east", "east-uid")
	legacy, ok := naming.LegacyServiceName(east.Name)
	if !ok || legacy != naming.ServicePrefix+sharedPrefix {
		t.Fatalf("unexpected legacy name %q, %v", legacy, ok)
	}
	hashed := naming.ServiceName(east.Name)
	otherUID := longCluster("-east", "someone-else")
	ctx := context.Background()

	for _, tt := range []struct {
		name     string
		recorded string
		objs     []client.Object
		want     string
	}{
		{name: "owned legacy Service keeps the legacy name", objs: []client.Object{ownedService(legacy, "10.0.0.1", east)}, want: legacy},
		{name: "owned certificate keeps the legacy name after the Service is gone", objs: []client.Object{legacyCert(east, legacy, legacy+".default")}, want: legacy},
		{name: "legacy Service of another owner is not adopted", objs: []client.Object{ownedService(legacy, "10.0.0.1", otherUID)}, want: hashed},
		{name: "certificate of another owner is not evidence", objs: []client.Object{legacyCert(otherUID, legacy)}, want: hashed},
		{name: "certificate without the legacy name is not evidence", objs: []client.Object{legacyCert(east, hashed)}, want: hashed},
		{name: "no evidence uses the hashed name", want: hashed},
		{name: "recorded legacy name is kept without evidence", recorded: legacy, want: legacy},
		{name: "recorded hashed name is kept despite legacy evidence", recorded: hashed, objs: []client.Object{ownedService(legacy, "10.0.0.1", east)}, want: hashed},
		{name: "unrecognized recorded value is replaced", recorded: "documentdb-service-other", want: hashed},
		{name: "owned certificate does not claim a legacy Service another owner controls", objs: []client.Object{ownedService(legacy, "10.0.0.1", otherUID), legacyCert(east, legacy)}, want: hashed},
		{name: "recorded legacy name is dropped once another owner controls that Service", recorded: legacy, objs: []client.Object{ownedService(legacy, "10.0.0.1", otherUID)}, want: hashed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := east.DeepCopy()
			if tt.recorded != "" {
				db.Annotations = map[string]string{key: tt.recorded}
			}
			c := newFakeClient(t, s, nil, append([]client.Object{db}, tt.objs...)...)
			got, err := gatewayServiceName(ctx, c, naming, db)
			if err != nil || got != tt.want {
				t.Fatalf("gatewayServiceName = %q, %v; want %q", got, err, tt.want)
			}
			stored := &dbpreview.DocumentDB{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(db), stored); err != nil {
				t.Fatal(err)
			}
			if stored.Annotations[key] != tt.want {
				t.Fatalf("recorded %q = %q, want %q", key, stored.Annotations[key], tt.want)
			}
		})
	}

	t.Run("short name is neither looked up nor recorded", func(t *testing.T) {
		s := newTestScheme(t)
		db := testCluster()
		c := newFakeClient(t, s, &interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errInjected
		}}, db)
		if got, err := gatewayServiceName(ctx, c, naming, db); err != nil || got != naming.ServicePrefix+"db" {
			t.Fatalf("gatewayServiceName = %q, %v", got, err)
		}
		if _, ok := db.Annotations[key]; ok {
			t.Fatal("short names must not be recorded")
		}
	})

	t.Run("cert-manager types need not be registered", func(t *testing.T) {
		s := runtime.NewScheme()
		for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, dbpreview.AddToScheme} {
			if err := add(s); err != nil {
				t.Fatal(err)
			}
		}
		db := east.DeepCopy()
		c := fake.NewClientBuilder().WithScheme(s).WithObjects(db).Build()
		if got, err := gatewayServiceName(ctx, c, naming, db); err != nil || got != hashed {
			t.Fatalf("gatewayServiceName = %q, %v; want %q", got, err, hashed)
		}
	})

	t.Run("concurrent choices do not overwrite each other", func(t *testing.T) {
		s := newTestScheme(t)
		db := east.DeepCopy()
		c := newFakeClient(t, s, nil, db)
		stale := &dbpreview.DocumentDB{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(db), stale); err != nil {
			t.Fatal(err)
		}
		// Another reconciler records first.
		if _, err := gatewayServiceName(ctx, c, naming, db); err != nil {
			t.Fatal(err)
		}
		if _, err := gatewayServiceName(ctx, c, naming, stale); !apierrors.IsConflict(err) {
			t.Fatalf("expected a conflict for the stale copy, got %v", err)
		}
	})

	t.Run("lookup and record errors propagate", func(t *testing.T) {
		s := newTestScheme(t)
		failGet := newFakeClient(t, s, &interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errInjected
		}}, east.DeepCopy())
		if _, err := gatewayServiceName(ctx, failGet, naming, east.DeepCopy()); err != errInjected {
			t.Fatalf("expected lookup error, got %v", err)
		}
		failPatch := newFakeClient(t, s, &interceptor.Funcs{Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return errInjected
		}}, east.DeepCopy())
		if _, err := gatewayServiceName(ctx, failPatch, naming, east.DeepCopy()); err != errInjected {
			t.Fatalf("expected record error, got %v", err)
		}
	})
}

// TestLegacyServiceRecreationKeepsCertificateValid is the reviewer's
// scenario: an upgraded long-name cluster has a legacy Service and a managed
// certificate; deleting the Service must not move the cluster to a hostname
// the certificate does not cover.
func TestLegacyServiceRecreationKeepsCertificateValid(t *testing.T) {
	naming := product.DocumentDBProfile().Naming
	east := longCluster("-east", "east-uid")
	legacy, _ := naming.LegacyServiceName(east.Name)
	sans := []string{legacy, legacy + "." + testNamespace, legacy + "." + testNamespace + ".svc"}

	s := newTestScheme(t)
	ctx := context.Background()
	c := newFakeClient(t, s, nil, east, ownedService(legacy, "10.0.0.1", east), legacyCert(east, sans...))
	key := ctrl.Request{NamespacedName: types.NamespacedName{Name: east.Name, Namespace: testNamespace}}
	r, _ := newClusterReconciler(c, s, &sqlRecorder{roleOut: "(1 row)"})
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatal(err)
	}

	// The Service is deleted (by a user or a cleanup tool) and recreated.
	if err := c.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: legacy, Namespace: testNamespace}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newCertReconciler(c, s).Reconcile(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, key); err != nil {
		t.Fatal(err)
	}

	if err := c.Get(ctx, types.NamespacedName{Name: legacy, Namespace: testNamespace}, &corev1.Service{}); err != nil {
		t.Fatalf("expected the Service to be recreated under its recorded name %q: %v", legacy, err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: naming.ServiceName(east.Name), Namespace: testNamespace}, &corev1.Service{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the hostname must not move to the hashed name, got %v", err)
	}
	cert := &cmapi.Certificate{}
	if err := c.Get(ctx, types.NamespacedName{Name: east.Name + "-gateway-cert", Namespace: testNamespace}, cert); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cert.Spec.DNSNames, legacy+"."+testNamespace+".svc") {
		t.Fatalf("certificate %v does not cover the Service hostname", cert.Spec.DNSNames)
	}
}

// TestClusterReconcileLongNamesDoNotShareService reproduces two clusters whose
// names differ only past the 63-character cut: each must publish its own
// Service endpoint, and the pre-existing cluster keeps its legacy Service.
func TestClusterReconcileLongNamesDoNotShareService(t *testing.T) {
	naming := product.DocumentDBProfile().Naming
	east, west := longCluster("-east", "east-uid"), longCluster("-west", "west-uid")
	legacy, _ := naming.LegacyServiceName(east.Name)
	westService := naming.ServiceName(west.Name)
	if westService == legacy || westService == naming.ServiceName(east.Name) {
		t.Fatalf("expected a distinct Service name for %q, got %q", west.Name, westService)
	}

	s := newTestScheme(t)
	ctx := context.Background()
	c := newFakeClient(t, s, nil, east, west,
		ownedService(legacy, "10.0.0.1", east), ownedService(westService, "10.0.0.2", west))
	r, _ := newClusterReconciler(c, s, &sqlRecorder{roleOut: "(1 row)"})

	for _, tt := range []struct {
		db *dbpreview.DocumentDB
		ip string
	}{{db: west, ip: "10.0.0.2"}, {db: east, ip: "10.0.0.1"}} {
		key := ctrl.Request{NamespacedName: types.NamespacedName{Name: tt.db.Name, Namespace: testNamespace}}
		if _, err := r.Reconcile(ctx, key); err != nil {
			t.Fatalf("%s: create reconcile: %v", tt.db.Name, err)
		}
		cluster := &cnpgv1.Cluster{}
		if err := c.Get(ctx, key.NamespacedName, cluster); err != nil {
			t.Fatal(err)
		}
		cluster.Status = cnpgv1.ClusterStatus{
			Phase:           CNPGClusterHealthyPhase,
			CurrentPrimary:  tt.db.Name + "-1",
			InstancesStatus: map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {tt.db.Name + "-1"}},
		}
		if err := c.Update(ctx, cluster); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Reconcile(ctx, key); err != nil {
			t.Fatalf("%s: converge reconcile: %v", tt.db.Name, err)
		}
		got := &dbpreview.DocumentDB{}
		if err := c.Get(ctx, key.NamespacedName, got); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.Status.ConnectionString, tt.ip) {
			t.Fatalf("%s published %q, want its own Service IP %s", tt.db.Name, got.Status.ConnectionString, tt.ip)
		}
	}

	if err := c.Get(ctx, types.NamespacedName{Name: naming.ServiceName(east.Name), Namespace: testNamespace}, &corev1.Service{}); !apierrors.IsNotFound(err) {
		t.Fatalf("existing cluster must keep its legacy Service instead of creating a hashed one, got %v", err)
	}
}

func TestCertificateNamesFollowResolvedService(t *testing.T) {
	naming := product.DocumentDBProfile().Naming
	east, west := longCluster("-east", "east-uid"), longCluster("-west", "west-uid")
	legacy, _ := naming.LegacyServiceName(east.Name)

	for _, tt := range []struct {
		db   *dbpreview.DocumentDB
		want string
	}{{db: east, want: legacy}, {db: west, want: naming.ServiceName(west.Name)}} {
		t.Run(tt.db.Name, func(t *testing.T) {
			s := newTestScheme(t)
			c := newFakeClient(t, s, nil, tt.db, ownedService(legacy, "10.0.0.1", east))
			key := ctrl.Request{NamespacedName: types.NamespacedName{Name: tt.db.Name, Namespace: testNamespace}}
			if _, err := newCertReconciler(c, s).Reconcile(context.Background(), key); err != nil {
				t.Fatal(err)
			}
			cert := &cmapi.Certificate{}
			if err := c.Get(context.Background(), types.NamespacedName{Name: tt.db.Name + "-gateway-cert", Namespace: testNamespace}, cert); err != nil {
				t.Fatal(err)
			}
			want := []string{tt.want, tt.want + "." + testNamespace, tt.want + "." + testNamespace + ".svc"}
			if !slices.Equal(cert.Spec.DNSNames, want) {
				t.Fatalf("DNSNames = %v, want %v", cert.Spec.DNSNames, want)
			}
		})
	}
}

// TestCollidingUpgradedClusterMovesToItsOwnServiceAndCertificate covers two
// long-name clusters that collided on one legacy Service before the upgrade:
// the cluster that does not own that Service moves to its hashed Service and
// its certificate gains the new hostname while keeping the old names.
func TestCollidingUpgradedClusterMovesToItsOwnServiceAndCertificate(t *testing.T) {
	naming := product.DocumentDBProfile().Naming
	east, west := longCluster("-east", "east-uid"), longCluster("-west", "west-uid")
	legacy, _ := naming.LegacyServiceName(west.Name)
	hashed := naming.ServiceName(west.Name)
	legacySANs := []string{legacy, legacy + "." + testNamespace, legacy + "." + testNamespace + ".svc"}

	for _, mode := range []*dbpreview.GatewayTLS{nil, {Mode: "CertManager", CertManager: &dbpreview.CertManagerTLS{IssuerRef: dbpreview.IssuerRef{Name: "ca"}}}} {
		name := "self-signed"
		if mode != nil {
			name = "cert-manager"
		}
		t.Run(name, func(t *testing.T) {
			s := newTestScheme(t)
			ctx := context.Background()
			db := west.DeepCopy()
			if mode != nil {
				db.Spec.TLS = &dbpreview.TLSConfiguration{Gateway: mode}
			}
			cert := legacyCert(db, legacySANs...)
			cert.Status.Conditions = []cmapi.CertificateCondition{{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue}}
			c := newFakeClient(t, s, nil, east, db, ownedService(legacy, "10.0.0.1", east), ownedService(hashed, "10.0.0.2", db), cert)
			key := ctrl.Request{NamespacedName: types.NamespacedName{Name: db.Name, Namespace: testNamespace}}

			res, err := newCertReconciler(c, s).Reconcile(ctx, key)
			if err != nil || res.RequeueAfter != RequeueAfterShort {
				t.Fatalf("expected a short requeue after updating DNS names, got %v, %v", res, err)
			}
			got := &cmapi.Certificate{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(cert), got); err != nil {
				t.Fatal(err)
			}
			want := append(append([]string{}, legacySANs...), hashed, hashed+"."+testNamespace, hashed+"."+testNamespace+".svc")
			if !slices.Equal(got.Spec.DNSNames, want) {
				t.Fatalf("DNSNames = %v, want %v", got.Spec.DNSNames, want)
			}
			if st := getDocumentDBByName(t, c, db.Name).Status.TLS; st == nil || st.Ready || st.Message != "Updating gateway certificate DNS names" {
				t.Fatalf("unexpected TLS status %+v", st)
			}

			// The next pass finds nothing to add.
			if _, err := newCertReconciler(c, s).Reconcile(ctx, key); err != nil {
				t.Fatal(err)
			}
			again := &cmapi.Certificate{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(cert), again); err != nil || !slices.Equal(again.Spec.DNSNames, want) {
				t.Fatalf("DNS names changed again: %v, %v", again.Spec.DNSNames, err)
			}

			r, _ := newClusterReconciler(c, s, &sqlRecorder{roleOut: "(1 row)"})
			if _, err := r.Reconcile(ctx, key); err != nil {
				t.Fatal(err)
			}
			if got := getDocumentDBByName(t, c, db.Name); got.Annotations[naming.ServiceNameAnnotation()] != hashed {
				t.Fatalf("expected %s to record %q, got %v", db.Name, hashed, got.Annotations)
			}
		})
	}
}

func TestCertificateOfAnotherResourceIsNeitherChangedNorUsed(t *testing.T) {
	naming := product.DocumentDBProfile().Naming
	west := longCluster("-west", "west-uid")
	legacy, _ := naming.LegacyServiceName(west.Name)
	// Left by a deleted DocumentDB of the same name and still Ready.
	cert := legacyCert(longCluster("-west", "deleted-uid"), legacy)
	cert.Status.Conditions = []cmapi.CertificateCondition{{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue}}
	s := newTestScheme(t)
	c := newFakeClient(t, s, nil, west, cert)
	key := ctrl.Request{NamespacedName: types.NamespacedName{Name: west.Name, Namespace: testNamespace}}
	res, err := newCertReconciler(c, s).Reconcile(context.Background(), key)
	if err != nil || res.RequeueAfter != RequeueAfterShort {
		t.Fatalf("expected a short requeue, got %v, %v", res, err)
	}
	got := &cmapi.Certificate{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cert), got); err != nil || !slices.Equal(got.Spec.DNSNames, []string{legacy}) {
		t.Fatalf("a certificate this cluster does not control was modified: %v, %v", got.Spec.DNSNames, err)
	}
	if st := getDocumentDBByName(t, c, west.Name).Status.TLS; st == nil || st.Ready || st.SecretName != "" {
		t.Fatalf("another resource's certificate must not be reported ready or used: %+v", st)
	}
}

func TestCertificateReadyMustMatchCurrentGeneration(t *testing.T) {
	s := newTestScheme(t)
	db := testCluster()
	svc := product.DocumentDBProfile().Naming.ServiceName(db.Name)
	cert := legacyCert(db, svc, svc+"."+testNamespace, svc+"."+testNamespace+".svc")
	cert.Generation = 2
	// cert-manager has not yet observed the latest spec (new DNS names).
	cert.Status.Conditions = []cmapi.CertificateCondition{{Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue, ObservedGeneration: 1}}
	c := newFakeClient(t, s, nil, db, cert)
	r := newCertReconciler(c, s)

	if res, err := r.Reconcile(context.Background(), certKey); err != nil || res.RequeueAfter != RequeueAfterShort {
		t.Fatalf("expected to wait for the current generation, got %v, %v", res, err)
	}
	if st := getTLSStatus(t, c); st.Ready {
		t.Fatalf("a Ready condition from an older generation must not report TLS ready: %+v", st)
	}

	got := &cmapi.Certificate{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(cert), got); err != nil {
		t.Fatal(err)
	}
	got.Status.Conditions[0].ObservedGeneration = 2
	if err := c.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	if res, err := r.Reconcile(context.Background(), certKey); err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected ready, got %v, %v", res, err)
	}
	if st := getTLSStatus(t, c); !st.Ready {
		t.Fatalf("expected TLS ready once the current generation is observed: %+v", st)
	}
}

func getDocumentDBByName(t *testing.T, c client.Client, name string) *dbpreview.DocumentDB {
	t.Helper()
	db := &dbpreview.DocumentDB{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: testNamespace}, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestStatusRefreshKeepsConcurrentServiceNameChoice(t *testing.T) {
	ctx := context.Background()
	s := newTestScheme(t)
	db := longCluster("-west", "west-uid")
	naming := product.DocumentDBProfile().Naming
	legacy, _ := naming.LegacyServiceName(db.Name)
	hashed := naming.ServiceName(db.Name)
	foreign := ownedService(legacy, "10.0.0.1", longCluster("-east", "east-uid"))
	c := newFakeClient(t, s, nil, db, foreign, legacyCert(db, legacy))
	stale := getDocumentDBByName(t, c, db.Name)
	winner := getDocumentDBByName(t, c, db.Name)
	if _, err := gatewayServiceName(ctx, c, naming, winner); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if err := newCertReconciler(c, s).updateTLSStatus(ctx, stale, func(st *dbpreview.TLSStatus) { st.Ready = false }); err != nil {
		t.Fatal(err)
	}
	if got, err := gatewayServiceName(ctx, c, naming, stale); err != nil || got != hashed {
		t.Fatalf("concurrent choice overwritten: name=%q, err=%v; want %q", got, err, hashed)
	}
}

func TestCertificateDNSPatchRejectsStaleSnapshots(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint("replacement=", replace), func(t *testing.T) {
			ctx := context.Background()
			s := newTestScheme(t)
			db := testCluster()
			cert := legacyCert(db, "original.example.com")
			cert.UID = "original-certificate"
			c := newFakeClient(t, s, nil, db, cert)
			stale := &cmapi.Certificate{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(cert), stale); err != nil {
				t.Fatal(err)
			}
			current := stale.DeepCopy()
			if replace {
				if err := c.Delete(ctx, current); err != nil {
					t.Fatal(err)
				}
				current.UID = "replacement-certificate"
				current.OwnerReferences[0].UID = "another-owner"
				current.ResourceVersion = ""
				current.Spec.DNSNames = []string{"replacement.example.com"}
				if err := c.Create(ctx, current); err != nil {
					t.Fatal(err)
				}
				// The fake tracker reuses its initial version on recreation;
				// advance it to model the API server's distinct replacement RV.
				current.Annotations = map[string]string{"replacement": "true"}
				if err := c.Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			} else {
				current.Spec.DNSNames = append(current.Spec.DNSNames, "concurrent.example.com")
				if err := c.Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			want := slices.Clone(current.Spec.DNSNames)
			if _, _, err := newCertReconciler(c, s).ensureServiceDNSNames(ctx, db, stale, []string{"documentdb-service-db"}); !apierrors.IsConflict(err) {
				t.Fatalf("stale certificate patch must conflict, got %v", err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(cert), current); err != nil || !slices.Equal(current.Spec.DNSNames, want) {
				t.Fatalf("concurrent certificate was changed: %v, %v; want %v", current.Spec.DNSNames, err, want)
			}
		})
	}
}

func TestLostAnnotationRecoversExistingHashedService(t *testing.T) {
	ctx := context.Background()
	db := longCluster("-west", "west-uid")
	naming := product.DocumentDBProfile().Naming
	legacy, _ := naming.LegacyServiceName(db.Name)
	hashed := naming.ServiceName(db.Name)
	c := newFakeClient(t, newTestScheme(t), nil, db,
		ownedService(hashed, "10.0.0.2", db), legacyCert(db, legacy, hashed))
	if got, err := gatewayServiceName(ctx, c, naming, db); err != nil || got != hashed {
		t.Fatalf("existing hashed endpoint abandoned after annotation loss: %q, %v", got, err)
	}
}

func TestMigrationHoldsEndpointUntilCertificateIssued(t *testing.T) {
	ctx := context.Background()
	s := newTestScheme(t)
	east, west := longCluster("-east", "east-uid"), longCluster("-west", "west-uid")
	naming := product.DocumentDBProfile().Naming
	legacy, _ := naming.LegacyServiceName(west.Name)
	hashed := naming.ServiceName(west.Name)
	cert := legacyCert(west, legacy, legacy+".default", legacy+".default.svc")
	cert.Generation = 1
	cert.Status.Conditions = []cmapi.CertificateCondition{{
		Type: cmapi.CertificateConditionReady, Status: cmmeta.ConditionTrue, ObservedGeneration: 1,
	}}
	west.Status.TLS = &dbpreview.TLSStatus{Ready: true, SecretName: cert.Spec.SecretName}
	c := newFakeClient(t, s, nil, east, west, cert,
		ownedService(legacy, "10.0.0.1", east), ownedService(hashed, "10.0.0.2", west))
	key := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(west)}
	r, _ := newClusterReconciler(c, s, &sqlRecorder{roleOut: "(1 row)"})
	assertEndpoint := func(enabled bool) {
		t.Helper()
		if _, err := r.Reconcile(ctx, key); err != nil {
			t.Fatal(err)
		}
		svc := &corev1.Service{}
		if err := c.Get(ctx, types.NamespacedName{Name: hashed, Namespace: testNamespace}, svc); err != nil {
			t.Fatal(err)
		}
		if got := svc.Spec.Selector["app"] == west.Name; got != enabled {
			t.Fatalf("endpoint enabled=%v, want %v; selector=%v", got, enabled, svc.Spec.Selector)
		}
	}
	assertEndpoint(false)
	if _, err := newCertReconciler(c, s).Reconcile(ctx, key); err != nil {
		t.Fatal(err)
	}
	updated := &cmapi.Certificate{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(cert), updated); err != nil {
		t.Fatal(err)
	}
	updated.Generation = 2
	if err := c.Update(ctx, updated); err != nil {
		t.Fatal(err)
	}
	assertEndpoint(false)
	updated.Status.Conditions[0].ObservedGeneration = 2
	if err := c.Update(ctx, updated); err != nil {
		t.Fatal(err)
	}
	assertEndpoint(true)
}
