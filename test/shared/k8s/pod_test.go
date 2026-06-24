// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestIsPodReady(t *testing.T) {
	cases := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{"nil", nil, false},
		{"no conditions", &corev1.Pod{}, false},
		{"ready true", &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}}}, true},
		{"ready false", &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionFalse},
		}}}, false},
		{"other condition true", &corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
			{Type: corev1.PodInitialized, Status: corev1.ConditionTrue},
		}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPodReady(tc.pod); got != tc.want {
				t.Fatalf("IsPodReady(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsPodRunningAndReady(t *testing.T) {
	ready := []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	cases := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{"nil", nil, false},
		{"running but not ready", &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, false},
		{"ready but pending", &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: ready}}, false},
		{"running and ready", &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: ready}}, true},
		{"succeeded (terminal)", &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded, Conditions: ready}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPodRunningAndReady(tc.pod); got != tc.want {
				t.Fatalf("IsPodRunningAndReady(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestTotalRestarts(t *testing.T) {
	cases := []struct {
		name string
		pod  *corev1.Pod
		want int32
	}{
		{"nil", nil, 0},
		{"no statuses", &corev1.Pod{}, 0},
		{"single container", &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "main", RestartCount: 3},
		}}}, 3},
		{"sums across containers", &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "main", RestartCount: 2},
			{Name: "sidecar", RestartCount: 5},
		}}}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TotalRestarts(tc.pod); got != tc.want {
				t.Fatalf("TotalRestarts(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestClusterLabelMatchesCNPG(t *testing.T) {
	// Sanity: detect upstream renames quickly. The real value lives in
	// cnpg/pkg/utils — we just re-export it.
	if ClusterLabel != "cnpg.io/cluster" {
		t.Fatalf("ClusterLabel = %q, want %q (cnpg upstream may have renamed)", ClusterLabel, "cnpg.io/cluster")
	}
}
