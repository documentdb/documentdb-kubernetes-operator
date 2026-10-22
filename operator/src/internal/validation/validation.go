// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package validation holds the product-neutral admission rules shared by the
// product validating webhooks: schema/binary version guards, image rollback,
// storage resize, restore schema compatibility, and the ImageVolume preflight.
package validation

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// Terms names the product and its version fields in validation messages.
type Terms struct {
	// Product is the product name, e.g. "DocumentDB".
	Product string
	// VersionField and ImageField are the spec paths that pin the extension
	// binary version, e.g. "spec.documentDBVersion" and "spec.image.documentDB".
	VersionField string
	ImageField   string
	// SchemaWithoutBinaryFields names the fields to set when an explicit
	// schemaVersion has no binary version to validate against.
	SchemaWithoutBinaryFields string
}

var rcImageTagPattern = regexp.MustCompile(`^[vV]?([0-9]+)\.([0-9]+)-[rR][cC][1-9][0-9]*(?:$|[-+])`)

// ExtractSemver returns the leading "X.Y.Z" portion from a tag string,
// normalizing "X.Y-RC<n>" tags to the extension version "X.Y.0".
// It returns "" when neither version format is recognized.
func ExtractSemver(tag string) string {
	if matches := rcImageTagPattern.FindStringSubmatch(tag); matches != nil {
		return matches[1] + "." + matches[2] + ".0"
	}
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

// ImageTagSemver returns the semver of an image reference's tag, or "" for
// digest references and unparseable tags.
func ImageTagSemver(ref string) string {
	if ref == "" || strings.Contains(ref, "@sha256:") {
		return ""
	}
	if tagIdx := strings.LastIndex(ref, ":"); tagIdx >= 0 {
		return ExtractSemver(ref[tagIdx+1:])
	}
	return ""
}

// ResolveBinaryVersion extracts the binary version pinned by a spec.
// Priority: image tag > version field > "" (unknown). Digest-only references
// (e.g., "image@sha256:...") are not parseable as versions.
func ResolveBinaryVersion(imageRef, version string) string {
	if semver := ImageTagSemver(imageRef); semver != "" {
		return semver
	}
	if v := ExtractSemver(version); v != "" {
		return v
	}
	return version
}

// SchemaVersionNotExceedsBinary ensures an explicit spec.schemaVersion is <= the binary version.
func SchemaVersionNotExceedsBinary(schemaVersion, binaryVersion string, t Terms) field.ErrorList {
	if schemaVersion == "" || schemaVersion == "auto" {
		return nil
	}

	path := field.NewPath("spec", "schemaVersion")
	if binaryVersion == "" {
		return field.ErrorList{field.Invalid(
			path,
			schemaVersion,
			"cannot set an explicit schemaVersion without also setting "+t.SchemaWithoutBinaryFields+"; "+
				"the webhook needs a binary version to validate against",
		)}
	}

	schemaExtensionVersion := util.SemverToExtensionVersion(schemaVersion)
	binaryExtensionVersion := util.SemverToExtensionVersion(binaryVersion)

	cmp, err := util.CompareExtensionVersions(schemaExtensionVersion, binaryExtensionVersion)
	if err != nil {
		return field.ErrorList{field.Invalid(
			path,
			schemaVersion,
			fmt.Sprintf("cannot validate schemaVersion: version comparison failed: %v", err),
		)}
	}
	if cmp > 0 {
		return field.ErrorList{field.Invalid(
			path,
			schemaVersion,
			fmt.Sprintf("schemaVersion %s exceeds the binary version %s; schema version must be <= binary version",
				schemaVersion, binaryVersion),
		)}
	}
	return nil
}

// ImageRollback blocks a new binary version below the installed schema version.
// Once ALTER EXTENSION UPDATE has run, the schema is irreversible. Running an older
// binary against a newer schema is untested and may cause data corruption.
// Callers should only invoke it when an image-related field changes.
func ImageRollback(installedSchemaVersion, newBinaryVersion string) field.ErrorList {
	if installedSchemaVersion == "" || newBinaryVersion == "" {
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

// StorageResize ensures PVC size can only grow, never shrink.
func StorageResize(pvcSizePath *field.Path, oldSize, newSize string) field.ErrorList {
	if oldSize == newSize {
		return nil
	}

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

// RestoreSource identifies where a restore draws its data — and thus its schema
// version — from, carrying the diagnostics and the spec field to flag on error.
type RestoreSource struct {
	Kind      string // SourceKindBackup or SourceKindPV
	Name      string
	FieldPath *field.Path
}

const (
	SourceKindBackup = "backup"
	SourceKindPV     = "PersistentVolume"
)

// BackupSource and PVSource build the RestoreSource of a recovery stanza at base.
func BackupSource(base *field.Path, name string) RestoreSource {
	return RestoreSource{Kind: SourceKindBackup, Name: name, FieldPath: base.Child("backup")}
}

func PVSource(base *field.Path, name string) RestoreSource {
	return RestoreSource{Kind: SourceKindPV, Name: name, FieldPath: base.Child("persistentVolume")}
}

// SourceSchemaVersion reads the schema version recorded on a restore source:
// readBackup returns it for a backup, and PVs carry it in pvAnnotation. It
// only reports warnings for I/O failures (source not found or unreadable); a
// successfully read source with no schema version returns ("", nil), leaving
// the "unknown schema" policy to CompareBinaryToSchema.
func SourceSchemaVersion(ctx context.Context, c client.Reader, src RestoreSource, pvAnnotation string, readBackup func() (string, error)) (string, admission.Warnings) {
	var schemaVersion string
	var readErr error

	switch src.Kind {
	case SourceKindBackup:
		schemaVersion, readErr = readBackup()
	case SourceKindPV:
		pv := &corev1.PersistentVolume{}
		if err := c.Get(ctx, client.ObjectKey{Name: src.Name}, pv); err != nil {
			readErr = err
		} else {
			schemaVersion = pv.Annotations[pvAnnotation]
		}
	}

	if readErr != nil {
		if apierrors.IsNotFound(readErr) {
			return "", admission.Warnings{
				fmt.Sprintf("%s %q not found: schema-version compatibility cannot be verified", src.Kind, src.Name),
			}
		}
		// Transient/API error: don't hard-block the restore, but warn.
		return "", admission.Warnings{
			fmt.Sprintf("failed to read %s %q: schema-version compatibility cannot be verified: %v", src.Kind, src.Name, readErr),
		}
	}
	return schemaVersion, nil
}

// CompareBinaryToSchema is the single authority on the restore decision. It warns
// (and allows) when either the source schema version or the target version is
// unknown, and rejects only a known target version that is older than a known
// schema.
func CompareBinaryToSchema(binaryVersion, schemaVersion string, src RestoreSource, t Terms) (admission.Warnings, field.ErrorList) {
	target := "the target " + t.Product + " version"
	if schemaVersion == "" {
		return admission.Warnings{
			fmt.Sprintf("%s %q has no recorded schema version: schema-version compatibility cannot be verified; "+
				"ensure %s (%s or %s) is >= the source's "+
				"schema version to avoid data corruption", src.Kind, src.Name, target, t.VersionField, t.ImageField),
		}, nil
	}
	if binaryVersion == "" {
		return admission.Warnings{
			fmt.Sprintf("cannot determine %s for restore from %s %q "+
				"(set %s or %s): compatibility with the %s's schema version %s cannot be verified",
				target, src.Kind, src.Name, t.VersionField, t.ImageField, src.Kind, schemaVersion),
		}, nil
	}

	binaryExtensionVersion := util.SemverToExtensionVersion(binaryVersion)
	schemaExtensionVersion := util.SemverToExtensionVersion(schemaVersion)

	cmp, err := util.CompareExtensionVersions(binaryExtensionVersion, schemaExtensionVersion)
	if err != nil {
		return admission.Warnings{
			fmt.Sprintf("cannot compare %s %s with the %s's schema version %s: %v; compatibility not verified",
				target, binaryVersion, src.Kind, schemaVersion, err),
		}, nil
	}
	if cmp < 0 {
		return nil, field.ErrorList{field.Forbidden(
			src.FieldPath,
			fmt.Sprintf(
				"restore blocked: %s %s is older than the %s's schema version %s. "+
					"Restoring onto an older %s version runs it against a newer, irreversible schema and may cause data corruption. "+
					"Set %s (or %s) to %s or newer.",
				target, binaryVersion, src.Kind, schemaVersion, t.Product, t.VersionField, t.ImageField, schemaVersion),
		)}
	}
	return nil, nil
}
