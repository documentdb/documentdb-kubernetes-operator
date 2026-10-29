// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

type backupReconciler = BackupReconciler[*dbpreview.DocumentDB, *dbpreview.Backup]

func newBackupReconciler(c client.Client, s *runtime.Scheme, rec record.EventRecorder) *backupReconciler {
	return &backupReconciler{
		Client:   c,
		Scheme:   s,
		Recorder: rec,
		Cluster:  product.DocumentDBAdapter{},
		Backups:  product.DocumentDBBackupAdapter{},
	}
}

var backupKey = ctrl.Request{NamespacedName: types.NamespacedName{Name: "bk", Namespace: testNamespace}}

func pendingBackup() *dbpreview.Backup {
	return testBackupFor("bk", "", time.Now())
}

func defaultSnapshotClass() *snapshotv1.VolumeSnapshotClass {
	return &snapshotv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "default-snap",
			Annotations: map[string]string{"snapshot.storage.kubernetes.io/is-default-class": "true"},
		},
		Driver:         "test.csi",
		DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
	}
}

func getBackup(t *testing.T, c client.Client) *dbpreview.Backup {
	t.Helper()
	b := &dbpreview.Backup{}
	if err := c.Get(context.Background(), backupKey.NamespacedName, b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	return b
}

func TestBackupReconcileNotFoundAndGetError(t *testing.T) {
	s := newTestScheme(t)
	r := newBackupReconciler(newFakeClient(t, s, nil), s, record.NewFakeRecorder(10))
	if res, err := r.Reconcile(context.Background(), backupKey); err != nil || res != (ctrl.Result{}) {
		t.Fatalf("expected empty result for a missing backup, got %v, %v", res, err)
	}

	c := newFakeClient(t, s, &interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return errInjected
	}})
	r = newBackupReconciler(c, s, record.NewFakeRecorder(10))
	if _, err := r.Reconcile(context.Background(), backupKey); err != errInjected {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestBackupReconcileDeletesExpiredBackup(t *testing.T) {
	s := newTestScheme(t)
	b := testBackupFor("bk", cnpgv1.BackupPhaseCompleted, time.Now().Add(-48*time.Hour))
	expired := metav1.NewTime(time.Now().Add(-time.Hour))
	b.Status.ExpiredAt = &expired
	rec := record.NewFakeRecorder(10)
	c := newFakeClient(t, s, nil, b)
	r := newBackupReconciler(c, s, rec)

	if _, err := r.Reconcile(context.Background(), backupKey); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err := c.Get(context.Background(), backupKey.NamespacedName, &dbpreview.Backup{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected expired backup to be deleted, got %v", err)
	}
	if events := drainEvents(rec); !hasEvent(events, "BackupExpired") || !hasEvent(events, "BackupDeleted") {
		t.Fatalf("expected expiry events, got %v", events)
	}
}

func TestBackupReconcileExpiredDeleteFailure(t *testing.T) {
	s := newTestScheme(t)
	b := testBackupFor("bk", cnpgv1.BackupPhaseCompleted, time.Now().Add(-48*time.Hour))
	expired := metav1.NewTime(time.Now().Add(-time.Hour))
	b.Status.ExpiredAt = &expired
	rec := record.NewFakeRecorder(10)
	c := newFakeClient(t, s, &interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		return errInjected
	}}, b)
	r := newBackupReconciler(c, s, rec)
	if _, err := r.Reconcile(context.Background(), backupKey); err != errInjected {
		t.Fatalf("expected injected error, got %v", err)
	}
	if !hasEvent(drainEvents(rec), "BackupDeleteFailed") {
		t.Fatal("expected BackupDeleteFailed event")
	}
}

func TestBackupReconcileDoneBackupRequeuesUntilExpiry(t *testing.T) {
	s := newTestScheme(t)
	b := testBackupFor("bk", cnpgv1.BackupPhaseCompleted, time.Now())
	expires := metav1.NewTime(time.Now().Add(2 * time.Hour))
	b.Status.ExpiredAt = &expires
	r := newBackupReconciler(newFakeClient(t, s, nil, b), s, record.NewFakeRecorder(10))
	res, err := r.Reconcile(context.Background(), backupKey)
	if err != nil || res.RequeueAfter <= time.Hour || res.RequeueAfter > 2*time.Hour {
		t.Fatalf("expected requeue at expiry, got %v, %v", res, err)
	}
}

func TestBackupReconcileFailsWithoutCluster(t *testing.T) {
	s := newTestScheme(t)
	rec := record.NewFakeRecorder(10)
	c := newFakeClient(t, s, nil, pendingBackup())
	r := newBackupReconciler(c, s, rec)
	res, err := r.Reconcile(context.Background(), backupKey)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := getBackup(t, c)
	if got.Status.Phase != cnpgv1.BackupPhaseFailed || !strings.Contains(got.Status.Message, "Failed to get associated DocumentDB cluster") {
		t.Fatalf("expected failed phase naming the cluster kind, got %+v", got.Status)
	}
	// Default 30-day retention from creation time.
	if got.Status.ExpiredAt == nil || res.RequeueAfter < 29*24*time.Hour {
		t.Fatalf("expected default retention expiry, got %v / %v", got.Status.ExpiredAt, res.RequeueAfter)
	}
	if !hasEvent(drainEvents(rec), "BackupFailed") {
		t.Fatal("expected BackupFailed event")
	}
}

func TestBackupReconcileFailsWithoutSnapshotClass(t *testing.T) {
	s := newTestScheme(t)
	cluster := testCluster()
	cluster.Spec.Backup = &dbpreview.BackupConfiguration{RetentionDays: 2}
	c := newFakeClient(t, s, nil, pendingBackup(), cluster)
	r := newBackupReconciler(c, s, record.NewFakeRecorder(10))
	res, err := r.Reconcile(context.Background(), backupKey)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := getBackup(t, c)
	if got.Status.Phase != cnpgv1.BackupPhaseFailed || !strings.Contains(got.Status.Message, "Please create a default VolumeSnapshotClass") {
		t.Fatalf("expected failure asking for a VolumeSnapshotClass, got %+v", got.Status)
	}
	// The cluster's retention applies.
	if res.RequeueAfter > 2*24*time.Hour || res.RequeueAfter < 47*time.Hour {
		t.Fatalf("expected cluster retention of 2 days, got %v", res.RequeueAfter)
	}
}

func TestBackupReconcileCreatesCNPGBackupForPrimary(t *testing.T) {
	s := newTestScheme(t)
	cluster := testCluster()
	cluster.Spec.Environment = "aks"
	rec := record.NewFakeRecorder(10)
	c := newFakeClient(t, s, nil, pendingBackup(), cluster)
	r := newBackupReconciler(c, s, rec)

	res, err := r.Reconcile(context.Background(), backupKey)
	if err != nil || res.RequeueAfter != 5*time.Second {
		t.Fatalf("expected 5s requeue after creating the CNPG backup, got %v, %v", res, err)
	}
	vsc := &snapshotv1.VolumeSnapshotClass{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "azure-disk-snapclass"}, vsc); err != nil {
		t.Fatalf("expected the AKS VolumeSnapshotClass to be created: %v", err)
	}
	cnpgBackup := &cnpgv1.Backup{}
	if err := c.Get(context.Background(), backupKey.NamespacedName, cnpgBackup); err != nil {
		t.Fatalf("expected CNPG backup: %v", err)
	}
	if cnpgBackup.Spec.Cluster.Name != "db" {
		t.Fatalf("expected CNPG backup of the single-region CNPG cluster, got %q", cnpgBackup.Spec.Cluster.Name)
	}
	if owner := metav1.GetControllerOf(cnpgBackup); owner == nil || owner.Name != "bk" {
		t.Fatalf("expected CNPG backup owned by the Backup, got %v", cnpgBackup.OwnerReferences)
	}
	if events := drainEvents(rec); !hasEvent(events, "BackupInitialized") || !hasEvent(events, "azure-disk-snapclass") {
		t.Fatalf("expected snapshot class and initialization events, got %v", events)
	}
}

func TestBackupReconcileReplicationRoles(t *testing.T) {
	notPrimary := util.NewReplicationContext(util.Replica, "", "")
	pendingPromotion := util.NewReplicationContext(util.Primary, "old", "new")

	t.Run("replica is skipped", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, pendingBackup(), testCluster(), defaultSnapshotClass())
		r := newBackupReconciler(c, s, record.NewFakeRecorder(10))
		r.Replication = &fakeReplication[*dbpreview.DocumentDB]{rc: notPrimary}
		if _, err := r.Reconcile(context.Background(), backupKey); err != nil {
			t.Fatal(err)
		}
		got := getBackup(t, c)
		if got.Status.Phase != dbpreview.BackupPhaseSkipped || !strings.Contains(got.Status.Message, "primary cluster") {
			t.Fatalf("expected skipped backup, got %+v", got.Status)
		}
	})

	t.Run("primary with disabled endpoint is deferred", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, pendingBackup(), testCluster(), defaultSnapshotClass())
		r := newBackupReconciler(c, s, record.NewFakeRecorder(10))
		r.Replication = &fakeReplication[*dbpreview.DocumentDB]{rc: pendingPromotion}
		res, err := r.Reconcile(context.Background(), backupKey)
		if err != nil || res.RequeueAfter != time.Minute {
			t.Fatalf("expected one-minute deferral, got %v, %v", res, err)
		}
		if err := c.Get(context.Background(), backupKey.NamespacedName, &cnpgv1.Backup{}); !apierrors.IsNotFound(err) {
			t.Fatalf("no CNPG backup should be created while deferred, got %v", err)
		}
	})

	t.Run("replication context error", func(t *testing.T) {
		s := newTestScheme(t)
		c := newFakeClient(t, s, nil, pendingBackup(), testCluster(), defaultSnapshotClass())
		r := newBackupReconciler(c, s, record.NewFakeRecorder(10))
		r.Replication = &fakeReplication[*dbpreview.DocumentDB]{contextErr: errInjected}
		if _, err := r.Reconcile(context.Background(), backupKey); err != errInjected {
			t.Fatalf("expected injected error, got %v", err)
		}
	})
}

func TestBackupReconcileMirrorsCNPGBackupStatus(t *testing.T) {
	s := newTestScheme(t)
	cluster := testCluster()
	cluster.Status.SchemaVersion = "0.110.0"
	stopped := metav1.NewTime(time.Now())
	cnpgBackup := &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "bk", Namespace: testNamespace},
		Status:     cnpgv1.BackupStatus{Phase: cnpgv1.BackupPhaseCompleted, StartedAt: &stopped, StoppedAt: &stopped},
	}
	c := newFakeClient(t, s, nil, pendingBackup(), cluster, defaultSnapshotClass(), cnpgBackup)
	r := newBackupReconciler(c, s, record.NewFakeRecorder(10))

	res, err := r.Reconcile(context.Background(), backupKey)
	if err != nil || res.RequeueAfter < 29*24*time.Hour {
		t.Fatalf("expected requeue at default expiry, got %v, %v", res, err)
	}
	got := getBackup(t, c)
	if got.Status.Phase != cnpgv1.BackupPhaseCompleted || got.Status.SchemaVersion != "0.110.0" || got.Status.ExpiredAt == nil {
		t.Fatalf("expected status mirrored from the CNPG backup, got %+v", got.Status)
	}
}

func TestBackupReconcileInProgressCNPGBackupRequeues(t *testing.T) {
	s := newTestScheme(t)
	cnpgBackup := &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "bk", Namespace: testNamespace},
		Status:     cnpgv1.BackupStatus{Phase: cnpgv1.BackupPhaseRunning},
	}
	c := newFakeClient(t, s, nil, pendingBackup(), testCluster(), defaultSnapshotClass(), cnpgBackup)
	r := newBackupReconciler(c, s, record.NewFakeRecorder(10))
	res, err := r.Reconcile(context.Background(), backupKey)
	if err != nil || res.RequeueAfter != 10*time.Second {
		t.Fatalf("expected 10s requeue for a running backup, got %v, %v", res, err)
	}
	if got := getBackup(t, c); got.Status.Phase != cnpgv1.BackupPhaseRunning {
		t.Fatalf("expected running phase, got %q", got.Status.Phase)
	}
}

func TestBackupReconcileClientErrors(t *testing.T) {
	isCNPGBackup := func(obj client.Object) bool { _, ok := obj.(*cnpgv1.Backup); return ok }
	isSnapshotClass := func(obj client.Object) bool { _, ok := obj.(*snapshotv1.VolumeSnapshotClass); return ok }
	isSnapshotClassList := func(obj client.ObjectList) bool { _, ok := obj.(*snapshotv1.VolumeSnapshotClassList); return ok }

	tests := []struct {
		name        string
		environment string
		funcs       interceptor.Funcs
		cnpgBackup  bool
		wantErr     bool
		wantMessage string
	}{
		{
			name: "listing snapshot classes fails",
			funcs: interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if isSnapshotClassList(list) {
					return errInjected
				}
				return c.List(ctx, list, opts...)
			}},
			wantMessage: "Failed to ensure VolumeSnapshotClass: injected failure",
		},
		{
			name:        "creating snapshot class fails",
			environment: "aks",
			funcs: interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if isSnapshotClass(obj) {
					return errInjected
				}
				return c.Create(ctx, obj, opts...)
			}},
			wantMessage: "Failed to ensure VolumeSnapshotClass: injected failure",
		},
		{
			name: "getting CNPG backup fails",
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if isCNPGBackup(obj) {
					return errInjected
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			wantErr: true,
		},
		{
			name: "creating CNPG backup fails",
			funcs: interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if isCNPGBackup(obj) {
					return errInjected
				}
				return c.Create(ctx, obj, opts...)
			}},
			wantMessage: "Failed to initialize backup: injected failure",
		},
		{
			name:       "patching mirrored status fails",
			cnpgBackup: true,
			funcs: interceptor.Funcs{SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
				return errInjected
			}},
			wantErr: true,
		},
		{
			name: "patching failed status fails",
			funcs: interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if isCNPGBackup(obj) {
						return errInjected
					}
					return c.Create(ctx, obj, opts...)
				},
				SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
					return errInjected
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScheme(t)
			cluster := testCluster()
			cluster.Spec.Environment = tt.environment
			objs := []client.Object{pendingBackup(), cluster}
			if tt.environment == "" {
				objs = append(objs, defaultSnapshotClass())
			}
			if tt.cnpgBackup {
				objs = append(objs, &cnpgv1.Backup{
					ObjectMeta: metav1.ObjectMeta{Name: "bk", Namespace: testNamespace},
					Status:     cnpgv1.BackupStatus{Phase: cnpgv1.BackupPhaseRunning},
				})
			}
			c := newFakeClient(t, s, &tt.funcs, objs...)
			r := newBackupReconciler(c, s, record.NewFakeRecorder(10))
			_, err := r.Reconcile(context.Background(), backupKey)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := getBackup(t, c); got.Status.Phase != cnpgv1.BackupPhaseFailed || got.Status.Message != tt.wantMessage {
				t.Fatalf("expected failed phase with %q, got %+v", tt.wantMessage, got.Status)
			}
		})
	}
}

func TestBackupCreateCNPGBackupInitializationError(t *testing.T) {
	s := newTestScheme(t)
	b := pendingBackup()
	c := newFakeClient(t, s, nil, b)
	// A scheme without the Backup kind cannot set the CNPG backup owner.
	r := newBackupReconciler(c, runtime.NewScheme(), record.NewFakeRecorder(10))
	if _, err := r.CreateCNPGBackup(context.Background(), b, nil, util.NewSingleRegionContext("db", "", "")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := getBackup(t, c); got.Status.Phase != cnpgv1.BackupPhaseFailed || !strings.HasPrefix(got.Status.Message, "Failed to initialize backup") {
		t.Fatalf("expected initialization failure, got %+v", got.Status)
	}
}

func TestBuildVolumeSnapshotClass(t *testing.T) {
	vsc := BuildVolumeSnapshotClass("aks")
	if vsc == nil || vsc.Driver != "disk.csi.azure.com" || vsc.Annotations["snapshot.storage.kubernetes.io/is-default-class"] != "true" ||
		vsc.DeletionPolicy != snapshotv1.VolumeSnapshotContentDelete {
		t.Fatalf("unexpected AKS snapshot class: %+v", vsc)
	}
	for _, env := range []string{"", "eks", "gke"} {
		if BuildVolumeSnapshotClass(env) != nil {
			t.Fatalf("expected no default snapshot class for %q", env)
		}
	}
}
