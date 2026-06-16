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

// ScaleUp increases spec.instancesPerNode by 1 (HA scale dimension; range 1-3).
type ScaleUp struct {
	client       monitor.ClusterClient
	healthMon    *monitor.HealthMonitor
	maxInstances int
	recovery     time.Duration
}

// NewScaleUp creates a ScaleUp operation. maxInstances is clamped to the
// CRD upper bound (3) to avoid admission rejections.
func NewScaleUp(client monitor.ClusterClient, health *monitor.HealthMonitor, maxInstances int, recovery time.Duration) *ScaleUp {
	if maxInstances > 3 {
		maxInstances = 3
	}
	return &ScaleUp{
		client:       client,
		healthMon:    health,
		maxInstances: maxInstances,
		recovery:     recovery,
	}
}

func (s *ScaleUp) Name() string  { return "scale-up" }
func (s *ScaleUp) Weight() int   { return 3 }

func (s *ScaleUp) Precondition(ctx context.Context) (bool, string) {
	current, err := s.client.GetInstancesPerNode(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot get instancesPerNode: %v", err)
	}
	if current >= s.maxInstances {
		return false, fmt.Sprintf("already at max instancesPerNode (%d)", s.maxInstances)
	}
	return true, ""
}

func (s *ScaleUp) Execute(ctx context.Context) error {
	current, err := s.client.GetInstancesPerNode(ctx)
	if err != nil {
		return fmt.Errorf("get instancesPerNode: %w", err)
	}

	target := current + 1
	if err := s.client.ScaleCluster(ctx, target); err != nil {
		return fmt.Errorf("scale to %d: %w", target, err)
	}

	// Wait for recovery (new pod becomes ready).
	recoveryCtx, cancel := context.WithTimeout(ctx, s.recovery)
	defer cancel()
	return s.healthMon.WaitForSteadyState(recoveryCtx)
}

func (s *ScaleUp) OutagePolicy() journal.OutagePolicy {
	return journal.OutagePolicy{
		AllowedDowntime:      30 * time.Second,
		AllowedWriteFailures: 20,
		MustRecoverWithin:    s.recovery,
	}
}

// ScaleDown decreases spec.instancesPerNode by 1 (HA scale dimension; range 1-3).
type ScaleDown struct {
	client       monitor.ClusterClient
	healthMon    *monitor.HealthMonitor
	minInstances int
	recovery     time.Duration
}

// NewScaleDown creates a ScaleDown operation. minInstances is clamped to the
// CRD lower bound (1) to avoid admission rejections.
func NewScaleDown(client monitor.ClusterClient, health *monitor.HealthMonitor, minInstances int, recovery time.Duration) *ScaleDown {
	if minInstances < 1 {
		minInstances = 1
	}
	return &ScaleDown{
		client:       client,
		healthMon:    health,
		minInstances: minInstances,
		recovery:     recovery,
	}
}

func (s *ScaleDown) Name() string  { return "scale-down" }
func (s *ScaleDown) Weight() int   { return 2 }

func (s *ScaleDown) Precondition(ctx context.Context) (bool, string) {
	current, err := s.client.GetInstancesPerNode(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot get instancesPerNode: %v", err)
	}
	if current <= s.minInstances {
		return false, fmt.Sprintf("already at min instancesPerNode (%d)", s.minInstances)
	}
	return true, ""
}

func (s *ScaleDown) Execute(ctx context.Context) error {
	current, err := s.client.GetInstancesPerNode(ctx)
	if err != nil {
		return fmt.Errorf("get instancesPerNode: %w", err)
	}

	target := current - 1
	if err := s.client.ScaleCluster(ctx, target); err != nil {
		return fmt.Errorf("scale to %d: %w", target, err)
	}

	// Wait for recovery (cluster stabilizes at new size).
	recoveryCtx, cancel := context.WithTimeout(ctx, s.recovery)
	defer cancel()
	return s.healthMon.WaitForSteadyState(recoveryCtx)
}

func (s *ScaleDown) OutagePolicy() journal.OutagePolicy {
	return journal.OutagePolicy{
		AllowedDowntime:      60 * time.Second,
		AllowedWriteFailures: 50,
		MustRecoverWithin:    s.recovery,
	}
}
