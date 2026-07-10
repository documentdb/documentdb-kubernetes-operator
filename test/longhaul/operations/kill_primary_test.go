// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
)

var _ = Describe("KillPrimaryPod", func() {
	It("Name is kill-primary-pod and Weight is 2", func() {
		k := NewKillPrimaryPod(&fakeClient{}, nil, time.Minute)
		Expect(k.Name()).To(Equal("kill-primary-pod"))
		Expect(k.Weight()).To(Equal(2))
	})

	It("OutagePolicy allows a moderate failover write-failure budget", func() {
		k := NewKillPrimaryPod(&fakeClient{}, nil, 3*time.Minute)
		p := k.OutagePolicy()
		Expect(p.AllowedWriteFailures).To(Equal(int64(50)))
		Expect(p.MustRecoverWithin).To(Equal(3 * time.Minute))
	})

	DescribeTable("Precondition",
		func(ipn int, ipnErr error, wantOK bool, wantReasonHas string) {
			c := &fakeClient{instancesPerNode: ipn, ipnErr: ipnErr}
			k := NewKillPrimaryPod(c, nil, time.Minute)

			ok, reason := k.Precondition(context.Background())
			Expect(ok).To(Equal(wantOK), "reason=%q", reason)
			if wantReasonHas != "" {
				Expect(reason).To(ContainSubstring(wantReasonHas))
			}
		},
		Entry("single-instance: ipn=1 -> skip", 1, nil, false, "no HA standby"),
		Entry("read error -> skip", 0, errors.New("boom"), false, "cannot read instancesPerNode"),
		Entry("HA: ipn=2 -> eligible", 2, nil, true, ""),
		Entry("HA: ipn=3 -> eligible", 3, nil, true, ""),
	)

	It("Execute deletes the reported primary pod", func() {
		c := &fakeClient{instancesPerNode: 2, primary: "cluster-1"}
		// The health monitor never reaches steady state here (its Run loop
		// isn't started), so Execute times out on WaitForSteadyState — but the
		// primary delete side-effect has already happened, which is what we
		// assert. A short recovery keeps the test fast.
		hm := monitor.NewHealthMonitor(c, journal.New(), time.Hour)
		k := NewKillPrimaryPod(c, hm, 500*time.Millisecond)

		_ = k.Execute(context.Background())
		c.mu.Lock()
		defer c.mu.Unlock()
		Expect(c.deletedPods).To(ConsistOf("cluster-1"))
	})

	It("Execute fails without deleting when the primary is unknown", func() {
		c := &fakeClient{instancesPerNode: 2, primaryErr: errors.New("no primary")}
		k := NewKillPrimaryPod(c, nil, time.Second)

		err := k.Execute(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("get primary instance"))
		c.mu.Lock()
		defer c.mu.Unlock()
		Expect(c.deletedPods).To(BeEmpty())
	})
})
