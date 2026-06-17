// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
)

// fakeClient is a minimal monitor.ClusterClient stub for unit tests.
type fakeClient struct {
	mu               sync.Mutex
	instancesPerNode int
	ipnErr           error
	imageTag         string
	scaleCalls       []int
	upgradeCalls     []string
}

func (f *fakeClient) GetClusterHealth(_ context.Context) (monitor.ClusterHealth, error) {
	return monitor.ClusterHealth{}, nil
}
func (f *fakeClient) GetCurrentDocumentDBImageTag(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.imageTag, nil
}
func (f *fakeClient) GetInstancesPerNode(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.instancesPerNode, f.ipnErr
}
func (f *fakeClient) ScaleCluster(_ context.Context, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scaleCalls = append(f.scaleCalls, n)
	f.instancesPerNode = n
	return nil
}
func (f *fakeClient) UpgradeDocumentDB(_ context.Context, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upgradeCalls = append(f.upgradeCalls, v)
	return nil
}

var _ = Describe("ScaleUp", func() {
	DescribeTable("clamps maxInstances to the CRD upper bound",
		func(in, want int) {
			s := NewScaleUp(&fakeClient{}, nil, in, time.Second)
			Expect(s.maxInstances()).To(Equal(want))
		},
		Entry("1->1", 1, 1),
		Entry("2->2", 2, 2),
		Entry("3->3", 3, 3),
		Entry("4 clamped to 3", 4, 3),
		Entry("99 clamped to 3", 99, 3),
	)

	It("Name and Weight are scale-up/3", func() {
		s := NewScaleUp(&fakeClient{}, nil, 3, time.Second)
		Expect(s.Name()).To(Equal("scale-up"))
		Expect(s.Weight()).To(Equal(3))
	})

	DescribeTable("Precondition",
		func(current int, ipnErr error, max int, wantOK bool, wantReasonHas string) {
			c := &fakeClient{instancesPerNode: current, ipnErr: ipnErr}
			s := NewScaleUp(c, nil, max, time.Second)
			ok, reason := s.Precondition(context.Background())
			Expect(ok).To(Equal(wantOK), "reason=%q", reason)
			if wantReasonHas != "" {
				Expect(reason).To(ContainSubstring(wantReasonHas))
			}
		},
		Entry("eligible: under max", 1, nil, 3, true, ""),
		Entry("eligible: just under max", 2, nil, 3, true, ""),
		Entry("blocked: at max", 3, nil, 3, false, "already at max"),
		Entry("blocked: ipn read error", 0, errors.New("apiserver down"), 3, false, "cannot get instancesPerNode"),
	)

	It("OutagePolicy uses tighter budgets and echoes MustRecoverWithin", func() {
		s := NewScaleUp(&fakeClient{}, nil, 3, 5*time.Minute)
		p := s.OutagePolicy()
		Expect(p.AllowedDowntime).To(Equal(30 * time.Second))
		Expect(p.AllowedWriteFailures).To(Equal(int64(20)))
		Expect(p.MustRecoverWithin).To(Equal(5 * time.Minute))
	})
})

var _ = Describe("ScaleDown", func() {
	DescribeTable("clamps minInstances to the CRD lower bound",
		func(in, want int) {
			s := NewScaleDown(&fakeClient{}, nil, in, time.Second)
			Expect(s.minInstances()).To(Equal(want))
		},
		Entry("0 -> 1", 0, 1),
		Entry("-5 -> 1", -5, 1),
		Entry("1 -> 1", 1, 1),
		Entry("2 -> 2", 2, 2),
		Entry("3 -> 3", 3, 3),
	)

	It("Name and Weight are scale-down/2", func() {
		s := NewScaleDown(&fakeClient{}, nil, 1, time.Second)
		Expect(s.Name()).To(Equal("scale-down"))
		Expect(s.Weight()).To(Equal(2))
	})

	DescribeTable("Precondition",
		func(current int, ipnErr error, min int, wantOK bool, wantReasonHas string) {
			c := &fakeClient{instancesPerNode: current, ipnErr: ipnErr}
			s := NewScaleDown(c, nil, min, time.Second)
			ok, reason := s.Precondition(context.Background())
			Expect(ok).To(Equal(wantOK), "reason=%q", reason)
			if wantReasonHas != "" {
				Expect(reason).To(ContainSubstring(wantReasonHas))
			}
		},
		Entry("eligible: above min", 3, nil, 1, true, ""),
		Entry("eligible: just above min", 2, nil, 1, true, ""),
		Entry("blocked: at min", 1, nil, 1, false, "already at min"),
		Entry("blocked: ipn read error", 0, errors.New("apiserver down"), 1, false, "cannot get instancesPerNode"),
	)

	It("OutagePolicy is more lenient than scale-up", func() {
		s := NewScaleDown(&fakeClient{}, nil, 1, 5*time.Minute)
		p := s.OutagePolicy()
		Expect(p.AllowedDowntime).To(Equal(60 * time.Second))
		Expect(p.AllowedWriteFailures).To(Equal(int64(50)))
	})
})
