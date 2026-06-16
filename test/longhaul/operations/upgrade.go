// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// VersionConfigMapName is the ConfigMap maintained by the monitor workflow
// that publishes the desired DocumentDB version.
const VersionConfigMapName = "longhaul-versions"

// VersionConfigMapKey is the key inside the ConfigMap that holds the
// desired DocumentDB image tag (e.g., "0.110.0").
const VersionConfigMapKey = "desired-documentdb-version"

// UpgradeDocumentDB performs an in-place version upgrade of the DocumentDB
// cluster, then waits for the rolling restart to complete and the cluster
// to return to steady state. Continuous verifiers will detect any data
// corruption introduced by the upgrade.
type UpgradeDocumentDB struct {
	client    monitor.ClusterClient
	clientset kubernetes.Interface
	healthMon *monitor.HealthMonitor
	j         *journal.Journal
	namespace string
	recovery  time.Duration
}

// NewUpgradeDocumentDB creates an UpgradeDocumentDB operation.
func NewUpgradeDocumentDB(
	client monitor.ClusterClient,
	clientset kubernetes.Interface,
	health *monitor.HealthMonitor,
	j *journal.Journal,
	namespace string,
	recovery time.Duration,
) *UpgradeDocumentDB {
	return &UpgradeDocumentDB{
		client:    client,
		clientset: clientset,
		healthMon: health,
		j:         j,
		namespace: namespace,
		recovery:  recovery,
	}
}

func (u *UpgradeDocumentDB) Name() string { return "upgrade-documentdb" }

// Weight is intentionally low — upgrades are infrequent in practice and
// should not crowd out scale/failover operations.
func (u *UpgradeDocumentDB) Weight() int { return 1 }

// Precondition is satisfied when the desired version published by the
// workflow differs from the running version observed in CR status,
// AND the cluster has at least one standby (instancesPerNode>=2) so the
// rolling upgrade has an HA failover target.
//
// Skipping when instancesPerNode<2 is intentional: a single-instance cluster
// has no standby to absorb writes, so a rolling upgrade WILL produce real
// downtime. Reporting that as a policy violation is a true positive but not
// useful — there is nothing the operator can do about it. The next scheduler
// tick (10s later) re-evaluates this precondition, so as soon as the cluster
// is scaled up to instancesPerNode>=2 the upgrade becomes eligible again.
// Note: the global cooldown is NOT consumed by a skipped operation —
// see scheduler.tryExecute(), lastOpTime is only updated after successful
// executeOp(). So this guard is "free" from a scheduling perspective.
func (u *UpgradeDocumentDB) Precondition(ctx context.Context) (bool, string) {
	desired, err := u.readDesiredVersion(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot read desired version: %v", err)
	}
	if desired == "" {
		return false, "no desired version published"
	}

	running, err := u.client.GetCurrentDocumentDBImageTag(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot read running version: %v", err)
	}
	if running == desired {
		return false, fmt.Sprintf("already at desired version %s", desired)
	}

	ipn, err := u.client.GetInstancesPerNode(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot read instancesPerNode: %v", err)
	}
	if ipn < 2 {
		return false, fmt.Sprintf("instancesPerNode=%d (no HA standby) — upgrade would cause real downtime; skipping", ipn)
	}
	return true, ""
}

func (u *UpgradeDocumentDB) Execute(ctx context.Context) error {
	desired, err := u.readDesiredVersion(ctx)
	if err != nil {
		return fmt.Errorf("read desired version: %w", err)
	}
	if desired == "" {
		return fmt.Errorf("desired version is empty")
	}

	running, _ := u.client.GetCurrentDocumentDBImageTag(ctx)
	if err := u.client.UpgradeDocumentDB(ctx, desired); err != nil {
		return fmt.Errorf("patch CR: %w", err)
	}

	// Poll status.documentDBImage until it reflects the desired version.
	pollCtx, cancel := context.WithTimeout(ctx, u.recovery)
	defer cancel()
	if err := u.waitForImage(pollCtx, desired, running); err != nil {
		return err
	}

	// Wait for the cluster to settle after the rolling restart.
	steadyCtx, cancel2 := context.WithTimeout(ctx, u.recovery)
	defer cancel2()
	return u.healthMon.WaitForSteadyState(steadyCtx)
}

func (u *UpgradeDocumentDB) waitForImage(ctx context.Context, desired, previous string) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for status.documentDBImage to become %s (was %s): %w", desired, previous, ctx.Err())
		case <-ticker.C:
			tag, err := u.client.GetCurrentDocumentDBImageTag(ctx)
			if err != nil {
				// Transient read errors during a rolling upgrade are
				// expected (apiserver throttling, brief CR webhook
				// blips, etc.) — retry on the next tick rather than
				// fail the whole operation. But surface them to the
				// journal so a sustained read outage is visible in
				// the report instead of being silently dropped.
				u.j.Warn("upgrade", fmt.Sprintf("status read error (will retry): %v", err))
				continue
			}
			if tag == desired {
				return nil
			}
		}
	}
}

func (u *UpgradeDocumentDB) readDesiredVersion(ctx context.Context) (string, error) {
	cm, err := u.clientset.CoreV1().ConfigMaps(u.namespace).Get(ctx, VersionConfigMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return cm.Data[VersionConfigMapKey], nil
}

// OutagePolicy allows for a longer disruption window during an upgrade
// because rolling restarts touch every pod sequentially.
func (u *UpgradeDocumentDB) OutagePolicy() journal.OutagePolicy {
	return journal.OutagePolicy{
		AllowedDowntime:      120 * time.Second,
		AllowedWriteFailures: 200,
		MustRecoverWithin:    u.recovery,
	}
}
