// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/documentdb/documentdb-operator/internal/imagemode"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// IsCombinedImageMode reports whether an existing CNPG Cluster was created in
// combined image mode. The mode is fixed at creation and recorded in the
// documentdb.io/image-mode annotation. If the annotation was removed, a cluster
// that mounts no extension and runs an image from the combined repository is
// still recognized, so syncing keeps working. Other clusters without the
// annotation (created by earlier operator versions, including 0.1.x clusters
// without an extension entry) are never treated as combined, so their image
// is not rewritten.
func IsCombinedImageMode(cluster *cnpgv1.Cluster) bool {
	if mode, ok := cluster.Annotations[imagemode.AnnotationKey]; ok {
		return mode == string(imagemode.Combined)
	}
	return len(cluster.Spec.PostgresConfiguration.Extensions) == 0 &&
		strings.HasPrefix(cluster.Spec.ImageName, util.CombinedImageRepo()+":")
}

// ApplyCombinedImageMode converts a rendered ImageVolume-mode Cluster into
// combined image mode: PostgreSQL runs from images.Combined, which already ships
// the extension, and no extension image volume is declared. The extension pull
// policy, when set, applies to the combined image. The
// shared_preload_libraries list is unchanged because the combined image
// installs the same libraries into the default PostgreSQL paths.
func ApplyCombinedImageMode(cluster *cnpgv1.Cluster, images product.Images) {
	cluster.Spec.ImageName = images.Combined
	cluster.Spec.PostgresConfiguration.Extensions = nil
	if images.PostgresExtensionPullPolicy != "" {
		cluster.Spec.ImagePullPolicy = images.PostgresExtensionPullPolicy
	}
}
