// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// Manager is the main entry point for telemetry operations.
type Manager struct {
	Client      *TelemetryClient
	Events      *EventTracker
	Metrics     *MetricsTracker
	operatorCtx *OperatorContext
	logger      logr.Logger
	k8sClient   client.Client
	stopCh      chan struct{}

	// metricsBuffer holds collected metrics until flushed (hourly or when cap is reached).
	metricsBuffer []bufferedMetric
	bufferMu      sync.Mutex
	bufferCap     int // max items before auto-flush
}

// bufferedMetric represents a metric waiting to be sent.
type bufferedMetric struct {
	name       string
	value      float64
	properties map[string]interface{}
}

// ManagerConfig contains configuration for the telemetry manager.
type ManagerConfig struct {
	OperatorVersion  string
	HelmChartVersion string
	Logger           logr.Logger
}

// NewManager creates a new telemetry Manager.
func NewManager(ctx context.Context, cfg ManagerConfig, k8sClient client.Client, clientset kubernetes.Interface) (*Manager, error) {
	// Detect operator context
	operatorCtx, err := detectOperatorContext(ctx, cfg, clientset)
	if err != nil {
		cfg.Logger.Error(err, "Failed to detect operator context, using defaults")
		operatorCtx = &OperatorContext{
			OperatorVersion:        cfg.OperatorVersion,
			KubernetesDistribution: DistributionOther,
			CloudProvider:          CloudProviderUnknown,
			StartupTimestamp:       time.Now(),
		}
	}

	// Create telemetry client
	telemetryClient := NewTelemetryClient(operatorCtx, WithLogger(cfg.Logger))

	// Create event and metrics trackers
	eventTracker := NewEventTracker(telemetryClient)
	metricsTracker := NewMetricsTracker(telemetryClient)

	return &Manager{
		Client:      telemetryClient,
		Events:      eventTracker,
		Metrics:     metricsTracker,
		operatorCtx: operatorCtx,
		logger:      cfg.Logger,
		k8sClient:   k8sClient,
		stopCh:      make(chan struct{}),
		bufferCap:   500, // Max buffered metrics before auto-flush; sized for ~4 collections/hour × ~15 metrics each with headroom
	}, nil
}

// Start begins telemetry collection.
func (m *Manager) Start() {
	m.Client.Start()

	// Send operator startup event
	m.Events.TrackOperatorStartup(OperatorStartupEvent{
		OperatorVersion:   m.operatorCtx.OperatorVersion,
		KubernetesVersion: m.operatorCtx.KubernetesVersion,
		CloudProvider:     string(m.operatorCtx.CloudProvider),
		StartupTimestamp:  m.operatorCtx.StartupTimestamp,
		RestartCount:      getRestartCount(),
		HelmChartVersion:  m.operatorCtx.HelmChartVersion,
	})

	m.logger.Info("Telemetry collection started",
		"enabled", m.Client.IsEnabled(),
		"operatorVersion", m.operatorCtx.OperatorVersion,
		"k8sVersion", m.operatorCtx.KubernetesVersion,
	)

	// Start periodic metrics reporting
	if m.Client.IsEnabled() && m.k8sClient != nil {
		go m.runPeriodicMetrics()
	}
}

// Stop gracefully stops telemetry collection.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.Client.Stop()
	m.logger.Info("Telemetry collection stopped")
}

// IsEnabled returns whether telemetry is enabled.
func (m *Manager) IsEnabled() bool {
	return m.Client.IsEnabled()
}

// GetOperatorContext returns the detected operator context.
func (m *Manager) GetOperatorContext() *OperatorContext {
	return m.operatorCtx
}

// runPeriodicMetrics collects metrics every 15 minutes into a buffer,
// and flushes the buffer to Application Insights every hour.
// If the buffer reaches its cap before the hour, it auto-flushes.
func (m *Manager) runPeriodicMetrics() {
	// Initial delay to let controllers start
	initialDelay := 30 * time.Second
	collectTicker := time.NewTicker(15 * time.Minute)
	flushTicker := time.NewTicker(1 * time.Hour)
	defer collectTicker.Stop()
	defer flushTicker.Stop()

	select {
	case <-time.After(initialDelay):
	case <-m.stopCh:
		return
	}

	// Collect immediately after initial delay
	m.collectPeriodicMetrics()
	for {
		select {
		case <-collectTicker.C:
			m.collectPeriodicMetrics()
		case <-flushTicker.C:
			m.flushMetricsBuffer()
		case <-m.stopCh:
			// Flush remaining buffer on shutdown
			m.flushMetricsBuffer()
			return
		}
	}
}

// bufferMetric adds a metric to the buffer. Auto-flushes if cap is reached.
func (m *Manager) bufferMetric(name string, value float64, properties map[string]interface{}) {
	m.bufferMu.Lock()
	m.metricsBuffer = append(m.metricsBuffer, bufferedMetric{
		name:       name,
		value:      value,
		properties: properties,
	})
	shouldFlush := len(m.metricsBuffer) >= m.bufferCap
	m.bufferMu.Unlock()

	if shouldFlush {
		m.flushMetricsBuffer()
	}
}

// flushMetricsBuffer sends all buffered metrics to Application Insights.
func (m *Manager) flushMetricsBuffer() {
	m.bufferMu.Lock()
	if len(m.metricsBuffer) == 0 {
		m.bufferMu.Unlock()
		return
	}
	batch := m.metricsBuffer
	m.metricsBuffer = nil
	m.bufferMu.Unlock()

	m.logger.V(1).Info("Flushing metrics buffer", "count", len(batch))
	for _, metric := range batch {
		m.Client.TrackMetric(metric.name, metric.value, metric.properties)
	}
}

// collectPeriodicMetrics gathers gauge-style metrics and buffers them.
// Metrics are flushed to Application Insights hourly or when buffer cap is reached.
func (m *Manager) collectPeriodicMetrics() {
	ctx := context.Background()

	// Buffer operator health
	m.bufferMetric("operator.health.status", 1, map[string]interface{}{
		"pod_name":       os.Getenv("HOSTNAME"),
		"namespace_hash": m.operatorCtx.OperatorNamespaceHash,
	})

	// List all DocumentDB clusters
	clusterList := &dbpreview.DocumentDBList{}
	if err := m.k8sClient.List(ctx, clusterList); err != nil {
		m.logger.V(1).Info("Failed to list DocumentDB clusters for periodic metrics", "error", err)
		return
	}

	// Buffer active cluster count
	m.bufferMetric("documentdb.clusters.active.count", float64(len(clusterList.Items)), map[string]interface{}{
		"cloud_provider": string(m.operatorCtx.CloudProvider),
	})

	tlsCount := 0
	lbCount := 0
	cipCount := 0

	for i := range clusterList.Items {
		cluster := &clusterList.Items[i]
		clusterID := GetResourceTelemetryID(cluster)

		// Buffer cluster configuration
		m.bufferMetric("documentdb.cluster.configuration", 1, map[string]interface{}{
			"cluster_id":         clusterID,
			"namespace_hash":     HashNamespace(cluster.Namespace),
			"node_count":         cluster.Spec.NodeCount,
			"instances_per_node": cluster.Spec.InstancesPerNode,
			"total_instances":    cluster.Spec.NodeCount * cluster.Spec.InstancesPerNode,
			"pvc_size_category":  string(categorizePVCSize(cluster.Spec.Resource.Storage.PvcSize)),
			"documentdb_version": cluster.Spec.DocumentDBVersion,
		})

		// Buffer replication
		if cluster.Spec.ClusterReplication != nil {
			replicaCount := len(cluster.Spec.ClusterReplication.ClusterList)
			m.bufferMetric("documentdb.cluster.replication.enabled", 1, map[string]interface{}{
				"cluster_id":                      clusterID,
				"cross_cloud_networking_strategy": cluster.Spec.ClusterReplication.CrossCloudNetworkingStrategy,
				"replica_count":                   replicaCount,
				"high_availability":               cluster.Spec.ClusterReplication.HighAvailability,
				"participating_cluster_count":     replicaCount,
			})
		}

		// TLS
		if cluster.Spec.TLS != nil {
			tlsCount++
		}

		// Service exposure
		if cluster.Spec.ExposeViaService.ServiceType == "LoadBalancer" {
			lbCount++
		} else if cluster.Spec.ExposeViaService.ServiceType != "" {
			cipCount++
		}

		// Plugin usage
		if cluster.Spec.SidecarInjectorPluginName != "" {
			m.bufferMetric("documentdb.plugin.usage.count", 1, map[string]interface{}{
				"sidecar_injector_plugin_enabled": true,
				"wal_replica_plugin_enabled":      cluster.Spec.WalReplicaPluginName != "",
			})
		}
	}

	// Buffer TLS count
	if tlsCount > 0 {
		m.bufferMetric("documentdb.tls.enabled.count", float64(tlsCount), nil)
	}

	// Buffer service exposure
	if lbCount > 0 {
		m.bufferMetric("documentdb.service_exposure.count", float64(lbCount), map[string]interface{}{
			"service_type":   "LoadBalancer",
			"cloud_provider": string(m.operatorCtx.CloudProvider),
		})
	}
	if cipCount > 0 {
		m.bufferMetric("documentdb.service_exposure.count", float64(cipCount), map[string]interface{}{
			"service_type":   "ClusterIP",
			"cloud_provider": string(m.operatorCtx.CloudProvider),
		})
	}

	// Buffer backup counts
	backupList := &dbpreview.BackupList{}
	if err := m.k8sClient.List(ctx, backupList); err == nil {
		m.bufferMetric("documentdb.backups.active.count", float64(len(backupList.Items)), nil)
	}

	// Buffer scheduled backup counts
	scheduledBackupList := &dbpreview.ScheduledBackupList{}
	if err := m.k8sClient.List(ctx, scheduledBackupList); err == nil {
		m.bufferMetric("documentdb.scheduled_backups.active.count", float64(len(scheduledBackupList.Items)), nil)
	}
}

// categorizePVCSize categorizes PVC size into small/medium/large.
func categorizePVCSize(size string) PVCSizeCategory {
	if size == "" {
		return "unknown"
	}
	num := 0
	for _, c := range size {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		} else {
			break
		}
	}
	switch {
	case num < 50:
		return PVCSizeSmall
	case num <= 200:
		return PVCSizeMedium
	default:
		return PVCSizeLarge
	}
}

// detectOperatorContext detects the deployment environment.
func detectOperatorContext(ctx context.Context, cfg ManagerConfig, clientset kubernetes.Interface) (*OperatorContext, error) {
	opCtx := &OperatorContext{
		OperatorVersion:  cfg.OperatorVersion,
		HelmChartVersion: cfg.HelmChartVersion,
		StartupTimestamp: time.Now(),
	}

	// Get Kubernetes version
	if clientset != nil {
		discoveryClient := clientset.Discovery()
		if serverVersion, err := discoveryClient.ServerVersion(); err == nil {
			opCtx.KubernetesVersion = serverVersion.GitVersion
			opCtx.KubernetesDistribution = DetectKubernetesDistribution(serverVersion.GitVersion)
		}
	}

	// Detect cloud provider from environment or node labels
	opCtx.CloudProvider = detectCloudProvider(ctx, clientset)

	// Detect Kubernetes cluster ID using Option 3: cloud-native identifiers + kube-system UID fallback
	opCtx.KubernetesClusterID = detectKubernetesClusterID(ctx, clientset, opCtx.CloudProvider)

	// Get operator namespace (hashed)
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		opCtx.OperatorNamespaceHash = HashNamespace(ns)
	}

	// Detect region from node labels if possible
	opCtx.Region = detectRegion(ctx, clientset)

	// Detect installation method
	opCtx.InstallationMethod = detectInstallationMethod()

	return opCtx, nil
}

// detectCloudProvider attempts to detect the cloud provider.
func detectCloudProvider(ctx context.Context, clientset kubernetes.Interface) CloudProvider {
	if clientset == nil {
		return CloudProviderUnknown
	}

	// Try to detect from node labels or provider ID
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		return CloudProviderUnknown
	}

	node := nodes.Items[0]

	// Check provider ID
	providerID := node.Spec.ProviderID
	switch {
	case containsAny(providerID, "azure", "aks"):
		return CloudProviderAKS
	case containsAny(providerID, "aws", "eks"):
		return CloudProviderEKS
	case containsAny(providerID, "gce", "gke"):
		return CloudProviderGKE
	}

	// Check node labels
	labels := node.Labels
	if labels != nil {
		if _, ok := labels["kubernetes.azure.com/cluster"]; ok {
			return CloudProviderAKS
		}
		if _, ok := labels["eks.amazonaws.com/nodegroup"]; ok {
			return CloudProviderEKS
		}
		if _, ok := labels["cloud.google.com/gke-nodepool"]; ok {
			return CloudProviderGKE
		}
	}

	return CloudProviderUnknown
}

// detectKubernetesClusterID generates a deterministic ID for the Kubernetes cluster
// using the Option 3 strategy: cloud-native identifiers when available, kube-system
// namespace UID as universal fallback. All values are hashed for privacy.
func detectKubernetesClusterID(ctx context.Context, clientset kubernetes.Interface, provider CloudProvider) string {
	if clientset == nil {
		return ""
	}

	// Try cloud-specific detection based on already-detected provider
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil && len(nodes.Items) > 0 {
		node := nodes.Items[0]

		switch provider {
		case CloudProviderAKS:
			if id := detectAKSClusterIdentity(node.Labels, node.Spec.ProviderID); id != "" {
				return hashForClusterID("aks", id)
			}
		case CloudProviderEKS:
			if id := detectEKSClusterIdentity(node.Labels); id != "" {
				return hashForClusterID("eks", id)
			}
		case CloudProviderGKE:
			if id := detectGKEClusterIdentity(node.Labels, node.Spec.ProviderID); id != "" {
				return hashForClusterID("gke", id)
			}
		}
	}

	// Universal fallback: kube-system namespace UID
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err == nil {
		return hashForClusterID("k8s", string(ns.UID))
	}

	return ""
}

// detectAKSClusterIdentity extracts AKS cluster identity from node metadata.
func detectAKSClusterIdentity(labels map[string]string, providerID string) string {
	// Check for AKS labels
	if sub, ok := labels["kubernetes.azure.com/subscription"]; ok && sub != "" {
		rg := labels["kubernetes.azure.com/resource-group"]
		return sub + "/" + rg
	}

	// Parse from providerID: azure:///subscriptions/{sub}/resourceGroups/{rg}/...
	if strings.HasPrefix(providerID, "azure://") {
		parts := strings.Split(providerID, "/")
		for i, p := range parts {
			if p == "subscriptions" && i+1 < len(parts) {
				sub := parts[i+1]
				for j := i; j < len(parts); j++ {
					if parts[j] == "resourceGroups" && j+1 < len(parts) {
						return sub + "/" + parts[j+1]
					}
				}
			}
		}
	}
	return ""
}

// detectEKSClusterIdentity extracts EKS cluster identity from node labels.
func detectEKSClusterIdentity(labels map[string]string) string {
	region := labels["topology.kubernetes.io/region"]
	clusterName := labels["alpha.eksctl.io/cluster-name"]
	if region != "" && clusterName != "" {
		return region + "/" + clusterName
	}
	return ""
}

// detectGKEClusterIdentity extracts GKE cluster identity from node metadata.
func detectGKEClusterIdentity(labels map[string]string, providerID string) string {
	// GKE providerID format: gce://project/zone/instance-name
	if strings.HasPrefix(providerID, "gce://") {
		parts := strings.Split(strings.TrimPrefix(providerID, "gce://"), "/")
		if len(parts) >= 1 {
			project := parts[0]
			clusterName := labels["cloud.google.com/gke-nodepool"]
			if clusterName != "" {
				return project + "/" + clusterName
			}
			return project
		}
	}
	return ""
}

// hashForClusterID hashes cloud identity data for privacy.
func hashForClusterID(provider, data string) string {
	input := fmt.Sprintf("documentdb-cluster:%s:%s", provider, data)
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:16])
}

// detectRegion attempts to detect the cloud region.
func detectRegion(ctx context.Context, clientset kubernetes.Interface) string {
	if clientset == nil {
		return ""
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		return ""
	}

	node := nodes.Items[0]
	if labels := node.Labels; labels != nil {
		// Standard Kubernetes topology label
		if region, ok := labels["topology.kubernetes.io/region"]; ok {
			return region
		}
		// Fallback to failure-domain label (deprecated but still used)
		if region, ok := labels["failure-domain.beta.kubernetes.io/region"]; ok {
			return region
		}
	}

	return ""
}

// detectInstallationMethod attempts to detect how the operator was installed.
func detectInstallationMethod() string {
	// Check for Helm-specific annotations/labels
	if os.Getenv("HELM_RELEASE_NAME") != "" {
		return "helm"
	}

	// Check for OLM (Operator Lifecycle Manager)
	if os.Getenv("OPERATOR_CONDITION_NAME") != "" {
		return "operator-sdk"
	}

	return "kubectl"
}

// getRestartCount returns the restart count (simplified implementation).
func getRestartCount() int {
	// In a real implementation, this would track restarts
	// For now, return 0 as this is initial startup
	return 0
}

// containsAny checks if s contains any of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if len(s) > 0 && len(sub) > 0 {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
