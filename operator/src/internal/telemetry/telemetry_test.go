// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"testing"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestNoopDocumentDBTelemetry(t *testing.T) {
	noop := NoopDocumentDBTelemetry{}
	// Should not panic
	noop.ClusterCreated(context.Background(), &dbpreview.DocumentDB{}, 1.0)
	noop.ClusterUpdated(context.Background(), &dbpreview.DocumentDB{}, "scale", 1.0)
	noop.ClusterDeleted(context.Background(), &dbpreview.DocumentDB{}, nil)
}

func TestNoopBackupTelemetry(t *testing.T) {
	noop := NoopBackupTelemetry{}
	noop.BackupCreated(context.Background(), &dbpreview.Backup{}, &dbpreview.DocumentDB{}, "on-demand")
	noop.BackupExpired(context.Background(), &dbpreview.Backup{}, nil)
	noop.BackupDeleted(context.Background(), &dbpreview.Backup{}, "expired")
}

func TestNoopScheduledBackupTelemetry(t *testing.T) {
	noop := NoopScheduledBackupTelemetry{}
	noop.ScheduledBackupCreated(context.Background(), &dbpreview.ScheduledBackup{}, &dbpreview.DocumentDB{})
}

func TestNewDocumentDBTelemetry_NilManager(t *testing.T) {
	adapter := NewDocumentDBTelemetry(nil)
	if _, ok := adapter.(NoopDocumentDBTelemetry); !ok {
		t.Error("expected NoopDocumentDBTelemetry when manager is nil")
	}
}

func TestNewBackupTelemetry_NilManager(t *testing.T) {
	adapter := NewBackupTelemetry(nil)
	if _, ok := adapter.(NoopBackupTelemetry); !ok {
		t.Error("expected NoopBackupTelemetry when manager is nil")
	}
}

func TestNewScheduledBackupTelemetry_NilManager(t *testing.T) {
	adapter := NewScheduledBackupTelemetry(nil)
	if _, ok := adapter.(NoopScheduledBackupTelemetry); !ok {
		t.Error("expected NoopScheduledBackupTelemetry when manager is nil")
	}
}

func TestCategorizePVCSize(t *testing.T) {
	tests := []struct {
		input    string
		expected PVCSizeCategory
	}{
		{"", "unknown"},
		{"10Gi", PVCSizeSmall},
		{"49Gi", PVCSizeSmall},
		{"50Gi", PVCSizeMedium},
		{"200Gi", PVCSizeMedium},
		{"201Gi", PVCSizeLarge},
		{"1000Gi", PVCSizeLarge},
	}

	for _, tc := range tests {
		result := categorizePVCSize(tc.input)
		if result != tc.expected {
			t.Errorf("categorizePVCSize(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestHashForClusterID(t *testing.T) {
	// Should be deterministic
	id1 := hashForClusterID("aks", "sub1/rg1")
	id2 := hashForClusterID("aks", "sub1/rg1")
	if id1 != id2 {
		t.Errorf("hashForClusterID not deterministic: %s != %s", id1, id2)
	}

	// Different inputs should produce different IDs
	id3 := hashForClusterID("aks", "sub2/rg2")
	if id1 == id3 {
		t.Error("different inputs produced same hash")
	}

	// Different providers should produce different IDs
	id4 := hashForClusterID("eks", "sub1/rg1")
	if id1 == id4 {
		t.Error("different providers produced same hash")
	}

	// Should be 32 hex chars (16 bytes)
	if len(id1) != 32 {
		t.Errorf("expected 32 char hex string, got %d chars", len(id1))
	}
}

func TestDetectAKSClusterIdentity(t *testing.T) {
	// With AKS labels
	labels := map[string]string{
		"kubernetes.azure.com/subscription":   "sub-123",
		"kubernetes.azure.com/resource-group": "rg-test",
	}
	id := detectAKSClusterIdentity(labels, "")
	if id != "sub-123/rg-test" {
		t.Errorf("expected 'sub-123/rg-test', got '%s'", id)
	}

	// With providerID
	id2 := detectAKSClusterIdentity(map[string]string{}, "azure:///subscriptions/sub-456/resourceGroups/rg-prod/providers/Microsoft.Compute/virtualMachineScaleSets/aks-nodepool/virtualMachines/0")
	if id2 != "sub-456/rg-prod" {
		t.Errorf("expected 'sub-456/rg-prod', got '%s'", id2)
	}

	// Neither
	id3 := detectAKSClusterIdentity(map[string]string{}, "")
	if id3 != "" {
		t.Errorf("expected empty, got '%s'", id3)
	}
}

func TestDetectEKSClusterIdentity(t *testing.T) {
	labels := map[string]string{
		"topology.kubernetes.io/region":  "us-west-2",
		"alpha.eksctl.io/cluster-name":   "my-cluster",
	}
	id := detectEKSClusterIdentity(labels)
	if id != "us-west-2/my-cluster" {
		t.Errorf("expected 'us-west-2/my-cluster', got '%s'", id)
	}

	// Missing labels
	id2 := detectEKSClusterIdentity(map[string]string{})
	if id2 != "" {
		t.Errorf("expected empty, got '%s'", id2)
	}
}

func TestDetectGKEClusterIdentity(t *testing.T) {
	labels := map[string]string{
		"cloud.google.com/gke-nodepool": "default-pool",
	}
	id := detectGKEClusterIdentity(labels, "gce://my-project/us-central1-a/instance-1")
	if id != "my-project/default-pool" {
		t.Errorf("expected 'my-project/default-pool', got '%s'", id)
	}

	// No GKE providerID
	id2 := detectGKEClusterIdentity(map[string]string{}, "aws://something")
	if id2 != "" {
		t.Errorf("expected empty, got '%s'", id2)
	}
}

func TestHashNamespace(t *testing.T) {
	h1 := HashNamespace("default")
	h2 := HashNamespace("default")
	if h1 != h2 {
		t.Error("HashNamespace not deterministic")
	}

	h3 := HashNamespace("kube-system")
	if h1 == h3 {
		t.Error("different namespaces produced same hash")
	}
}

// backupWithUID creates a Backup with a specific UID for testing adapters.
func backupWithUID(uid string) *dbpreview.Backup {
	return &dbpreview.Backup{
		ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(uid),
			Name:      "test-backup",
			Namespace: "test-ns",
		},
	}
}

func TestBackupTelemetryImpl_BackupDeleted_NilManager(t *testing.T) {
	// NoopBackupTelemetry should not panic
	noop := NoopBackupTelemetry{}
	noop.BackupDeleted(context.Background(), backupWithUID("uid-1"), "manual")
}
