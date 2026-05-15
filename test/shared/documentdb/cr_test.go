// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package documentdb

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(previewv1.AddToScheme(s)).To(Succeed())
	return s
}

var _ = Describe("Shared CR helpers", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Get and List", func() {
		It("returns the named object and filters by namespace", func() {
			s := newScheme()
			objs := []client.Object{
				&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}},
				&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns1"}},
				&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns2"}},
			}
			c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()

			got, err := Get(ctx, c, types.NamespacedName{Name: "a", Namespace: "ns1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("a"))

			items, err := List(ctx, c, "ns1")
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(2))

			all, err := List(ctx, c, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(HaveLen(3))
		})
	})

	Describe("PatchSpec", func() {
		It("mutates and persists the spec", func() {
			s := newScheme()
			dd := &previewv1.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"},
				Spec:       previewv1.DocumentDBSpec{NodeCount: 1, InstancesPerNode: 1},
			}
			c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()

			fresh, err := Get(ctx, c, client.ObjectKeyFromObject(dd))
			Expect(err).NotTo(HaveOccurred())
			Expect(PatchSpec(ctx, c, fresh, func(spec *previewv1.DocumentDBSpec) {
				spec.LogLevel = "debug"
			})).To(Succeed())

			after, err := Get(ctx, c, client.ObjectKeyFromObject(dd))
			Expect(err).NotTo(HaveOccurred())
			Expect(after.Spec.LogLevel).To(Equal("debug"))
		})
	})

	Describe("IsHealthy", func() {
		It("only matches the canonical ReadyStatus", func() {
			Expect(IsHealthy(nil)).To(BeFalse())
			Expect(IsHealthy(&previewv1.DocumentDB{})).To(BeFalse())
			Expect(IsHealthy(&previewv1.DocumentDB{
				Status: previewv1.DocumentDBStatus{Status: ReadyStatus},
			})).To(BeTrue())
			Expect(IsHealthy(&previewv1.DocumentDB{
				Status: previewv1.DocumentDBStatus{Status: "Running"},
			})).To(BeFalse())
		})
	})

	Describe("WaitHealthy", func() {
		It("returns an error when the deadline elapses", func() {
			s := newScheme()
			dd := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}}
			c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
			ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			defer cancel()
			Expect(WaitHealthy(ctx, c, client.ObjectKeyFromObject(dd), 200*time.Millisecond)).To(HaveOccurred())
		})
	})

	Describe("Delete", func() {
		It("removes the object from the API", func() {
			s := newScheme()
			dd := &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1"}}
			c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
			Expect(Delete(ctx, c, dd, 2*time.Second)).To(Succeed())
			_, err := Get(ctx, c, client.ObjectKeyFromObject(dd))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("PatchInstances", func() {
		It("updates InstancesPerNode", func() {
			s := newScheme()
			dd := &previewv1.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{Name: "dd", Namespace: "ns1"},
				Spec:       previewv1.DocumentDBSpec{NodeCount: 1, InstancesPerNode: 2},
			}
			c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
			Expect(PatchInstances(ctx, c, "ns1", "dd", 3)).To(Succeed())

			got, err := Get(ctx, c, types.NamespacedName{Namespace: "ns1", Name: "dd"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Spec.InstancesPerNode).To(Equal(3))
		})

		It("is a no-op when the requested value already matches", func() {
			s := newScheme()
			dd := &previewv1.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{Name: "dd", Namespace: "ns1", ResourceVersion: "7"},
				Spec:       previewv1.DocumentDBSpec{NodeCount: 1, InstancesPerNode: 2},
			}
			c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(dd).Build()
			Expect(PatchInstances(ctx, c, "ns1", "dd", 2)).To(Succeed())
		})

		It("rejects values outside the supported range", func() {
			s := newScheme()
			c := fakeclient.NewClientBuilder().WithScheme(s).Build()
			for _, n := range []int{0, 4, -1} {
				Expect(PatchInstances(ctx, c, "ns1", "dd", n)).To(HaveOccurred(),
					"PatchInstances(%d) should fail", n)
			}
		})

		It("returns an error when the DocumentDB does not exist", func() {
			s := newScheme()
			c := fakeclient.NewClientBuilder().WithScheme(s).Build()
			Expect(PatchInstances(ctx, c, "ns1", "missing", 2)).To(HaveOccurred())
		})
	})
})
