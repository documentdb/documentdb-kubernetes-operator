// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/cnpg"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var documentdbLog = logf.Log.WithName("documentdb-webhook")

// DocumentDBValidator validates DocumentDB resources on create and update.
type DocumentDBValidator struct {
	client.Client
}

var _ admission.Validator[*dbpreview.DocumentDB] = &DocumentDBValidator{}

// SetupWebhookWithManager registers the validating webhook with the manager.
func (v *DocumentDBValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr, &dbpreview.DocumentDB{}).
		WithValidator(v).
		Complete()
}

// NOTE: The kubebuilder marker below is used for local development with `make run`.
// For Helm-based deployments, the authoritative webhook configuration is in
// operator/documentdb-helm-chart/templates/10_documentdb_webhook.yaml.
// +kubebuilder:webhook:path=/validate-documentdb-io-preview-documentdb,mutating=false,failurePolicy=fail,sideEffects=None,groups=documentdb.io,resources=dbs,verbs=create;update,versions=preview,name=vdocumentdb.kb.io,admissionReviewVersions=v1

// ValidateCreate validates a DocumentDB resource on creation.
func (v *DocumentDBValidator) ValidateCreate(ctx context.Context, documentdb *dbpreview.DocumentDB) (admission.Warnings, error) {
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
func (v *DocumentDBValidator) ValidateUpdate(_ context.Context, oldDB, newDB *dbpreview.DocumentDB) (admission.Warnings, error) {
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
func (v *DocumentDBValidator) ValidateDelete(_ context.Context, _ *dbpreview.DocumentDB) (admission.Warnings, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Spec-level validations (run on both create and update)
// ---------------------------------------------------------------------------

// validate runs all spec-level validation rules, returning a combined error list.
func (v *DocumentDBValidator) validate(db *dbpreview.DocumentDB) (allErrs field.ErrorList) {
	type validationFunc func(*dbpreview.DocumentDB) field.ErrorList
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
func (v *DocumentDBValidator) validateResources(db *dbpreview.DocumentDB) field.ErrorList {
	return cnpg.ValidateResources(db, cnpg.DefaultSplitConfig())
}

// validateSchemaVersionNotExceedsBinary ensures spec.schemaVersion <= binary version.
func (v *DocumentDBValidator) validateSchemaVersionNotExceedsBinary(db *dbpreview.DocumentDB) field.ErrorList {
	if db.Spec.SchemaVersion == "" || db.Spec.SchemaVersion == "auto" {
		return nil
	}

	binaryVersion := resolveBinaryVersion(db)
	if binaryVersion == "" {
		return field.ErrorList{field.Invalid(
			field.NewPath("spec", "schemaVersion"),
			db.Spec.SchemaVersion,
			"cannot set an explicit schemaVersion without also setting spec.documentDBVersion or spec.documentDBImage; "+
				"the webhook needs a binary version to validate against",
		)}
	}

	schemaExtensionVersion := util.SemverToExtensionVersion(db.Spec.SchemaVersion)
	binaryExtensionVersion := util.SemverToExtensionVersion(binaryVersion)

	cmp, err := util.CompareExtensionVersions(schemaExtensionVersion, binaryExtensionVersion)
	if err != nil {
		return field.ErrorList{field.Invalid(
			field.NewPath("spec", "schemaVersion"),
			db.Spec.SchemaVersion,
			fmt.Sprintf("cannot validate schemaVersion: version comparison failed: %v", err),
		)}
	}
	if cmp > 0 {
		return field.ErrorList{field.Invalid(
			field.NewPath("spec", "schemaVersion"),
			db.Spec.SchemaVersion,
			fmt.Sprintf("schemaVersion %s exceeds the binary version %s; schema version must be <= binary version",
				db.Spec.SchemaVersion, binaryVersion),
		)}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Update-only validations (compare old and new)
// ---------------------------------------------------------------------------

// validateChanges runs all update-specific validation rules that compare old vs new state.
func (v *DocumentDBValidator) validateChanges(newDB, oldDB *dbpreview.DocumentDB) (allErrs field.ErrorList) {
	type validationFunc func(newDB, oldDB *dbpreview.DocumentDB) field.ErrorList
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
func (v *DocumentDBValidator) validateImageRollback(newDB, oldDB *dbpreview.DocumentDB) field.ErrorList {
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

	newBinaryVersion := resolveBinaryVersion(newDB)
	if newBinaryVersion == "" {
		return nil
	}

	newBinaryExtensionVersion := util.SemverToExtensionVersion(newBinaryVersion)
	schemaExtensionVersion := util.SemverToExtensionVersion(installedSchemaVersion)

	cmp, err := util.CompareExtensionVersions(newBinaryExtensionVersion, schemaExtensionVersion)
	if err != nil {
		return field.ErrorList{field.Forbidden(
			field.NewPath("spec"),
			fmt.Sprintf("cannot validate image rollback: version comparison failed: %v", err),
		)}
	}
	if cmp < 0 {
		return field.ErrorList{field.Forbidden(
			field.NewPath("spec"),
			fmt.Sprintf(
				"image rollback blocked: requested version %s is older than installed schema version %s. "+
					"ALTER EXTENSION has no downgrade path — running an older binary with a newer schema may cause data corruption. "+
					"To recover, restore from backup or update to a version >= %s.",
				newBinaryVersion, installedSchemaVersion, installedSchemaVersion),
		)}
	}
	return nil
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
func (v *DocumentDBValidator) validateRestoreSchemaCompatibility(ctx context.Context, newDB *dbpreview.DocumentDB) (admission.Warnings, field.ErrorList) {
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

	return compareBinaryToSchema(resolveEffectiveBinaryVersion(newDB), schemaVersion, src)
}

// restoreSource identifies where a restore draws its data — and thus its schema
// version — from, carrying the diagnostics and the spec field to flag on error.
type restoreSource struct {
	kind      string // sourceKindBackup or sourceKindPV
	name      string
	fieldPath *field.Path
}

const (
	sourceKindBackup = "backup"
	sourceKindPV     = "PersistentVolume"
)

// restoreSourceFor identifies the restore source from a recovery configuration,
// returning false when neither a backup nor a PersistentVolume source is set.
func restoreSourceFor(recovery *dbpreview.RecoveryConfiguration) (restoreSource, bool) {
	if recovery.Backup.Name != "" {
		return restoreSource{
			kind:      sourceKindBackup,
			name:      recovery.Backup.Name,
			fieldPath: field.NewPath("spec", "bootstrap", "recovery", "backup"),
		}, true
	}
	if recovery.PersistentVolume != nil && recovery.PersistentVolume.Name != "" {
		return restoreSource{
			kind:      sourceKindPV,
			name:      recovery.PersistentVolume.Name,
			fieldPath: field.NewPath("spec", "bootstrap", "recovery", "persistentVolume"),
		}, true
	}
	return restoreSource{}, false
}

// resolveSourceSchemaVersion reads the restore source and returns its recorded
// schema version. It only reports warnings for I/O failures (source not found or
// unreadable); a successfully read source with no schema version returns ("",
// nil), leaving the "unknown schema" policy to compareBinaryToSchema so that both
// empty-input cases are decided in one place. Backup and PV are handled
// identically.
func (v *DocumentDBValidator) resolveSourceSchemaVersion(ctx context.Context, namespace string, src restoreSource) (string, admission.Warnings) {
	var schemaVersion string
	var readErr error

	switch src.kind {
	case sourceKindBackup:
		backup := &dbpreview.Backup{}
		if err := v.Get(ctx, client.ObjectKey{Name: src.name, Namespace: namespace}, backup); err != nil {
			readErr = err
		} else {
			schemaVersion = backup.Status.SchemaVersion
		}
	case sourceKindPV:
		pv := &corev1.PersistentVolume{}
		if err := v.Get(ctx, client.ObjectKey{Name: src.name}, pv); err != nil {
			readErr = err
		} else {
			schemaVersion = pv.Annotations[util.AnnotationSchemaVersion]
		}
	}

	if readErr != nil {
		if apierrors.IsNotFound(readErr) {
			return "", admission.Warnings{
				fmt.Sprintf("%s %q not found: schema-version compatibility cannot be verified", src.kind, src.name),
			}
		}
		// Transient/API error: don't hard-block the restore, but warn.
		return "", admission.Warnings{
			fmt.Sprintf("failed to read %s %q: schema-version compatibility cannot be verified: %v", src.kind, src.name, readErr),
		}
	}
	return schemaVersion, nil
}

// compareBinaryToSchema is the single authority on the restore decision. It warns
// (and allows) when either the source schema version or the target DocumentDB
// version is unknown, and rejects only a known target version that is older than a
// known schema.
func compareBinaryToSchema(binaryVersion, schemaVersion string, src restoreSource) (admission.Warnings, field.ErrorList) {
	if schemaVersion == "" {
		return admission.Warnings{
			fmt.Sprintf("%s %q has no recorded schema version: schema-version compatibility cannot be verified; "+
				"ensure the target DocumentDB version (spec.documentDBVersion or spec.image.documentDB) is >= the source's "+
				"schema version to avoid data corruption", src.kind, src.name),
		}, nil
	}
	if binaryVersion == "" {
		return admission.Warnings{
			fmt.Sprintf("cannot determine the target DocumentDB version for restore from %s %q "+
				"(set spec.documentDBVersion or spec.image.documentDB): compatibility with the %s's schema version %s cannot be verified",
				src.kind, src.name, src.kind, schemaVersion),
		}, nil
	}

	binaryExtensionVersion := util.SemverToExtensionVersion(binaryVersion)
	schemaExtensionVersion := util.SemverToExtensionVersion(schemaVersion)

	cmp, err := util.CompareExtensionVersions(binaryExtensionVersion, schemaExtensionVersion)
	if err != nil {
		return admission.Warnings{
			fmt.Sprintf("cannot compare the target DocumentDB version %s with the %s's schema version %s: %v; compatibility not verified",
				binaryVersion, src.kind, schemaVersion, err),
		}, nil
	}
	if cmp < 0 {
		return nil, field.ErrorList{field.Forbidden(
			src.fieldPath,
			fmt.Sprintf(
				"restore blocked: the target DocumentDB version %s is older than the %s's schema version %s. "+
					"Restoring onto an older DocumentDB version runs it against a newer, irreversible schema and may cause data corruption. "+
					"Set spec.documentDBVersion (or spec.image.documentDB) to %s or newer.",
				binaryVersion, src.kind, schemaVersion, schemaVersion),
		)}
	}
	return nil, nil
}

// validateImmutableFields rejects updates to fields that cannot be changed after creation.
// Note: credentialSecret, storageClass, and sidecarInjectorPluginName are enforced via
// CEL transition rules on the CRD schema (see documentdb_types.go).
func (v *DocumentDBValidator) validateImmutableFields(newDB, oldDB *dbpreview.DocumentDB) field.ErrorList {
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
func (v *DocumentDBValidator) validateStorageResize(newDB, oldDB *dbpreview.DocumentDB) field.ErrorList {
	oldSize := oldDB.Spec.Resource.Storage.PvcSize
	newSize := newDB.Spec.Resource.Storage.PvcSize
	if oldSize == newSize {
		return nil
	}

	pvcSizePath := field.NewPath("spec", "resource", "storage", "pvcSize")
	var allErrs field.ErrorList

	oldQty, errOld := resource.ParseQuantity(oldSize)
	if errOld != nil {
		allErrs = append(allErrs, field.Invalid(
			pvcSizePath,
			oldSize,
			fmt.Sprintf("existing pvcSize is not a valid resource quantity: %v", errOld),
		))
	}

	newQty, errNew := resource.ParseQuantity(newSize)
	if errNew != nil {
		allErrs = append(allErrs, field.Invalid(
			pvcSizePath,
			newSize,
			fmt.Sprintf("pvcSize must be a valid resource quantity: %v", errNew),
		))
	}

	if len(allErrs) > 0 {
		return allErrs
	}

	if newQty.Cmp(oldQty) < 0 {
		return field.ErrorList{field.Forbidden(
			pvcSizePath,
			fmt.Sprintf("storage size can only be increased; attempted shrink from %s to %s", oldSize, newSize),
		)}
	}
	return nil
}

// isBootstrapEqual compares two BootstrapConfiguration pointers for equality.
func isBootstrapEqual(a, b *dbpreview.BootstrapConfiguration) bool {
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
func resolveBinaryVersion(db *dbpreview.DocumentDB) string {
	if ref := specImageDocumentDB(db); ref != "" {
		// Ignore digest-only references — they don't carry a version tag
		if strings.Contains(ref, "@sha256:") {
			return db.Spec.DocumentDBVersion
		}
		if tagIdx := strings.LastIndex(ref, ":"); tagIdx >= 0 {
			tag := ref[tagIdx+1:]
			// Extract leading semver (X.Y.Z) from tags like "0.112.0-amd64"
			if semver := extractSemver(tag); semver != "" {
				return semver
			}
		}
	}
	return db.Spec.DocumentDBVersion
}

// resolveEffectiveBinaryVersion returns the binary version the operator will
// actually run for db, including the operator-wide default the controller applies
// when the spec pins no version. Restore validation uses this rather than the
// spec-only resolveBinaryVersion so a restore whose effective binary would be
// older than the source schema is blocked, not merely warned.
func resolveEffectiveBinaryVersion(db *dbpreview.DocumentDB) string {
	if v := resolveBinaryVersion(db); v != "" {
		return v
	}
	// The spec pins no parseable version. Resolve the exact image the controller
	// would actually run (a pinned but unparseable image, the DOCUMENTDB_VERSION
	// env default, the ChangeStreams image, or the built-in default) via the shared
	// helper, and read its semver tag. Anything without a parseable semver tag
	// (a digest, or the changestream image) stays unknown so the restore is warned,
	// not falsely blocked — keeping the webhook in step with the controller.
	image := util.GetDocumentDBImageForInstance(db)
	if tagIdx := strings.LastIndex(image, ":"); tagIdx >= 0 {
		if semver := extractSemver(image[tagIdx+1:]); semver != "" {
			return semver
		}
	}
	return ""
}

func specImageDocumentDB(db *dbpreview.DocumentDB) string {
	if db == nil || db.Spec.Image == nil {
		return ""
	}
	return db.Spec.Image.DocumentDB
}

// extractSemver returns the leading "X.Y.Z" portion from a tag string,
// or "" if the tag doesn't start with a valid semver pattern.
func extractSemver(tag string) string {
	// Match digits.digits.digits at start of string
	parts := strings.SplitN(tag, ".", 3)
	if len(parts) < 3 {
		return ""
	}
	// Validate major and minor are numeric
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return ""
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return ""
	}
	// Third part may have a suffix (e.g., "0-amd64"), take only leading digits
	thirdPart := parts[2]
	i := 0
	for i < len(thirdPart) && thirdPart[i] >= '0' && thirdPart[i] <= '9' {
		i++
	}
	if i == 0 {
		return ""
	}
	return parts[0] + "." + parts[1] + "." + thirdPart[:i]
}
