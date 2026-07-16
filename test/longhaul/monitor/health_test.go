// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

// fakeClusterClient is a minimal ClusterClient stub for tests.
type fakeClusterClient struct {
	mu     sync.Mutex
	health ClusterHealth
	err    error
}

func (f *fakeClusterClient) setHealth(h ClusterHealth) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.health = h
	f.err = nil
}
func (f *fakeClusterClient) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}
func (f *fakeClusterClient) GetClusterHealth(_ context.Context) (ClusterHealth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.health, f.err
}
func (f *fakeClusterClient) GetCurrentDocumentDBImageTag(_ context.Context) (string, error) {
	return "", nil
}
func (f *fakeClusterClient) GetInstancesPerNode(_ context.Context) (int, error)  { return 1, nil }
func (f *fakeClusterClient) ScaleCluster(_ context.Context, _ int) error         { return nil }
func (f *fakeClusterClient) UpgradeDocumentDB(_ context.Context, _ string) error { return nil }

var _ = Describe("HealthMonitor", func() {
	Describe("IsSteadyState", func() {
		It("is false before any check", func() {
			c := &fakeClusterClient{}
			h := NewHealthMonitor(c, journal.New(), 100*time.Millisecond)
			Expect(h.IsSteadyState()).To(BeFalse())
		})

		It("becomes true after staying healthy beyond steadyStateWait", func() {
			c := &fakeClusterClient{}
			c.setHealth(ClusterHealth{AllPodsReady: true, CRReady: true, ReadyPods: 2, TotalPods: 2})
			h := NewHealthMonitor(c, journal.New(), 50*time.Millisecond)

			h.check(context.Background())
			Expect(h.IsSteadyState()).To(BeFalse(), "first healthy check sets steadySince but elapsed=0")

			time.Sleep(60 * time.Millisecond)
			Expect(h.IsSteadyState()).To(BeTrue())
		})

		It("resets to false when health is lost", func() {
			c := &fakeClusterClient{}
			c.setHealth(ClusterHealth{AllPodsReady: true, CRReady: true})
			h := NewHealthMonitor(c, journal.New(), 1*time.Millisecond)
			h.check(context.Background())
			time.Sleep(2 * time.Millisecond)
			Expect(h.IsSteadyState()).To(BeTrue())

			c.setHealth(ClusterHealth{AllPodsReady: false, CRReady: false})
			h.check(context.Background())
			Expect(h.IsSteadyState()).To(BeFalse())
		})

		It("resets to false on poll error", func() {
			c := &fakeClusterClient{}
			c.setHealth(ClusterHealth{AllPodsReady: true, CRReady: true})
			h := NewHealthMonitor(c, journal.New(), 1*time.Millisecond)
			h.check(context.Background())
			time.Sleep(2 * time.Millisecond)
			Expect(h.IsSteadyState()).To(BeTrue())

			c.setErr(errors.New("apiserver unreachable"))
			h.check(context.Background())
			Expect(h.IsSteadyState()).To(BeFalse())
		})
	})

	Describe("WaitForSteadyState", func() {
		It("returns an error when the context is cancelled", func() {
			c := &fakeClusterClient{}
			h := NewHealthMonitor(c, journal.New(), 10*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			Expect(h.WaitForSteadyState(ctx)).To(HaveOccurred())
		})

		It("returns nil once steady state is reached", func() {
			c := &fakeClusterClient{}
			c.setHealth(ClusterHealth{AllPodsReady: true, CRReady: true})
			h := NewHealthMonitor(c, journal.New(), 1*time.Millisecond)

			h.check(context.Background())
			time.Sleep(5 * time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			Expect(h.WaitForSteadyState(ctx)).To(Succeed())
		})
	})
})
