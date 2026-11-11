// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package preview contains the deprecated documentdb.io/preview API.
//
// It exists only so the CRDs keep serving documentdb.io/preview for resources
// created by operator 1.0.0. The *_types.go files are generated from api/v1 by
// hack/sync-preview-api.sh (run by `make manifests`) because the API server
// converts between the two versions with strategy None, which requires
// identical schemas. Edit api/v1, never this package. All operator code uses
// api/v1. This package will be removed in a future release.
// +kubebuilder:object:generate=true
// +groupName=documentdb.io
package preview

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "documentdb.io", Version: "preview"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
