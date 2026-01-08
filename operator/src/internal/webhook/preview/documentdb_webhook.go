// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// log is for logging in this package.
var documentdbLog = logf.Log.WithName("documentdb-webhook").WithValues("version", "preview")

// DocumentDBWebhook handles validation for DocumentDB resources
type DocumentDBWebhook struct{}

// SetupWebhookWithManager registers the webhook with the manager
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&dbpreview.DocumentDB{}).
		WithValidator(&DocumentDBWebhook{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-documentdb-io-preview-documentdb,mutating=false,failurePolicy=fail,sideEffects=None,groups=documentdb.io,resources=dbs,verbs=create;update,versions=preview,name=vdocumentdb.kb.io,admissionReviewVersions=v1

var _ admission.CustomValidator = &DocumentDBWebhook{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (w *DocumentDBWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	documentdb, ok := obj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB object but got %T", obj)
	}

	documentdbLog.Info("validate create", "name", documentdb.Name, "namespace", documentdb.Namespace)

	allErrs := w.validate(documentdb)
	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(
		schema.GroupKind{Group: "documentdb.io", Kind: "DocumentDB"},
		documentdb.Name,
		allErrs,
	)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (w *DocumentDBWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	documentdb, ok := newObj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB object but got %T", newObj)
	}

	documentdbLog.Info("validate update", "name", documentdb.Name, "namespace", documentdb.Namespace)

	allErrs := w.validate(documentdb)
	if len(allErrs) == 0 {
		return nil, nil
	}

	return nil, apierrors.NewInvalid(
		schema.GroupKind{Group: "documentdb.io", Kind: "DocumentDB"},
		documentdb.Name,
		allErrs,
	)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (w *DocumentDBWebhook) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	documentdb, ok := obj.(*dbpreview.DocumentDB)
	if !ok {
		return nil, fmt.Errorf("expected DocumentDB object but got %T", obj)
	}

	documentdbLog.Info("validate delete", "name", documentdb.Name, "namespace", documentdb.Namespace)
	// No validation needed for delete
	return nil, nil
}

// validate groups the validation logic for DocumentDB returning a list of all encountered errors
func (w *DocumentDBWebhook) validate(r *dbpreview.DocumentDB) field.ErrorList {
	type validationFunc func(*dbpreview.DocumentDB) field.ErrorList

	validations := []validationFunc{
		w.validateBootstrapRecovery,
		// Add more validation functions here as needed
	}

	var allErrs field.ErrorList
	for _, validate := range validations {
		allErrs = append(allErrs, validate(r)...)
	}

	return allErrs
}

// validateBootstrapRecovery validates that backup and PVC recovery are not both specified
func (w *DocumentDBWebhook) validateBootstrapRecovery(documentdb *dbpreview.DocumentDB) field.ErrorList {
	// If bootstrap is not configured, everything is ok
	if documentdb.Spec.Bootstrap == nil || documentdb.Spec.Bootstrap.Recovery == nil {
		return nil
	}

	var result field.ErrorList
	recovery := documentdb.Spec.Bootstrap.Recovery

	// Validate that both backup and PVC are not specified together
	if recovery.Backup.Name != "" && recovery.PVC.Name != "" {
		result = append(result, field.Invalid(
			field.NewPath("spec", "bootstrap", "recovery"),
			recovery,
			"cannot specify both backup and PVC recovery at the same time",
		))
	}

	return result
}
