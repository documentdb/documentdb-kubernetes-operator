// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("UpgradeDocumentDB", func() {
	It("Name is upgrade-documentdb and Weight is 1 (low so it doesn't crowd out other ops)", func() {
		u := NewUpgradeDocumentDB(&fakeClient{}, fake.NewSimpleClientset(), nil, nil, "ns", time.Minute)
		Expect(u.Name()).To(Equal("upgrade-documentdb"))
		Expect(u.Weight()).To(Equal(1))
	})

	It("OutagePolicy gives upgrades a more lenient failure budget", func() {
		u := NewUpgradeDocumentDB(&fakeClient{}, fake.NewSimpleClientset(), nil, nil, "ns", 10*time.Minute)
		p := u.OutagePolicy()
		Expect(p.AllowedWriteFailures).To(Equal(int64(200)))
		Expect(p.MustRecoverWithin).To(Equal(10 * time.Minute))
	})

	Describe("readDesiredVersion", func() {
		It("returns empty string and no error when ConfigMap is missing", func() {
			cs := fake.NewSimpleClientset()
			u := NewUpgradeDocumentDB(&fakeClient{}, cs, nil, nil, "ns", time.Minute)

			got, err := u.readDesiredVersion(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeEmpty())
		})

		It("returns the value when CM has the expected key", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: VersionConfigMapName, Namespace: "ns"},
				Data:       map[string]string{VersionConfigMapKey: "0.110.0"},
			}
			cs := fake.NewSimpleClientset(cm)
			u := NewUpgradeDocumentDB(&fakeClient{}, cs, nil, nil, "ns", time.Minute)

			got, err := u.readDesiredVersion(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal("0.110.0"))
		})

		It("returns empty string when CM exists but the key is missing", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: VersionConfigMapName, Namespace: "ns"},
				Data:       map[string]string{"unrelated": "value"},
			}
			cs := fake.NewSimpleClientset(cm)
			u := NewUpgradeDocumentDB(&fakeClient{}, cs, nil, nil, "ns", time.Minute)

			got, err := u.readDesiredVersion(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeEmpty())
		})
	})

	DescribeTable("Precondition",
		func(desired, runningTag string, ipn int, wantOK bool, wantReasonHas string) {
			const ns = "ns"
			cs := fake.NewSimpleClientset()
			if desired != "" {
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: VersionConfigMapName, Namespace: ns},
					Data:       map[string]string{VersionConfigMapKey: desired},
				}
				cs = fake.NewSimpleClientset(cm)
			}
			c := &fakeClient{instancesPerNode: ipn, imageTag: runningTag}
			u := NewUpgradeDocumentDB(c, cs, nil, nil, ns, time.Minute)

			ok, reason := u.Precondition(context.Background())
			Expect(ok).To(Equal(wantOK), "reason=%q", reason)
			if wantReasonHas != "" {
				Expect(reason).To(ContainSubstring(wantReasonHas))
			}
		},
		Entry("no desired version published", "", "0.109.0", 2, false, "no desired version"),
		Entry("already at desired", "0.110.0", "0.110.0", 2, false, "already at desired"),
		Entry("single-instance: ipn=1 -> skip", "0.110.0", "0.109.0", 1, false, "no HA standby"),
		Entry("eligible: HA + version differs", "0.110.0", "0.109.0", 2, true, ""),
		Entry("eligible: max HA", "0.110.0", "0.109.0", 3, true, ""),
	)
})
