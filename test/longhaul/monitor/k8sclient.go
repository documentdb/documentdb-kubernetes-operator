// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	shareddb "github.com/documentdb/documentdb-operator/test/shared/documentdb"
	sharedk8s "github.com/documentdb/documentdb-operator/test/shared/k8s"
)

// PodMetrics holds resource usage for a single pod.
type PodMetrics struct {
	Name     string
	MemoryMB float64
	CPUCores float64
}

// K8sClusterClient implements ClusterClient using real Kubernetes API calls.
type K8sClusterClient struct {
	clientset     kubernetes.Interface
	crClient      ctrlclient.Client
	metricsClient metricsv.Interface
	namespace     string
	clusterName   string
	metricsAvail  atomic.Bool
}

// K8sClientConfig holds configuration for creating a K8sClusterClient.
type K8sClientConfig struct {
	Namespace   string
	ClusterName string
	Kubeconfig  string // optional, empty uses in-cluster
}

// NewK8sClusterClient creates a real Kubernetes cluster client.
// It first attempts in-cluster config, then falls back to KUBECONFIG.
func NewK8sClusterClient(cfg K8sClientConfig) (*K8sClusterClient, error) {
	restConfig, err := buildRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build rest config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	scheme, err := shareddb.NewScheme()
	if err != nil {
		return nil, fmt.Errorf("failed to build scheme: %w", err)
	}
	crClient, err := ctrlclient.New(restConfig, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	// Try to create metrics client (graceful fallback).
	metricsClient, metricsOK := tryMetricsClient(restConfig)

	client := &K8sClusterClient{
		clientset:     clientset,
		crClient:      crClient,
		metricsClient: metricsClient,
		namespace:     cfg.Namespace,
		clusterName:   cfg.ClusterName,
	}
	client.metricsAvail.Store(metricsOK)

	return client, nil
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	// Try in-cluster first.
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig.
	if kubeconfig == "" {
		kubeconfig = clientcmd.RecommendedHomeFile
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func tryMetricsClient(config *rest.Config) (metricsv.Interface, bool) {
	mc, err := metricsv.NewForConfig(config)
	if err != nil {
		log.Printf("[k8sclient] metrics client creation failed (leak detection disabled): %v", err)
		return nil, false
	}
	return mc, true
}

// Clientset returns the underlying typed Kubernetes clientset. Exposed so the
// rest of the driver (e.g. the report ConfigMap writer) can reuse it instead
// of building a second clientset against the same REST config.
func (k *K8sClusterClient) Clientset() kubernetes.Interface { return k.clientset }

// getCR fetches the target DocumentDB CR via the shared typed helper. Wraps
// the namespaced-name lookup that's otherwise repeated in every method below.
func (k *K8sClusterClient) getCR(ctx context.Context) (*previewv1.DocumentDB, error) {
	return shareddb.Get(ctx, k.crClient, types.NamespacedName{Namespace: k.namespace, Name: k.clusterName})
}

// GetClusterHealth queries pod status and CR status to determine cluster health.
func (k *K8sClusterClient) GetClusterHealth(ctx context.Context) (ClusterHealth, error) {
	health := ClusterHealth{Timestamp: time.Now()}

	// List pods with the CNPG cluster label.
	labelSelector := fmt.Sprintf("%s=%s", sharedk8s.ClusterLabel, k.clusterName)
	pods, err := k.clientset.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return health, fmt.Errorf("failed to list pods: %w", err)
	}

	health.TotalPods = len(pods.Items)
	var totalRestarts int32
	readyCount := 0

	for i := range pods.Items {
		pod := &pods.Items[i]
		if sharedk8s.IsPodReady(pod) {
			readyCount++
		}
		totalRestarts += sharedk8s.TotalRestarts(pod)
	}

	health.ReadyPods = readyCount
	health.AllPodsReady = readyCount == health.TotalPods && health.TotalPods > 0
	health.RestartCount = totalRestarts

	// Get the DocumentDB CR status via the shared typed helper. Using
	// shareddb.IsHealthy keeps the readiness predicate consistent with
	// the e2e suite (single source of truth for ReadyStatus).
	dd, err := k.getCR(ctx)
	if err != nil {
		return health, fmt.Errorf("failed to get DocumentDB CR: %w", err)
	}
	health.CRReady = shareddb.IsHealthy(dd)

	return health, nil
}

// GetInstancesPerNode reads spec.instancesPerNode from the DocumentDB CR.
// Range is 1-3 per the CRD; 1 means no HA, >=2 means at least one standby.
//
// The previous unstructured-based implementation returned 1 when the
// field was omitted from the CR (operator default). The typed
// previewv1.DocumentDB gives a zero-value of 0 for omitted ints, so we
// preserve the original semantics explicitly here.
func (k *K8sClusterClient) GetInstancesPerNode(ctx context.Context) (int, error) {
	dd, err := k.getCR(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get DocumentDB CR: %w", err)
	}
	if dd.Spec.InstancesPerNode == 0 {
		return 1, nil
	}
	return dd.Spec.InstancesPerNode, nil
}

// ScaleCluster patches spec.instancesPerNode on the DocumentDB CR.
//
// Note: spec.nodeCount is hard-capped at 1 by the CRD (minimum=maximum=1),
// so the only scale dimension exposed today is instancesPerNode (range 1-3).
// Each instance is a CNPG replica (1 primary + N-1 standbys); growing this
// dimension is what gives the cluster HA.
func (k *K8sClusterClient) ScaleCluster(ctx context.Context, instancesPerNode int) error {
	if err := shareddb.PatchInstances(ctx, k.crClient, k.namespace, k.clusterName, instancesPerNode); err != nil {
		return fmt.Errorf("failed to patch DocumentDB CR: %w", err)
	}
	return nil
}

// GetCurrentDocumentDBImageTag reads status.documentDBImage from the CR
// and returns the tag portion (after the last colon).
func (k *K8sClusterClient) GetCurrentDocumentDBImageTag(ctx context.Context) (string, error) {
	dd, err := k.getCR(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get DocumentDB CR: %w", err)
	}

	image := dd.Status.DocumentDBImage
	if image == "" {
		return "", nil
	}
	idx := strings.LastIndex(image, ":")
	if idx < 0 || idx == len(image)-1 {
		return "", nil
	}
	return image[idx+1:], nil
}

// UpgradeDocumentDB patches the DocumentDB CR to set documentDBVersion
// and schemaVersion="auto" so the operator performs a rolling upgrade.
// NOTE: the CRD field is documentDBVersion (capital DB), not documentDbVersion.
func (k *K8sClusterClient) UpgradeDocumentDB(ctx context.Context, version string) error {
	dd, err := k.getCR(ctx)
	if err != nil {
		return fmt.Errorf("failed to get DocumentDB CR: %w", err)
	}
	if err := shareddb.PatchSpec(ctx, k.crClient, dd, func(spec *previewv1.DocumentDBSpec) {
		spec.DocumentDBVersion = version
		spec.SchemaVersion = "auto"
	}); err != nil {
		return fmt.Errorf("failed to patch DocumentDB CR: %w", err)
	}
	return nil
}

// GetPodMetrics queries metrics-server for pod resource usage.
// Returns nil, nil if metrics-server is not available.
func (k *K8sClusterClient) GetPodMetrics(ctx context.Context) ([]PodMetrics, error) {
	if !k.metricsAvail.Load() || k.metricsClient == nil {
		return nil, nil
	}

	labelSelector := fmt.Sprintf("%s=%s", sharedk8s.ClusterLabel, k.clusterName)
	podMetricsList, err := k.metricsClient.MetricsV1beta1().PodMetricses(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		// Metrics API might have become unavailable.
		log.Printf("[k8sclient] metrics query failed (disabling): %v", err)
		k.metricsAvail.Store(false)
		return nil, nil
	}

	var result []PodMetrics
	for _, pm := range podMetricsList.Items {
		var totalMemBytes int64
		var totalCPUMillis int64
		for _, c := range pm.Containers {
			totalMemBytes += c.Usage.Memory().Value()
			totalCPUMillis += c.Usage.Cpu().MilliValue()
		}
		result = append(result, PodMetrics{
			Name:     pm.Name,
			MemoryMB: float64(totalMemBytes) / (1024 * 1024),
			CPUCores: float64(totalCPUMillis) / 1000.0,
		})
	}

	return result, nil
}

// MetricsAvailable returns whether metrics-server is usable.
func (k *K8sClusterClient) MetricsAvailable() bool {
	return k.metricsAvail.Load()
}

// scheduledBackupLabel is the label the ScheduledBackup reconciler stamps
// on every child Backup CR it creates (see
// preview.ScheduledBackup.CreateBackup). Selecting on it isolates the
// backups produced by our canary ScheduledBackup from any ad-hoc Backup
// objects that might exist in the namespace.
const scheduledBackupLabel = "scheduledbackup"

// EnsureScheduledBackup creates the ScheduledBackup CR for the target
// cluster if it does not already exist. It is idempotent: an existing
// ScheduledBackup with the same name is left untouched (a restart of the
// driver must not reset the accumulated backup history).
func (k *K8sClusterClient) EnsureScheduledBackup(ctx context.Context, name, schedule string, retentionDays int) error {
	existing := &previewv1.ScheduledBackup{}
	err := k.crClient.Get(ctx, types.NamespacedName{Namespace: k.namespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ScheduledBackup %s/%s: %w", k.namespace, name, err)
	}

	rd := retentionDays
	sb := &previewv1.ScheduledBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: k.namespace,
		},
		Spec: previewv1.ScheduledBackupSpec{
			Cluster:       cnpgv1.LocalObjectReference{Name: k.clusterName},
			Schedule:      schedule,
			RetentionDays: &rd,
		},
	}
	if err := k.crClient.Create(ctx, sb); err != nil {
		return fmt.Errorf("failed to create ScheduledBackup %s/%s: %w", k.namespace, name, err)
	}
	return nil
}

// GetScheduledBackup fetches the ScheduledBackup CR by name in the target
// namespace.
func (k *K8sClusterClient) GetScheduledBackup(ctx context.Context, name string) (*previewv1.ScheduledBackup, error) {
	sb := &previewv1.ScheduledBackup{}
	if err := k.crClient.Get(ctx, types.NamespacedName{Namespace: k.namespace, Name: name}, sb); err != nil {
		return nil, fmt.Errorf("failed to get ScheduledBackup %s/%s: %w", k.namespace, name, err)
	}
	return sb, nil
}

// ListScheduledChildBackups returns every Backup CR that the named
// ScheduledBackup produced, identified by the "scheduledbackup" label.
func (k *K8sClusterClient) ListScheduledChildBackups(ctx context.Context, scheduledBackupName string) ([]previewv1.Backup, error) {
	var list previewv1.BackupList
	if err := k.crClient.List(ctx, &list,
		ctrlclient.InNamespace(k.namespace),
		ctrlclient.MatchingLabels{scheduledBackupLabel: scheduledBackupName},
	); err != nil {
		return nil, fmt.Errorf("failed to list child Backups for ScheduledBackup %s: %w", scheduledBackupName, err)
	}
	return list.Items, nil
}
