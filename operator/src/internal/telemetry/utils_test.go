// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package telemetry

import (
	"testing"
)

func TestCategorizeScheduleFrequency(t *testing.T) {
	tests := []struct {
		input    string
		expected ScheduleFrequency
	}{
		{"0 * * * *", ScheduleFrequencyHourly},
		{"30 * * * *", ScheduleFrequencyHourly},
		{"0 2 * * *", ScheduleFrequencyDaily},
		{"0 0 * * *", ScheduleFrequencyDaily},
		{"0 2 * * 0", ScheduleFrequencyWeekly},
		{"0 3 * * 1", ScheduleFrequencyWeekly},
		{"*/5 * * * *", ScheduleFrequencyCustom},
		{"0 */6 * * *", ScheduleFrequencyCustom},
		{"invalid", ScheduleFrequencyCustom},
		{"", ScheduleFrequencyCustom},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := CategorizeScheduleFrequency(tc.input)
			if result != tc.expected {
				t.Errorf("CategorizeScheduleFrequency(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestCategorizeCSIDriver(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"disk.csi.azure.com", "azure-disk"},
		{"ebs.csi.aws.com", "aws-ebs"},
		{"pd.csi.storage.gke.io", "gce-pd"},
		{"something-else", "other"},
		{"", "other"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := CategorizeCSIDriver(tc.input)
			if result != tc.expected {
				t.Errorf("CategorizeCSIDriver(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestMapCloudProviderToString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"aks", "aks"},
		{"eks", "eks"},
		{"gke", "gke"},
		{"", "unknown"},
		{"other", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := MapCloudProviderToString(tc.input)
			if result != tc.expected {
				t.Errorf("MapCloudProviderToString(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestDetectKubernetesDistribution(t *testing.T) {
	tests := []struct {
		input    string
		expected KubernetesDistribution
	}{
		{"v1.30.0", DistributionOther},
		{"v1.30.0-gke.1", DistributionGKE},
		{"v1.30.0-eks-abc", DistributionEKS},
		{"v1.30.0+rke2r1", DistributionRancher},
		{"", DistributionOther},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := DetectKubernetesDistribution(tc.input)
			if result != tc.expected {
				t.Errorf("DetectKubernetesDistribution(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestCategorizePVCSize_Exported(t *testing.T) {
	tests := []struct {
		input    string
		expected PVCSizeCategory
	}{
		{"10Gi", PVCSizeSmall},
		{"50Gi", PVCSizeMedium},
		{"500Gi", PVCSizeLarge},
	}
	for _, tc := range tests {
		result := CategorizePVCSize(tc.input)
		if result != tc.expected {
			t.Errorf("CategorizePVCSize(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}
