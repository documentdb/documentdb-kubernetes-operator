// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package imagemode

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2" //nolint:revive
	. "github.com/onsi/gomega"    //nolint:revive

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// probeClient returns a controller-runtime client whose Create behaviour models
// how a particular API server responds to the dry-run probe Pods.
func probeClient(createFn func(pod *corev1.Pod) error) client.Client {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
				return createFn(obj.(*corev1.Pod))
			},
		}).
		Build()
}

func hasImageVolumeSource(pod *corev1.Pod) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.Image != nil {
			return true
		}
	}
	return false
}

var (
	apiSupported = func(_ *corev1.Pod) error { return nil }
	apiRejects   = func(pod *corev1.Pod) error {
		if hasImageVolumeSource(pod) {
			return apierrors.NewInvalid(schema.GroupKind{Kind: "Pod"}, pod.Name, field.ErrorList{
				field.Forbidden(field.NewPath("spec", "volumes").Index(0).Child("image"), "image volumes are disabled by feature-gate"),
			})
		}
		return nil
	}
	apiPrunes = func(pod *corev1.Pod) error {
		for i := range pod.Spec.Volumes {
			pod.Spec.Volumes[i].Image = nil
		}
		return nil
	}
	apiBlocksAll = func(_ *corev1.Pod) error {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("exceeded quota"))
	}
	// failOn makes the baseline (image=false) or image-volume (image=true) probe
	// request fail with err and admits the other one.
	failOn = func(image bool, err error) func(*corev1.Pod) error {
		return func(pod *corev1.Pod) error {
			if hasImageVolumeSource(pod) == image {
				return err
			}
			return nil
		}
	}
)

func node(name, runtime string, unschedulable bool) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{ContainerRuntimeVersion: runtime}},
	}
}

func clientset(minor string, nodes ...corev1.Node) *kubefake.Clientset {
	objs := make([]runtime.Object, 0, len(nodes))
	for i := range nodes {
		objs = append(objs, &nodes[i])
	}
	cs := kubefake.NewSimpleClientset(objs...)
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{Major: "1", Minor: minor}
	return cs
}

var _ = Describe("RuntimeSupportsImageVolume", func() {
	DescribeTable("parses container runtime versions",
		func(runtime string, expected bool) {
			Expect(RuntimeSupportsImageVolume(runtime)).To(Equal(expected))
		},
		Entry("containerd 1.7", "containerd://1.7.24", false),
		Entry("containerd 1.7 with distro suffix", "containerd://1.7.27-1", false),
		Entry("containerd 2.0", "containerd://2.0.5", false),
		Entry("containerd 2.1", "containerd://2.1.3", true),
		Entry("containerd 2.2", "containerd://2.2.0", true),
		Entry("containerd with v prefix", "containerd://v2.1.0", true),
		Entry("containerd 3.x", "containerd://3.0.0", true),
		Entry("CRI-O 1.32", "cri-o://1.32.4", false),
		Entry("CRI-O 1.33", "cri-o://1.33.1", true),
		Entry("docker", "docker://24.0.7", false),
		Entry("unknown scheme", "foo://9.9.9", false),
		Entry("garbage version", "containerd://abc", false),
		Entry("empty", "", false),
	)
})

var _ = Describe("CheckNodes", func() {
	It("passes when every schedulable node supports image volumes", func() {
		ok, _ := CheckNodes([]corev1.Node{
			node("a", "containerd://2.1.3", false),
			node("b", "cri-o://1.33.0", false),
		})
		Expect(ok).To(BeTrue())
	})

	It("fails when any schedulable node runs an old runtime", func() {
		ok, reason := CheckNodes([]corev1.Node{
			node("a", "containerd://2.1.3", false),
			node("b", "containerd://1.7.24", false),
		})
		Expect(ok).To(BeFalse())
		Expect(reason).To(ContainSubstring(`node "b"`))
		Expect(reason).To(ContainSubstring("containerd://1.7.24"))
	})

	It("ignores unschedulable nodes", func() {
		ok, _ := CheckNodes([]corev1.Node{
			node("old", "containerd://1.7.24", true),
			node("new", "containerd://2.1.3", false),
		})
		Expect(ok).To(BeTrue())
	})

	It("does not decide when no schedulable node can be checked", func() {
		ok, note := CheckNodes([]corev1.Node{node("cordoned", "containerd://1.7.24", true)})
		Expect(ok).To(BeTrue())
		Expect(note).To(ContainSubstring("not verified"))
	})

	It("ignores Windows nodes and nodes that have not reported a runtime yet", func() {
		windows := node("win", "containerd://1.7.20+azure", false)
		windows.Labels = map[string]string{corev1.LabelOSStable: "windows"}
		registering := node("new", "", false)
		ok, note := CheckNodes([]corev1.Node{windows, registering, node("linux", "containerd://2.1.3", false)})
		Expect(ok).To(BeTrue())
		Expect(note).To(BeEmpty())
	})
})

var _ = Describe("ConfiguredMode", func() {
	DescribeTable("reads DOCUMENTDB_IMAGE_MODE",
		func(value string, expected Mode) {
			GinkgoT().Setenv(util.DOCUMENTDB_IMAGE_MODE_ENV, value)
			Expect(ConfiguredMode()).To(Equal(expected))
		},
		Entry("unset", "", Auto),
		Entry("auto", "auto", Auto),
		Entry("imageVolume", "imageVolume", ImageVolume),
		Entry("combined", "combined", Combined),
		Entry("invalid falls back to auto", "bogus", Auto),
	)
})

var _ = Describe("ProbeAPI", func() {
	DescribeTable("interprets the differential dry-run",
		func(createFn func(*corev1.Pod) error, expected ProbeResult) {
			result, err := ProbeAPI(context.Background(), probeClient(createFn), "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(expected))
		},
		Entry("both pods admitted", apiSupported, ProbeSupported),
		Entry("image pod rejected", apiRejects, ProbeUnsupported),
		Entry("image volume pruned (gate off)", apiPrunes, ProbeUnsupported),
		Entry("baseline rejected", apiBlocksAll, ProbeInconclusive),
	)

	DescribeTable("returns retryable failures of either request as errors",
		func(createFn func(*corev1.Pod) error) {
			_, err := ProbeAPI(context.Background(), probeClient(createFn), "default")
			Expect(err).To(HaveOccurred())
		},
		Entry("baseline timeout", failOn(false, apierrors.NewTimeoutError("etcd slow", 1))),
		Entry("image timeout", failOn(true, apierrors.NewTimeoutError("etcd slow", 1))),
		Entry("image throttled", failOn(true, apierrors.NewTooManyRequests("slow down", 1))),
		Entry("image server error", failOn(true, apierrors.NewInternalError(errors.New("boom")))),
		Entry("baseline transport error", failOn(false, errors.New("connection refused"))),
		Entry("baseline unauthorized (expired token)", failOn(false, apierrors.NewUnauthorized("token expired"))),
	)

	It("submits probe pods that satisfy the restricted Pod Security profile and quotas requiring limits", func() {
		var seen *corev1.Pod
		_, err := ProbeAPI(context.Background(), probeClient(func(p *corev1.Pod) error { seen = p; return nil }), "default")
		Expect(err).NotTo(HaveOccurred())
		Expect(seen).NotTo(BeNil())
		Expect(*seen.Spec.SecurityContext.RunAsNonRoot).To(BeTrue())
		Expect(seen.Spec.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
		Expect(*seen.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation).To(BeFalse())
		Expect(seen.Spec.Containers[0].SecurityContext.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")))
		// Namespaces whose quota or policy requires requests/limits must admit it.
		res := seen.Spec.Containers[0].Resources
		Expect(res.Requests).To(HaveKey(corev1.ResourceCPU))
		Expect(res.Requests).To(HaveKey(corev1.ResourceMemory))
		Expect(res.Limits).To(HaveKey(corev1.ResourceCPU))
		Expect(res.Limits).To(HaveKey(corev1.ResourceMemory))
	})
})

var _ = Describe("Detector", func() {
	newNode := node("n", "containerd://2.1.3", false)
	oldNode := node("n", "containerd://1.7.24", false)

	DescribeTable("selects a mode in auto",
		func(cs *kubefake.Clientset, createFn func(*corev1.Pod) error, expected Mode) {
			d := &Detector{Client: probeClient(createFn), Clientset: cs}
			mode, reason, err := d.Detect(context.Background(), "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(expected))
			Expect(reason).NotTo(BeEmpty())
		},
		Entry("new runtime + API supported", clientset("35", newNode), apiSupported, ImageVolume),
		Entry("new runtime + API gate off", clientset("34", newNode), apiPrunes, Combined),
		Entry("old runtime even when API supports it", clientset("35", oldNode), apiSupported, Combined),
		Entry("no nodes defers to the API server", clientset("35"), apiSupported, ImageVolume),
		Entry("no nodes and API gate off", clientset("34"), apiPrunes, Combined),
		Entry("inconclusive probe on 1.35", clientset("35", newNode), apiBlocksAll, ImageVolume),
		Entry("inconclusive probe on 1.35+ (vendor minor)", clientset("35+", newNode), apiBlocksAll, ImageVolume),
		Entry("inconclusive probe on 1.34", clientset("34", newNode), apiBlocksAll, Combined),
	)

	It("falls back to combined when the operator may not list nodes", func() {
		cs := clientset("35", newNode)
		cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("rbac"))
		})
		d := &Detector{Client: probeClient(apiSupported), Clientset: cs}
		mode, reason, err := d.Detect(context.Background(), "default")
		Expect(err).NotTo(HaveOccurred())
		Expect(mode).To(Equal(Combined))
		Expect(reason).To(ContainSubstring("forbidden"))
	})

	It("returns an error instead of a mode when listing nodes fails transiently", func() {
		cs := clientset("35", newNode)
		cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		})
		d := &Detector{Client: probeClient(apiSupported), Clientset: cs}
		_, _, err := d.Detect(context.Background(), "default")
		Expect(err).To(MatchError(ContainSubstring("connection refused")))
	})

	DescribeTable("returns an error instead of a mode when a probe request fails transiently",
		func(createFn func(*corev1.Pod) error) {
			d := &Detector{Client: probeClient(createFn), Clientset: clientset("34", newNode)}
			mode, _, err := d.Detect(context.Background(), "default")
			Expect(err).To(MatchError(ContainSubstring("probing image volume support")))
			Expect(mode).To(BeEmpty())
		},
		Entry("baseline timeout", failOn(false, apierrors.NewTimeoutError("etcd slow", 1))),
		Entry("image timeout", failOn(true, apierrors.NewTimeoutError("etcd slow", 1))),
	)

	DescribeTable("honours a forced mode without probing",
		func(value string, expected Mode) {
			GinkgoT().Setenv(util.DOCUMENTDB_IMAGE_MODE_ENV, value)
			d := &Detector{Client: probeClient(func(*corev1.Pod) error {
				Fail("probe must not run when the mode is forced")
				return nil
			}), Clientset: clientset("30", oldNode)}
			mode, _, err := d.Detect(context.Background(), "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal(expected))
		},
		Entry("imageVolume", "imageVolume", ImageVolume),
		Entry("combined", "combined", Combined),
	)
})
