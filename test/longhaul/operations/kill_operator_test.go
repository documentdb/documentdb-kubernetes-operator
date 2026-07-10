// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const opNS = "documentdb-operator"

func operatorDeployment(desired, ready, unavailable int32, gen, observed int64) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       OperatorDeploymentName,
			Namespace:  opNS,
			Generation: gen,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &desired,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "documentdb-operator"}},
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas:       ready,
			UnavailableReplicas: unavailable,
			ObservedGeneration:  observed,
		},
	}
}

func operatorPod(name string, phase corev1.PodPhase, ageSeconds int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         opNS,
			Labels:            map[string]string{"app": "documentdb-operator"},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Duration(ageSeconds) * time.Second)),
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

var _ = Describe("KillOperatorPod", func() {
	It("Name is kill-operator-pod and Weight is 2", func() {
		k := NewKillOperatorPod(fake.NewSimpleClientset(), opNS, time.Minute)
		Expect(k.Name()).To(Equal("kill-operator-pod"))
		Expect(k.Weight()).To(Equal(2))
	})

	It("OutagePolicy uses the near-zero NoOutagePolicy budget", func() {
		k := NewKillOperatorPod(fake.NewSimpleClientset(), opNS, 2*time.Minute)
		p := k.OutagePolicy()
		Expect(p.AllowedWriteFailures).To(Equal(journal.NoOutageWriteFailureCushion))
		Expect(p.MustRecoverWithin).To(Equal(2 * time.Minute))
	})

	Describe("Precondition", func() {
		It("skips when the deployment is missing", func() {
			k := NewKillOperatorPod(fake.NewSimpleClientset(), opNS, time.Minute)
			ok, reason := k.Precondition(context.Background())
			Expect(ok).To(BeFalse())
			Expect(reason).To(ContainSubstring("cannot get operator deployment"))
		})

		It("skips when the deployment is not available", func() {
			dep := operatorDeployment(1, 0, 1, 1, 1)
			k := NewKillOperatorPod(fake.NewSimpleClientset(dep), opNS, time.Minute)
			ok, reason := k.Precondition(context.Background())
			Expect(ok).To(BeFalse())
			Expect(reason).To(ContainSubstring("not currently available"))
		})

		It("is eligible when the deployment is available", func() {
			dep := operatorDeployment(1, 1, 0, 1, 1)
			k := NewKillOperatorPod(fake.NewSimpleClientset(dep), opNS, time.Minute)
			ok, _ := k.Precondition(context.Background())
			Expect(ok).To(BeTrue())
		})
	})

	Describe("Execute", func() {
		It("deletes the oldest running operator pod and returns once available", func() {
			dep := operatorDeployment(1, 1, 0, 1, 1)
			newer := operatorPod("op-new", corev1.PodRunning, 10)
			older := operatorPod("op-old", corev1.PodRunning, 100)
			cs := fake.NewSimpleClientset(dep, newer, older)
			k := NewKillOperatorPod(cs, opNS, time.Minute)

			err := k.Execute(context.Background())
			Expect(err).NotTo(HaveOccurred())

			_, getErr := cs.CoreV1().Pods(opNS).Get(context.Background(), "op-old", metav1.GetOptions{})
			Expect(getErr).To(HaveOccurred(), "oldest pod should have been deleted")
			_, getErr = cs.CoreV1().Pods(opNS).Get(context.Background(), "op-new", metav1.GetOptions{})
			Expect(getErr).NotTo(HaveOccurred(), "newer pod should be untouched")
		})

		It("fails when no running pod matches the selector", func() {
			dep := operatorDeployment(1, 1, 0, 1, 1)
			pending := operatorPod("op-pending", corev1.PodPending, 10)
			cs := fake.NewSimpleClientset(dep, pending)
			k := NewKillOperatorPod(cs, opNS, time.Minute)

			err := k.Execute(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no running operator pod"))
		})
	})
})
