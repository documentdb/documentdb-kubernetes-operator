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

// KillPrimaryPod deletes the CNPG primary pod to exercise the automatic
// failover path: CNPG must promote a standby, and the cluster must return to
// steady state within the recovery budget. The continuous workload verifier
// independently catches any data loss caused by the failover.
type KillPrimaryPod struct {
	client    monitor.ClusterClient
	healthMon *monitor.HealthMonitor
	recovery  time.Duration
}

// NewKillPrimaryPod creates a KillPrimaryPod operation.
func NewKillPrimaryPod(client monitor.ClusterClient, health *monitor.HealthMonitor, recovery time.Duration) *KillPrimaryPod {
	return &KillPrimaryPod{
		client:    client,
		healthMon: health,
		recovery:  recovery,
	}
}

func (k *KillPrimaryPod) Name() string { return "kill-primary-pod" }

func (k *KillPrimaryPod) Weight() int { return 2 }

// Precondition requires at least one standby (instancesPerNode>=2). Killing the
// sole instance of a single-instance cluster would cause guaranteed downtime
// with no failover target — a true-but-useless policy violation. The same guard
// (and rationale) is used by UpgradeDocumentDB; skips don't consume the
// scheduler cooldown, so this is free to re-evaluate on the next tick.
func (k *KillPrimaryPod) Precondition(ctx context.Context) (bool, string) {
	ipn, err := k.client.GetInstancesPerNode(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot read instancesPerNode: %v", err)
	}
	if ipn < 2 {
		return false, fmt.Sprintf("instancesPerNode=%d (no HA standby) — killing primary would cause real downtime; skipping", ipn)
	}
	return true, ""
}

func (k *KillPrimaryPod) Execute(ctx context.Context) error {
	primary, err := k.client.GetPrimaryInstance(ctx)
	if err != nil {
		return fmt.Errorf("get primary instance: %w", err)
	}
	if err := k.client.DeletePod(ctx, primary); err != nil {
		return fmt.Errorf("delete primary pod %s: %w", primary, err)
	}

	// Wait for CNPG to elect a new primary and the cluster to settle.
	recoveryCtx, cancel := context.WithTimeout(ctx, k.recovery)
	defer cancel()
	return k.healthMon.WaitForSteadyState(recoveryCtx)
}

// OutagePolicy allows a moderate write-failure budget: failover briefly
// interrupts writes while a standby is promoted, similar to scale-down.
func (k *KillPrimaryPod) OutagePolicy() journal.OutagePolicy {
	return journal.OutagePolicy{
		AllowedWriteFailures: 50,
		MustRecoverWithin:    k.recovery,
	}
}
