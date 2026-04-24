package fixtures

import (
	"context"
	"fmt"
	"sync"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

// SharedScaleNamespace returns the per-process namespace name used by
// the shared scale fixture cluster. The name embeds the current RunID
// so concurrent runs cannot collide on the same namespace during
// teardown.
func SharedScaleNamespace() string {
	return fmt.Sprintf("e2e-shared-scale-%s-%s", RunID(), procID())
}

// SharedScaleName is the DocumentDB CR name used by the shared scale
// fixture cluster.
const SharedScaleName = "shared-scale"

// sharedScaleInstances is the baseline InstancesPerNode value the
// scale fixture is created with and reset to between specs.
const sharedScaleInstances = 2

// SharedScaleHandle is the mutable handle to the shared scale
// DocumentDB cluster used by tests/scale/. Unlike SharedROHandle it
// exposes full access to the underlying CR and provides ResetToTwoInstances
// to restore state between specs.
type SharedScaleHandle struct {
	namespace string
	name      string
}

// Namespace returns the namespace of the shared scale cluster.
func (h *SharedScaleHandle) Namespace() string { return h.namespace }

// Name returns the name of the shared scale cluster.
func (h *SharedScaleHandle) Name() string { return h.name }

// GetCR fetches the current state of the underlying DocumentDB CR.
func (h *SharedScaleHandle) GetCR(ctx context.Context, c client.Client) (*previewv1.DocumentDB, error) {
	dd := &previewv1.DocumentDB{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.namespace, Name: h.name}, dd); err != nil {
		return nil, fmt.Errorf("get shared-scale documentdb: %w", err)
	}
	return dd, nil
}

// ResetToTwoInstances restores the shared scale cluster to
// instancesPerNode=sharedScaleInstances (the default 2) and waits for
// both the operator's DocumentDB status to report healthy and the
// underlying CNPG Cluster's readyInstances to equal 2. Call from an
// AfterEach to leave the fixture in a known state for the next spec.
//
// The CNPG convergence wait is essential: the DocumentDB CR status
// can flip to Ready before the PostgreSQL layer has re-added the
// second replica, which would cause the next spec's scale assertions
// to observe a transient single-instance cluster.
func (h *SharedScaleHandle) ResetToTwoInstances(ctx context.Context, c client.Client) error {
	dd := &previewv1.DocumentDB{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.namespace, Name: h.name}, dd); err != nil {
		return fmt.Errorf("get shared-scale for reset: %w", err)
	}
	if dd.Spec.InstancesPerNode != sharedScaleInstances {
		patch := client.MergeFrom(dd.DeepCopy())
		dd.Spec.InstancesPerNode = sharedScaleInstances
		if err := c.Patch(ctx, dd, patch); err != nil {
			return fmt.Errorf("patch shared-scale back to %d instances: %w", sharedScaleInstances, err)
		}
	}
	if err := waitDocumentDBHealthy(ctx, c, h.namespace, h.name, defaultFixtureCreateTimeout); err != nil {
		return err
	}
	return waitCNPGReadyInstances(ctx, c, h.namespace, h.name, sharedScaleInstances, defaultFixtureCreateTimeout)
}

// waitCNPGReadyInstances polls the CNPG Cluster associated with the
// DocumentDB named (ns, name) until its Status.ReadyInstances matches
// want. The CNPG Cluster is assumed to carry the same name as the
// DocumentDB CR (the non-replicated convention used across the
// operator).
func waitCNPGReadyInstances(ctx context.Context, c client.Client, namespace, name string, want int, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, defaultPollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		cl := &cnpgv1.Cluster{}
		err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, cl)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("get CNPG cluster %s/%s: %w", namespace, name, err)
		}
		return cl.Status.ReadyInstances == want, nil
	})
}

var (
	sharedScale     *SharedScaleHandle
	sharedScaleOnce sync.Once
	sharedScaleErr  error
)

// GetOrCreateSharedScale returns the session-scoped shared scale
// DocumentDB fixture, creating it lazily on first call. Subsequent
// calls return the same handle.
func GetOrCreateSharedScale(ctx context.Context, c client.Client) (*SharedScaleHandle, error) {
	sharedScaleOnce.Do(func() {
		ns := SharedScaleNamespace()
		if err := ensureNamespace(ctx, c, ns, FixtureSharedScale); err != nil {
			sharedScaleErr = err
			return
		}
		if err := ensureCredentialSecret(ctx, c, ns, defaultCredentialSecretName, FixtureSharedScale); err != nil {
			sharedScaleErr = err
			return
		}
		dd, err := renderDocumentDB(
			"base/documentdb.yaml.template",
			baseVars(ns, SharedScaleName, fmt.Sprintf("%d", sharedScaleInstances)),
		)
		if err != nil {
			sharedScaleErr = err
			return
		}
		if err := createDocumentDB(ctx, c, dd, FixtureSharedScale); err != nil {
			sharedScaleErr = err
			return
		}
		if err := waitDocumentDBHealthy(ctx, c, ns, SharedScaleName, defaultFixtureCreateTimeout); err != nil {
			sharedScaleErr = fmt.Errorf("waiting for shared-scale to become healthy: %w", err)
			return
		}
		if err := waitCNPGReadyInstances(ctx, c, ns, SharedScaleName, sharedScaleInstances, defaultFixtureCreateTimeout); err != nil {
			sharedScaleErr = fmt.Errorf("waiting for CNPG readyInstances=%d: %w", sharedScaleInstances, err)
			return
		}
		sharedScale = &SharedScaleHandle{namespace: ns, name: SharedScaleName}
	})
	return sharedScale, sharedScaleErr
}

// TeardownSharedScale deletes every resource stamped with
// (LabelRunID=RunID(), LabelFixture=FixtureSharedScale). Safe to call
// multiple times; invoke from SynchronizedAfterSuite.
func TeardownSharedScale(ctx context.Context, c client.Client) error {
	sharedScale = nil
	return teardownFixtureByLabels(ctx, c, FixtureSharedScale)
}
