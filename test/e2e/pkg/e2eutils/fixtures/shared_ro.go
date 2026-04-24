package fixtures

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

// SharedRONamespace returns the per-process namespace name used by the
// shared read-only fixture cluster. The name embeds the current RunID
// so concurrent runs (e.g., parallel CI jobs) cannot collide on the
// same namespace and stomp one another during teardown.
func SharedRONamespace() string {
	return fmt.Sprintf("e2e-shared-ro-%s-%s", RunID(), procID())
}

// SharedROName is the DocumentDB CR name used by the shared read-only
// fixture cluster.
const SharedROName = "shared-ro"

// SharedROHandle is a read-only proxy over the shared RO DocumentDB
// cluster. Callers must NOT mutate the underlying CR. The handle only
// exposes accessors; there are no Patch/Delete methods.
type SharedROHandle struct {
	namespace string
	name      string
}

// Namespace returns the namespace of the shared RO cluster.
func (h *SharedROHandle) Namespace() string { return h.namespace }

// Name returns the name of the shared RO cluster.
func (h *SharedROHandle) Name() string { return h.name }

// GetCR fetches a fresh copy of the underlying DocumentDB CR. The
// returned CR is a deep copy; mutating it has no effect on the live
// resource. Callers that try to Update/Patch the returned CR against
// the API server will succeed silently only if they re-use the real
// client — prefer to treat this as read-only.
func (h *SharedROHandle) GetCR(ctx context.Context, c client.Client) (*previewv1.DocumentDB, error) {
	dd := &previewv1.DocumentDB{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: h.namespace, Name: h.name}, dd); err != nil {
		return nil, fmt.Errorf("get shared-ro documentdb: %w", err)
	}
	return dd, nil
}

var (
	sharedRO     *SharedROHandle
	sharedROOnce sync.Once
	sharedROErr  error
)

// GetOrCreateSharedRO returns the session-scoped shared read-only
// DocumentDB fixture, creating it lazily on first call. Subsequent
// calls return the same handle. Errors are cached: a failed first
// attempt will not be retried within the same process.
func GetOrCreateSharedRO(ctx context.Context, c client.Client) (*SharedROHandle, error) {
	sharedROOnce.Do(func() {
		ns := SharedRONamespace()
		if err := ensureNamespace(ctx, c, ns, FixtureSharedRO); err != nil {
			sharedROErr = err
			return
		}
		if err := ensureCredentialSecret(ctx, c, ns, defaultCredentialSecretName, FixtureSharedRO); err != nil {
			sharedROErr = err
			return
		}
		dd, err := renderDocumentDB("base/documentdb.yaml.template", baseVars(ns, SharedROName, "1"))
		if err != nil {
			sharedROErr = err
			return
		}
		if err := createDocumentDB(ctx, c, dd, FixtureSharedRO); err != nil {
			sharedROErr = err
			return
		}
		if err := waitDocumentDBHealthy(ctx, c, ns, SharedROName, defaultFixtureCreateTimeout); err != nil {
			sharedROErr = fmt.Errorf("waiting for shared-ro to become healthy: %w", err)
			return
		}
		sharedRO = &SharedROHandle{namespace: ns, name: SharedROName}
	})
	return sharedRO, sharedROErr
}

// TeardownSharedRO deletes every resource stamped with
// (LabelRunID=RunID(), LabelFixture=FixtureSharedRO). This is
// label-selector-driven so a process that never called
// GetOrCreateSharedRO but observes leftover resources from a previous
// run can still clean up. Safe to call multiple times; callers should
// invoke it from SynchronizedAfterSuite.
func TeardownSharedRO(ctx context.Context, c client.Client) error {
	sharedRO = nil
	return teardownFixtureByLabels(ctx, c, FixtureSharedRO)
}
