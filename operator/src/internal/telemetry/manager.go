// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"os"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Manager is the main entry point for telemetry operations.
type Manager struct {
	Client         *TelemetryClient
	Events         *EventTracker
	Metrics        *MetricsTracker
	GUIDs          *GUIDManager
	operatorCtx    *OperatorContext
	logger         logr.Logger
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

	// Create GUID manager
	guidManager := NewGUIDManager(k8sClient)

	// Create event and metrics trackers
	eventTracker := NewEventTracker(telemetryClient, guidManager)
	metricsTracker := NewMetricsTracker(telemetryClient)

	return &Manager{
		Client:      telemetryClient,
		Events:      eventTracker,
		Metrics:     metricsTracker,
		GUIDs:       guidManager,
		operatorCtx: operatorCtx,
		logger:      cfg.Logger,
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
}

// Stop gracefully stops telemetry collection.
func (m *Manager) Stop() {
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
