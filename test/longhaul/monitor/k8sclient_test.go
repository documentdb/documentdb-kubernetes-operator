// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	shareddoc "github.com/documentdb/documentdb-operator/test/shared/documentdb"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(previewv1.AddToScheme(s)).To(Succeed())
	return s
}

func newTestDocumentDB(ns, name string, modify func(dd *previewv1.DocumentDB)) *previewv1.DocumentDB {
	dd := &previewv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
	}
	if modify != nil {
		modify(dd)
	}
	return dd
}

// newTestK8sClient builds a K8sClusterClient backed by fake clients. Failures
// are reported via Gomega's Expect, so it must be called from inside a spec.
func newTestK8sClient(ns, cluster string, cs *fake.Clientset, objs ...ctrlclient.Object) *K8sClusterClient {
	scheme := newTestScheme()
	builder := fakeclient.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &K8sClusterClient{
		clientset:   cs,
		crClient:    builder.Build(),
		namespace:   ns,
		clusterName: cluster,
	}
}

var _ = Describe("K8sClusterClient", func() {
	const ns, cluster = "default", "documentdb-cluster"

	DescribeTable("isPodReady",
		func(pod *corev1.Pod, want bool) {
			Expect(isPodReady(pod)).To(Equal(want))
		},
		Entry("no conditions", &corev1.Pod{}, false),
		Entry("PodReady=True",
			&corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			}}}, true),
		Entry("PodReady=False",
			&corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			}}}, false),
		Entry("only PodScheduled=True (no Ready condition)",
			&corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			}}}, false),
	)

	Describe("GetClusterHealth", func() {
		It("aggregates pods filtered by cnpg.io/cluster and reads CR status", func() {
			pods := []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pod1", Labels: map[string]string{"cnpg.io/cluster": cluster}},
					Status: corev1.PodStatus{
						Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
						ContainerStatuses: []corev1.ContainerStatus{{RestartCount: 1}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "pod2", Labels: map[string]string{"cnpg.io/cluster": cluster}},
					Status: corev1.PodStatus{
						Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
						ContainerStatuses: []corev1.ContainerStatus{{RestartCount: 2}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "other", Labels: map[string]string{"cnpg.io/cluster": "other-cluster"}},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
					},
				},
			}
			cr := newTestDocumentDB(ns, cluster, func(dd *previewv1.DocumentDB) {
				dd.Status.Status = shareddoc.ReadyStatus
			})
			k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(pods...), cr)

			got, err := k.GetClusterHealth(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.TotalPods).To(Equal(2))
			Expect(got.ReadyPods).To(Equal(2))
			Expect(got.AllPodsReady).To(BeTrue())
			Expect(got.RestartCount).To(Equal(int32(3)))
			Expect(got.CRReady).To(BeTrue())
		})

		It("flags CRReady=false when status is not healthy and no pods exist", func() {
			cr := newTestDocumentDB(ns, cluster, func(dd *previewv1.DocumentDB) {
				dd.Status.Status = "Reconciling"
			})
			k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)

			got, err := k.GetClusterHealth(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.CRReady).To(BeFalse())
			Expect(got.AllPodsReady).To(BeFalse())
		})
	})

	Describe("GetInstancesPerNode", func() {
		DescribeTable("returns spec.instancesPerNode (defaulting to 1)",
			func(modify func(dd *previewv1.DocumentDB), want int) {
				cr := newTestDocumentDB(ns, cluster, modify)
				k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)
				got, err := k.GetInstancesPerNode(context.Background())
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(want))
			},
			Entry("explicit ipn=2",
				func(dd *previewv1.DocumentDB) { dd.Spec.InstancesPerNode = 2 }, 2),
			Entry("explicit ipn=3",
				func(dd *previewv1.DocumentDB) { dd.Spec.InstancesPerNode = 3 }, 3),
			Entry("unset defaults to 1",
				func(dd *previewv1.DocumentDB) {}, 1),
		)

		It("returns an error when the CR is missing", func() {
			k := newTestK8sClient(ns, "missing", fake.NewSimpleClientset())
			_, err := k.GetInstancesPerNode(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})

	It("ScaleCluster patches instancesPerNode", func() {
		cr := newTestDocumentDB(ns, cluster, func(dd *previewv1.DocumentDB) {
			dd.Spec.InstancesPerNode = 1
		})
		k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)

		Expect(k.ScaleCluster(context.Background(), 3)).To(Succeed())
		got, err := k.GetInstancesPerNode(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(3))
	})

	DescribeTable("GetCurrentDocumentDBImageTag",
		func(image string, want string) {
			cr := newTestDocumentDB(ns, cluster, func(dd *previewv1.DocumentDB) {
				dd.Status.DocumentDBImage = image
			})
			k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)
			got, err := k.GetCurrentDocumentDBImageTag(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("empty string", "", ""),
		Entry("image with tag", "ghcr.io/foo/documentdb:0.109.0", "0.109.0"),
		Entry("registry without tag", "ghcr.io/foo/documentdb", ""),
		Entry("trailing colon (malformed)", "ghcr.io/foo/documentdb:", ""),
		Entry("semver tag with port-like host", "host:5000/foo/documentdb:0.110.0-rc1", "0.110.0-rc1"),
	)

	It("UpgradeDocumentDB patches version fields", func() {
		cr := newTestDocumentDB(ns, cluster, nil)
		k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)

		Expect(k.UpgradeDocumentDB(context.Background(), "0.110.0")).To(Succeed())

		var got previewv1.DocumentDB
		Expect(k.crClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: cluster}, &got)).To(Succeed())
		Expect(got.Spec.DocumentDBVersion).To(Equal("0.110.0"))
		Expect(got.Spec.SchemaVersion).To(Equal("auto"))
	})

	Describe("metrics", func() {
		It("MetricsAvailable mirrors the metricsAvail flag", func() {
			k := &K8sClusterClient{metricsAvail: true}
			Expect(k.MetricsAvailable()).To(BeTrue())
			k.metricsAvail = false
			Expect(k.MetricsAvailable()).To(BeFalse())
		})

		It("GetPodMetrics returns nil/nil when metrics are unavailable", func() {
			k := &K8sClusterClient{metricsAvail: false}
			got, err := k.GetPodMetrics(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeNil())
		})
	})
})
