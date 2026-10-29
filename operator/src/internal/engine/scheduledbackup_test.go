// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/product"
)

type scheduledReconciler = ScheduledBackupReconciler[*dbv1.DocumentDB, *dbv1.ScheduledBackup, *dbv1.Backup]

func newScheduledReconciler(c client.Client, s *runtime.Scheme, rec record.EventRecorder) *scheduledReconciler {
	return &scheduledReconciler{
		Client:    c,
		Scheme:    s,
		Recorder:  rec,
		Cluster:   product.DocumentDBAdapter{},
		Scheduled: product.DocumentDBScheduledBackupAdapter{},
		Backups:   product.DocumentDBBackupAdapter{},
	}
}

func testCluster() *dbv1.DocumentDB {
	return &dbv1.DocumentDB{
		TypeMeta:   metav1.TypeMeta{APIVersion: "documentdb.io/v1", Kind: "DocumentDB"},
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: testNamespace, UID: "db-uid"},
	}
}

func testScheduledBackup(schedule string) *dbv1.ScheduledBackup {
	return &dbv1.ScheduledBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: testNamespace},
		Spec: dbv1.ScheduledBackupSpec{
			Schedule: schedule,
			Cluster:  cnpgv1.LocalObjectReference{Name: "db"},
		},
	}
}

func testBackupFor(name string, phase cnpgv1.BackupPhase, created time.Time) *dbv1.Backup {
	return &dbv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, CreationTimestamp: metav1.NewTime(created)},
		Spec:       dbv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "db"}},
		Status:     dbv1.BackupStatus{Phase: phase},
	}
}

var scheduledKey = ctrl.Request{NamespacedName: types.NamespacedName{Name: "nightly", Namespace: testNamespace}}

func TestScheduledBackupReconcileNotFound(t *testing.T) {
	s := newTestScheme(t)
	r := newScheduledReconciler(newFakeClient(t, s, nil), s, record.NewFakeRecorder(10))
	res, err := r.Reconcile(context.Background(), scheduledKey)
	if err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected empty result without error, got %v, %v", res, err)
	}
}

func TestScheduledBackupReconcileGetError(t *testing.T) {
	s := newTestScheme(t)
	c := newFakeClient(t, s, &interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return errInjected
		},
	})
	r := newScheduledReconciler(c, s, record.NewFakeRecorder(10))
	if _, err := r.Reconcile(context.Background(), scheduledKey); err != errInjected {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestScheduledBackupReconcileMissingClusterRecordsEvent(t *testing.T) {
	s := newTestScheme(t)
	rec := record.NewFakeRecorder(10)
	r := newScheduledReconciler(newFakeClient(t, s, nil, testScheduledBackup("@hourly")), s, rec)
	if _, err := r.Reconcile(context.Background(), scheduledKey); err == nil {
		t.Fatal("expected error when the referenced cluster does not exist")
	}
	if events := drainEvents(rec); !hasEvent(events, "ClusterNotFound") || !hasEvent(events, "DocumentDB cluster") {
		t.Fatalf("expected ClusterNotFound event naming the product kind, got %v", events)
	}
}

func TestScheduledBackupReconcileCreatesDueBackup(t *testing.T) {
	s := newTestScheme(t)
	rec := record.NewFakeRecorder(10)
	sb := testScheduledBackup("* * * * *")
	sb.Spec.RetentionDays = ptrInt(7)
	past := metav1.NewTime(time.Now().Add(-time.Hour))
	sb.Status.NextScheduledTime = &past
	c := newFakeClient(t, s, nil, sb, testCluster())
	r := newScheduledReconciler(c, s, rec)

	res, err := r.Reconcile(context.Background(), scheduledKey)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter <= 0 || res.RequeueAfter > time.Minute {
		t.Fatalf("expected requeue within a minute for a per-minute schedule, got %v", res.RequeueAfter)
	}

	var backups dbv1.BackupList
	if err := c.List(context.Background(), &backups); err != nil {
		t.Fatal(err)
	}
	if len(backups.Items) != 1 {
		t.Fatalf("expected one created backup, got %d", len(backups.Items))
	}
	b := backups.Items[0]
	if b.Labels[dbv1.ScheduledBackupLabel] != "nightly" || b.Spec.Cluster.Name != "db" ||
		b.Spec.RetentionDays == nil || *b.Spec.RetentionDays != 7 {
		t.Fatalf("backup not derived from the scheduled backup: %+v", b)
	}

	var got dbv1.ScheduledBackup
	if err := c.Get(context.Background(), scheduledKey.NamespacedName, &got); err != nil {
		t.Fatal(err)
	}
	if owner := metav1.GetControllerOf(&got); owner == nil || owner.Name != "db" || owner.Kind != "DocumentDB" {
		t.Fatalf("expected the cluster to own the scheduled backup, got %v", got.OwnerReferences)
	}
	if got.Status.LastScheduledTime == nil || got.Status.NextScheduledTime == nil ||
		!got.Status.NextScheduledTime.After(got.Status.LastScheduledTime.Time) {
		t.Fatalf("expected last and next scheduled times to be recorded, got %+v", got.Status)
	}
	if events := drainEvents(rec); !hasEvent(events, "BackupSchedule") {
		t.Fatalf("expected BackupSchedule event, got %v", events)
	}
}

func TestScheduledBackupReconcileWaitsForRunningBackup(t *testing.T) {
	s := newTestScheme(t)
	sb := testScheduledBackup("* * * * *")
	running := testBackupFor("running", cnpgv1.BackupPhaseRunning, time.Now())
	c := newFakeClient(t, s, nil, sb, testCluster(), running)
	r := newScheduledReconciler(c, s, record.NewFakeRecorder(10))

	res, err := r.Reconcile(context.Background(), scheduledKey)
	if err != nil || res.RequeueAfter != time.Minute {
		t.Fatalf("expected one-minute requeue while a backup runs, got %v, %v", res, err)
	}
	var backups dbv1.BackupList
	if err := c.List(context.Background(), &backups); err != nil {
		t.Fatal(err)
	}
	if len(backups.Items) != 1 {
		t.Fatalf("expected no new backup while one is running, got %d", len(backups.Items))
	}
}

func TestScheduledBackupReconcileSchedulesFromLatestBackup(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := newTestScheme(t)
	sb := testScheduledBackup("@every 24h")
	// A schedule computed from the older backup would already be due.
	older := testBackupFor("older", cnpgv1.BackupPhaseCompleted, now.Add(-48*time.Hour))
	newer := testBackupFor("newer", cnpgv1.BackupPhaseFailed, now.Add(-time.Hour))
	c := newFakeClient(t, s, nil, sb, testCluster(), older, newer)
	r := newScheduledReconciler(c, s, record.NewFakeRecorder(10))

	res, err := r.Reconcile(context.Background(), scheduledKey)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	next := newer.CreationTimestamp.Add(24 * time.Hour)
	if res.RequeueAfter <= 22*time.Hour || res.RequeueAfter > 24*time.Hour {
		t.Fatalf("expected requeue about 23h away, 24h after the latest backup, got %v", res.RequeueAfter)
	}
	var got dbv1.ScheduledBackup
	if err := c.Get(context.Background(), scheduledKey.NamespacedName, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastScheduledTime != nil {
		t.Fatal("no backup should have been scheduled before the next run time")
	}
	if got.Status.NextScheduledTime == nil || !got.Status.NextScheduledTime.UTC().Equal(next) {
		t.Fatalf("expected next run at %v, got %v", next, got.Status.NextScheduledTime)
	}
	var backups dbv1.BackupList
	if err := c.List(context.Background(), &backups); err != nil {
		t.Fatal(err)
	}
	if len(backups.Items) != 2 {
		t.Fatalf("expected no new backup, got %d", len(backups.Items))
	}
}

func TestScheduledBackupReconcileErrors(t *testing.T) {
	isScheduled := func(obj client.Object) bool { _, ok := obj.(*dbv1.ScheduledBackup); return ok }
	tests := []struct {
		name      string
		funcs     interceptor.Funcs
		schedule  string
		wantEvent string
	}{
		{
			name:     "owner reference update fails",
			schedule: "* * * * *",
			funcs: interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				return errInjected
			}},
		},
		{
			name:      "invalid schedule",
			schedule:  "not a cron",
			wantEvent: "InvalidSchedule",
		},
		{
			name:     "listing backups fails",
			schedule: "* * * * *",
			funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return errInjected
			}},
		},
		{
			name:      "creating backup fails",
			schedule:  "* * * * *",
			wantEvent: "BackupCreation",
			funcs: interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return errInjected
			}},
		},
		{
			name:     "status update fails",
			schedule: "0 0 1 1 *",
			funcs: interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if isScheduled(obj) {
					return errInjected
				}
				return c.SubResource(sub).Update(ctx, obj, opts...)
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			rec := record.NewFakeRecorder(10)
			sb := testScheduledBackup(tt.schedule)
			past := metav1.NewTime(time.Now().Add(-time.Hour))
			sb.Status.NextScheduledTime = &past
			c := newFakeClient(t, s, &tt.funcs, sb, testCluster())
			r := newScheduledReconciler(c, s, rec)
			if _, err := r.Reconcile(context.Background(), scheduledKey); err == nil {
				t.Fatal("expected error")
			}
			if tt.wantEvent != "" && !hasEvent(drainEvents(rec), tt.wantEvent) {
				t.Fatalf("expected %s event", tt.wantEvent)
			}
		})
	}
}

func TestScheduledBackupEnsureOwnerReferenceKeepsExistingOwner(t *testing.T) {
	s := newTestScheme(t)
	sb := testScheduledBackup("@daily")
	sb.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "x"}}
	// No cluster exists: an existing owner must short-circuit the lookup.
	r := newScheduledReconciler(newFakeClient(t, s, nil, sb), s, record.NewFakeRecorder(10))
	if err := r.EnsureOwnerReference(context.Background(), sb); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(sb.OwnerReferences) != 1 || sb.OwnerReferences[0].Name != "other" {
		t.Fatalf("existing owner reference changed: %v", sb.OwnerReferences)
	}
}

func TestScheduledBackupEnsureOwnerReferenceSchemeError(t *testing.T) {
	s := newTestScheme(t)
	sb := testScheduledBackup("@daily")
	c := newFakeClient(t, s, nil, sb, testCluster())
	// The reconciler scheme does not know the cluster kind, so the owner
	// reference cannot be built.
	r := newScheduledReconciler(c, runtime.NewScheme(), record.NewFakeRecorder(10))
	if err := r.EnsureOwnerReference(context.Background(), sb); err == nil {
		t.Fatal("expected error setting the owner reference")
	}
}

// indexerManager records the field index registered by SetupWithManager.
type indexerManager struct {
	manager.Manager
	field   string
	extract client.IndexerFunc
}

func (m *indexerManager) GetFieldIndexer() client.FieldIndexer { return m }

func (m *indexerManager) IndexField(_ context.Context, _ client.Object, field string, extract client.IndexerFunc) error {
	m.field, m.extract = field, extract
	return errInjected
}

func TestScheduledBackupSetupWithManagerIndexesBackupsByCluster(t *testing.T) {
	s := newTestScheme(t)
	r := newScheduledReconciler(nil, s, nil)
	mgr := &indexerManager{}
	if err := r.SetupWithManager(mgr); err != errInjected {
		t.Fatalf("expected index registration error to propagate, got %v", err)
	}
	if mgr.field != BackupClusterField {
		t.Fatalf("expected index on %q, got %q", BackupClusterField, mgr.field)
	}
	if got := mgr.extract(testBackupFor("b", "", time.Now())); len(got) != 1 || got[0] != "db" {
		t.Fatalf("expected the backup's cluster name, got %v", got)
	}
	if got := mgr.extract(testCluster()); got != nil {
		t.Fatalf("expected nil for non-Backup objects, got %v", got)
	}
}

func ptrInt(v int) *int { return &v }
