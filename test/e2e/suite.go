package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/namespaces"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/operatorhealth"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/testenv"
)

// suiteEnv holds the process-wide CNPG TestingEnvironment used by every
// spec in the current test binary. It is populated by SetupSuite and
// cleared by TeardownSuite. Each Ginkgo test binary (root + per-area)
// gets its own copy; state is not shared across binaries.
var (
	suiteEnv     *environment.TestingEnvironment
	suiteEnvOnce sync.Once
	suiteEnvErr  error

	// suiteGate is the operator-pod churn sentinel captured at
	// SetupSuite time. It is reused by [CheckOperatorUnchanged]
	// from per-area BeforeEach hooks so a single operator restart
	// during the run aborts every subsequent spec instead of
	// producing confusing downstream failures.
	suiteGate *operatorhealth.Gate
)

// SuiteEnv returns the TestingEnvironment initialized by SetupSuite.
// Specs must invoke this only after SynchronizedBeforeSuite has run on
// the local node; a nil return means setup was skipped or failed.
func SuiteEnv() *environment.TestingEnvironment { return suiteEnv }

// SetupSuite builds the shared TestingEnvironment (idempotent) and runs
// the operator-health gate, failing fast if the operator pod is not
// Ready within timeout. Intended to be called from
// SynchronizedBeforeSuite in the suite_test.go of every test binary.
func SetupSuite(ctx context.Context, operatorReadyTimeout time.Duration) error {
	suiteEnvOnce.Do(func() {
		// Propagate the resolved run id into every package that
		// stamps it onto fixtures. Both fixtures and namespaces
		// must see the same value before any namespace is
		// derived so per-spec names collide deterministically
		// across binaries when E2E_RUN_ID is exported.
		fixtures.SetRunID(RunID())
		namespaces.SetRunIDFunc(RunID)

		env, err := testenv.NewDocumentDBTestingEnvironment(ctx)
		if err != nil {
			suiteEnvErr = fmt.Errorf("building TestingEnvironment: %w", err)
			return
		}
		suiteEnv = env
		if err := gateOperatorReady(ctx, env.Client, testenv.DefaultOperatorNamespace, operatorReadyTimeout); err != nil {
			suiteEnvErr = fmt.Errorf("operator health gate: %w", err)
		}
	})
	return suiteEnvErr
}

// TeardownSuite releases the shared fixtures created during the suite
// run. Safe to call even when SetupSuite failed or was never invoked.
// Errors from individual fixture teardowns are joined so the caller
// sees every problem rather than just the first.
func TeardownSuite(ctx context.Context) error {
	if suiteEnv == nil || suiteEnv.Client == nil {
		return nil
	}
	var errs []error
	if err := fixtures.TeardownSharedRO(ctx, suiteEnv.Client); err != nil && !isNotFound(err) {
		errs = append(errs, fmt.Errorf("teardown shared-ro: %w", err))
	}
	if err := fixtures.TeardownSharedScale(ctx, suiteEnv.Client); err != nil && !isNotFound(err) {
		errs = append(errs, fmt.Errorf("teardown shared-scale: %w", err))
	}
	return errors.Join(errs...)
}

// CheckOperatorUnchanged verifies that the operator pod captured at
// SetupSuite time is still running with the same UID and restart count.
// Returns nil when suiteGate has not been initialized yet (e.g., the
// caller is in the root binary before SynchronizedBeforeSuite), or when
// the operator pod matches the snapshot. Any drift returns a wrapped
// error and flips the package-level churn sentinel so subsequent
// SkipIfChurned calls observe it.
//
// Every per-area suite (except tests/upgrade/, where operator restarts
// are expected) should invoke this from a BeforeEach:
//
//	var _ = BeforeEach(func() {
//	    Expect(e2e.CheckOperatorUnchanged()).To(Succeed())
//	})
func CheckOperatorUnchanged() error {
	if suiteGate == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return suiteGate.Verify(ctx)
}

// gateOperatorReady waits up to timeout for the DocumentDB operator pod
// to reach Ready=True and stores the captured [operatorhealth.Gate] in
// the package-level suiteGate so [CheckOperatorUnchanged] can reuse it.
func gateOperatorReady(ctx context.Context, c client.Client, ns string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	const poll = 2 * time.Second
	var lastReason string
	for {
		pod, err := findOperatorPodForGate(ctx, c, ns)
		switch {
		case err == nil && podReady(pod):
			g, gateErr := operatorhealth.NewGate(ctx, c, ns)
			if gateErr != nil {
				return fmt.Errorf("snapshot operator gate: %w", gateErr)
			}
			suiteGate = g
			return nil
		case err != nil:
			lastReason = err.Error()
		default:
			lastReason = fmt.Sprintf("pod %s/%s not ready yet", ns, pod.Name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("operator pod in %q not ready after %s: %s", ns, timeout, lastReason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// findOperatorPodForGate locates the operator pod via the same label
// selector operatorhealth uses. Kept private to avoid cycling the
// internals of operatorhealth — if that package grows an exported
// finder, switch to it.
func findOperatorPodForGate(ctx context.Context, c client.Client, ns string) (*corev1.Pod, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods,
		client.InNamespace(ns),
		client.MatchingLabels{operatorhealth.PodLabelKey: operatorhealth.PodLabelValue},
	); err != nil {
		return nil, fmt.Errorf("listing operator pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no operator pods with %s=%s in %s",
			operatorhealth.PodLabelKey, operatorhealth.PodLabelValue, ns)
	}
	return &pods.Items[0], nil
}

func podReady(pod *corev1.Pod) bool {
	if pod == nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// isNotFound detects "resource gone" errors returned by fixture
// teardown so the suite does not fail when fixtures were never created
// (e.g., a smoke-only run).
func isNotFound(err error) bool {
	return err != nil && apierrors.IsNotFound(err)
}

// ArtifactsDir returns the directory E2E artifacts (logs, junit reports)
// should be written to. The default layout isolates each ginkgo binary
// run and each parallel process:
//
//	./_artifacts/<RunID>/proc-<N>/
//
// The directory is created lazily on first call. Override the entire
// path via E2E_ARTIFACTS_DIR — the override is taken verbatim (no RunID
// or proc suffix is appended).
func ArtifactsDir() string {
	if v := os.Getenv("E2E_ARTIFACTS_DIR"); v != "" {
		_ = os.MkdirAll(v, 0o755)
		return v
	}
	dir := filepath.Join(".", "_artifacts", RunID(), "proc-"+procIDString())
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// procIDString returns the Ginkgo parallel process id or "1" when
// unset. Kept separate from the fixtures procID helper to avoid a
// circular dependency and because callers in suite.go only need a
// string, not the int form.
func procIDString() string {
	if v := os.Getenv("GINKGO_PARALLEL_PROCESS"); v != "" {
		return v
	}
	return "1"
}
