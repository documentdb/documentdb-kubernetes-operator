// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
)

// scaleOp parameterizes the scale-up / scale-down operations.
// ScaleUp and ScaleDown are ~95% identical (same fields, Precondition / Execute
// differ only by delta sign, the bound comparison, and policy constants), so
// they share one implementation via this struct. NewScaleUp / NewScaleDown
// remain the public surface.
type scaleOp struct {
	client    monitor.ClusterClient
	healthMon *monitor.HealthMonitor
	name      string
	weight    int
	delta     int    // +1 for scale-up, -1 for scale-down
	bound     int    // upper bound for up; lower bound for down
	boundKind string // "max" or "min" — only used in human-readable reasons
	recovery  time.Duration
	policy    journal.OutagePolicy
}

func (s *scaleOp) Name() string { return s.name }
func (s *scaleOp) Weight() int  { return s.weight }

func (s *scaleOp) Precondition(ctx context.Context) (bool, string) {
	current, err := s.client.GetInstancesPerNode(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot get instancesPerNode: %v", err)
	}
	if s.atBound(current) {
		return false, fmt.Sprintf("already at %s instancesPerNode (%d)", s.boundKind, s.bound)
	}
	return true, ""
}

func (s *scaleOp) Execute(ctx context.Context) error {
	current, err := s.client.GetInstancesPerNode(ctx)
	if err != nil {
		return fmt.Errorf("get instancesPerNode: %w", err)
	}

	target := current + s.delta
	if err := s.client.ScaleCluster(ctx, target); err != nil {
		return fmt.Errorf("scale to %d: %w", target, err)
	}

	// Wait for recovery (new pod ready / cluster stabilizes at new size).
	recoveryCtx, cancel := context.WithTimeout(ctx, s.recovery)
	defer cancel()
	return s.healthMon.WaitForSteadyState(recoveryCtx)
}

func (s *scaleOp) OutagePolicy() journal.OutagePolicy { return s.policy }

// atBound reports whether the current size already equals the operation's
// target bound (max for up, min for down).
func (s *scaleOp) atBound(current int) bool {
	if s.delta > 0 {
		return current >= s.bound
	}
	return current <= s.bound
}

// ScaleUp is a scale-up operation. Exported as a concrete type so the existing
// callers keep their (*ScaleUp) return type — internally it's a thin wrapper.
type ScaleUp struct{ scaleOp }

// NewScaleUp creates a ScaleUp operation. maxInstances is clamped to the
// CRD upper bound (3) to avoid admission rejections.
func NewScaleUp(client monitor.ClusterClient, health *monitor.HealthMonitor, maxInstances int, recovery time.Duration) *ScaleUp {
	if maxInstances > 3 {
		maxInstances = 3
	}
	return &ScaleUp{scaleOp{
		client:    client,
		healthMon: health,
		name:      "scale-up",
		weight:    3,
		delta:     +1,
		bound:     maxInstances,
		boundKind: "max",
		recovery:  recovery,
		policy: journal.OutagePolicy{
			AllowedDowntime:      30 * time.Second,
			AllowedWriteFailures: 20,
			MustRecoverWithin:    recovery,
		},
	}}
}

// maxInstances exposes the upper bound for tests that previously read it directly.
func (s *ScaleUp) maxInstances() int { return s.bound }

// ScaleDown is a scale-down operation. See ScaleUp comment.
type ScaleDown struct{ scaleOp }

// NewScaleDown creates a ScaleDown operation. minInstances is clamped to the
// CRD lower bound (1) to avoid admission rejections.
func NewScaleDown(client monitor.ClusterClient, health *monitor.HealthMonitor, minInstances int, recovery time.Duration) *ScaleDown {
	if minInstances < 1 {
		minInstances = 1
	}
	return &ScaleDown{scaleOp{
		client:    client,
		healthMon: health,
		name:      "scale-down",
		weight:    2,
		delta:     -1,
		bound:     minInstances,
		boundKind: "min",
		recovery:  recovery,
		policy: journal.OutagePolicy{
			AllowedDowntime:      60 * time.Second,
			AllowedWriteFailures: 50,
			MustRecoverWithin:    recovery,
		},
	}}
}

// minInstances exposes the lower bound for tests that previously read it directly.
func (s *ScaleDown) minInstances() int { return s.bound }
