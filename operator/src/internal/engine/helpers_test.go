// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	cnpg "github.com/documentdb/documentdb-operator/internal/cnpg"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

const testNamespace = "default"

var errInjected = errors.New("injected failure")

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		dbpreview.AddToScheme,
		cnpgv1.AddToScheme,
		snapshotv1.AddToScheme,
		cmapi.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("add to scheme: %v", err)
		}
	}
	return s
}

// newFakeClient builds a fake client with status subresources and the Backup
// cluster index registered, plus optional interceptors.
func newFakeClient(t *testing.T, s *runtime.Scheme, funcs *interceptor.Funcs, objs ...client.Object) client.Client {
	t.Helper()
	b := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&dbpreview.DocumentDB{}, &dbpreview.Backup{}, &dbpreview.ScheduledBackup{}).
		WithIndex(&dbpreview.Backup{}, BackupClusterField, func(o client.Object) []string {
			return []string{o.(*dbpreview.Backup).Spec.Cluster.Name}
		})
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	return b.Build()
}

// drainEvents returns all events currently buffered in a fake recorder.
func drainEvents(rec *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case e := <-rec.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}

func hasEvent(events []string, substr string) bool {
	for _, e := range events {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// fakeReplication is a configurable ReplicationHooks implementation.
type fakeReplication[T client.Object] struct {
	rc            *util.ReplicationContext
	contextErr    error
	addErr        error
	syncOps       []cnpg.JSONPatch
	syncErr       error
	syncRequeue   time.Duration
	primaryResult *ctrl.Result
	netResult     *ctrl.Result

	addCalled  bool
	syncCalled bool
}

func (f *fakeReplication[T]) Context(context.Context, T) (*util.ReplicationContext, error) {
	return f.rc, f.contextErr
}

func (f *fakeReplication[T]) AddToClusterSpec(context.Context, T, *util.ReplicationContext, *cnpgv1.Cluster) error {
	f.addCalled = true
	return f.addErr
}

func (f *fakeReplication[T]) SyncChanges(context.Context, *cnpgv1.Cluster, *cnpgv1.Cluster, T, *util.ReplicationContext) ([]cnpg.JSONPatch, error, time.Duration) {
	f.syncCalled = true
	return f.syncOps, f.syncErr, f.syncRequeue
}

func (f *fakeReplication[T]) ReconcilePrimary(context.Context, T, *util.ReplicationContext, *cnpgv1.Cluster) *ctrl.Result {
	return f.primaryResult
}

func (f *fakeReplication[T]) ReconcileNetworking(context.Context, T, *util.ReplicationContext) *ctrl.Result {
	return f.netResult
}
