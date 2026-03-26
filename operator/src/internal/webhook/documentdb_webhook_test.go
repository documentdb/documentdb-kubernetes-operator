// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"testing"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestDocumentDB(version, schemaVersion, image string) *dbpreview.DocumentDB {
	db := &dbpreview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-db",
			Namespace: "default",
		},
		Spec: dbpreview.DocumentDBSpec{
			NodeCount:        1,
			InstancesPerNode: 1,
			Resource: dbpreview.Resource{
				Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
			},
		},
	}
	if version != "" {
		db.Spec.DocumentDBVersion = version
	}
	if schemaVersion != "" {
		db.Spec.SchemaVersion = schemaVersion
	}
	if image != "" {
		db.Spec.DocumentDBImage = image
	}
	return db
}

func TestValidateCreate_AllowsValidSpec(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("0.112.0", "", "")
	warnings, err := v.ValidateCreate(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateCreate_AllowsAutoSchemaVersion(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("0.112.0", "auto", "")
	warnings, err := v.ValidateCreate(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateCreate_AllowsSchemaVersionEqualToBinary(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("0.112.0", "0.112.0", "")
	warnings, err := v.ValidateCreate(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateCreate_AllowsSchemaVersionBelowBinary(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("0.112.0", "0.110.0", "")
	warnings, err := v.ValidateCreate(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateCreate_RejectsSchemaVersionAboveBinary(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("0.110.0", "0.112.0", "")
	_, err := v.ValidateCreate(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when schemaVersion > binaryVersion, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestValidateCreate_AllowsSchemaVersionWithImageTag(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("", "0.112.0", "ghcr.io/documentdb/documentdb:0.112.0")
	warnings, err := v.ValidateCreate(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateCreate_RejectsSchemaVersionAboveImageTag(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("", "0.115.0", "ghcr.io/documentdb/documentdb:0.112.0")
	_, err := v.ValidateCreate(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when schemaVersion > image tag version, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestValidateCreate_AllowsNoBinaryVersion(t *testing.T) {
	// When no binary version can be resolved, skip validation (operator defaults will apply)
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("", "0.112.0", "")
	warnings, err := v.ValidateCreate(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error when binary version unknown, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateUpdate_AllowsValidUpgrade(t *testing.T) {
	v := &DocumentDBValidator{}
	oldDB := newTestDocumentDB("0.110.0", "", "")
	oldDB.Status.SchemaVersion = "0.110.0"
	newDB := newTestDocumentDB("0.112.0", "", "")
	warnings, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateUpdate_BlocksImageRollbackBelowSchema(t *testing.T) {
	v := &DocumentDBValidator{}
	oldDB := newTestDocumentDB("0.112.0", "auto", "")
	oldDB.Status.SchemaVersion = "0.112.0"
	newDB := newTestDocumentDB("0.110.0", "auto", "")
	_, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
	if err == nil {
		t.Fatal("expected error when rolling back below schema version, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestValidateUpdate_AllowsImageRollbackWhenNoSchema(t *testing.T) {
	v := &DocumentDBValidator{}
	oldDB := newTestDocumentDB("0.112.0", "", "")
	// No schema version installed — rollback is safe
	newDB := newTestDocumentDB("0.110.0", "", "")
	warnings, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
	if err != nil {
		t.Fatalf("expected no error when no schema installed, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateUpdate_AllowsSameVersionOnUpdate(t *testing.T) {
	v := &DocumentDBValidator{}
	oldDB := newTestDocumentDB("0.112.0", "auto", "")
	oldDB.Status.SchemaVersion = "0.112.0"
	newDB := newTestDocumentDB("0.112.0", "auto", "")
	warnings, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
	if err != nil {
		t.Fatalf("expected no error for same version, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestValidateUpdate_BlocksSchemaVersionAboveBinary(t *testing.T) {
	v := &DocumentDBValidator{}
	oldDB := newTestDocumentDB("0.110.0", "", "")
	oldDB.Status.SchemaVersion = "0.110.0"
	newDB := newTestDocumentDB("0.110.0", "0.112.0", "")
	_, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
	if err == nil {
		t.Fatal("expected error when schemaVersion > binary on update, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestValidateUpdate_ImageRollbackBlockedViaImageField(t *testing.T) {
	v := &DocumentDBValidator{}
	oldDB := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.112.0")
	oldDB.Status.SchemaVersion = "0.112.0"
	newDB := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.110.0")
	_, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
	if err == nil {
		t.Fatal("expected error when rolling back image below schema, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestValidateDelete_AlwaysAllowed(t *testing.T) {
	v := &DocumentDBValidator{}
	db := newTestDocumentDB("0.112.0", "auto", "")
	warnings, err := v.ValidateDelete(context.Background(), db)
	if err != nil {
		t.Fatalf("expected no error on delete, got %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
}

func TestResolveBinaryVersion_PrefersImageTag(t *testing.T) {
	db := newTestDocumentDB("0.110.0", "", "ghcr.io/documentdb/documentdb:0.112.0")
	v := resolveBinaryVersion(db)
	if v != "0.112.0" {
		t.Fatalf("expected 0.112.0 from image tag, got %s", v)
	}
}

func TestResolveBinaryVersion_FallsBackToDocumentDBVersion(t *testing.T) {
	db := newTestDocumentDB("0.110.0", "", "")
	v := resolveBinaryVersion(db)
	if v != "0.110.0" {
		t.Fatalf("expected 0.110.0, got %s", v)
	}
}

func TestResolveBinaryVersion_ReturnsEmptyWhenNoneSet(t *testing.T) {
	db := newTestDocumentDB("", "", "")
	v := resolveBinaryVersion(db)
	if v != "" {
		t.Fatalf("expected empty, got %s", v)
	}
}
