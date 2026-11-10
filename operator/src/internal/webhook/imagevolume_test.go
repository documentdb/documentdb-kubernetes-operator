// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package webhook

import (
	"context"
	"fmt"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// podHasImageVolume reports whether any of the Pod's volumes uses an
// ImageVolumeSource — the field a gate-off API server rejects.
func podHasImageVolume(obj client.Object) bool {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return false
	}
	for _, vol := range pod.Spec.Volumes {
		if vol.VolumeSource.Image != nil {
			return true
		}
	}
	return false
}

// probeScheme returns a scheme that knows about core/v1 Pods.
func probeScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return scheme
}

// newValidatorWithCreate builds a DocumentDBValidator backed by a fake client
// whose Create behaviour is supplied by createFn — modelling how a particular
// API server responds to the probe Pods. createCount, when non-nil, is
// incremented on every intercepted Create so tests can assert whether the
// probe ran at all.
func newValidatorWithCreate(createCount *int32, createFn func(obj client.Object) error) *DocumentDBValidator {
	c := fake.NewClientBuilder().
		WithScheme(probeScheme()).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
				if createCount != nil {
					atomic.AddInt32(createCount, 1)
				}
				return createFn(obj)
			},
		}).
		Build()
	return &DocumentDBValidator{Client: c}
}

// acceptAll models an API server with ImageVolume enabled: every probe Pod is
// admitted.
func acceptAll(_ client.Object) error { return nil }

// rejectImageVolume models an API server with the ImageVolume gate OFF: the
// baseline (emptyDir) probe is admitted, but any Pod carrying an image volume
// is rejected.
func rejectImageVolume(obj client.Object) error {
	if podHasImageVolume(obj) {
		return fmt.Errorf("Pod \"probe\" is invalid: spec.volumes[0].image: Forbidden: image volumes are disabled by feature-gate")
	}
	return nil
}

// rejectAll models an environment (PSA, ResourceQuota, another webhook) that
// blocks the baseline probe Pod, making the result inconclusive.
func rejectAll(_ client.Object) error {
	return fmt.Errorf("pods \"probe\" is forbidden: violates PodSecurity \"restricted:latest\"")
}

// pruneImageVolume models a Kubernetes 1.33/1.34 API server with the ImageVolume
// beta gate OFF: the Pod is admitted (no error), but the API server silently
// strips the disabled ImageVolumeSource from the stored/returned spec. This is
// the failure mode that a plain error check misses.
func pruneImageVolume(obj client.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].VolumeSource.Image != nil {
			pod.Spec.Volumes[i].VolumeSource.Image = nil
		}
	}
	return nil
}

var _ = Describe("ImageVolume admission check", func() {
	BeforeEach(func() {
		// The positive-result cache is a package global; reset it so specs
		// don't leak state into one another.
		imageVolumeConfirmed.Store(false)
	})

	Context("when imageMode is auto or combined", func() {
		for _, mode := range []string{"", "auto", "combined"} {
			It(fmt.Sprintf("never probes or blocks (DOCUMENTDB_IMAGE_MODE=%q)", mode), func() {
				GinkgoT().Setenv(util.DOCUMENTDB_IMAGE_MODE_ENV, mode)
				var creates int32
				v := newValidatorWithCreate(&creates, rejectImageVolume)
				Expect(v.ensureImageVolumeSupported(context.Background(), "default")).To(Succeed())
				Expect(atomic.LoadInt32(&creates)).To(BeZero())
			})
		}
	})

	Context("when imageMode is imageVolume", func() {
		BeforeEach(func() {
			GinkgoT().Setenv(util.DOCUMENTDB_IMAGE_MODE_ENV, "imageVolume")
		})

		It("allows creation and caches the positive result when supported", func() {
			v := newValidatorWithCreate(nil, acceptAll)
			Expect(v.ensureImageVolumeSupported(context.Background(), "default")).To(Succeed())
			Expect(imageVolumeConfirmed.Load()).To(BeTrue())
		})

		It("blocks creation with an actionable error when unsupported", func() {
			v := newValidatorWithCreate(nil, rejectImageVolume)
			err := v.ensureImageVolumeSupported(context.Background(), "default")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ImageVolume feature is not enabled"))
			Expect(err.Error()).To(ContainSubstring("imageMode"))
			Expect(imageVolumeConfirmed.Load()).To(BeFalse())
		})

		It("blocks creation when the image volume is admitted but pruned (gate off)", func() {
			v := newValidatorWithCreate(nil, pruneImageVolume)
			err := v.ensureImageVolumeSupported(context.Background(), "default")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ImageVolume feature is not enabled"))
			Expect(imageVolumeConfirmed.Load()).To(BeFalse())
		})

		It("fails open (allows creation) on an inconclusive probe without caching", func() {
			v := newValidatorWithCreate(nil, rejectAll)
			Expect(v.ensureImageVolumeSupported(context.Background(), "default")).To(Succeed())
			Expect(imageVolumeConfirmed.Load()).To(BeFalse())
		})

		It("short-circuits without probing once the positive result is cached", func() {
			var creates int32
			v := newValidatorWithCreate(&creates, rejectImageVolume)
			imageVolumeConfirmed.Store(true)

			Expect(v.ensureImageVolumeSupported(context.Background(), "default")).To(Succeed())
			Expect(atomic.LoadInt32(&creates)).To(BeZero())
		})
	})
})
