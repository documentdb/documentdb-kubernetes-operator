// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"testing"
	"time"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// newTestManager creates a Manager with a disabled client for testing adapters.
func newTestManager() *Manager {
	client := newDisabledClient()
	return &Manager{
		Client:  client,
		Events:  NewEventTracker(client),
		Metrics: NewMetricsTracker(client),
		operatorCtx: &OperatorContext{
			CloudProvider: CloudProviderAKS,
		},
	}
}

func testDocumentDB() *dbpreview.DocumentDB {
	return &dbpreview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("cluster-uid-123"),
			Name:              "test-cluster",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-24 * time.Hour)),
		},
		Spec: dbpreview.DocumentDBSpec{
			NodeCount:        1,
			InstancesPerNode: 1,
		Resource: dbpreview.Resource{
				Storage: dbpreview.StorageConfiguration{
					PvcSize: "10Gi",
				},
			},
			ExposeViaService: dbpreview.ExposeViaService{
				ServiceType: "ClusterIP",
			},
		},
	}
}

func testBackup() *dbpreview.Backup {
	retentionDays := 7
	return &dbpreview.Backup{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("backup-uid-456"),
			Name:              "test-backup",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
		},
		Spec: dbpreview.BackupSpec{
			RetentionDays: &retentionDays,
		},
	}
}

func testScheduledBackup() *dbpreview.ScheduledBackup {
	retentionDays := 14
	return &dbpreview.ScheduledBackup{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID("sb-uid-789"),
			Name:      "test-scheduled-backup",
			Namespace: "test-ns",
		},
		Spec: dbpreview.ScheduledBackupSpec{
			Schedule:      "0 2 * * *",
			RetentionDays: &retentionDays,
		},
	}
}

func TestDocumentDBTelemetryImpl_ClusterCreated(t *testing.T) {
	mgr := newTestManager()
	adapter := &documentDBTelemetryImpl{mgr: mgr}
	// Should not panic
	adapter.ClusterCreated(context.Background(), testDocumentDB(), 1.5)
}

func TestDocumentDBTelemetryImpl_ClusterUpdated(t *testing.T) {
	mgr := newTestManager()
	adapter := &documentDBTelemetryImpl{mgr: mgr}
	adapter.ClusterUpdated(context.Background(), testDocumentDB(), "scale", 0.5)
}

func TestDocumentDBTelemetryImpl_ClusterDeleted(t *testing.T) {
	mgr := newTestManager()
	adapter := &documentDBTelemetryImpl{mgr: mgr}
	// k8sClient is nil — should handle gracefully (no backup count)
	adapter.ClusterDeleted(context.Background(), testDocumentDB(), nil)
}

func TestDocumentDBTelemetryImpl_ClusterCreated_WithRecovery(t *testing.T) {
	mgr := newTestManager()
	adapter := &documentDBTelemetryImpl{mgr: mgr}
	cluster := testDocumentDB()
	cluster.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
		Recovery: &dbpreview.RecoveryConfiguration{},
	}
	adapter.ClusterCreated(context.Background(), cluster, 2.0)
}

func TestDocumentDBTelemetryImpl_ClusterCreated_WithTLS(t *testing.T) {
	mgr := newTestManager()
	adapter := &documentDBTelemetryImpl{mgr: mgr}
	cluster := testDocumentDB()
	cluster.Spec.TLS = &dbpreview.TLSConfiguration{}
	adapter.ClusterCreated(context.Background(), cluster, 1.0)
}

func TestBackupTelemetryImpl_BackupCreated(t *testing.T) {
	mgr := newTestManager()
	adapter := &backupTelemetryImpl{mgr: mgr}
	adapter.BackupCreated(context.Background(), testBackup(), testDocumentDB(), "on-demand")
}

func TestBackupTelemetryImpl_BackupCreated_Scheduled(t *testing.T) {
	mgr := newTestManager()
	adapter := &backupTelemetryImpl{mgr: mgr}
	adapter.BackupCreated(context.Background(), testBackup(), testDocumentDB(), "scheduled")
}

func TestBackupTelemetryImpl_BackupCreated_NilCluster(t *testing.T) {
	mgr := newTestManager()
	adapter := &backupTelemetryImpl{mgr: mgr}
	adapter.BackupCreated(context.Background(), testBackup(), nil, "on-demand")
}

func TestBackupTelemetryImpl_BackupExpired(t *testing.T) {
	mgr := newTestManager()
	adapter := &backupTelemetryImpl{mgr: mgr}
	// k8sClient is nil — should handle gracefully
	adapter.BackupExpired(context.Background(), testBackup(), nil)
}

func TestBackupTelemetryImpl_BackupDeleted(t *testing.T) {
	mgr := newTestManager()
	adapter := &backupTelemetryImpl{mgr: mgr}
	adapter.BackupDeleted(context.Background(), testBackup(), "expired")
}

func TestBackupTelemetryImpl_BackupDeleted_Manual(t *testing.T) {
	mgr := newTestManager()
	adapter := &backupTelemetryImpl{mgr: mgr}
	adapter.BackupDeleted(context.Background(), testBackup(), "manual")
}

func TestScheduledBackupTelemetryImpl_Created(t *testing.T) {
	mgr := newTestManager()
	adapter := &scheduledBackupTelemetryImpl{mgr: mgr}
	adapter.ScheduledBackupCreated(context.Background(), testScheduledBackup(), testDocumentDB())
}

func TestScheduledBackupTelemetryImpl_Created_NilCluster(t *testing.T) {
	mgr := newTestManager()
	adapter := &scheduledBackupTelemetryImpl{mgr: mgr}
	adapter.ScheduledBackupCreated(context.Background(), testScheduledBackup(), nil)
}

func TestNewDocumentDBTelemetry_WithDisabledManager(t *testing.T) {
	// Disabled manager returns noop (client has no instrumentation key)
	mgr := newTestManager()
	adapter := NewDocumentDBTelemetry(mgr)
	if _, ok := adapter.(NoopDocumentDBTelemetry); !ok {
		t.Error("expected noop adapter when manager client is disabled")
	}
}

func TestNewBackupTelemetry_WithDisabledManager(t *testing.T) {
	mgr := newTestManager()
	adapter := NewBackupTelemetry(mgr)
	if _, ok := adapter.(NoopBackupTelemetry); !ok {
		t.Error("expected noop adapter when manager client is disabled")
	}
}

func TestNewScheduledBackupTelemetry_WithDisabledManager(t *testing.T) {
	mgr := newTestManager()
	adapter := NewScheduledBackupTelemetry(mgr)
	if _, ok := adapter.(NoopScheduledBackupTelemetry); !ok {
		t.Error("expected noop adapter when manager client is disabled")
	}
}
