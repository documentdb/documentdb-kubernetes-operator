// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/documentdb/documentdb-operator/internal/imagemode"
	"github.com/documentdb/documentdb-operator/internal/validation"
)

// imageVolumeConfirmed caches a positive detection of ImageVolume support.
// See validation.ImageVolumeProbe.Confirmed.
var imageVolumeConfirmed atomic.Bool

func (v *DocumentDBValidator) imageVolumeProbe() *validation.ImageVolumeProbe {
	return &validation.ImageVolumeProbe{
		Client:    v.Client,
		Confirmed: &imageVolumeConfirmed,
		Product:   "DocumentDB",
		Log:       documentdbLog,
	}
}

// ensureImageVolumeSupported blocks DocumentDB creation when the operator is
// forced into ImageVolume mode (DOCUMENTDB_IMAGE_MODE=imageVolume) and the
// cluster does not support the Kubernetes ImageVolume feature. See
// validation.ImageVolumeProbe.
//
// In the default "auto" mode, and in "combined" mode, creation is never blocked:
// the controller falls back to the combined PostgreSQL + DocumentDB image when
// ImageVolume is unavailable (see docs/designs/combined-image-fallback.md).
func (v *DocumentDBValidator) ensureImageVolumeSupported(ctx context.Context, namespace string) error {
	if imagemode.ConfiguredMode() != imagemode.ImageVolume {
		return nil
	}
	if err := v.imageVolumeProbe().Ensure(ctx, namespace); err != nil {
		return fmt.Errorf("%w. The operator is configured with imageMode=imageVolume; set the Helm value "+
			"imageMode to \"auto\" or \"combined\" to use the combined PostgreSQL + DocumentDB image instead", err)
	}
	return nil
}
