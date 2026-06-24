// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package k8s

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Shared pod helpers", func() {
	Describe("IsPodReady", func() {
		DescribeTable("returns whether the pod has PodReady=True",
			func(pod *corev1.Pod, want bool) {
				Expect(IsPodReady(pod)).To(Equal(want))
			},
			Entry("nil pod", nil, false),
			Entry("no conditions", &corev1.Pod{}, false),
			Entry("PodReady=True",
				&corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionTrue},
				}}}, true),
			Entry("PodReady=False",
				&corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionFalse},
				}}}, false),
			Entry("other condition true (PodInitialized) is not a PodReady signal",
				&corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
					{Type: corev1.PodInitialized, Status: corev1.ConditionTrue},
				}}}, false),
		)
	})

	Describe("IsPodRunningAndReady", func() {
		ready := []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		DescribeTable("requires both Phase=Running and PodReady=True",
			func(pod *corev1.Pod, want bool) {
				Expect(IsPodRunningAndReady(pod)).To(Equal(want))
			},
			Entry("nil pod", nil, false),
			Entry("running but not ready",
				&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, false),
			Entry("ready but pending",
				&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: ready}}, false),
			Entry("running and ready",
				&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: ready}}, true),
			Entry("succeeded (terminal) is not Running",
				&corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded, Conditions: ready}}, false),
		)
	})

	Describe("TotalRestarts", func() {
		DescribeTable("sums RestartCount across all containers",
			func(pod *corev1.Pod, want int32) {
				Expect(TotalRestarts(pod)).To(Equal(want))
			},
			Entry("nil pod", nil, int32(0)),
			Entry("no statuses", &corev1.Pod{}, int32(0)),
			Entry("single container",
				&corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
					{Name: "main", RestartCount: 3},
				}}}, int32(3)),
			Entry("sums across containers",
				&corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
					{Name: "main", RestartCount: 2},
					{Name: "sidecar", RestartCount: 5},
				}}}, int32(7)),
		)
	})

	Describe("ClusterLabel", func() {
		// Sanity: detect upstream renames quickly. The real value lives
		// in cnpg/pkg/utils — we just re-export it.
		It("matches the CNPG cluster label key", func() {
			Expect(ClusterLabel).To(Equal("cnpg.io/cluster"))
		})
	})
})
