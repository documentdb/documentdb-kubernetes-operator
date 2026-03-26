// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"

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

	It("skips validation when no binary version can be resolved", func() {
		db := newTestDocumentDB("", "0.112.0", "")
		result := v.validateSchemaVersionNotExceedsBinary(db)
		Expect(result).To(BeEmpty())
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
})
