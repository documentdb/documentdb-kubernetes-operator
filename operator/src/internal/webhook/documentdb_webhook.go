// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var documentdbLog = logf.Log.WithName("documentdb-webhook")

// DocumentDBValidator validates DocumentDB resources on create and update.
type DocumentDBValidator struct {
	client.Client
}

var _ webhook.CustomValidator = &DocumentDBValidator{}

// SetupWebhookWithManager registers the validating webhook with the manager.
func (v *DocumentDBValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&dbpreview.DocumentDB{}).
		WithValidator(v).
		Complete()
}

// NOTE: The kubebuilder marker below is used for local development with `make run`.
// For Helm-based deployments, the authoritative webhook configuration is in
// operator/documentdb-helm-chart/templates/10_documentdb_webhook.yaml.
// +kubebuilder:webhook:path=/validate-documentdb-io-preview-documentdb,mutating=false,failurePolicy=fail,sideEffects=None,groups=documentdb.io,resources=dbs,verbs=create;update,versions=preview,name=vdocumentdb.kb.io,admissionReviewVersions=v1

// ValidateCreate validates a DocumentDB resource on creation.
func (v *DocumentDBValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	documentdb, ok := obj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB but got %T", obj)
	}
	documentdbLog.Info("Validation for DocumentDB upon creation", "name", documentdb.Name, "namespace", documentdb.Namespace)

	allErrs := v.validate(documentdb)
	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(
		schema.GroupKind{Group: "documentdb.io", Kind: "DocumentDB"},
		documentdb.Name, allErrs)
}

// ValidateUpdate validates a DocumentDB resource on update.
func (v *DocumentDBValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newDB, ok := newObj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB but got %T", newObj)
	}
	oldDB, ok := oldObj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB but got %T", oldObj)
	}
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
func (v *DocumentDBValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
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
		// Add new spec-level validations here.
	}
	for _, fn := range validations {
		allErrs = append(allErrs, fn(db)...)
	}
	return allErrs
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
		// Add new update-only validations here.
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
		newDB.Spec.DocumentDBImage == oldDB.Spec.DocumentDBImage {
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
// Helpers
// ---------------------------------------------------------------------------

// resolveBinaryVersion extracts the effective binary version from a DocumentDB spec.
// Priority: documentDBImage tag > documentDBVersion > "" (unknown).
// Digest-only references (e.g., "image@sha256:...") are not parseable as versions
// and return "".
func resolveBinaryVersion(db *dbpreview.DocumentDB) string {
	if db.Spec.DocumentDBImage != "" {
		ref := db.Spec.DocumentDBImage
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
