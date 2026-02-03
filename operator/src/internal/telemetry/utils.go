// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// HashNamespace creates a SHA-256 hash of a namespace name for privacy.
func HashNamespace(namespace string) string {
	hash := sha256.Sum256([]byte(namespace))
	return hex.EncodeToString(hash[:])
}

// CategorizePVCSize categorizes a PVC size string into small/medium/large.
func CategorizePVCSize(pvcSize string) PVCSizeCategory {
	if pvcSize == "" {
		return PVCSizeSmall
	}

	quantity, err := resource.ParseQuantity(pvcSize)
	if err != nil {
		return PVCSizeSmall
	}

	// Convert to Gi for comparison
	sizeGi := quantity.Value() / (1024 * 1024 * 1024)

	switch {
	case sizeGi < 50:
		return PVCSizeSmall
	case sizeGi <= 200:
		return PVCSizeMedium
	default:
		return PVCSizeLarge
	}
}

// CategorizeScheduleFrequency categorizes a cron expression into frequency categories.
func CategorizeScheduleFrequency(cronExpr string) ScheduleFrequency {
	if cronExpr == "" {
		return ScheduleFrequencyCustom
	}

	parts := strings.Fields(cronExpr)
	if len(parts) < 5 {
		return ScheduleFrequencyCustom
	}

	// Simple heuristics for common patterns
	minute, hour, dayOfMonth, _, dayOfWeek := parts[0], parts[1], parts[2], parts[3], parts[4]

	// Hourly: runs every hour (e.g., "0 * * * *")
	if minute != "*" && hour == "*" && dayOfMonth == "*" && dayOfWeek == "*" {
		return ScheduleFrequencyHourly
	}

	// Daily: runs once per day (e.g., "0 2 * * *")
	if minute != "*" && hour != "*" && dayOfMonth == "*" && dayOfWeek == "*" {
		return ScheduleFrequencyDaily
	}

	// Weekly: runs once per week (e.g., "0 2 * * 0")
	if minute != "*" && hour != "*" && dayOfMonth == "*" && dayOfWeek != "*" {
		return ScheduleFrequencyWeekly
	}

	return ScheduleFrequencyCustom
}

// CategorizeCSIDriver categorizes a CSI driver name.
func CategorizeCSIDriver(driverName string) string {
	switch {
	case strings.Contains(driverName, "azure") || strings.Contains(driverName, "disk.csi.azure.com"):
		return "azure-disk"
	case strings.Contains(driverName, "aws") || strings.Contains(driverName, "ebs.csi.aws.com"):
		return "aws-ebs"
	case strings.Contains(driverName, "gce") || strings.Contains(driverName, "pd.csi.storage.gke.io"):
		return "gce-pd"
	default:
		return "other"
	}
}

// MapCloudProviderToString converts CloudProvider to string.
func MapCloudProviderToString(env string) string {
	switch strings.ToLower(env) {
	case "aks":
		return "aks"
	case "eks":
		return "eks"
	case "gke":
		return "gke"
	default:
		return "unknown"
	}
}

// DetectKubernetesDistribution detects the Kubernetes distribution from version info.
func DetectKubernetesDistribution(versionInfo string) KubernetesDistribution {
	versionLower := strings.ToLower(versionInfo)

	switch {
	case strings.Contains(versionLower, "eks"):
		return DistributionEKS
	case strings.Contains(versionLower, "aks") || strings.Contains(versionLower, "azure"):
		return DistributionAKS
	case strings.Contains(versionLower, "gke"):
		return DistributionGKE
	case strings.Contains(versionLower, "openshift"):
		return DistributionOpenShift
	case strings.Contains(versionLower, "rancher") || strings.Contains(versionLower, "rke"):
		return DistributionRancher
	case strings.Contains(versionLower, "tanzu") || strings.Contains(versionLower, "vmware"):
		return DistributionVMwareTanzu
	default:
		return DistributionOther
	}
}
