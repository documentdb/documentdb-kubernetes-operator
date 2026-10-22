// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"sync/atomic"

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

// ensureImageVolumeSupported blocks DocumentDB creation when the cluster does
// not support the Kubernetes ImageVolume feature. See validation.ImageVolumeProbe.
func (v *DocumentDBValidator) ensureImageVolumeSupported(ctx context.Context, namespace string) error {
	return v.imageVolumeProbe().Ensure(ctx, namespace)
}

// probeImageVolume performs the differential dry-run probe. The second return
// value reports whether the result is conclusive.
func (v *DocumentDBValidator) probeImageVolume(ctx context.Context, namespace string) (supported bool, conclusive bool) {
	return v.imageVolumeProbe().Probe(ctx, namespace)
}
