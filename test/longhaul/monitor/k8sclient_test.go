// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

var testCRGVR = schema.GroupVersionResource{
	Group:    "documentdb.io",
	Version:  "preview",
	Resource: "dbs",
}

func newTestCR(ns, name string, modify func(obj map[string]interface{})) *unstructured.Unstructured {
	obj := map[string]interface{}{
		"apiVersion": "documentdb.io/preview",
		"kind":       "DocumentDB",
		"metadata": map[string]interface{}{
			"namespace": ns,
			"name":      name,
		},
		"spec":   map[string]interface{}{},
		"status": map[string]interface{}{},
	}
	if modify != nil {
		modify(obj)
	}
	u := &unstructured.Unstructured{Object: obj}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "documentdb.io", Version: "preview", Kind: "DocumentDB"})
	return u
}

// newTestK8sClient builds a K8sClusterClient backed by fake clients. Failures
// are reported via Gomega's Expect, so it must be called from inside a spec.
func newTestK8sClient(ns, cluster string, cs *fake.Clientset, objs ...runtime.Object) *K8sClusterClient {
	scheme := runtime.NewScheme()
	gvk := schema.GroupVersionKind{Group: "documentdb.io", Version: "preview", Kind: "DocumentDB"}
	listGVK := schema.GroupVersionKind{Group: "documentdb.io", Version: "preview", Kind: "DocumentDBList"}
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
	gvrToListKind := map[schema.GroupVersionResource]string{testCRGVR: "DocumentDBList"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
	for _, o := range objs {
		u, ok := o.(*unstructured.Unstructured)
		Expect(ok).To(BeTrue(), "newTestK8sClient: object is not *unstructured.Unstructured: %T", o)
		Expect(dyn.Tracker().Create(testCRGVR, u, u.GetNamespace())).To(Succeed())
	}
	return &K8sClusterClient{
		clientset:     cs,
		dynamicClient: dyn,
		namespace:     ns,
		clusterName:   cluster,
		crGVR:         testCRGVR,
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
			cr := newTestCR(ns, cluster, func(o map[string]interface{}) {
				o["status"] = map[string]interface{}{"status": "Cluster in healthy state"}
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
			cr := newTestCR(ns, cluster, func(o map[string]interface{}) {
				o["status"] = map[string]interface{}{"status": "Reconciling"}
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
			func(modify func(map[string]interface{}), want int) {
				cr := newTestCR(ns, cluster, modify)
				k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)
				got, err := k.GetInstancesPerNode(context.Background())
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(want))
			},
			Entry("explicit ipn=2",
				func(o map[string]interface{}) { o["spec"] = map[string]interface{}{"instancesPerNode": int64(2)} }, 2),
			Entry("explicit ipn=3",
				func(o map[string]interface{}) { o["spec"] = map[string]interface{}{"instancesPerNode": int64(3)} }, 3),
			Entry("unset defaults to 1",
				func(o map[string]interface{}) { o["spec"] = map[string]interface{}{} }, 1),
		)

		It("returns an error when the CR is missing", func() {
			k := newTestK8sClient(ns, "missing", fake.NewSimpleClientset())
			_, err := k.GetInstancesPerNode(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})

	It("ScaleCluster patches instancesPerNode", func() {
		cr := newTestCR(ns, cluster, func(o map[string]interface{}) {
			o["spec"] = map[string]interface{}{"instancesPerNode": int64(1)}
		})
		k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)

		Expect(k.ScaleCluster(context.Background(), 3)).To(Succeed())
		got, err := k.GetInstancesPerNode(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(3))
	})

	DescribeTable("GetCurrentDocumentDBImageTag",
		func(image string, setStatus bool, want string) {
			cr := newTestCR(ns, cluster, func(o map[string]interface{}) {
				if setStatus {
					o["status"] = map[string]interface{}{"documentDBImage": image}
				}
			})
			k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)
			got, err := k.GetCurrentDocumentDBImageTag(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("unset", "", false, ""),
		Entry("empty string", "", true, ""),
		Entry("image with tag", "ghcr.io/foo/documentdb:0.109.0", true, "0.109.0"),
		Entry("registry without tag", "ghcr.io/foo/documentdb", true, ""),
		Entry("trailing colon (malformed)", "ghcr.io/foo/documentdb:", true, ""),
		Entry("semver tag with port-like host", "host:5000/foo/documentdb:0.110.0-rc1", true, "0.110.0-rc1"),
	)

	It("UpgradeDocumentDB patches version fields", func() {
		cr := newTestCR(ns, cluster, nil)
		k := newTestK8sClient(ns, cluster, fake.NewSimpleClientset(), cr)

		Expect(k.UpgradeDocumentDB(context.Background(), "0.110.0")).To(Succeed())

		got, err := k.dynamicClient.Resource(testCRGVR).Namespace(ns).Get(context.Background(), cluster, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		ver, _, _ := unstructured.NestedString(got.Object, "spec", "documentDBVersion")
		Expect(ver).To(Equal("0.110.0"))
		schemaVer, _, _ := unstructured.NestedString(got.Object, "spec", "schemaVersion")
		Expect(schemaVer).To(Equal("auto"))
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
