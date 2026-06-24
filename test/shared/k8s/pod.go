// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package k8s contains thin pod-inspection helpers shared between the
// long-haul driver and the e2e suite. These are tiny by design — the
// goal is one canonical implementation of the "is this pod usable?"
// predicate plus the CNPG cluster-label key so neither suite needs to
// re-derive them inline.
package k8s

import (
	utilslabels "github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	corev1 "k8s.io/api/core/v1"
)

// ClusterLabel is the pod/PVC label key CNPG stamps on every resource it
// owns for a given Cluster. Re-exported from cnpg's pkg/utils so callers
// in this repo have a single import path and don't need to know that
// the upstream constant lives outside the cnpg-i contract surface.
const ClusterLabel = utilslabels.ClusterLabelName

// IsPodReady reports whether the pod has the standard PodReady=True
// condition. It does not check the pod phase; callers that care about
// the pod actually running should use IsPodRunningAndReady.
func IsPodReady(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// IsPodRunningAndReady is the stricter check used by callers that want
// to guarantee the pod is both in the Running phase and reports
// PodReady=True. Equivalent to the CNPG suite's pod-readiness gate.
func IsPodRunningAndReady(pod *corev1.Pod) bool {
	if pod == nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	return IsPodReady(pod)
}

// TotalRestarts sums RestartCount across every container status on the
// pod. Matches CNPG's PodRestarted semantics — a single returned int32
// that conflates init/regular containers but is sufficient for
// "anything restarted recently?" gates.
func TotalRestarts(pod *corev1.Pod) int32 {
	if pod == nil {
		return 0
	}
	var total int32
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
}
