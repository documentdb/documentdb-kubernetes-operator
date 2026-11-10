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
	Timestamp    time.Time
	AllPodsReady bool
	ReadyPods    int
	TotalPods    int
	CRReady      bool
	RestartCount int32
}

// ClusterClient is the interface for querying cluster state.
// This allows testing with mocks instead of a real k8s client.
type ClusterClient interface {
	// GetClusterHealth returns the current health of the target cluster.
	GetClusterHealth(ctx context.Context) (ClusterHealth, error)

	// GetCurrentDocumentDBImageTag returns the DocumentDB version in the tag of
	// status.documentDBImage (e.g., "0.109.0" from "ghcr.io/.../documentdb:0.109.0",
	// and "1.0.0" from the combined image "ghcr.io/.../documentdb-combined:18-1.0.0").
	// Returns empty string if status not yet populated.
	GetCurrentDocumentDBImageTag(ctx context.Context) (string, error)

	// GetInstancesPerNode returns spec.instancesPerNode (range 1-3).
	// 1 means single-instance (no HA); >=2 means at least one standby exists.
	GetInstancesPerNode(ctx context.Context) (int, error)

	// ScaleCluster sets the desired spec.instancesPerNode value (CRD range 1-3).
	ScaleCluster(ctx context.Context, instancesPerNode int) error

	// UpgradeDocumentDB patches spec.documentDBVersion and spec.schemaVersion="auto".
	UpgradeDocumentDB(ctx context.Context, version string) error

	// GetPrimaryInstance returns the name of the pod currently serving as the
	// CNPG primary (from Cluster.status.currentPrimary). The pod name equals
	// the CNPG instance name. Returns an error if no primary is known yet.
	GetPrimaryInstance(ctx context.Context) (string, error)

	// DeletePod deletes the named pod in the cluster namespace. Used by chaos
	// operations to inject pod-loss faults.
	DeletePod(ctx context.Context, name string) error
}

// HealthMonitor continuously monitors cluster health and tracks steady-state.
type HealthMonitor struct {
	client          ClusterClient
	journal         *journal.Journal
	steadyStateWait time.Duration

	mu             sync.RWMutex
	lastHealth     ClusterHealth
	steadySince    time.Time // time when cluster became healthy
	healthySamples int
	// invalidateGen is bumped by InvalidateSteadyState. A health sample fetched
	// before an invalidation must not establish steadySince, so check()
	// snapshots this generation before its (unlocked) fetch and discards the
	// sample if the generation moved while the fetch was in flight.
	invalidateGen uint64
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
	// Snapshot the invalidation generation before the (unlocked) fetch so a
	// disruption that opens while GetClusterHealth is in flight is detected
	// below and this pre-disruption sample is not allowed to establish a fresh
	// steady-state epoch.
	h.mu.RLock()
	genAtFetch := h.invalidateGen
	h.mu.RUnlock()

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

	// If an invalidation happened while this sample was being fetched, the
	// reading predates the disruption. Record it as the latest observation but
	// do not let it satisfy the steady-state gate; the next fetch (started
	// after the invalidation) will re-establish steadySince if warranted.
	staleSample := h.invalidateGen != genAtFetch

	if isHealthy && !staleSample {
		if h.steadySince.IsZero() {
			h.steadySince = time.Now()
		}
		h.healthySamples++
	} else {
		if isHealthy && staleSample {
			h.journal.Info("health", "discarding pre-disruption health sample after steady-state invalidation")
		} else if !h.steadySince.IsZero() {
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

// InvalidateSteadyState resets the steady-state epoch so the next successful
// WaitForSteadyState / IsSteadyState must observe a *fresh* continuous-healthy
// interval: at least one health sample taken after this call, then
// steadyStateWait of continuous health. Callers invoke it when they open a
// disruption (e.g. patch the topology or delete a pod) so a stale
// pre-operation steadySince — the monitor polls on its own, slower cadence —
// cannot satisfy the post-operation recovery gate before the monitor has
// actually observed the change.
func (h *HealthMonitor) InvalidateSteadyState() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steadySince = time.Time{}
	h.healthySamples = 0
	h.invalidateGen++
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
