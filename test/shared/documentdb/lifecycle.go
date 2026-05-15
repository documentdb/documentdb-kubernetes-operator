// Package documentdb provides framework-agnostic CRUD and lifecycle
// helpers for the DocumentDB preview CR. It is consumed by both the
// e2e suite (which wraps these in gomega.Eventually) and the long-haul
// driver (a plain Go binary). Manifest rendering / template assembly
// stays in test/e2e/pkg/e2eutils/documentdb because it depends on the
// e2e-only embedded manifests tree.
package documentdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

const (
	// DefaultWaitPoll is the polling interval used by WaitHealthy and
	// Delete. Callers that need finer control can implement their own
	// polling loop using Get + isHealthy.
	DefaultWaitPoll = 2 * time.Second

	// ReadyStatus is the DocumentDBStatus.Status value the operator
	// surfaces once the underlying CNPG cluster is healthy. It mirrors
	// the CNPG Cluster status verbatim (see
	// operator/src/api/preview/documentdb_types.go). Single source of
	// truth for both e2e and long-haul callers.
	ReadyStatus = "Cluster in healthy state"
)

// Get fetches the DocumentDB identified by key.
func Get(ctx context.Context, c client.Client, key client.ObjectKey) (*previewv1.DocumentDB, error) {
	if c == nil {
		return nil, errors.New("Get: client must not be nil")
	}
	dd := &previewv1.DocumentDB{}
	if err := c.Get(ctx, key, dd); err != nil {
		return nil, err
	}
	return dd, nil
}

// PatchInstances loads the DocumentDB ns/name and patches its
// Spec.InstancesPerNode to want. Returns an error if the CR cannot be
// fetched, the desired value is out of the supported range (1..3 per
// the CRD), or the patch fails. When the CR already has the desired
// value the call is a no-op and returns nil.
func PatchInstances(ctx context.Context, c client.Client, ns, name string, want int) error {
	if c == nil {
		return errors.New("PatchInstances: client must not be nil")
	}
	if want < 1 || want > 3 {
		return fmt.Errorf("PatchInstances: want=%d out of supported range 1..3", want)
	}
	dd := &previewv1.DocumentDB{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, dd); err != nil {
		return fmt.Errorf("get DocumentDB %s/%s: %w", ns, name, err)
	}
	if dd.Spec.InstancesPerNode == want {
		return nil
	}
	before := dd.DeepCopy()
	dd.Spec.InstancesPerNode = want
	if err := c.Patch(ctx, dd, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("patch DocumentDB %s/%s instances=%d: %w", ns, name, want, err)
	}
	return nil
}

// PatchSpec applies a merge-from patch that mutates the provided
// DocumentDB's spec in place. mutate receives a pointer to the Spec
// and may set any fields; the diff against the pre-mutation object is
// sent to the API server.
func PatchSpec(ctx context.Context, c client.Client, dd *previewv1.DocumentDB, mutate func(*previewv1.DocumentDBSpec)) error {
	if dd == nil || mutate == nil {
		return errors.New("PatchSpec: dd and mutate must not be nil")
	}
	before := dd.DeepCopy()
	mutate(&dd.Spec)
	if err := c.Patch(ctx, dd, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("patching DocumentDB %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	return nil
}

// WaitHealthy polls until the DocumentDB named by key reports a
// healthy status or the timeout elapses. "Healthy" is defined as
// Status.Status == ReadyStatus.
//
// The polling interval is DefaultWaitPoll; the function returns nil on
// first healthy observation or an error describing the last observed
// state on timeout.
func WaitHealthy(ctx context.Context, c client.Client, key client.ObjectKey, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last previewv1.DocumentDB
	for {
		if err := c.Get(ctx, key, &last); err == nil {
			if IsHealthy(&last) {
				return nil
			}
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting DocumentDB %s: %w", key, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for DocumentDB %s to be healthy (last status=%q)",
				timeout, key, last.Status.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultWaitPoll):
		}
	}
}

// IsHealthy implements the predicate documented on WaitHealthy.
// Exported so callers (e.g. long-haul HealthMonitor) can reuse the
// same definition without duplicating the readiness string.
func IsHealthy(dd *previewv1.DocumentDB) bool {
	if dd == nil {
		return false
	}
	return dd.Status.Status == ReadyStatus
}

// Delete issues a foreground delete on the given DocumentDB and polls
// until the object is gone or timeout elapses.
func Delete(ctx context.Context, c client.Client, dd *previewv1.DocumentDB, timeout time.Duration) error {
	if dd == nil {
		return errors.New("Delete: dd must not be nil")
	}
	if err := c.Delete(ctx, dd); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting DocumentDB %s/%s: %w", dd.Namespace, dd.Name, err)
	}
	key := client.ObjectKeyFromObject(dd)
	deadline := time.Now().Add(timeout)
	for {
		var got previewv1.DocumentDB
		err := c.Get(ctx, key, &got)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("polling deletion of %s: %w", key, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for DocumentDB %s to be deleted", timeout, key)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultWaitPoll):
		}
	}
}

// List returns all DocumentDB objects in the given namespace.
func List(ctx context.Context, c client.Client, ns string) ([]previewv1.DocumentDB, error) {
	if c == nil {
		return nil, errors.New("List: client must not be nil")
	}
	var ddl previewv1.DocumentDBList
	if err := c.List(ctx, &ddl, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing DocumentDB in %s: %w", ns, err)
	}
	return ddl.Items, nil
}
