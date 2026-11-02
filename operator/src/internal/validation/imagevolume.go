// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package validation

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// imageVolumeProbeImage is a syntactically valid image reference used only for
// the dry-run capability probe. It is never pulled or scheduled because every
// probe Pod is submitted with client.DryRunAll.
const imageVolumeProbeImage = "registry.k8s.io/pause:3.10"

// imageVolumeProbeVolumeName is the volume/mount name shared by both probe Pods.
const imageVolumeProbeVolumeName = "probe"

// ImageVolumeProbe is the cluster-capability preflight for the Kubernetes
// ImageVolume feature the operator relies on to mount the extension.
type ImageVolumeProbe struct {
	Client client.Client
	// Confirmed caches a positive detection of ImageVolume support.
	//
	// Only positive results are cached: once the cluster is known to support
	// ImageVolume, subsequent creates skip the probe entirely (steady-state
	// latency ~0). A negative or inconclusive result is never cached, so if an
	// administrator enables the feature gate later the next create re-probes and
	// succeeds without restarting the operator.
	Confirmed *atomic.Bool
	// Product is a display name used only in the error message.
	Product string
	Log     logr.Logger
}

// Ensure blocks cluster creation when the cluster does
// not support the Kubernetes ImageVolume feature (feature gate off, or a
// runtime/version that lacks it). The operator mounts the DocumentDB extension
// into the PostgreSQL pod via a corev1.ImageVolumeSource, so without this
// feature the cluster can never become ready.
//
// The check is intentionally fail-open: it blocks only when it can positively
// prove the feature is unavailable. If the cluster environment prevents a
// conclusive answer (e.g. Pod Security Admission or a ResourceQuota rejects the
// baseline probe Pod), creation is allowed and the reconcile loop surfaces any
// real failure later.
func (v *ImageVolumeProbe) Ensure(ctx context.Context, namespace string) error {
	if v.Confirmed.Load() {
		return nil
	}

	supported, conclusive := v.Probe(ctx, namespace)
	if !conclusive {
		// Could not determine support; do not block on an inconclusive probe.
		return nil
	}
	if supported {
		v.Confirmed.Store(true)
		return nil
	}

	return fmt.Errorf(
		"the Kubernetes ImageVolume feature is not enabled on this cluster, but it is required by the %s "+
			"operator to mount the %s extension into PostgreSQL pods. ImageVolume is GA (on by default) in "+
			"Kubernetes 1.35+. On Kubernetes 1.33/1.34 you may enable the ImageVolume feature gate on both the "+
			"kube-apiserver and every kubelet; the container runtime must also support it (containerd >= 2.1 or "+
			"CRI-O >= 1.33), which this preflight cannot verify. "+
			"See https://kubernetes.io/docs/concepts/storage/volumes/#image",
		v.Product, v.Product,
	)
}

// Probe performs a differential server-side dry-run to detect
// ImageVolume support without relying on error-string matching.
//
// It submits two dry-run Pods that are identical except for a single volume:
//   - baseline: an emptyDir volume (always a valid volume source), and
//   - image:    an ImageVolumeSource volume.
//
// Interpretation:
//   - baseline rejected             -> the environment blocks pod creation
//     entirely (PSA, quota, other webhooks); inconclusive.
//   - baseline ok, image rejected   -> the image volume is the sole difference,
//     so ImageVolume is unsupported.
//   - baseline ok, image accepted but the ImageVolumeSource was pruned from the
//     returned Pod -> the feature gate is off, so the API server silently drops
//     the disabled field instead of rejecting it; ImageVolume is unsupported.
//   - baseline ok, image accepted and the ImageVolumeSource survived -> the
//     feature is supported.
//
// The pruning case is the important one: on Kubernetes 1.33/1.34 with the
// ImageVolume beta gate OFF, a dry-run create does NOT fail; the API server
// admits the Pod and simply strips the unknown/disabled volume source. Relying
// on a create error alone therefore false-positives as "supported". We close
// that gap by inspecting the returned object for field retention.
//
// The second return value reports whether the result is conclusive.
func (v *ImageVolumeProbe) Probe(ctx context.Context, namespace string) (supported bool, conclusive bool) {
	if _, err := v.dryRunProbePod(ctx, namespace, baselineProbeVolume()); err != nil {
		v.Log.Info("ImageVolume preflight inconclusive: baseline probe pod rejected", "error", err)
		return false, false
	}

	returned, err := v.dryRunProbePod(ctx, namespace, imageProbeVolume())
	if err != nil {
		v.Log.Info("ImageVolume preflight: image-volume probe pod rejected while baseline passed; feature unavailable", "error", err)
		return false, true
	}

	if !hasImageVolume(returned) {
		v.Log.Info("ImageVolume preflight: image-volume probe pod admitted but the ImageVolumeSource was pruned from the returned spec; feature gate is off, feature unavailable")
		return false, true
	}

	return true, true
}

// hasImageVolume reports whether the server-returned Pod still carries the probe
// volume as an ImageVolumeSource. When the ImageVolume feature gate is off the
// API server prunes the field, so its absence means the feature is unavailable.
func hasImageVolume(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, vol := range pod.Spec.Volumes {
		if vol.Name == imageVolumeProbeVolumeName {
			return vol.Image != nil
		}
	}
	return false
}

// dryRunProbePod submits a minimal Pod carrying the given probe volume as a
// server-side dry-run (never persisted) and returns the Pod as processed by the
// API server (with defaulting and field pruning applied) alongside its verdict.
func (v *ImageVolumeProbe) dryRunProbePod(ctx context.Context, namespace string, volume corev1.Volume) (*corev1.Pod, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "imagevolume-preflight-",
			Namespace:    namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "probe",
				Image: imageVolumeProbeImage,
				VolumeMounts: []corev1.VolumeMount{{
					Name:      imageVolumeProbeVolumeName,
					MountPath: "/probe",
				}},
			}},
			Volumes: []corev1.Volume{volume},
		},
	}
	err := v.Client.Create(ctx, pod, client.DryRunAll)
	return pod, err
}

// baselineProbeVolume returns an emptyDir volume, which every Kubernetes
// version accepts. It establishes whether the environment permits creating the
// probe Pod at all.
func baselineProbeVolume() corev1.Volume {
	return corev1.Volume{
		Name: imageVolumeProbeVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

// imageProbeVolume returns an ImageVolumeSource volume, the feature under test.
func imageProbeVolume() corev1.Volume {
	return corev1.Volume{
		Name: imageVolumeProbeVolumeName,
		VolumeSource: corev1.VolumeSource{
			Image: &corev1.ImageVolumeSource{
				Reference:  imageVolumeProbeImage,
				PullPolicy: corev1.PullIfNotPresent,
			},
		},
	}
}
