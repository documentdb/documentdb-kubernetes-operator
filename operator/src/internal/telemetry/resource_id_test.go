// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"testing"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGetResourceTelemetryID(t *testing.T) {
	obj := &dbpreview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("abc123-def456-ghi789"),
		},
	}

	id := GetResourceTelemetryID(obj)
	if id != "abc123-def456-ghi789" {
		t.Errorf("expected 'abc123-def456-ghi789', got '%s'", id)
	}
}

func TestGetResourceTelemetryID_EmptyUID(t *testing.T) {
	obj := &dbpreview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{},
	}

	id := GetResourceTelemetryID(obj)
	if id != "" {
		t.Errorf("expected empty string, got '%s'", id)
	}
}

func TestGetResourceTelemetryID_Backup(t *testing.T) {
	obj := &dbpreview.Backup{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("backup-uid-123"),
		},
	}

	id := GetResourceTelemetryID(obj)
	if id != "backup-uid-123" {
		t.Errorf("expected 'backup-uid-123', got '%s'", id)
	}
}
