// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var log = logf.Log.WithName("documentdb-webhook")

// DocumentDBValidator validates DocumentDB resources on create and update.
type DocumentDBValidator struct {
	client.Client
}

// SetupWebhookWithManager registers the validating webhook with the manager.
func (v *DocumentDBValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	v.Client = mgr.GetClient()
	return ctrl.NewWebhookManagedBy(mgr).
		For(&dbpreview.DocumentDB{}).
		WithValidator(v).
		Complete()
}

// +kubebuilder:webhook:path=/validate-documentdb-io-preview-documentdb,mutating=false,failurePolicy=fail,sideEffects=None,groups=documentdb.io,resources=dbs,verbs=create;update,versions=preview,name=vdocumentdb.kb.io,admissionReviewVersions=v1

// ValidateCreate validates a DocumentDB resource on creation.
func (v *DocumentDBValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	documentdb, ok := obj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB but got %T", obj)
	}
	log.Info("validating create", "name", documentdb.Name)

	return validateSpec(documentdb)
}

// ValidateUpdate validates a DocumentDB resource on update.
func (v *DocumentDBValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	newDB, ok := newObj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB but got %T", newObj)
	}
	oldDB, ok := oldObj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB but got %T", oldObj)
	}
	log.Info("validating update", "name", newDB.Name)

	warnings, err := validateSpec(newDB)
	if err != nil {
		return warnings, err
	}

	// Validate image rollback is not below installed schema version.
	// This is the primary enforcement point — blocks the spec change before it is persisted.
	if rollbackErr := validateImageRollback(oldDB, newDB); rollbackErr != nil {
		return warnings, rollbackErr
	}

	return warnings, nil
}

// ValidateDelete is a no-op for DocumentDB.
func (v *DocumentDBValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateSpec checks spec-level invariants that apply to both create and update.
func validateSpec(db *dbpreview.DocumentDB) (admission.Warnings, error) {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// If schemaVersion is set to an explicit version (not empty, not "auto"),
	// it must be <= the resolved binary version.
	if db.Spec.SchemaVersion != "" && db.Spec.SchemaVersion != "auto" {
		binaryVersion := resolveBinaryVersion(db)
		if binaryVersion != "" {
			if err := validateSchemaNotExceedsBinary(db.Spec.SchemaVersion, binaryVersion, specPath); err != nil {
				allErrs = append(allErrs, err)
			}
		}
	}

	if len(allErrs) > 0 {
		return nil, allErrs.ToAggregate()
	}
	return nil, nil
}

// validateSchemaNotExceedsBinary ensures schemaVersion <= binaryVersion.
func validateSchemaNotExceedsBinary(schemaVersion, binaryVersion string, specPath *field.Path) *field.Error {
	schemaPg := util.SemverToExtensionVersion(schemaVersion)
	binaryPg := util.SemverToExtensionVersion(binaryVersion)

	cmp, err := util.CompareExtensionVersions(schemaPg, binaryPg)
	if err != nil {
		// If we can't parse versions, let it through — controller will catch it
		return nil
	}
	if cmp > 0 {
		return field.Invalid(
			specPath.Child("schemaVersion"),
			schemaVersion,
			fmt.Sprintf("schemaVersion %s exceeds the binary version %s; schema version must be <= binary version", schemaVersion, binaryVersion),
		)
	}
	return nil
}

// validateImageRollback blocks image downgrades below the installed schema version.
// Once ALTER EXTENSION UPDATE has run, the schema is irreversible. Running an older
// binary against a newer schema is untested and may cause data corruption.
func validateImageRollback(oldDB, newDB *dbpreview.DocumentDB) error {
	installedSchemaVersion := oldDB.Status.SchemaVersion
	if installedSchemaVersion == "" {
		return nil
	}

	// Determine the new binary version from the updated spec
	newBinaryVersion := resolveBinaryVersion(newDB)
	if newBinaryVersion == "" {
		return nil
	}

	// Compare: newBinaryVersion must be >= installedSchemaVersion
	newPg := util.SemverToExtensionVersion(newBinaryVersion)
	schemaPg := util.SemverToExtensionVersion(installedSchemaVersion)

	cmp, err := util.CompareExtensionVersions(newPg, schemaPg)
	if err != nil {
		// Can't parse — let it through, controller has defense-in-depth
		return nil
	}
	if cmp < 0 {
		return field.Forbidden(
			field.NewPath("spec"),
			fmt.Sprintf(
				"image rollback blocked: requested version %s is older than installed schema version %s. "+
					"ALTER EXTENSION has no downgrade path — running an older binary with a newer schema may cause data corruption. "+
					"To recover, restore from backup or update to a version >= %s.",
				newBinaryVersion, installedSchemaVersion, installedSchemaVersion),
		)
	}
	return nil
}

// resolveBinaryVersion extracts the effective binary version from a DocumentDB spec.
// Priority: documentDBImage tag > documentDBVersion > "" (unknown).
func resolveBinaryVersion(db *dbpreview.DocumentDB) string {
	// If explicit image is set, extract tag
	if db.Spec.DocumentDBImage != "" {
		if tagIdx := strings.LastIndex(db.Spec.DocumentDBImage, ":"); tagIdx >= 0 {
			return db.Spec.DocumentDBImage[tagIdx+1:]
		}
	}
	// Fall back to documentDBVersion
	return db.Spec.DocumentDBVersion
}
