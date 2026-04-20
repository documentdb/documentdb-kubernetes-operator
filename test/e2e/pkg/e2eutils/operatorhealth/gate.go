// Package operatorhealth exposes a "churn gate" for the DocumentDB E2E
// suite: a lightweight equivalent of CNPG's tests/utils/operator
// PodRestarted / PodRenamed semantics, plus a sentinel that lets
// non-disruptive specs skip themselves after a prior spec has bounced
// the operator.
//
// Typical use from a suite-level BeforeEach/AfterEach:
//
//	var gate *operatorhealth.Gate
//
//	BeforeSuite(func() {
//	    var err error
//	    gate, err = operatorhealth.NewGate(ctx, env.Client, operatorhealth.DefaultNamespace)
//	    Expect(err).NotTo(HaveOccurred())
//	})
//
//	BeforeEach(operatorhealth.BeforeEachHook(gate))
//	AfterEach(operatorhealth.AfterEachHook(gate))
//
// Disruptive specs that intentionally bounce the operator should mark
// the sentinel themselves via MarkChurned() so the AfterEach hook can
// keep its idempotent semantics.
package operatorhealth

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // Ginkgo DSL is intentional.

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultNamespace is where the Helm chart installs the DocumentDB
// operator.
const DefaultNamespace = "documentdb-operator"

// PodLabelSelector is the label the operator Deployment stamps on its
// Pod spec (verified from a live kind cluster: `app=documentdb-operator`).
// If the chart changes the selector, update this constant.
const (
	PodLabelKey   = "app"
	PodLabelValue = "documentdb-operator"
)

// operatorChurned is a process-wide sentinel that records whether the
// operator pod has been observed to restart/rename. Once set, it stays
// set for the remainder of the process (the gate is advisory, not a
// correctness gate).
var operatorChurned atomic.Bool

// Gate snapshots the identity and restart count of the operator pod so
// later Check calls can decide whether the operator churned underneath
// us.
type Gate struct {
	c               client.Client
	ns              string
	initialUID      types.UID
	initialRestarts int32
	initialPodName  string
}

// NewGate discovers the current operator pod in ns and captures its
// identity. If no pod is found the caller can decide whether that's a
// fatal condition (typical for non-disruptive suites) or tolerable.
func NewGate(ctx context.Context, c client.Client, ns string) (*Gate, error) {
	if c == nil {
		return nil, errors.New("NewGate: client must not be nil")
	}
	if ns == "" {
		ns = DefaultNamespace
	}
	pod, err := findOperatorPod(ctx, c, ns)
	if err != nil {
		return nil, err
	}
	return &Gate{
		c:               c,
		ns:              ns,
		initialUID:      pod.UID,
		initialPodName:  pod.Name,
		initialRestarts: totalRestarts(pod),
	}, nil
}

// Check re-reads the operator pod and reports whether it is still the
// same instance with the same restart count. A drift in UID, name, or
// restart count returns healthy=false with a short reason suitable for
// logging.
func (g *Gate) Check(ctx context.Context) (healthy bool, reason string, err error) {
	if g == nil {
		return false, "gate is nil", errors.New("Check: gate is nil")
	}
	pod, err := findOperatorPod(ctx, g.c, g.ns)
	if err != nil {
		return false, err.Error(), err
	}
	switch {
	case pod.UID != g.initialUID:
		return false, fmt.Sprintf("operator pod UID changed: %s -> %s", g.initialUID, pod.UID), nil
	case pod.Name != g.initialPodName:
		return false, fmt.Sprintf("operator pod renamed: %s -> %s", g.initialPodName, pod.Name), nil
	case totalRestarts(pod) != g.initialRestarts:
		return false, fmt.Sprintf("operator pod restart count changed: %d -> %d",
			g.initialRestarts, totalRestarts(pod)), nil
	}
	return true, "", nil
}

// Verify is a convenience wrapper over [Gate.Check] returning nil when
// the operator pod matches the snapshot captured by [NewGate] and an
// error (wrapping the observed reason) otherwise. It also flips the
// process-wide churn sentinel so subsequent calls to [SkipIfChurned]
// observe the drift.
//
// Typical use from an area's BeforeEach:
//
//	BeforeEach(func() { Expect(gate.Verify(ctx)).To(Succeed()) })
func (g *Gate) Verify(ctx context.Context) error {
	if g == nil {
		return errors.New("Verify: gate is nil")
	}
	healthy, reason, err := g.Check(ctx)
	if err != nil {
		MarkChurned()
		return fmt.Errorf("operator health check failed: %w", err)
	}
	if !healthy {
		MarkChurned()
		return fmt.Errorf("operator churn detected: %s", reason)
	}
	return nil
}

// MarkChurned sets the process-wide sentinel, causing SkipIfChurned to
// skip subsequent non-disruptive specs. Disruptive specs that know they
// bounced the operator should call this in their AfterEach.
func MarkChurned() { operatorChurned.Store(true) }

// HasChurned reports the current sentinel state.
func HasChurned() bool { return operatorChurned.Load() }

// SkipIfChurned calls Ginkgo's Skip if a prior spec (or an explicit
// MarkChurned call) has observed operator churn. Intended for use from
// BeforeEach of non-disruptive area suites.
func SkipIfChurned() {
	if HasChurned() {
		Skip("operator churned in a previous spec; skipping non-disruptive spec")
	}
}

// BeforeEachHook returns a Ginkgo BeforeEach body that calls
// SkipIfChurned. If gate is nil the hook still honors the sentinel so
// disruptive specs can flip it without a live Gate.
func BeforeEachHook(gate *Gate) func() {
	_ = gate // reserved: future versions may refresh gate snapshot here
	return func() { SkipIfChurned() }
}

// AfterEachHook returns a Ginkgo AfterEach body that re-checks the
// operator pod and flips the sentinel if churn is detected. A nil gate
// disables the check.
func AfterEachHook(gate *Gate) func() {
	return func() {
		if gate == nil {
			return
		}
		healthy, reason, err := gate.Check(context.Background())
		if err != nil || !healthy {
			if reason == "" && err != nil {
				reason = err.Error()
			}
			GinkgoWriter.Printf("operatorhealth: marking churned: %s\n", reason)
			MarkChurned()
		}
	}
}

// findOperatorPod looks up the first operator pod matching
// PodLabelKey=PodLabelValue in ns. Returns a NotFound error if none
// exist.
func findOperatorPod(ctx context.Context, c client.Client, ns string) (*corev1.Pod, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods,
		client.InNamespace(ns),
		client.MatchingLabels{PodLabelKey: PodLabelValue},
	); err != nil {
		return nil, fmt.Errorf("listing operator pods in %q: %w", ns, err)
	}
	if len(pods.Items) == 0 {
		return nil, apierrors.NewNotFound(corev1.Resource("pods"),
			fmt.Sprintf("%s=%s in %s", PodLabelKey, PodLabelValue, ns))
	}
	return &pods.Items[0], nil
}

// totalRestarts sums RestartCount across all container statuses on pod.
// Matches CNPG's PodRestarted semantics.
func totalRestarts(pod *corev1.Pod) int32 {
	if pod == nil {
		return 0
	}
	var total int32
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
}
