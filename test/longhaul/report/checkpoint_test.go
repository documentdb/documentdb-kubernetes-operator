// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("CheckpointReporter", func() {
	It("emit() is safe with a nil clientset (logs to stdout, does not panic)", func() {
		r := NewCheckpointReporter(nil, "ns", time.Second, func() Summary {
			return Summary{Result: ResultPass, Duration: time.Minute}
		})
		Expect(func() { r.emit(context.Background()) }).NotTo(Panic())
	})

	It("creates the ConfigMap on first emit and labels it identifiably", func() {
		cs := fake.NewSimpleClientset()
		r := NewCheckpointReporter(cs, "ns", time.Second, func() Summary {
			return Summary{Result: ResultPass, Duration: 2 * time.Hour, OpsExecuted: 5}
		})

		r.emit(context.Background())

		cm, err := cs.CoreV1().ConfigMaps("ns").Get(context.Background(), ConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cm.Data).To(HaveKey("latest-report"))
		Expect(cm.Data).To(HaveKey("last-updated"))
		Expect(cm.Data).To(HaveKey("result"))
		// PASS at intermediate checkpoint is persisted as RUNNING so consumers
		// can distinguish in-flight from final state.
		Expect(cm.Data["result"]).To(Equal("RUNNING"))
		Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "longhaul-test"))
	})

	It("persists FAIL results as FAIL", func() {
		cs := fake.NewSimpleClientset()
		r := NewCheckpointReporter(cs, "ns", time.Second, func() Summary {
			return Summary{Result: ResultFail, FailReason: "data loss"}
		})

		r.emit(context.Background())

		cm, err := cs.CoreV1().ConfigMaps("ns").Get(context.Background(), ConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cm.Data["result"]).To(Equal("FAIL"))
	})

	It("Updates the existing ConfigMap on subsequent emits", func() {
		cs := fake.NewSimpleClientset()

		calls := 0
		r := NewCheckpointReporter(cs, "ns", time.Second, func() Summary {
			calls++
			return Summary{Result: ResultPass, Duration: time.Duration(calls) * time.Hour, OpsExecuted: calls * 10}
		})

		r.emit(context.Background())
		cm1, err := cs.CoreV1().ConfigMaps("ns").Get(context.Background(), ConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		report1 := cm1.Data["latest-report"]

		// Fake clientset doesn't bump ResourceVersion automatically, so assert
		// on content change instead.
		r.emit(context.Background())
		cm2, err := cs.CoreV1().ConfigMaps("ns").Get(context.Background(), ConfigMapName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cm2.Data["latest-report"]).NotTo(Equal(report1))
		Expect(calls).To(Equal(2))
	})
})
