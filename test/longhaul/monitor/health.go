// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package monitor provides health monitoring and resource leak detection
// for the target DocumentDB cluster during long haul tests.
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
)

const (
	// healthCheckInterval is how often the health monitor polls cluster state.
	healthCheckInterval = 5 * time.Second
)

// ClusterHealth represents the observed health of the cluster at a point in time.
type ClusterHealth struct {
	Timestamp     time.Time
	AllPodsReady  bool
	ReadyPods     int
	TotalPods     int
	CRReady       bool
	RestartCount  int32
	WritesHealthy bool
}

// ClusterClient is the interface for querying cluster state.
// This allows testing with mocks instead of a real k8s client.
type ClusterClient interface {
	// GetClusterHealth returns the current health of the target cluster.
	GetClusterHealth(ctx context.Context) (ClusterHealth, error)

	// GetCurrentReplicas returns the current number of replicas.
	GetCurrentReplicas(ctx context.Context) (int, error)

	// ScaleCluster sets the desired replica count.
	ScaleCluster(ctx context.Context, replicas int) error
}

// HealthMonitor continuously monitors cluster health and tracks steady-state.
type HealthMonitor struct {
	client          ClusterClient
	journal         *journal.Journal
	steadyStateWait time.Duration

	mu              sync.RWMutex
	lastHealth      ClusterHealth
	steadySince     time.Time // time when cluster became healthy
	healthySamples  int
}

// NewHealthMonitor creates a monitor that polls the cluster for health status.
func NewHealthMonitor(client ClusterClient, j *journal.Journal, steadyStateWait time.Duration) *HealthMonitor {
	return &HealthMonitor{
		client:          client,
		journal:         j,
		steadyStateWait: steadyStateWait,
	}
}

// Run starts the health monitoring loop. Blocks until context is cancelled.
func (h *HealthMonitor) Run(ctx context.Context) {
	h.journal.Info("health", "health monitor started")
	defer h.journal.Info("health", "health monitor stopped")

	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.check(ctx)
		}
	}
}

func (h *HealthMonitor) check(ctx context.Context) {
	health, err := h.client.GetClusterHealth(ctx)
	if err != nil {
		h.journal.Warn("health", fmt.Sprintf("health check failed: %v", err))
		h.mu.Lock()
		h.steadySince = time.Time{}
		h.mu.Unlock()
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	prev := h.lastHealth
	h.lastHealth = health

	isHealthy := health.AllPodsReady && health.CRReady

	if isHealthy {
		if h.steadySince.IsZero() {
			h.steadySince = time.Now()
		}
		h.healthySamples++
	} else {
		if !h.steadySince.IsZero() {
			h.journal.Warn("health", fmt.Sprintf(
				"cluster lost steady state: pods=%d/%d cr_ready=%v",
				health.ReadyPods, health.TotalPods, health.CRReady))
		}
		h.steadySince = time.Time{}
		h.healthySamples = 0
	}

	// Log transitions.
	if prev.AllPodsReady && !health.AllPodsReady {
		h.journal.Warn("health", fmt.Sprintf("pods degraded: %d/%d ready",
			health.ReadyPods, health.TotalPods))
	} else if !prev.AllPodsReady && health.AllPodsReady {
		h.journal.Info("health", "all pods ready")
	}
}

// IsSteadyState returns true if the cluster has been continuously healthy
// for at least the configured steady-state duration.
func (h *HealthMonitor) IsSteadyState() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.steadySince.IsZero() {
		return false
	}
	return time.Since(h.steadySince) >= h.steadyStateWait
}

// LastHealth returns the most recent health observation.
func (h *HealthMonitor) LastHealth() ClusterHealth {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastHealth
}

// WaitForSteadyState blocks until the cluster reaches steady state or context expires.
func (h *HealthMonitor) WaitForSteadyState(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for steady state: %w", ctx.Err())
		case <-ticker.C:
			if h.IsSteadyState() {
				return nil
			}
		}
	}
}
