// Package helmop provides thin wrappers around the `helm` CLI for the
// DocumentDB E2E upgrade specs. The upgrade area owns its own operator
// install — it installs a previous-released chart, deploys a DocumentDB,
// then upgrades the chart to the PR's build — so these helpers are
// disruptive by design and must only be used from specs running with
// `ginkgo -procs=1`.
//
// The helpers shell out to the `helm` binary on PATH. Required CLI:
// `helm` v3.13+ (Helm 3 with `upgrade --install`, `--wait`, and
// `--version` behavior used here). No in-process Helm SDK dependency is
// pulled in so the test module footprint stays small.
//
// Typical flow from a spec:
//
//	_ = helmop.Uninstall(ctx, "documentdb-operator", "documentdb-operator")
//	Expect(helmop.Install(ctx, "documentdb-operator", "documentdb-operator",
//	    "documentdb/documentdb-operator", "0.1.2", nil)).To(Succeed())
//	Expect(helmop.WaitOperatorReady(ctx, env, "documentdb-operator",
//	    2*time.Minute)).To(Succeed())
//	Expect(helmop.Upgrade(ctx, "documentdb-operator", "documentdb-operator",
//	    "/path/to/pr-chart", "", nil)).To(Succeed())
package helmop

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"

	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/operatorhealth"
)

// DefaultTimeout bounds every helm invocation. Individual callers may
// pass a context with a tighter deadline.
const DefaultTimeout = 10 * time.Minute

// run executes the helm CLI with the supplied args. stdout/stderr are
// merged so error messages from helm are surfaced verbatim.
func run(ctx context.Context, args ...string) error {
	if _, err := exec.LookPath("helm"); err != nil {
		return fmt.Errorf("helm CLI not found on PATH: %w", err)
	}
	cctx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(cctx, "helm", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helm %v: %w\n---helm output---\n%s", args, err, out.String())
	}
	return nil
}

// setFlags renders a values map to deterministic `--set key=value`
// arguments. Sorted by key so command lines are reproducible in logs.
func setFlags(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, values[k]))
	}
	return args
}

// Install installs the chart at the given release name / namespace. The
// namespace is created if absent. version may be empty to use the
// latest chart version reachable from the repo/URL. values is an
// optional `--set key=value` map.
func Install(ctx context.Context, releaseName, namespace, chart, version string, values map[string]string) error {
	if releaseName == "" || namespace == "" || chart == "" {
		return fmt.Errorf("helmop.Install: releaseName, namespace and chart are required")
	}
	args := []string{
		"install", releaseName, chart,
		"--namespace", namespace,
		"--create-namespace",
		"--wait",
	}
	if version != "" {
		args = append(args, "--version", version)
	}
	args = append(args, setFlags(values)...)
	return run(ctx, args...)
}

// Upgrade upgrades an existing release, or installs it if the release
// is absent (helm upgrade --install semantics).
func Upgrade(ctx context.Context, releaseName, namespace, chart, version string, values map[string]string) error {
	if releaseName == "" || namespace == "" || chart == "" {
		return fmt.Errorf("helmop.Upgrade: releaseName, namespace and chart are required")
	}
	args := []string{
		"upgrade", "--install", releaseName, chart,
		"--namespace", namespace,
		"--create-namespace",
		"--wait",
	}
	if version != "" {
		args = append(args, "--version", version)
	}
	args = append(args, setFlags(values)...)
	return run(ctx, args...)
}

// Uninstall removes a release. A missing release is not an error so
// callers can use Uninstall as an idempotent reset.
func Uninstall(ctx context.Context, releaseName, namespace string) error {
	if releaseName == "" || namespace == "" {
		return fmt.Errorf("helmop.Uninstall: releaseName and namespace are required")
	}
	err := run(ctx, "uninstall", releaseName, "--namespace", namespace, "--wait", "--ignore-not-found")
	return err
}

// WaitOperatorReady polls the operator namespace until a pod with the
// operator label is Ready or the timeout expires. It deliberately
// reuses operatorhealth's label selector so callers observe the same
// pod the churn gate watches.
func WaitOperatorReady(ctx context.Context, env *environment.TestingEnvironment, namespace string, timeout time.Duration) error {
	if env == nil || env.Client == nil {
		return fmt.Errorf("helmop.WaitOperatorReady: nil env/client")
	}
	if namespace == "" {
		return fmt.Errorf("helmop.WaitOperatorReady: namespace required")
	}
	deadline := time.Now().Add(timeout)
	const poll = 3 * time.Second
	var lastReason string
	for {
		ready, reason, err := operatorReadyOnce(ctx, env.Client, namespace)
		if err == nil && ready {
			return nil
		}
		if err != nil {
			lastReason = err.Error()
		} else {
			lastReason = reason
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("operator pod in %q not ready after %s: %s", namespace, timeout, lastReason)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func operatorReadyOnce(ctx context.Context, c client.Client, namespace string) (bool, string, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods,
		client.InNamespace(namespace),
		client.MatchingLabels{operatorhealth.PodLabelKey: operatorhealth.PodLabelValue},
	); err != nil {
		return false, "", fmt.Errorf("list operator pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return false, "no operator pods yet", nil
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, cond := range p.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true, "", nil
			}
		}
	}
	return false, fmt.Sprintf("%d operator pod(s) present but none Ready", len(pods.Items)), nil
}
