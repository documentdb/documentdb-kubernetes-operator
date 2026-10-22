// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

type clusterReconciler = ClusterReconciler[*dbpreview.DocumentDB]

var clusterKey = ctrl.Request{NamespacedName: types.NamespacedName{Name: "db", Namespace: testNamespace}}

var finalizer = product.DocumentDBProfile().Naming.Finalizer()

const extensionVersionsUpToDate = " default_version | installed_version\n" +
	"-----------------+-------------------\n" +
	" 0.110-0         | 0.110-0\n(1 row)"

// sqlRecorder is a scripted SQLExecutor that records the statements it runs.
type sqlRecorder struct {
	mu       sync.Mutex
	commands []string
	roleOut  string
	roleErr  error
	grantErr error
	extErr   error
}

func (s *sqlRecorder) exec(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, sql)
	switch {
	case strings.Contains(sql, "pg_available_extensions"):
		return extensionVersionsUpToDate, s.extErr
	case strings.HasPrefix(strings.TrimSpace(strings.ToUpper(sql)), "GRANT"):
		return "", s.grantErr
	case strings.Contains(sql, "documentdb_admin_role"):
		return s.roleOut, s.roleErr
	}
	return "", nil
}

func (s *sqlRecorder) ran(substr string) bool {
	for _, c := range s.commands {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func newClusterReconciler(c client.Client, s *runtime.Scheme, sql *sqlRecorder) (*clusterReconciler, *record.FakeRecorder) {
	rec := record.NewFakeRecorder(20)
	return &clusterReconciler{
		Client:      c,
		Scheme:      s,
		Recorder:    rec,
		SQLExecutor: sql.exec,
		Adapter:     product.DocumentDBAdapter{},
	}, rec
}

// reconcilableCluster returns a DocumentDB with the finalizer already present.
func reconcilableCluster() *dbpreview.DocumentDB {
	db := testCluster()
	db.Finalizers = []string{finalizer}
	db.Spec.InstancesPerNode = 1
	db.Spec.Resource.Storage.PvcSize = "1Gi"
	return db
}

func gatewayService(ip string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: product.DocumentDBProfile().Naming.ServiceName("db"), Namespace: testNamespace},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: ip,
			Ports:     []corev1.ServicePort{{Name: "gateway", Port: 10260}},
		},
	}
}

// healthyCNPGCluster returns a healthy CNPG cluster that already matches the
// images the reconciler renders, so no rollout is needed.
func healthyCNPGCluster(t *testing.T, s *runtime.Scheme, db *dbpreview.DocumentDB) *cnpgv1.Cluster {
	t.Helper()
	seed := db.DeepCopy()
	seed.Spec.ExposeViaService.ServiceType = ""
	c := newFakeClient(t, s, nil, seed)
	r, _ := newClusterReconciler(c, s, &sqlRecorder{})
	if _, err := r.Reconcile(context.Background(), clusterKey); err != nil {
		t.Fatalf("seed reconcile: %v", err)
	}
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), clusterKey.NamespacedName, cluster); err != nil {
		t.Fatalf("seed CNPG cluster: %v", err)
	}
	cluster.ResourceVersion = ""
	cluster.Status = cnpgv1.ClusterStatus{
		Phase:           CNPGClusterHealthyPhase,
		CurrentPrimary:  "db-1",
		InstancesStatus: map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"db-1"}},
	}
	return cluster
}

func getDocumentDB(t *testing.T, c client.Client) *dbpreview.DocumentDB {
	t.Helper()
	db := &dbpreview.DocumentDB{}
	if err := c.Get(context.Background(), clusterKey.NamespacedName, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestClusterReconcileMissingResourceCleansUpRBAC(t *testing.T) {
	s := newTestScheme(t)
	meta := metav1.ObjectMeta{Name: "db", Namespace: testNamespace}
	c := newFakeClient(t, s, nil, &rbacv1.Role{ObjectMeta: meta}, &rbacv1.RoleBinding{ObjectMeta: meta}, &corev1.ServiceAccount{ObjectMeta: meta})
	r, _ := newClusterReconciler(c, s, &sqlRecorder{})
	r.Mutex = &sync.Mutex{}

	if res, err := r.Reconcile(context.Background(), clusterKey); err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected clean no-op, got %v, %v", res, err)
	}
	for _, obj := range []client.Object{&rbacv1.Role{}, &rbacv1.RoleBinding{}, &corev1.ServiceAccount{}} {
		if err := c.Get(context.Background(), clusterKey.NamespacedName, obj); !apierrors.IsNotFound(err) {
			t.Fatalf("expected %T to be cleaned up, got %v", obj, err)
		}
	}
}

func TestClusterCleanupToleratesDeleteFailures(t *testing.T) {
	s := newTestScheme(t)
	meta := metav1.ObjectMeta{Name: "db", Namespace: testNamespace}
	c := newFakeClient(t, s, &interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		return errInjected
	}}, &rbacv1.Role{ObjectMeta: meta}, &rbacv1.RoleBinding{ObjectMeta: meta}, &corev1.ServiceAccount{ObjectMeta: meta})
	r, _ := newClusterReconciler(c, s, &sqlRecorder{})
	if err := r.Cleanup(context.Background(), clusterKey); err != nil {
		t.Fatalf("cleanup must continue past delete failures, got %v", err)
	}
}

func TestClusterReconcileGetError(t *testing.T) {
	s := newTestScheme(t)
	c := newFakeClient(t, s, &interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return errInjected
	}})
	r, _ := newClusterReconciler(c, s, &sqlRecorder{})
	if _, err := r.Reconcile(context.Background(), clusterKey); err != errInjected {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestClusterReconcileFinalizerLifecycle(t *testing.T) {
	t.Run("adds finalizer and requeues", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, testCluster())
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		res, err := r.Reconcile(context.Background(), clusterKey)
		if err != nil || res.IsZero() || res.RequeueAfter != 0 {
			t.Fatalf("expected requeue after adding finalizer, got %v, %v", res, err)
		}
		if got := getDocumentDB(t, c); !slices.Contains(got.Finalizers, finalizer) {
			t.Fatalf("expected finalizer %q, got %v", finalizer, got.Finalizers)
		}
	})

	t.Run("add finalizer update fails", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, &interceptor.Funcs{Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			return errInjected
		}}, testCluster())
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		if _, err := r.Reconcile(context.Background(), clusterKey); err != errInjected {
			t.Fatalf("expected injected error, got %v", err)
		}
	})

	deleting := func(finalizers ...string) *dbpreview.DocumentDB {
		db := testCluster()
		now := metav1.Now()
		db.DeletionTimestamp = &now
		db.Finalizers = finalizers
		return db
	}
	retainedPV := func(name string) *corev1.PersistentVolume {
		naming := product.DocumentDBProfile().Naming
		return &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{
			naming.ClusterLabel():   "db",
			naming.NamespaceLabel(): testNamespace,
		}}}
	}

	t.Run("deletion warns about retained PVs and removes finalizer", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, deleting(finalizer, "other/keep"), retainedPV("pv-a"), retainedPV("pv-b"))
		r, rec := newClusterReconciler(c, s, &sqlRecorder{})
		if res, err := r.Reconcile(context.Background(), clusterKey); err != nil || res != (ctrl.Result{}) {
			t.Fatalf("expected deletion to complete, got %v, %v", res, err)
		}
		events := drainEvents(rec)
		if !hasEvent(events, "PVsRetained") || !hasEvent(events, "kubectl delete pv pv-a pv-b") {
			t.Fatalf("expected PV retention warning listing PVs, got %v", events)
		}
		if got := getDocumentDB(t, c); slices.Contains(got.Finalizers, finalizer) {
			t.Fatalf("expected finalizer removed, got %v", got.Finalizers)
		}
	})

	t.Run("deletion without our finalizer is a no-op", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, deleting("other/keep"))
		r, rec := newClusterReconciler(c, s, &sqlRecorder{})
		if res, err := r.Reconcile(context.Background(), clusterKey); err != nil || res != (ctrl.Result{}) {
			t.Fatalf("expected no-op, got %v, %v", res, err)
		}
		if events := drainEvents(rec); len(events) != 0 {
			t.Fatalf("expected no events, got %v", events)
		}
	})

	t.Run("deletion continues when listing PVs fails", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, &interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errInjected
		}}, deleting(finalizer, "other/keep"))
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		if _, err := r.Reconcile(context.Background(), clusterKey); err != nil {
			t.Fatalf("PV warning failure must not block deletion, got %v", err)
		}
		if got := getDocumentDB(t, c); slices.Contains(got.Finalizers, finalizer) {
			t.Fatal("expected finalizer removed")
		}
	})

	t.Run("remove finalizer update fails", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, &interceptor.Funcs{Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			return errInjected
		}}, deleting(finalizer))
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		r.Recorder = nil
		if _, err := r.Reconcile(context.Background(), clusterKey); err != errInjected {
			t.Fatalf("expected injected error, got %v", err)
		}
	})
}

func TestClusterEmitPVRetentionWarningWithoutPVs(t *testing.T) {
	s := newTestScheme(t)
	c := newFakeClient(t, s, nil)
	r, rec := newClusterReconciler(c, s, &sqlRecorder{})
	if err := r.EmitPVRetentionWarning(context.Background(), testCluster()); err != nil {
		t.Fatal(err)
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Fatalf("expected no warning without PVs, got %v", events)
	}
}

func TestClusterReconcileReplicationContext(t *testing.T) {
	t.Run("context error", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, reconcilableCluster())
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		r.Replication = &fakeReplication[*dbpreview.DocumentDB]{contextErr: errInjected}
		if _, err := r.Reconcile(context.Background(), clusterKey); err != errInjected {
			t.Fatalf("expected injected error, got %v", err)
		}
	})

	t.Run("not present deletes owned resources", func(t *testing.T) {
		s := newTestScheme(t)
		db := reconcilableCluster()
		owned := metav1.OwnerReference{APIVersion: "documentdb.io/preview", Kind: "DocumentDB", Name: "db", UID: db.UID}
		svc := gatewayService("10.0.0.1")
		svc.OwnerReferences = []metav1.OwnerReference{owned}
		cnpgCluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: testNamespace, OwnerReferences: []metav1.OwnerReference{owned}}}
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: testNamespace}}
		c := newFakeClient(t, s, nil, db, svc, cnpgCluster, role)
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		r.Replication = &fakeReplication[*dbpreview.DocumentDB]{rc: util.NewReplicationContext(util.NotPresent, "", "")}
		if res, err := r.Reconcile(context.Background(), clusterKey); err != nil || res != (ctrl.Result{}) {
			t.Fatalf("expected no-op result, got %v, %v", res, err)
		}
		for _, obj := range []client.Object{&corev1.Service{}, &cnpgv1.Cluster{}, &rbacv1.Role{}} {
			key := types.NamespacedName{Name: "db", Namespace: testNamespace}
			if _, ok := obj.(*corev1.Service); ok {
				key.Name = svc.Name
			}
			if err := c.Get(context.Background(), key, obj); !apierrors.IsNotFound(err) {
				t.Fatalf("expected %T deleted, got %v", obj, err)
			}
		}
	})

	t.Run("not present propagates owned resource deletion failure", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, &interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errInjected
		}}, reconcilableCluster())
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		r.Replication = &fakeReplication[*dbpreview.DocumentDB]{rc: util.NewReplicationContext(util.NotPresent, "", "")}
		if _, err := r.Reconcile(context.Background(), clusterKey); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestClusterReconcileCreatesResourcesThenConverges(t *testing.T) {
	s := newTestScheme(t)
	db := reconcilableCluster()
	db.Spec.ExposeViaService.ServiceType = "ClusterIP"
	db.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: true}
	c := newFakeClient(t, s, nil, db, gatewayService("10.0.0.7"))
	sql := &sqlRecorder{roleOut: "(0 rows)"}
	r, _ := newClusterReconciler(c, s, sql)

	res, err := r.Reconcile(context.Background(), clusterKey)
	if err != nil || res.RequeueAfter != RequeueAfterLong {
		t.Fatalf("expected long requeue after creating the CNPG cluster, got %v, %v", res, err)
	}
	for _, obj := range []client.Object{&rbacv1.Role{}, &rbacv1.RoleBinding{}, &corev1.ServiceAccount{}, &cnpgv1.Cluster{}} {
		if err := c.Get(context.Background(), clusterKey.NamespacedName, obj); err != nil {
			t.Fatalf("expected %T to be created: %v", obj, err)
		}
	}
	cmKey := types.NamespacedName{Name: otelcfg.ConfigMapName("db"), Namespace: testNamespace}
	if err := c.Get(context.Background(), cmKey, &corev1.ConfigMap{}); err != nil {
		t.Fatalf("expected OTel ConfigMap while monitoring is enabled: %v", err)
	}

	// The CNPG cluster becomes healthy.
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), clusterKey.NamespacedName, cluster); err != nil {
		t.Fatal(err)
	}
	cluster.Status = cnpgv1.ClusterStatus{
		Phase:           CNPGClusterHealthyPhase,
		CurrentPrimary:  "db-1",
		InstancesStatus: map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"db-1"}},
	}
	if err := c.Update(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}

	// Disable monitoring: the ConfigMap is removed after the cluster sync.
	got := getDocumentDB(t, c)
	got.Spec.Monitoring = nil
	if err := c.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}

	res, err = r.Reconcile(context.Background(), clusterKey)
	if err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected converged reconcile, got %v, %v", res, err)
	}
	if !sql.ran("GRANT") {
		t.Fatalf("expected replication role grant, ran %v", sql.commands)
	}
	if err := c.Get(context.Background(), cmKey, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected OTel ConfigMap deleted after disabling monitoring, got %v", err)
	}
	got = getDocumentDB(t, c)
	if got.Status.Status != CNPGClusterHealthyPhase || !strings.Contains(got.Status.ConnectionString, "10.0.0.7") ||
		got.Status.SchemaVersion != "0.110.0" {
		t.Fatalf("expected status with phase, connection string and schema version, got %+v", got.Status)
	}
}

func TestClusterReconcileLoadBalancerServiceWithReplicationHooks(t *testing.T) {
	s := newTestScheme(t)
	db := reconcilableCluster()
	db.Spec.ExposeViaService.ServiceType = "LoadBalancer"
	svc := gatewayService("")
	svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: "db.example.com"}}
	cluster := healthyCNPGCluster(t, s, db)
	c := newFakeClient(t, s, nil, db, svc, cluster)
	sql := &sqlRecorder{roleOut: "(1 row)"}
	r, _ := newClusterReconciler(c, s, sql)
	hooks := &fakeReplication[*dbpreview.DocumentDB]{rc: util.NewReplicationContext(util.Primary, "db", "db")}
	hooks.rc.CNPGClusterName = "db"
	r.Replication = hooks

	if res, err := r.Reconcile(context.Background(), clusterKey); err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected converged reconcile, got %v, %v", res, err)
	}
	if !hooks.addCalled || !hooks.syncCalled {
		t.Fatal("expected replication hooks to extend and sync the cluster")
	}
	if sql.ran("GRANT") {
		t.Fatal("role grant must be skipped when already granted")
	}
	updated := &corev1.Service{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: svc.Name, Namespace: testNamespace}, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Fatalf("expected LoadBalancer service, got %v", updated.Spec.Type)
	}
	if got := getDocumentDB(t, c); !strings.Contains(got.Status.ConnectionString, "db.example.com") {
		t.Fatalf("expected connection string through the load balancer hostname, got %q", got.Status.ConnectionString)
	}
}

func TestClusterReconcileReplicationHookResults(t *testing.T) {
	stop := &ctrl.Result{RequeueAfter: 42 * time.Second}
	tests := []struct {
		name  string
		hooks *fakeReplication[*dbpreview.DocumentDB]
		want  ctrl.Result
	}{
		{name: "add to spec fails", hooks: &fakeReplication[*dbpreview.DocumentDB]{addErr: errInjected}, want: ctrl.Result{RequeueAfter: RequeueAfterShort}},
		{name: "sync requests a wait", hooks: &fakeReplication[*dbpreview.DocumentDB]{syncErr: errInjected, syncRequeue: 7 * time.Second}, want: ctrl.Result{RequeueAfter: 7 * time.Second}},
		{name: "primary reconciliation stops", hooks: &fakeReplication[*dbpreview.DocumentDB]{primaryResult: stop}, want: *stop},
		{name: "networking reconciliation stops", hooks: &fakeReplication[*dbpreview.DocumentDB]{netResult: stop}, want: *stop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := reconcilableCluster()
			c := newFakeClient(t, s, nil, db, healthyCNPGCluster(t, s, db))
			r, _ := newClusterReconciler(c, s, &sqlRecorder{roleOut: "(1 row)"})
			tt.hooks.rc = util.NewReplicationContext(util.Primary, "db", "db")
			tt.hooks.rc.CNPGClusterName = "db"
			r.Replication = tt.hooks
			res, err := r.Reconcile(context.Background(), clusterKey)
			if err != nil || res != tt.want {
				t.Fatalf("expected %v, got %v, %v", tt.want, res, err)
			}
		})
	}
}

func TestClusterReconcileStageFailuresRequeue(t *testing.T) {
	isType := func(obj client.Object, want client.Object) bool { return typeName(obj) == typeName(want) }
	failGet := func(want client.Object) interceptor.Funcs {
		return interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if isType(obj, want) {
				return errInjected
			}
			return c.Get(ctx, key, obj, opts...)
		}}
	}
	failCreate := func(want client.Object) interceptor.Funcs {
		return interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if isType(obj, want) {
				return errInjected
			}
			return c.Create(ctx, obj, opts...)
		}}
	}

	tests := []struct {
		name       string
		service    bool
		serviceIP  string
		monitoring bool
		healthy    bool
		sql        *sqlRecorder
		funcs      interceptor.Funcs
		want       ctrl.Result
		wantSQLRun string
		wantNoCNPG bool
		staleImage bool
	}{
		{name: "service upsert fails", service: true, serviceIP: "10.0.0.1", funcs: failGet(&corev1.Service{}), want: ctrl.Result{RequeueAfter: RequeueAfterShort}, wantNoCNPG: true},
		{name: "service IP not assigned pauses", service: true, want: ctrl.Result{}, wantNoCNPG: true},
		{name: "RBAC fails", funcs: failCreate(&rbacv1.Role{}), want: ctrl.Result{RequeueAfter: RequeueAfterShort}, wantNoCNPG: true},
		{name: "OTel ConfigMap fails", monitoring: true, funcs: failCreate(&corev1.ConfigMap{}), want: ctrl.Result{RequeueAfter: RequeueAfterShort}, wantNoCNPG: true},
		{name: "CNPG cluster create fails", funcs: failCreate(&cnpgv1.Cluster{}), want: ctrl.Result{RequeueAfter: RequeueAfterShort}, wantNoCNPG: true},
		{name: "CNPG cluster get fails", funcs: failGet(&cnpgv1.Cluster{}), want: ctrl.Result{RequeueAfter: RequeueAfterShort}, wantNoCNPG: true},
		{
			name: "CNPG cluster sync fails", healthy: true, staleImage: true,
			funcs: interceptor.Funcs{Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				return errInjected
			}},
			want: ctrl.Result{RequeueAfter: RequeueAfterShort},
		},
		{
			name: "OTel ConfigMap cleanup fails", healthy: true,
			funcs: interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok {
					return errInjected
				}
				return c.Delete(ctx, obj, opts...)
			}},
			want: ctrl.Result{RequeueAfter: RequeueAfterShort},
		},
		{name: "role check fails", healthy: true, sql: &sqlRecorder{roleErr: errInjected}, want: ctrl.Result{RequeueAfter: RequeueAfterLong}},
		{name: "role grant fails", healthy: true, sql: &sqlRecorder{roleOut: "(0 rows)", grantErr: errInjected}, want: ctrl.Result{RequeueAfter: RequeueAfterShort}, wantSQLRun: "GRANT"},
		{name: "extension upgrade check fails", healthy: true, sql: &sqlRecorder{roleOut: "(1 row)", extErr: errInjected}, want: ctrl.Result{RequeueAfter: RequeueAfterShort}},
		{
			name: "status update failure is tolerated", healthy: true, sql: &sqlRecorder{roleOut: "(1 row)"},
			funcs: interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				return errInjected
			}},
			want: ctrl.Result{RequeueAfter: RequeueAfterShort},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := reconcilableCluster()
			if tt.monitoring {
				db.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: true}
			}
			var objs []client.Object
			if tt.healthy {
				cluster := healthyCNPGCluster(t, s, db)
				if tt.staleImage {
					cluster.Spec.Plugins[0].Parameters[util.PLUGIN_PARAM_GATEWAY_IMAGE] = "example.com/gateway:old"
				}
				objs = append(objs, cluster)
			}
			if tt.service {
				db.Spec.ExposeViaService.ServiceType = "ClusterIP"
				objs = append(objs, gatewayService(tt.serviceIP))
			}
			objs = append(objs, db)
			c := newFakeClient(t, s, &tt.funcs, objs...)
			sql := tt.sql
			if sql == nil {
				sql = &sqlRecorder{roleOut: "(1 row)"}
			}
			r, _ := newClusterReconciler(c, s, sql)
			res, err := r.Reconcile(context.Background(), clusterKey)
			if err != nil || res != tt.want {
				t.Fatalf("expected %v, got %v, %v", tt.want, res, err)
			}
			if tt.wantSQLRun != "" && !sql.ran(tt.wantSQLRun) {
				t.Fatalf("expected %q to run, ran %v", tt.wantSQLRun, sql.commands)
			}
			if tt.wantNoCNPG {
				clusters := &cnpgv1.ClusterList{}
				if err := c.List(context.Background(), clusters); err != nil || len(clusters.Items) != 0 {
					t.Fatalf("expected no CNPG cluster after an earlier stage failed, got %d (%v)", len(clusters.Items), err)
				}
			}
		})
	}
}

func TestClusterReconcilePVRecovery(t *testing.T) {
	recoveringCluster := func() *dbpreview.DocumentDB {
		db := reconcilableCluster()
		db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{Recovery: &dbpreview.RecoveryConfiguration{
			PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "old-pv"},
		}}
		return db
	}
	pv := func(phase corev1.PersistentVolumePhase, claimed bool) *corev1.PersistentVolume {
		p := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "old-pv"},
			Spec: corev1.PersistentVolumeSpec{
				Capacity:    corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			},
			Status: corev1.PersistentVolumeStatus{Phase: phase},
		}
		if claimed {
			p.Spec.ClaimRef = &corev1.ObjectReference{Name: "old-claim", Namespace: testNamespace}
		}
		return p
	}
	tempPVCKey := types.NamespacedName{Name: util.TempPVCNameForPVRecovery("db"), Namespace: testNamespace}

	t.Run("reconcile creates temp PVC and requeues", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, recoveringCluster(), pv(corev1.VolumeAvailable, false))
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		res, err := r.Reconcile(context.Background(), clusterKey)
		if err != nil || res.RequeueAfter != RequeueAfterShort {
			t.Fatalf("expected short requeue, got %v, %v", res, err)
		}
		pvc := &corev1.PersistentVolumeClaim{}
		if err := c.Get(context.Background(), tempPVCKey, pvc); err != nil {
			t.Fatalf("expected temp PVC: %v", err)
		}
		if pvc.Spec.VolumeName != "old-pv" || metav1.GetControllerOf(pvc) == nil {
			t.Fatalf("expected owned temp PVC bound to the PV, got %+v", pvc)
		}
		if err := c.Get(context.Background(), clusterKey.NamespacedName, &cnpgv1.Cluster{}); !apierrors.IsNotFound(err) {
			t.Fatal("CNPG cluster must wait for the temp PVC to bind")
		}
	})

	t.Run("reconcile surfaces missing PV", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, recoveringCluster())
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		if _, err := r.Reconcile(context.Background(), clusterKey); err == nil || !strings.Contains(err.Error(), "old-pv not found") {
			t.Fatalf("expected missing PV error, got %v", err)
		}
	})

	errorCases := []struct {
		name    string
		objs    []client.Object
		funcs   interceptor.Funcs
		scheme  *runtime.Scheme
		wantErr string
	}{
		{
			name: "deleting temp PVC after recovery fails",
			objs: []client.Object{
				&cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: testNamespace}, Status: cnpgv1.ClusterStatus{Phase: CNPGClusterHealthyPhase}},
				&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: tempPVCKey.Name, Namespace: testNamespace}},
			},
			funcs: interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return errInjected
			}},
			wantErr: "failed to delete temp PVC",
		},
		{
			name: "getting CNPG cluster fails",
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*cnpgv1.Cluster); ok {
					return errInjected
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			wantErr: "failed to get CNPG cluster",
		},
		{
			name: "getting temp PVC fails",
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.PersistentVolumeClaim); ok {
					return errInjected
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			wantErr: "failed to get temp PVC",
		},
		{
			name: "getting PV fails",
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.PersistentVolume); ok {
					return errInjected
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			wantErr: "failed to get PV old-pv",
		},
		{
			name:    "PV is bound elsewhere",
			objs:    []client.Object{pv(corev1.VolumeBound, true)},
			wantErr: "must be Available or Released",
		},
		{
			name: "clearing claimRef fails",
			objs: []client.Object{pv(corev1.VolumeReleased, true)},
			funcs: interceptor.Funcs{Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				return errInjected
			}},
			wantErr: "failed to clear claimRef",
		},
		{
			name:    "temp PVC owner reference fails",
			objs:    []client.Object{pv(corev1.VolumeAvailable, false)},
			scheme:  runtime.NewScheme(),
			wantErr: "failed to set owner reference on temp PVC",
		},
		{
			name: "creating temp PVC fails",
			objs: []client.Object{pv(corev1.VolumeAvailable, false)},
			funcs: interceptor.Funcs{Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				return errInjected
			}},
			wantErr: "failed to create temp PVC",
		},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := recoveringCluster()
			c := newFakeClient(t, s, &tt.funcs, append(tt.objs, db)...)
			r, _ := newClusterReconciler(c, s, &sqlRecorder{})
			if tt.scheme != nil {
				r.Scheme = tt.scheme
			}
			_, err := r.ReconcilePVRecovery(context.Background(), db, testNamespace, "db")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}

	t.Run("released PV claimRef is cleared before binding", func(t *testing.T) {
		s := newTestScheme(t)
		db := recoveringCluster()
		c := newFakeClient(t, s, nil, db, pv(corev1.VolumeReleased, true))
		r, _ := newClusterReconciler(c, s, &sqlRecorder{})
		res, err := r.ReconcilePVRecovery(context.Background(), db, testNamespace, "db")
		if err != nil || res.RequeueAfter != RequeueAfterShort {
			t.Fatalf("expected requeue after clearing claimRef, got %v, %v", res, err)
		}
		got := &corev1.PersistentVolume{}
		if err := c.Get(context.Background(), types.NamespacedName{Name: "old-pv"}, got); err != nil {
			t.Fatal(err)
		}
		if got.Spec.ClaimRef != nil {
			t.Fatalf("expected claimRef cleared, got %v", got.Spec.ClaimRef)
		}
	})
}

func TestClusterEnsureRBACFailures(t *testing.T) {
	for _, target := range []client.Object{&rbacv1.Role{}, &corev1.ServiceAccount{}, &rbacv1.RoleBinding{}} {
		t.Run(typeName(target), func(t *testing.T) {
			s := newTestScheme(t)
			c := newFakeClient(t, s, &interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if typeName(obj) == typeName(target) {
					return errInjected
				}
				return c.Create(ctx, obj, opts...)
			}})
			r, _ := newClusterReconciler(c, s, &sqlRecorder{})
			if err := r.EnsureRBAC(context.Background(), testCluster(), testNamespace); err != errInjected {
				t.Fatalf("expected injected error, got %v", err)
			}
		})
	}
}

func TestClusterOtelConfigMapErrors(t *testing.T) {
	s := newTestScheme(t)
	r, _ := newClusterReconciler(newFakeClient(t, s, nil), runtime.NewScheme(), &sqlRecorder{})
	if err := r.ReconcileOtelConfigMap(context.Background(), testCluster(), testNamespace); err == nil ||
		!strings.Contains(err.Error(), "failed to set owner reference") {
		t.Fatalf("expected owner reference error, got %v", err)
	}
	if err := r.DeleteOtelConfigMap(context.Background(), "db", testNamespace); err != nil {
		t.Fatalf("deleting an absent ConfigMap must succeed, got %v", err)
	}
}

func TestClusterSetupWithManagerRequiresSQLExecutor(t *testing.T) {
	r := &clusterReconciler{Adapter: product.DocumentDBAdapter{}}
	if err := r.SetupWithManager(nil, "documentdb"); err == nil || !strings.Contains(err.Error(), "SQLExecutor must be configured") {
		t.Fatalf("expected SQLExecutor configuration error, got %v", err)
	}
}

func typeName(obj client.Object) string { return fmt.Sprintf("%T", obj) }
