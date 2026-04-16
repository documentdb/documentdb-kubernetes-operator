// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
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

var _ = Describe("schema version validation", func() {
	var v *DocumentDBValidator

	BeforeEach(func() {
		v = &DocumentDBValidator{}
	})

	It("allows an empty schemaVersion", func() {
		db := newTestDocumentDB("0.112.0", "", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(BeEmpty())
	})

	It("allows schemaVersion set to auto", func() {
		db := newTestDocumentDB("0.112.0", "auto", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(BeEmpty())
	})

	It("allows schemaVersion equal to binary version", func() {
		db := newTestDocumentDB("0.112.0", "0.112.0", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(BeEmpty())
	})

	It("allows schemaVersion below binary version", func() {
		db := newTestDocumentDB("0.112.0", "0.110.0", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(BeEmpty())
	})

	It("rejects schemaVersion above binary version", func() {
		db := newTestDocumentDB("0.110.0", "0.112.0", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("exceeds the binary version"))
	})

	It("allows schemaVersion equal to image tag version", func() {
		db := newTestDocumentDB("", "0.112.0", "ghcr.io/documentdb/documentdb:0.112.0")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(BeEmpty())
	})

	It("rejects schemaVersion above image tag version", func() {
		db := newTestDocumentDB("", "0.115.0", "ghcr.io/documentdb/documentdb:0.112.0")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("exceeds the binary version"))
	})

	It("rejects explicit schemaVersion when no binary version can be resolved", func() {
		db := newTestDocumentDB("", "0.112.0", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("cannot set an explicit schemaVersion without also setting"))
	})

	It("rejects when version comparison fails due to unparseable version", func() {
		db := newTestDocumentDB("invalid", "0.112.0", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("version comparison failed"))
	})
})

var _ = Describe("image rollback validation", func() {
	var v *DocumentDBValidator

	BeforeEach(func() {
		v = &DocumentDBValidator{}
	})

	It("allows upgrade above installed schema version", func() {
		oldDB := newTestDocumentDB("0.110.0", "", "")
		oldDB.Status.SchemaVersion = "0.110.0"
		newDB := newTestDocumentDB("0.112.0", "", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(BeEmpty())
	})

	It("blocks image rollback below installed schema version", func() {
		oldDB := newTestDocumentDB("0.112.0", "auto", "")
		oldDB.Status.SchemaVersion = "0.112.0"
		newDB := newTestDocumentDB("0.110.0", "auto", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("image rollback blocked"))
	})

	It("allows rollback when no schema version is installed", func() {
		oldDB := newTestDocumentDB("0.112.0", "", "")
		newDB := newTestDocumentDB("0.110.0", "", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(BeEmpty())
	})

	It("allows same version on update", func() {
		oldDB := newTestDocumentDB("0.112.0", "auto", "")
		oldDB.Status.SchemaVersion = "0.112.0"
		newDB := newTestDocumentDB("0.112.0", "auto", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(BeEmpty())
	})

	It("blocks image rollback via documentDBImage field", func() {
		oldDB := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.112.0")
		oldDB.Status.SchemaVersion = "0.112.0"
		newDB := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.110.0")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("image rollback blocked"))
	})

	It("skips validation when new binary version cannot be resolved", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Status.SchemaVersion = "0.112.0"
		newDB := newTestDocumentDB("", "", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(BeEmpty())
	})

	It("skips validation when image fields are unchanged (non-image patch)", func() {
		oldDB := newTestDocumentDB("0.112.0", "", "")
		oldDB.Status.SchemaVersion = "0.112.0"
		// Same documentDBVersion, no image change — e.g., PV reclaim policy patch
		newDB := newTestDocumentDB("0.112.0", "", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(BeEmpty())
	})

	It("rejects when version comparison fails due to unparseable version", func() {
		oldDB := newTestDocumentDB("invalid-old", "", "")
		oldDB.Status.SchemaVersion = "invalid"
		newDB := newTestDocumentDB("invalid-new", "", "")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Detail).To(ContainSubstring("version comparison failed"))
	})

	It("skips validation when image changes to unparseable tag", func() {
		oldDB := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.112.0")
		oldDB.Status.SchemaVersion = "0.112.0"
		newDB := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:latest")
		result := v.validateImageRollback(newDB, oldDB)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("ValidateCreate admission handler", func() {
	var v *DocumentDBValidator

	BeforeEach(func() {
		v = &DocumentDBValidator{}
	})

	It("allows a valid DocumentDB resource", func() {
		db := newTestDocumentDB("0.112.0", "", "")
		warnings, err := v.ValidateCreate(context.Background(), db)
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())
	})

	It("rejects a resource with schemaVersion above binary", func() {
		db := newTestDocumentDB("0.110.0", "0.112.0", "")
		_, err := v.ValidateCreate(context.Background(), db)
		Expect(err).To(HaveOccurred())
	})

	It("returns error for non-DocumentDB object", func() {
		_, err := v.ValidateCreate(context.Background(), &dbpreview.Backup{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected DocumentDB"))
	})
})

var _ = Describe("ValidateUpdate admission handler", func() {
	var v *DocumentDBValidator

	BeforeEach(func() {
		v = &DocumentDBValidator{}
	})

	It("allows a valid upgrade", func() {
		oldDB := newTestDocumentDB("0.110.0", "", "")
		oldDB.Status.SchemaVersion = "0.110.0"
		newDB := newTestDocumentDB("0.112.0", "", "")
		warnings, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())
	})

	It("rejects rollback below installed schema version", func() {
		oldDB := newTestDocumentDB("0.112.0", "auto", "")
		oldDB.Status.SchemaVersion = "0.112.0"
		newDB := newTestDocumentDB("0.110.0", "auto", "")
		_, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
		Expect(err).To(HaveOccurred())
	})

	It("rejects schemaVersion above binary on update", func() {
		oldDB := newTestDocumentDB("0.110.0", "", "")
		oldDB.Status.SchemaVersion = "0.110.0"
		newDB := newTestDocumentDB("0.110.0", "0.112.0", "")
		_, err := v.ValidateUpdate(context.Background(), oldDB, newDB)
		Expect(err).To(HaveOccurred())
	})

	It("returns error when newObj is not a DocumentDB", func() {
		oldDB := newTestDocumentDB("0.110.0", "", "")
		_, err := v.ValidateUpdate(context.Background(), oldDB, &dbpreview.Backup{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected DocumentDB"))
	})

	It("returns error when oldObj is not a DocumentDB", func() {
		newDB := newTestDocumentDB("0.112.0", "", "")
		_, err := v.ValidateUpdate(context.Background(), &dbpreview.Backup{}, newDB)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected DocumentDB"))
	})
})

var _ = Describe("ValidateDelete admission handler", func() {
	It("always allows deletion", func() {
		v := &DocumentDBValidator{}
		db := newTestDocumentDB("0.112.0", "auto", "")
		warnings, err := v.ValidateDelete(context.Background(), db)
		Expect(err).ToNot(HaveOccurred())
		Expect(warnings).To(BeEmpty())
	})
})

var _ = Describe("resolveBinaryVersion helper", func() {
	It("prefers the image tag over documentDBVersion", func() {
		db := newTestDocumentDB("0.110.0", "", "ghcr.io/documentdb/documentdb:0.112.0")
		Expect(resolveBinaryVersion(db)).To(Equal("0.112.0"))
	})

	It("falls back to documentDBVersion when no image is set", func() {
		db := newTestDocumentDB("0.110.0", "", "")
		Expect(resolveBinaryVersion(db)).To(Equal("0.110.0"))
	})

	It("returns empty when neither image nor version is set", func() {
		db := newTestDocumentDB("", "", "")
		Expect(resolveBinaryVersion(db)).To(BeEmpty())
	})

	It("extracts semver from tag with architecture suffix", func() {
		db := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.112.0-amd64")
		Expect(resolveBinaryVersion(db)).To(Equal("0.112.0"))
	})

	It("falls back to documentDBVersion for digest-only references", func() {
		db := newTestDocumentDB("0.112.0", "", "ghcr.io/documentdb/documentdb@sha256:abc123")
		Expect(resolveBinaryVersion(db)).To(Equal("0.112.0"))
	})

	It("returns empty for digest-only reference with no documentDBVersion", func() {
		db := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb@sha256:abc123")
		Expect(resolveBinaryVersion(db)).To(BeEmpty())
	})

	It("handles image with port in registry and tag", func() {
		db := newTestDocumentDB("", "", "localhost:5000/documentdb:0.112.0")
		Expect(resolveBinaryVersion(db)).To(Equal("0.112.0"))
	})
})

var _ = Describe("extractSemver helper", func() {
	It("extracts clean semver", func() {
		Expect(extractSemver("0.112.0")).To(Equal("0.112.0"))
	})

	It("extracts semver from tag with suffix", func() {
		Expect(extractSemver("0.112.0-amd64")).To(Equal("0.112.0"))
	})

	It("returns empty for non-semver tag", func() {
		Expect(extractSemver("latest")).To(BeEmpty())
	})

	It("returns empty for empty string", func() {
		Expect(extractSemver("")).To(BeEmpty())
	})

	It("returns empty for non-numeric major", func() {
		Expect(extractSemver("abc.112.0")).To(BeEmpty())
	})

	It("returns empty for non-numeric minor", func() {
		Expect(extractSemver("0.abc.0")).To(BeEmpty())
	})

	It("returns empty for non-numeric patch", func() {
		Expect(extractSemver("0.112.abc")).To(BeEmpty())
	})
})

var _ = Describe("validateImmutableFields", func() {
	v := &DocumentDBValidator{}

	It("rejects credential secret change", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.DocumentDbCredentialSecret = "original-secret"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.DocumentDbCredentialSecret = "new-secret"

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.documentDbCredentialSecret"))
	})

	It("rejects storage class change", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Resource.Storage.StorageClass = "standard"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Resource.Storage.StorageClass = "premium"

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.resource.storage.storageClass"))
	})

	It("rejects sidecar plugin name change", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.SidecarInjectorPluginName = "original-plugin"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.SidecarInjectorPluginName = "new-plugin"

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.sidecarInjectorPluginName"))
	})

	It("rejects bootstrap config change", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
			},
		}
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "different-backup"},
			},
		}

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.bootstrap"))
	})

	It("allows bootstrap nil-to-nil (both unset)", func() {
		oldDB := newTestDocumentDB("", "", "")
		newDB := newTestDocumentDB("", "", "")

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})

	It("allows unchanged immutable fields", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.DocumentDbCredentialSecret = "my-secret"
		oldDB.Spec.Resource.Storage.StorageClass = "standard"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.DocumentDbCredentialSecret = "my-secret"
		newDB.Spec.Resource.Storage.StorageClass = "standard"

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})

	It("reports multiple immutable field violations at once", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.DocumentDbCredentialSecret = "old-secret"
		oldDB.Spec.Resource.Storage.StorageClass = "standard"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.DocumentDbCredentialSecret = "new-secret"
		newDB.Spec.Resource.Storage.StorageClass = "premium"

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(HaveLen(2))
	})
})

var _ = Describe("validateStorageResize", func() {
	v := &DocumentDBValidator{}

	It("allows storage size increase", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Resource.Storage.PvcSize = "10Gi"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Resource.Storage.PvcSize = "20Gi"

		errs := v.validateStorageResize(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})

	It("rejects storage size decrease", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Resource.Storage.PvcSize = "20Gi"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Resource.Storage.PvcSize = "10Gi"

		errs := v.validateStorageResize(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.resource.storage.pvcSize"))
		Expect(errs[0].Detail).To(ContainSubstring("shrink"))
	})

	It("allows same size (no-op)", func() {
		oldDB := newTestDocumentDB("", "", "")
		newDB := newTestDocumentDB("", "", "")

		errs := v.validateStorageResize(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})

	It("skips validation when old size is empty", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Resource.Storage.PvcSize = ""
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Resource.Storage.PvcSize = "10Gi"

		errs := v.validateStorageResize(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})
})
