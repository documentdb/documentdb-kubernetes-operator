// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// OperatorDeploymentName is the fixed name of the operator Deployment. The
// operator is a cluster singleton, so this name is stable across installs.
const OperatorDeploymentName = "documentdb-operator"

// KillOperatorPod deletes the running operator pod to verify that an operator
// restart does not disrupt the data plane. The CNPG-managed database keeps
// serving reads and writes while the Deployment reschedules the control plane,
// so the workload verifier should observe (near) zero write failures. Recovery
// is asserted by the Deployment returning to Available.
type KillOperatorPod struct {
	clientset  kubernetes.Interface
	namespace  string
	deployment string
	recovery   time.Duration
}

// NewKillOperatorPod creates a KillOperatorPod operation targeting the operator
// Deployment in the given namespace.
func NewKillOperatorPod(clientset kubernetes.Interface, namespace string, recovery time.Duration) *KillOperatorPod {
	return &KillOperatorPod{
		clientset:  clientset,
		namespace:  namespace,
		deployment: OperatorDeploymentName,
		recovery:   recovery,
	}
}

func (k *KillOperatorPod) Name() string { return "kill-operator-pod" }

func (k *KillOperatorPod) Weight() int { return 2 }

// Precondition requires the operator Deployment to exist and currently be
// Available, so the fault isn't stacked on an already-restarting operator.
func (k *KillOperatorPod) Precondition(ctx context.Context) (bool, string) {
	dep, err := k.getDeployment(ctx)
	if err != nil {
		return false, fmt.Sprintf("cannot get operator deployment: %v", err)
	}
	if !isDeploymentAvailable(dep) {
		return false, "operator deployment not currently available"
	}
	return true, ""
}

func (k *KillOperatorPod) Execute(ctx context.Context) error {
	dep, err := k.getDeployment(ctx)
	if err != nil {
		return fmt.Errorf("get operator deployment: %w", err)
	}

	// Resolve the pod set from the Deployment's own selector so we don't
	// depend on the release-name-derived "app" label value.
	selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels).String()
	pods, err := k.clientset.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return fmt.Errorf("list operator pods: %w", err)
	}

	target := oldestRunningPod(pods.Items)
	if target == "" {
		return fmt.Errorf("no running operator pod found for selector %q", selector)
	}
	if err := k.clientset.CoreV1().Pods(k.namespace).Delete(ctx, target, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete operator pod %s: %w", target, err)
	}

	// Wait for the Deployment to reschedule and become Available again.
	recoveryCtx, cancel := context.WithTimeout(ctx, k.recovery)
	defer cancel()
	return k.waitForDeploymentAvailable(recoveryCtx)
}

// OutagePolicy tolerates only a small number of write failures: an operator
// restart must not take down the data plane. A non-zero budget absorbs
// coincidental blips (e.g. a scrape or client reconnect) without flagging a
// false policy violation.
func (k *KillOperatorPod) OutagePolicy() journal.OutagePolicy {
	return journal.OutagePolicy{
		AllowedWriteFailures: 5,
		MustRecoverWithin:    k.recovery,
	}
}

func (k *KillOperatorPod) getDeployment(ctx context.Context) (*appsv1.Deployment, error) {
	return k.clientset.AppsV1().Deployments(k.namespace).Get(ctx, k.deployment, metav1.GetOptions{})
}

func (k *KillOperatorPod) waitForDeploymentAvailable(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if dep, err := k.getDeployment(ctx); err == nil && isDeploymentAvailable(dep) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for operator deployment to become available: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// isDeploymentAvailable reports whether the Deployment has its full desired
// replica count ready with none unavailable and the observed generation caught
// up to the latest spec.
func isDeploymentAvailable(dep *appsv1.Deployment) bool {
	if dep == nil {
		return false
	}
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	if dep.Status.ObservedGeneration < dep.Generation {
		return false
	}
	return dep.Status.ReadyReplicas >= desired && dep.Status.UnavailableReplicas == 0
}

// oldestRunningPod returns the name of the oldest pod in the Running phase, or
// "" if none are running. Targeting the oldest makes the choice deterministic.
func oldestRunningPod(pods []corev1.Pod) string {
	name := ""
	var oldest time.Time
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase != corev1.PodRunning || p.DeletionTimestamp != nil {
			continue
		}
		ts := p.CreationTimestamp.Time
		if name == "" || ts.Before(oldest) {
			name = p.Name
			oldest = ts
		}
	}
	return name
}
