// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/documentdb/documentdb-operator/internal/cnpg"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
	"github.com/documentdb/documentdb-operator/internal/validation"
)

var documentdbLog = logf.Log.WithName("documentdb-webhook")

// documentDBTerms names DocumentDB's version fields in validation messages.
var documentDBTerms = validation.Terms{
	Product:                   "DocumentDB",
	VersionField:              "spec.documentDBVersion",
	ImageField:                "spec.image.documentDB",
	SchemaWithoutBinaryFields: "spec.documentDBVersion or spec.documentDBImage",
}

// DocumentDBValidator validates DocumentDB resources on create and update.
type DocumentDBValidator struct {
	client.Client
}

var _ admission.Validator[*dbv1.DocumentDB] = &DocumentDBValidator{}

// SetupWebhookWithManager registers the validating webhook with the manager.
func (v *DocumentDBValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr, &dbv1.DocumentDB{}).
		WithValidator(v).
		Complete()
}

// NOTE: The kubebuilder marker below is used for local development with `make run`.
// For Helm-based deployments, the authoritative webhook configuration is in
// operator/documentdb-helm-chart/templates/10_documentdb_webhook.yaml.
// +kubebuilder:webhook:path=/validate-documentdb-io-v1-documentdb,mutating=false,failurePolicy=fail,sideEffects=None,groups=documentdb.io,resources=dbs,verbs=create;update,versions=v1,name=vdocumentdb.kb.io,admissionReviewVersions=v1

// ValidateCreate validates a DocumentDB resource on creation.
func (v *DocumentDBValidator) ValidateCreate(ctx context.Context, documentdb *dbv1.DocumentDB) (admission.Warnings, error) {
	documentdbLog.Info("Validation for DocumentDB upon creation", "name", documentdb.Name, "namespace", documentdb.Namespace)

	// Cluster-capability preflight: block creation up-front when the cluster
	// does not support the ImageVolume feature the operator relies on to mount
	// the DocumentDB extension. Runs only on create because it reflects a
	// cluster-wide capability, not a per-spec property.
	if err := v.ensureImageVolumeSupported(ctx, documentdb.Namespace); err != nil {
		return nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "documentdb.io", Resource: "dbs"},
			documentdb.Name, err)
	}

	allErrs := v.validate(documentdb)

	// Restore-specific compatibility check. Runs only on create because bootstrap
	// is immutable afterward. May both add errors (hard block) and warnings.
	warnings, restoreErrs := v.validateRestoreSchemaCompatibility(ctx, documentdb)
	allErrs = append(allErrs, restoreErrs...)

	if len(allErrs) == 0 {
		return warnings, nil
	}
	return warnings, apierrors.NewInvalid(
		schema.GroupKind{Group: "documentdb.io", Kind: "DocumentDB"},
		documentdb.Name, allErrs)
}

// ValidateUpdate validates a DocumentDB resource on update.
func (v *DocumentDBValidator) ValidateUpdate(_ context.Context, oldDB, newDB *dbv1.DocumentDB) (admission.Warnings, error) {
	documentdbLog.Info("Validation for DocumentDB upon update", "name", newDB.Name, "namespace", newDB.Namespace)

	allErrs := append(
		v.validate(newDB),
		v.validateChanges(newDB, oldDB)...,
	)
	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(
		schema.GroupKind{Group: "documentdb.io", Kind: "DocumentDB"},
		newDB.Name, allErrs)
}

// ValidateDelete is a no-op for DocumentDB.
func (v *DocumentDBValidator) ValidateDelete(_ context.Context, _ *dbv1.DocumentDB) (admission.Warnings, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Spec-level validations (run on both create and update)
// ---------------------------------------------------------------------------

// validate runs all spec-level validation rules, returning a combined error list.
func (v *DocumentDBValidator) validate(db *dbv1.DocumentDB) (allErrs field.ErrorList) {
	type validationFunc func(*dbv1.DocumentDB) field.ErrorList
	validations := []validationFunc{
		v.validateSchemaVersionNotExceedsBinary,
		v.validateResources,
		// Add new spec-level validations here.
	}
	for _, fn := range validations {
		allErrs = append(allErrs, fn(db)...)
	}
	return allErrs
}

// validateResources ensures spec.resource is consistent under the
// envelope-optional model: the pod memory/cpu envelope may be omitted only when
// the gateway and database both specify that dimension, and an explicit envelope
// must leave room for PostgreSQL after the sidecar reservations.
func (v *DocumentDBValidator) validateResources(db *dbv1.DocumentDB) field.ErrorList {
	return cnpg.ValidateResources(db, cnpg.DefaultSplitConfig())
}

// validateSchemaVersionNotExceedsBinary ensures spec.schemaVersion <= binary version.
func (v *DocumentDBValidator) validateSchemaVersionNotExceedsBinary(db *dbv1.DocumentDB) field.ErrorList {
	return validation.SchemaVersionNotExceedsBinary(db.Spec.SchemaVersion, resolveBinaryVersion(db), documentDBTerms)
}

// ---------------------------------------------------------------------------
// Update-only validations (compare old and new)
// ---------------------------------------------------------------------------

// validateChanges runs all update-specific validation rules that compare old vs new state.
func (v *DocumentDBValidator) validateChanges(newDB, oldDB *dbv1.DocumentDB) (allErrs field.ErrorList) {
	type validationFunc func(newDB, oldDB *dbv1.DocumentDB) field.ErrorList
	validations := []validationFunc{
		v.validateImageRollback,
		v.validateImmutableFields,
		v.validateStorageResize,
	}
	for _, fn := range validations {
		allErrs = append(allErrs, fn(newDB, oldDB)...)
	}
	return allErrs
}

// validateImageRollback blocks image downgrades below the installed schema version.
// Once ALTER EXTENSION UPDATE has run, the schema is irreversible. Running an older
// binary against a newer schema is untested and may cause data corruption.
func (v *DocumentDBValidator) validateImageRollback(newDB, oldDB *dbv1.DocumentDB) field.ErrorList {
	installedSchemaVersion := oldDB.Status.SchemaVersion
	if installedSchemaVersion == "" {
		return nil
	}

	// Only check rollback when an image-related field is actually changing.
	// This avoids false positives on unrelated patches (e.g., PV reclaim policy)
	// where the image tag may not represent the extension version (e.g., CI tags
	// like "0.2.0-test-12345" where 0.2.0 is the chart version, not the extension).
	if newDB.Spec.DocumentDBVersion == oldDB.Spec.DocumentDBVersion &&
		specImageDocumentDB(newDB) == specImageDocumentDB(oldDB) {
		return nil
	}

	return validation.ImageRollback(installedSchemaVersion, resolveBinaryVersion(newDB))
}

// ---------------------------------------------------------------------------
// Restore validation (create-only)
// ---------------------------------------------------------------------------

// validateRestoreSchemaCompatibility validates that a restore's target binary
// version is compatible with the schema version the source was taken at.
//
// "Binary version" here is the DocumentDB extension version the new cluster will
// run (resolved from spec.documentDBVersion / spec.image.documentDB, or the
// operator default) — the same value surfaced to users in messages as the
// "DocumentDB version". The "schema version" is the extension schema version the
// source data was written at.
//
// A restore is a physical recovery: the restored catalog (including the
// documentdb extension schema) comes back at source-time schema version, while
// the new cluster's binary version is chosen independently. Running an older
// binary against a newer, irreversible schema risks data corruption, and the
// rollback guard (validateImageRollback) cannot catch it because a fresh restore
// has an empty status.
//
// Rules:
//   - binary >= schema  → allowed (schema catch-up handled by the two-phase upgrade flow)
//   - binary <  schema  → rejected
//   - schema or binary version unknown → allowed with a warning
//
// It orchestrates three single-purpose steps: identify the restore source,
// resolve that source's schema version, and compare it against the effective
// binary version. Backup-CR restores read the schema from Backup.Status; PV
// restores read it from the source PV's annotation (stamped by the PV
// controller). Both sources follow the same logic: when the schema version
// cannot be determined the restore is allowed with a warning; only a resolved
// binary older than a known schema is rejected.
func (v *DocumentDBValidator) validateRestoreSchemaCompatibility(ctx context.Context, newDB *dbv1.DocumentDB) (admission.Warnings, field.ErrorList) {
	if newDB.Spec.Bootstrap == nil || newDB.Spec.Bootstrap.Recovery == nil {
		return nil, nil
	}
	src, ok := restoreSourceFor(newDB.Spec.Bootstrap.Recovery)
	if !ok {
		return nil, nil // no recognizable restore source to validate
	}

	schemaVersion, warnings := v.resolveSourceSchemaVersion(ctx, newDB.Namespace, src)
	if len(warnings) > 0 {
		return warnings, nil
	}

	return validation.CompareBinaryToSchema(resolveEffectiveBinaryVersion(newDB), schemaVersion, src, documentDBTerms)
}

// restoreSourceFor identifies the restore source from a recovery configuration,
// returning false when neither a backup nor a PersistentVolume source is set.
func restoreSourceFor(recovery *dbv1.RecoveryConfiguration) (validation.RestoreSource, bool) {
	base := field.NewPath("spec", "bootstrap", "recovery")
	if recovery.Backup.Name != "" {
		return validation.BackupSource(base, recovery.Backup.Name), true
	}
	if recovery.PersistentVolume != nil && recovery.PersistentVolume.Name != "" {
		return validation.PVSource(base, recovery.PersistentVolume.Name), true
	}
	return validation.RestoreSource{}, false
}

// resolveSourceSchemaVersion reads the restore source and returns its recorded
// schema version. See validation.SourceSchemaVersion.
func (v *DocumentDBValidator) resolveSourceSchemaVersion(ctx context.Context, namespace string, src validation.RestoreSource) (string, admission.Warnings) {
	return validation.SourceSchemaVersion(ctx, v.Client, src, util.AnnotationSchemaVersion, func() (string, error) {
		backup := &dbv1.Backup{}
		if err := v.Get(ctx, client.ObjectKey{Name: src.Name, Namespace: namespace}, backup); err != nil {
			return "", err
		}
		return backup.Status.SchemaVersion, nil
	})
}

// validateImmutableFields rejects updates to fields that cannot be changed after creation.
// Note: credentialSecret, storageClass, and sidecarInjectorPluginName are enforced via
// CEL transition rules on the CRD schema (see documentdb_types.go).
func (v *DocumentDBValidator) validateImmutableFields(newDB, oldDB *dbv1.DocumentDB) field.ErrorList {
	var allErrs field.ErrorList

	// Bootstrap configuration is only used during initial cluster creation and is
	// ignored afterward. Setting it to nil (cleanup) is allowed, but changing to a
	// different value is rejected since it cannot re-bootstrap a running cluster.
	// This is kept in the webhook (not CEL) because it's an optional pointer field
	// where CEL transition rules don't reliably catch all mutation patterns.
	if newDB.Spec.Bootstrap != nil && !isBootstrapEqual(newDB.Spec.Bootstrap, oldDB.Spec.Bootstrap) {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "bootstrap"),
			"bootstrap configuration cannot be changed after cluster creation",
		))
	}

	return allErrs
}

// validateStorageResize ensures PVC size can only grow, never shrink.
func (v *DocumentDBValidator) validateStorageResize(newDB, oldDB *dbv1.DocumentDB) field.ErrorList {
	return validation.StorageResize(field.NewPath("spec", "resource", "storage", "pvcSize"),
		oldDB.Spec.Resource.Storage.PvcSize, newDB.Spec.Resource.Storage.PvcSize)
}

// isBootstrapEqual compares two BootstrapConfiguration pointers for equality.
func isBootstrapEqual(a, b *dbv1.BootstrapConfiguration) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resolveBinaryVersion extracts the effective binary version from a DocumentDB spec.
// Priority: image.documentDB tag > documentDBVersion > "" (unknown).
// Digest-only references (e.g., "image@sha256:...") are not parseable as versions
// and return "".
func resolveBinaryVersion(db *dbv1.DocumentDB) string {
	return validation.ResolveBinaryVersion(specImageDocumentDB(db), db.Spec.DocumentDBVersion)
}

// resolveEffectiveBinaryVersion returns the binary version the operator will
// actually run for db, including the operator-wide default the controller applies
// when the spec pins no version. Restore validation uses this rather than the
// spec-only resolveBinaryVersion so a restore whose effective binary would be
// older than the source schema is blocked, not merely warned.
func resolveEffectiveBinaryVersion(db *dbv1.DocumentDB) string {
	if v := resolveBinaryVersion(db); v != "" {
		return v
	}
	// The spec pins no parseable version. Resolve the exact image the controller
	// would actually run (a pinned but unparseable image, the DOCUMENTDB_VERSION
	// env default, the ChangeStreams image, or the built-in default) via the shared
	// helper, and read its semver tag. Anything without a parseable semver tag
	// (a digest, or the changestream image) stays unknown so the restore is warned,
	// not falsely blocked — keeping the webhook in step with the controller.
	return validation.ImageTagSemver(product.DocumentDBAdapter{}.ExtensionImage(db))
}

func specImageDocumentDB(db *dbv1.DocumentDB) string {
	if db == nil || db.Spec.Image == nil {
		return ""
	}
	return db.Spec.Image.DocumentDB
}

// extractSemver returns the leading "X.Y.Z" portion from a tag string.
// See validation.ExtractSemver.
func extractSemver(tag string) string { return validation.ExtractSemver(tag) }
