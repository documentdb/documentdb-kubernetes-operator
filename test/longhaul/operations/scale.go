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

// ScaleUp increases the cluster replica count by 1.
type ScaleUp struct {
	client      monitor.ClusterClient
	healthMon   *monitor.HealthMonitor
	maxReplicas int
	recovery    time.Duration
}

// NewScaleUp creates a ScaleUp operation.
func NewScaleUp(client monitor.ClusterClient, health *monitor.HealthMonitor, maxReplicas int, recovery time.Duration) *ScaleUp {
	return &ScaleUp{
		client:      client,
		healthMon:   health,
		maxReplicas: maxReplicas,
		recovery:    recovery,
	}
}

func (s *ScaleUp) Name() string  { return "scale-up" }
func (s *ScaleUp) Weight() int   { return 3 }

func (s *ScaleUp) Precondition(ctx context.Context) (bool, string) {
	current, err := s.client.GetCurrentReplicas(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot get replicas: %v", err)
	}
	if current >= s.maxReplicas {
		return false, fmt.Sprintf("already at max replicas (%d)", s.maxReplicas)
	}
	return true, ""
}

func (s *ScaleUp) Execute(ctx context.Context) error {
	current, err := s.client.GetCurrentReplicas(ctx)
	if err != nil {
		return fmt.Errorf("get replicas: %w", err)
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

// ScaleDown decreases the cluster replica count by 1.
type ScaleDown struct {
	client      monitor.ClusterClient
	healthMon   *monitor.HealthMonitor
	minReplicas int
	recovery    time.Duration
}

// NewScaleDown creates a ScaleDown operation.
func NewScaleDown(client monitor.ClusterClient, health *monitor.HealthMonitor, minReplicas int, recovery time.Duration) *ScaleDown {
	return &ScaleDown{
		client:      client,
		healthMon:   health,
		minReplicas: minReplicas,
		recovery:    recovery,
	}
}

func (s *ScaleDown) Name() string  { return "scale-down" }
func (s *ScaleDown) Weight() int   { return 2 }

func (s *ScaleDown) Precondition(ctx context.Context) (bool, string) {
	current, err := s.client.GetCurrentReplicas(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot get replicas: %v", err)
	}
	if current <= s.minReplicas {
		return false, fmt.Sprintf("already at min replicas (%d)", s.minReplicas)
	}
	return true, ""
}

func (s *ScaleDown) Execute(ctx context.Context) error {
	current, err := s.client.GetCurrentReplicas(ctx)
	if err != nil {
		return fmt.Errorf("get replicas: %w", err)
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
