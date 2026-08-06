// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

type fakeWebhookManager struct {
	ctrl.Manager
	client        ctrlclient.Client
	scheme        *runtime.Scheme
	config        *rest.Config
	webhookServer webhook.Server
}

func (m *fakeWebhookManager) GetClient() ctrlclient.Client {
	return m.client
}

func (m *fakeWebhookManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *fakeWebhookManager) GetConfig() *rest.Config {
	return m.config
}

func (m *fakeWebhookManager) GetWebhookServer() webhook.Server {
	return m.webhookServer
}

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
		db.Spec.Image = &dbpreview.ImageSpec{DocumentDB: image}
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

var _ = Describe("SetupWebhookWithManager", func() {
	It("wires client and registers webhook", func() {
		scheme := runtime.NewScheme()
		Expect(dbpreview.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		mgr := &fakeWebhookManager{
			client:        fakeClient,
			scheme:        scheme,
			config:        &rest.Config{Host: "https://127.0.0.1"},
			webhookServer: webhook.NewServer(webhook.Options{}),
		}

		v := &DocumentDBValidator{}
		Expect(v.SetupWebhookWithManager(mgr)).To(Succeed())
		Expect(v.Client).To(Equal(fakeClient))
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
		// ValidateCreate now runs the ImageVolume capability probe, which
		// issues dry-run Pod creates. Back the validator with a client that
		// models a cluster where ImageVolume is supported so these specs
		// exercise the spec-level validation path.
		imageVolumeConfirmed.Store(false)
		v = newValidatorWithCreate(nil, acceptAll)
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

	It("blocks creation when the cluster lacks ImageVolume support", func() {
		v = newValidatorWithCreate(nil, rejectImageVolume)
		db := newTestDocumentDB("0.112.0", "", "")
		_, err := v.ValidateCreate(context.Background(), db)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ImageVolume feature is not enabled"))
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

var _ = Describe("resolveEffectiveBinaryVersion helper", func() {
	It("returns the spec version when one is set", func() {
		db := newTestDocumentDB("0.112.0", "", "")
		Expect(resolveEffectiveBinaryVersion(db)).To(Equal("0.112.0"))
	})

	It("falls back to the default image version when neither image nor version is set", func() {
		db := newTestDocumentDB("", "", "")
		defaultVersion := resolveBinaryVersion(newTestDocumentDB("", "", util.DEFAULT_DOCUMENTDB_IMAGE))
		Expect(resolveEffectiveBinaryVersion(db)).To(Equal(defaultVersion))
	})

	It("prefers the DOCUMENTDB_VERSION env override over the default", func() {
		GinkgoT().Setenv(util.DOCUMENTDB_VERSION_ENV, "0.115.0")
		db := newTestDocumentDB("", "", "")
		Expect(resolveEffectiveBinaryVersion(db)).To(Equal("0.115.0"))
	})

	It("stays unknown for a digest-only image with no version (does not use the default)", func() {
		db := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb@sha256:abc123")
		Expect(resolveEffectiveBinaryVersion(db)).To(BeEmpty())
	})

	It("stays unknown when the ChangeStreams gate selects a non-semver image", func() {
		// With no version/env and the ChangeStreams gate on, the controller runs the
		// changestream image (non-semver tag). The webhook must treat it as unknown
		// (warn), not silently compare against the default, to stay in step with the
		// controller.
		db := newTestDocumentDB("", "", "")
		db.Spec.FeatureGates = map[string]bool{dbpreview.FeatureGateChangeStreams: true}
		Expect(resolveEffectiveBinaryVersion(db)).To(BeEmpty())
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

	// Note: credentialSecret, storageClass, and sidecarInjectorPluginName immutability
	// is now enforced via CEL transition rules on the CRD schema (see documentdb_types.go).
	// Only bootstrap is validated in the webhook because it's an optional pointer field
	// where CEL transition rules don't reliably catch all mutation patterns.

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

	It("allows unchanged bootstrap configuration", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
			},
		}
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
			},
		}

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})

	It("allows bootstrap removal (set to nil is cleanup)", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
			},
		}
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Bootstrap = nil

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(BeEmpty())
	})

	It("rejects bootstrap addition on running cluster (nil to set)", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Bootstrap = nil
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
			},
		}

		errs := v.validateImmutableFields(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.bootstrap"))
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

	It("rejects invalid old pvcSize", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Resource.Storage.PvcSize = "not-a-quantity"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Resource.Storage.PvcSize = "10Gi"

		errs := v.validateStorageResize(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.resource.storage.pvcSize"))
		Expect(errs[0].Detail).To(ContainSubstring("existing pvcSize is not a valid resource quantity"))
	})

	It("rejects invalid new pvcSize", func() {
		oldDB := newTestDocumentDB("", "", "")
		oldDB.Spec.Resource.Storage.PvcSize = "10Gi"
		newDB := newTestDocumentDB("", "", "")
		newDB.Spec.Resource.Storage.PvcSize = "abc"

		errs := v.validateStorageResize(newDB, oldDB)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.resource.storage.pvcSize"))
		Expect(errs[0].Detail).To(ContainSubstring("pvcSize must be a valid resource quantity"))
	})
})

var _ = Describe("resource envelope validation", func() {
	var v *DocumentDBValidator

	BeforeEach(func() { v = &DocumentDBValidator{} })

	newDB := func() *dbpreview.DocumentDB {
		return newTestDocumentDB("", "", "")
	}

	It("allows an explicit pod memory envelope", func() {
		db := newDB()
		db.Spec.Resource.Memory = "8Gi"
		Expect(v.validateResources(db)).To(BeEmpty())
	})

	It("allows omitting the envelope when gateway and database memory are both set", func() {
		db := newDB()
		db.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "512Mi"}
		db.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "4Gi"}
		Expect(v.validateResources(db)).To(BeEmpty())
	})

	It("rejects a partially specified memory split with no envelope", func() {
		db := newDB()
		db.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "512Mi"}
		Expect(v.validateResources(db)).ToNot(BeEmpty())
	})

	It("allows leaving a dimension entirely unmanaged", func() {
		Expect(v.validateResources(newDB())).To(BeEmpty())
	})

	It("rejects explicit database memory exceeding the envelope", func() {
		db := newDB()
		db.Spec.Resource.Memory = "4Gi"
		db.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "8Gi"}
		Expect(v.validateResources(db)).ToNot(BeEmpty())
	})

	It("rejects a partially specified cpu split with no envelope", func() {
		db := newDB()
		db.Spec.Resource.Database = &dbpreview.ComponentResources{CPU: "2"}
		Expect(v.validateResources(db)).ToNot(BeEmpty())
	})
})

func newRestoreDocumentDB(name, version, backupName string) *dbpreview.DocumentDB {
	db := newTestDocumentDB(version, "", "")
	db.Name = name
	db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
		Recovery: &dbpreview.RecoveryConfiguration{
			Backup: cnpgv1.LocalObjectReference{Name: backupName},
		},
	}
	return db
}

func newBackupWithSchema(name, schemaVersion string) *dbpreview.Backup {
	return &dbpreview.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     dbpreview.BackupStatus{SchemaVersion: schemaVersion},
	}
}

func newValidatorWithObjects(objs ...ctrlclient.Object) *DocumentDBValidator {
	scheme := runtime.NewScheme()
	Expect(dbpreview.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &DocumentDBValidator{Client: fakeClient}
}

func newPVRestoreDocumentDB(name, version, pvName string) *dbpreview.DocumentDB {
	db := newTestDocumentDB(version, "", "")
	db.Name = name
	db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
		Recovery: &dbpreview.RecoveryConfiguration{
			PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: pvName},
		},
	}
	return db
}

func newPVWithSchema(name, schemaVersion string) *corev1.PersistentVolume {
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if schemaVersion != "" {
		pv.Annotations = map[string]string{util.AnnotationSchemaVersion: schemaVersion}
	}
	return pv
}

var _ = Describe("restore schema compatibility validation", func() {
	ctx := context.Background()

	It("is a no-op when there is no bootstrap recovery", func() {
		v := newValidatorWithObjects()
		db := newTestDocumentDB("0.112.0", "", "")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(BeEmpty())
	})

	It("allows restore when binary version equals backup schema version", func() {
		backup := newBackupWithSchema("bk", "0.112.0")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "0.112.0", "bk")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(BeEmpty())
	})

	It("allows restore when binary version is newer than backup schema version", func() {
		backup := newBackupWithSchema("bk", "0.110.0")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "0.112.0", "bk")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(BeEmpty())
	})

	It("rejects restore when binary version is older than backup schema version", func() {
		backup := newBackupWithSchema("bk", "0.112.0")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "0.110.0", "bk")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Detail).To(ContainSubstring("older than the backup's schema version"))
	})

	It("warns when the backup has no recorded schema version", func() {
		backup := newBackupWithSchema("bk", "")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "0.110.0", "bk")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("no recorded schema version"))
	})

	It("warns when the referenced backup does not exist", func() {
		v := newValidatorWithObjects()
		db := newRestoreDocumentDB("restored", "0.110.0", "missing")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("not found"))
	})

	It("blocks restore when no version is set and the default binary is older than the backup schema", func() {
		// With no spec.documentDBVersion/image, the controller applies the operator
		// default. Restoring a newer schema onto it would run an older binary against
		// a newer schema, so admission blocks it.
		backup := newBackupWithSchema("bk", "999.0.0")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "", "bk")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Detail).To(ContainSubstring("older than the backup's schema version"))
	})

	It("allows restore when no version is set and the default binary is >= the backup schema", func() {
		backup := newBackupWithSchema("bk", "0.110.0")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "", "bk")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(BeEmpty())
	})

	It("warns when the restore binary version cannot be determined (digest-only image)", func() {
		// A digest-only image pins an image whose version is unknown at admission,
		// and it takes priority over the operator default, so compatibility can only
		// be warned about, not verified.
		backup := newBackupWithSchema("bk", "0.112.0")
		v := newValidatorWithObjects(backup)
		db := newRestoreDocumentDB("restored", "", "bk")
		db.Spec.Image = &dbpreview.ImageSpec{DocumentDB: "ghcr.io/documentdb/documentdb@sha256:abc123"}
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("cannot determine the target DocumentDB version"))
	})

	It("warns when restoring from a PersistentVolume with an explicit version", func() {
		v := newValidatorWithObjects()
		db := newTestDocumentDB("0.112.0", "", "")
		db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "pv-1"},
			},
		}
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("PersistentVolume"))
	})

	It("warns (does not block) on a PersistentVolume restore that omits an explicit binary version", func() {
		v := newValidatorWithObjects()
		db := newTestDocumentDB("", "", "")
		db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "pv-1"},
			},
		}
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("PersistentVolume"))
	})

	It("allows a PersistentVolume restore when only image.documentDB is set", func() {
		v := newValidatorWithObjects()
		db := newTestDocumentDB("", "", "ghcr.io/documentdb/documentdb:0.112.0")
		db.Spec.Bootstrap = &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "pv-1"},
			},
		}
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("PersistentVolume"))
	})

	It("allows a PersistentVolume restore when the PV annotation schema equals the binary version", func() {
		pv := newPVWithSchema("pv-1", "0.112.0")
		v := newValidatorWithObjects(pv)
		db := newPVRestoreDocumentDB("restored", "0.112.0", "pv-1")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(BeEmpty())
	})

	It("allows a PersistentVolume restore when the binary version is newer than the PV annotation schema", func() {
		pv := newPVWithSchema("pv-1", "0.110.0")
		v := newValidatorWithObjects(pv)
		db := newPVRestoreDocumentDB("restored", "0.112.0", "pv-1")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(BeEmpty())
	})

	It("rejects a PersistentVolume restore when the binary version is older than the PV annotation schema", func() {
		pv := newPVWithSchema("pv-1", "0.112.0")
		v := newValidatorWithObjects(pv)
		db := newPVRestoreDocumentDB("restored", "0.110.0", "pv-1")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(warnings).To(BeEmpty())
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Detail).To(ContainSubstring("older than the PersistentVolume's schema version"))
	})

	It("warns when the PV has no schema annotation (same as a backup with no recorded schema)", func() {
		pv := newPVWithSchema("pv-1", "")
		v := newValidatorWithObjects(pv)
		db := newPVRestoreDocumentDB("restored", "", "pv-1")
		warnings, errs := v.validateRestoreSchemaCompatibility(ctx, db)
		Expect(errs).To(BeEmpty())
		Expect(warnings).To(HaveLen(1))
		Expect(warnings[0]).To(ContainSubstring("no recorded schema version"))
	})
})
