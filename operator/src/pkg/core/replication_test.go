// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/pkg/core"
)

// accountReplication is a second product's multi-region hooks: it reports a
// fixed replication context built through the public constructor and marks
// non-primary CNPG clusters as replicas of the primary.
type accountReplication struct {
	rc    *core.ReplicationContext
	added int
}

var _ core.ReplicationHooks[*account] = (*accountReplication)(nil)

func (h *accountReplication) Context(context.Context, *account) (*core.ReplicationContext, error) {
	return h.rc, nil
}

func (h *accountReplication) AddToClusterSpec(_ context.Context, _ *account, rc *core.ReplicationContext, desired *cnpgv1.Cluster) error {
	h.added++
	if !rc.IsPrimary() {
		enabled := true
		desired.Spec.ReplicaCluster = &cnpgv1.ReplicaClusterConfiguration{Enabled: &enabled, Source: rc.PrimaryCNPGClusterName}
	}
	return nil
}

func (h *accountReplication) SyncChanges(context.Context, *cnpgv1.Cluster, *cnpgv1.Cluster, *account, *core.ReplicationContext) ([]core.JSONPatch, error, time.Duration) {
	return nil, nil, 0
}

func (h *accountReplication) ReconcilePrimary(context.Context, *account, *core.ReplicationContext, *cnpgv1.Cluster) *ctrl.Result {
	return nil
}

func (h *accountReplication) ReconcileNetworking(context.Context, *account, *core.ReplicationContext) *ctrl.Result {
	return nil
}

// replicatedAccountAdapter grants a replication admin role on primaries.
type replicatedAccountAdapter struct{ accountAdapter }

func (replicatedAccountAdapter) Profile() core.ProductProfile {
	p := accountAdapter{}.Profile()
	p.Postgres.ReplicationAdminRole = "cosmos_admin"
	return p
}

func (a replicatedAccountAdapter) Intent(obj *account) core.ClusterIntent {
	intent := accountAdapter{}.Intent(obj)
	intent.Product = a.Profile()
	return intent
}

// regionContext returns the context of region "acct" in a two-region topology.
func regionContext(state core.ReplicationState, currentLocalPrimary, targetLocalPrimary string) *core.ReplicationContext {
	rc := core.NewReplicationContext(state, currentLocalPrimary, targetLocalPrimary)
	rc.CNPGClusterName = "acct"
	rc.PrimaryCNPGClusterName = "acct-primary"
	rc.OtherCNPGClusterNames = []string{"acct-primary"}
	return rc
}

func replicationScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, cnpgv1.AddToScheme, dbv1.AddToScheme, snapshotv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	scheme.AddKnownTypeWithName(accountGV.WithKind("Account"), &account{})
	scheme.AddKnownTypeWithName(accountGV.WithKind("AccountList"), &accountList{})
	metav1.AddToGroupVersion(scheme, accountGV)
	return scheme
}

func TestReplicationContextStates(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		rc                                         *core.ReplicationContext
		primary, replicating, notPresent, endpoint bool
	}{
		{"single region", core.NewSingleRegionContext("acct", "", ""), true, false, false, true},
		{"no replication ignores promotion", core.NewReplicationContext(core.NoReplication, "a-1", "a-2"), true, false, false, true},
		{"primary", regionContext(core.ReplicationPrimary, "acct-1", "acct-1"), true, true, false, true},
		{"primary awaiting promotion", regionContext(core.ReplicationPrimary, "acct-1", "acct-2"), true, true, false, false},
		{"replica", regionContext(core.ReplicationReplica, "", ""), false, true, false, true},
		{"replica awaiting promotion", regionContext(core.ReplicationReplica, "", "acct-2"), false, true, false, false},
		{"not present", core.NewReplicationContext(core.ReplicationNotPresent, "", ""), false, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rc.IsPrimary(); got != tc.primary {
				t.Errorf("IsPrimary = %t", got)
			}
			if got := tc.rc.IsReplicating(); got != tc.replicating {
				t.Errorf("IsReplicating = %t", got)
			}
			if got := tc.rc.IsNotPresent(); got != tc.notPresent {
				t.Errorf("IsNotPresent = %t", got)
			}
			if got := tc.rc.EndpointEnabled(); got != tc.endpoint {
				t.Errorf("EndpointEnabled = %t", got)
			}
		})
	}
}

// TestClusterReconcilerReplicationStates drives the shared cluster reconciler
// with publicly constructed replication contexts.
func TestClusterReconcilerReplicationStates(t *testing.T) {
	for _, tc := range []struct {
		name           string
		rc             *core.ReplicationContext
		wantHookCalls  int
		wantReplica    bool
		wantRoleGrants bool
	}{
		{"single region", core.NewSingleRegionContext("acct", "", ""), 0, false, true},
		{"primary", regionContext(core.ReplicationPrimary, "acct-1", "acct-1"), 1, false, true},
		{"replica", regionContext(core.ReplicationReplica, "", ""), 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := replicationScheme(t)
			acct := &account{
				ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"},
				Spec:       accountSpec{Instances: 1, PvcSize: "10Gi"},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acct).
				WithStatusSubresource(&account{}, &cnpgv1.Cluster{}).Build()
			hooks := &accountReplication{rc: tc.rc}
			var sql []string
			r := &core.ClusterReconciler[*account]{
				Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10),
				Adapter: replicatedAccountAdapter{}, Replication: hooks,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, cmd string) (string, error) {
					sql = append(sql, cmd)
					return "(1 row)", nil
				},
			}
			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "acct", Namespace: "ns"}}
			for range 2 { // finalizer, then create
				if _, err := r.Reconcile(ctx, req); err != nil {
					t.Fatal(err)
				}
			}
			cluster := &cnpgv1.Cluster{}
			if err := c.Get(ctx, client.ObjectKey{Name: "acct", Namespace: "ns"}, cluster); err != nil {
				t.Fatal(err)
			}
			if hooks.added != tc.wantHookCalls {
				t.Errorf("AddToClusterSpec calls = %d, want %d", hooks.added, tc.wantHookCalls)
			}
			if isReplica := cluster.Spec.ReplicaCluster != nil && cluster.Spec.ReplicaCluster.Source == "acct-primary"; isReplica != tc.wantReplica {
				t.Errorf("replica cluster = %+v, want replica %t", cluster.Spec.ReplicaCluster, tc.wantReplica)
			}

			cluster.Status.Phase = "Cluster in healthy state"
			cluster.Status.CurrentPrimary = "acct-1"
			cluster.Status.InstancesStatus = map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"acct-1"}}
			if err := c.Status().Update(ctx, cluster); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			roleCheck := core.ReplicationRoleCheckSQL("cosmos_admin")
			checked := false
			for _, cmd := range sql {
				checked = checked || strings.Contains(cmd, roleCheck)
			}
			if checked != tc.wantRoleGrants {
				t.Errorf("replication role checked = %t, want %t (sql %q)", checked, tc.wantRoleGrants, sql)
			}
		})
	}
}

func TestClusterReconcilerNotPresentSkipsCluster(t *testing.T) {
	ctx := context.Background()
	scheme := replicationScheme(t)
	acct := &account{ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acct).Build()
	hooks := &accountReplication{rc: core.NewReplicationContext(core.ReplicationNotPresent, "", "")}
	r := &core.ClusterReconciler[*account]{
		Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10),
		Adapter: accountAdapter{}, Replication: hooks,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "acct", Namespace: "ns"}}
	for range 2 {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	clusters := &cnpgv1.ClusterList{}
	if err := c.List(ctx, clusters); err != nil {
		t.Fatal(err)
	}
	if len(clusters.Items) != 0 || hooks.added != 0 {
		t.Fatalf("not-present region rendered %d clusters and %d hook calls", len(clusters.Items), hooks.added)
	}
}

type backupAdapter struct{}

func (backupAdapter) New() *dbv1.Backup                        { return &dbv1.Backup{} }
func (backupAdapter) NewList() client.ObjectList               { return &dbv1.BackupList{} }
func (backupAdapter) Spec(b *dbv1.Backup) *dbv1.BackupSpec     { return &b.Spec }
func (backupAdapter) Status(b *dbv1.Backup) *dbv1.BackupStatus { return &b.Status }
func (backupAdapter) Items(list client.ObjectList) []*dbv1.Backup {
	items := []*dbv1.Backup{}
	for i := range list.(*dbv1.BackupList).Items {
		items = append(items, &list.(*dbv1.BackupList).Items[i])
	}
	return items
}

// TestBackupReconcilerReplicationGating shows that backups run only on a
// primary whose endpoint is enabled (no promotion pending).
func TestBackupReconcilerReplicationGating(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rc          *core.ReplicationContext
		wantCreated bool
		wantPhase   cnpgv1.BackupPhase
		wantRequeue time.Duration
	}{
		{"primary", regionContext(core.ReplicationPrimary, "acct-1", "acct-1"), true, "", 5 * time.Second},
		{"primary awaiting promotion", regionContext(core.ReplicationPrimary, "acct-1", "acct-2"), false, "", time.Minute},
		{"replica", regionContext(core.ReplicationReplica, "", ""), false, dbv1.BackupPhaseSkipped, 0}, // requeues for expiry
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := replicationScheme(t)
			acct := &account{ObjectMeta: metav1.ObjectMeta{Name: "acct", Namespace: "ns", UID: "uid-1"}}
			backup := &dbv1.Backup{
				ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns", UID: "uid-b"},
				Spec:       dbv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "acct"}},
			}
			vsc := &snapshotv1.VolumeSnapshotClass{
				ObjectMeta: metav1.ObjectMeta{Name: "default", Annotations: map[string]string{"snapshot.storage.kubernetes.io/is-default-class": "true"}},
				Driver:     "disk.csi.example.com",
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(acct, backup, vsc).
				WithStatusSubresource(&dbv1.Backup{}).Build()
			r := &core.BackupReconciler[*account, *dbv1.Backup]{
				Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10),
				Cluster: accountAdapter{}, Backups: backupAdapter{},
				Replication: &accountReplication{rc: tc.rc},
			}
			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "b1", Namespace: "ns"}})
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantPhase == "" && res.RequeueAfter != tc.wantRequeue {
				t.Errorf("requeue = %v, want %v", res.RequeueAfter, tc.wantRequeue)
			}
			cnpgBackup := &cnpgv1.Backup{}
			err = c.Get(ctx, client.ObjectKey{Name: "b1", Namespace: "ns"}, cnpgBackup)
			if created := err == nil; created != tc.wantCreated {
				t.Fatalf("CNPG backup created = %t, want %t (err %v)", created, tc.wantCreated, err)
			}
			if tc.wantCreated && cnpgBackup.Spec.Cluster.Name != "acct" {
				t.Errorf("CNPG backup cluster = %q, want acct", cnpgBackup.Spec.Cluster.Name)
			}
			got := &dbv1.Backup{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(backup), got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Phase != tc.wantPhase {
				t.Errorf("backup phase = %q, want %q", got.Status.Phase, tc.wantPhase)
			}
		})
	}
}
