// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"strings"
	"testing"
)

func TestReplicationContext_IsPrimary(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		expected bool
	}{
		{
			name: "NoReplication state returns true",
			context: ReplicationContext{
				state: NoReplication,
			},
			expected: true,
		},
		{
			name: "Primary state returns true",
			context: ReplicationContext{
				state: Primary,
			},
			expected: true,
		},
		{
			name: "Replica state returns false",
			context: ReplicationContext{
				state: Replica,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.IsPrimary()
			if result != tt.expected {
				t.Errorf("IsPrimary() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestReplicationContext_IsReplicating(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		expected bool
	}{
		{
			name: "NoReplication state returns false",
			context: ReplicationContext{
				state: NoReplication,
			},
			expected: false,
		},
		{
			name: "Primary state returns true",
			context: ReplicationContext{
				state: Primary,
			},
			expected: true,
		},
		{
			name: "Replica state returns true",
			context: ReplicationContext{
				state: Replica,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.IsReplicating()
			if result != tt.expected {
				t.Errorf("IsReplicating() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestReplicationContext_GetReplicationSource(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		expected string
	}{
		{
			name: "Replica state returns primary cluster",
			context: ReplicationContext{
				state:                  Replica,
				PrimaryCNPGClusterName: "primary-cluster",
				OtherCNPGClusterNames:  []string{"other-cluster-1", "other-cluster-2"},
			},
			expected: "primary-cluster",
		},
		{
			name: "Primary state returns first other cluster",
			context: ReplicationContext{
				state:                  Primary,
				PrimaryCNPGClusterName: "primary-cluster",
				OtherCNPGClusterNames:  []string{"other-cluster-1", "other-cluster-2"},
			},
			expected: "other-cluster-1",
		},
		{
			name: "Replica state with empty OtherCNPGClusterNames still returns primary cluster",
			context: ReplicationContext{
				state:                  Replica,
				PrimaryCNPGClusterName: "primary-cluster",
				OtherCNPGClusterNames:  []string{},
			},
			expected: "primary-cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.GetReplicationSource()
			if result != tt.expected {
				t.Errorf("GetReplicationSource() = %q, expected %q", result, tt.expected)
			}
		})
	}

	// Document panic behavior for edge cases where OtherCNPGClusterNames is empty.
	// These test cases verify that GetReplicationSource panics when called
	// inappropriately, documenting the precondition that OtherCNPGClusterNames must be
	// non-empty when state is Primary or NoReplication.
	t.Run("Primary state with empty OtherCNPGClusterNames panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when Primary state has empty OtherCNPGClusterNames slice, but no panic occurred")
			}
		}()
		ctx := ReplicationContext{
			state:                 Primary,
			OtherCNPGClusterNames: []string{},
		}
		_ = ctx.GetReplicationSource()
	})

	t.Run("NoReplication state with empty OtherCNPGClusterNames panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when NoReplication state has empty OtherCNPGClusterNames slice, but no panic occurred")
			}
		}()
		ctx := ReplicationContext{
			state:                 NoReplication,
			OtherCNPGClusterNames: []string{},
		}
		_ = ctx.GetReplicationSource()
	})
}

func TestReplicationContext_EndpointEnabled(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		expected bool
	}{
		{
			name: "NoReplication state always returns true",
			context: ReplicationContext{
				state: NoReplication,
			},
			expected: true,
		},
		{
			name: "Primary state with matching primaries returns true",
			context: ReplicationContext{
				state:               Primary,
				currentLocalPrimary: "pod-1",
				targetLocalPrimary:  "pod-1",
			},
			expected: true,
		},
		{
			name: "Primary state with non-matching primaries returns false",
			context: ReplicationContext{
				state:               Primary,
				currentLocalPrimary: "pod-1",
				targetLocalPrimary:  "pod-2",
			},
			expected: false,
		},
		{
			name: "Replica state with matching primaries returns true",
			context: ReplicationContext{
				state:               Replica,
				currentLocalPrimary: "pod-1",
				targetLocalPrimary:  "pod-1",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.EndpointEnabled()
			if result != tt.expected {
				t.Errorf("EndpointEnabled() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestReplicationContext_IsAzureFleetNetworking(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		expected bool
	}{
		{
			name: "AzureFleet strategy returns true",
			context: ReplicationContext{
				CrossCloudNetworkingStrategy: AzureFleet,
			},
			expected: true,
		},
		{
			name: "Istio strategy returns false",
			context: ReplicationContext{
				CrossCloudNetworkingStrategy: Istio,
			},
			expected: false,
		},
		{
			name: "None strategy returns false",
			context: ReplicationContext{
				CrossCloudNetworkingStrategy: None,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.IsAzureFleetNetworking()
			if result != tt.expected {
				t.Errorf("IsAzureFleetNetworking() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestReplicationContext_IsIstioNetworking(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		expected bool
	}{
		{
			name: "Istio strategy returns true",
			context: ReplicationContext{
				CrossCloudNetworkingStrategy: Istio,
			},
			expected: true,
		},
		{
			name: "AzureFleet strategy returns false",
			context: ReplicationContext{
				CrossCloudNetworkingStrategy: AzureFleet,
			},
			expected: false,
		},
		{
			name: "None strategy returns false",
			context: ReplicationContext{
				CrossCloudNetworkingStrategy: None,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.IsIstioNetworking()
			if result != tt.expected {
				t.Errorf("IsIstioNetworking() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestReplicationContext_String(t *testing.T) {
	tests := []struct {
		name     string
		context  ReplicationContext
		contains []string
	}{
		{
			name: "NoReplication state",
			context: ReplicationContext{
				CNPGClusterName:        "self-cluster",
				state:                  NoReplication,
				OtherCNPGClusterNames:  []string{"other-1"},
				PrimaryCNPGClusterName: "primary",
				currentLocalPrimary:    "local-1",
				targetLocalPrimary:     "target-1",
			},
			contains: []string{"self-cluster", "NoReplication", "other-1"},
		},
		{
			name: "Primary state",
			context: ReplicationContext{
				CNPGClusterName:       "primary-self",
				state:                 Primary,
				OtherCNPGClusterNames: []string{"replica-1", "replica-2"},
			},
			contains: []string{"primary-self", "Primary", "replica-1"},
		},
		{
			name: "Replica state",
			context: ReplicationContext{
				CNPGClusterName:        "replica-self",
				state:                  Replica,
				PrimaryCNPGClusterName: "the-primary",
			},
			contains: []string{"replica-self", "Replica"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.context.String()
			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("String() = %q, expected to contain %q", result, substr)
				}
			}
		})
	}
}

func TestReplicationContext_GenerateExternalClusterServices(t *testing.T) {
	tests := []struct {
		name          string
		context       ReplicationContext
		docdbName     string
		namespace     string
		fleetEnabled  bool
		expectedCount int
	}{
		{
			name: "generates services for others without fleet",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{"cluster-a", "cluster-b"},
			},
			docdbName:     "mydb",
			namespace:     "default",
			fleetEnabled:  false,
			expectedCount: 2,
		},
		{
			name: "generates services for others with fleet enabled",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{"cluster-a", "cluster-b"},
				CNPGClusterName:       "self-cluster",
			},
			docdbName:     "mydb",
			namespace:     "production",
			fleetEnabled:  true,
			expectedCount: 2,
		},
		{
			name: "empty others list",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{},
			},
			docdbName:     "mydb",
			namespace:     "default",
			fleetEnabled:  false,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := 0
			for clusterName, serviceName := range tt.context.GenerateExternalClusterServices(tt.docdbName, tt.namespace, tt.fleetEnabled) {
				count++
				if clusterName == "" {
					t.Error("Cluster name should not be empty")
				}
				if serviceName == "" {
					t.Error("Service name should not be empty")
				}
			}

			if count != tt.expectedCount {
				t.Errorf("Generated %d services, expected %d", count, tt.expectedCount)
			}
		})
	}
}

func TestReplicationContext_GenerateIncomingServiceNames(t *testing.T) {
	tests := []struct {
		name          string
		context       ReplicationContext
		docdbName     string
		resourceGroup string
		expectedCount int
	}{
		{
			name: "generates incoming service names",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{"cluster-a", "cluster-b"},
				CNPGClusterName:       "self-cluster",
			},
			docdbName:     "mydb",
			resourceGroup: "rg1",
			expectedCount: 2,
		},
		{
			name: "empty others list",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{},
				CNPGClusterName:       "self-cluster",
			},
			docdbName:     "mydb",
			resourceGroup: "rg1",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := 0
			for serviceName := range tt.context.GenerateIncomingServiceNames(tt.docdbName, tt.resourceGroup) {
				count++
				if serviceName == "" {
					t.Error("Service name should not be empty")
				}
			}

			if count != tt.expectedCount {
				t.Errorf("Generated %d service names, expected %d", count, tt.expectedCount)
			}
		})
	}
}

func TestReplicationContext_GenerateOutgoingServiceNames(t *testing.T) {
	tests := []struct {
		name          string
		context       ReplicationContext
		docdbName     string
		resourceGroup string
		expectedCount int
	}{
		{
			name: "generates outgoing service names",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{"cluster-a", "cluster-b"},
				CNPGClusterName:       "self-cluster",
			},
			docdbName:     "mydb",
			resourceGroup: "rg1",
			expectedCount: 2,
		},
		{
			name: "empty others list",
			context: ReplicationContext{
				OtherCNPGClusterNames: []string{},
				CNPGClusterName:       "self-cluster",
			},
			docdbName:     "mydb",
			resourceGroup: "rg1",
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count := 0
			for serviceName := range tt.context.GenerateOutgoingServiceNames(tt.docdbName, tt.resourceGroup) {
				count++
				if serviceName == "" {
					t.Error("Service name should not be empty")
				}
			}

			if count != tt.expectedCount {
				t.Errorf("Generated %d service names, expected %d", count, tt.expectedCount)
			}
		})
	}
}

func TestGenerateCNPGClusterName(t *testing.T) {
	tests := []struct {
		name        string
		docdbName   string
		clusterName string
		maxLength   int
	}{
		{
			name:        "short names",
			docdbName:   "mydb",
			clusterName: "cluster-1",
			maxLength:   CNPG_MAX_CLUSTER_NAME_LENGTH,
		},
		{
			name:        "long documentdb name",
			docdbName:   "this-is-a-very-long-documentdb-name-that-exceeds-normal-limits",
			clusterName: "cluster-1",
			maxLength:   CNPG_MAX_CLUSTER_NAME_LENGTH,
		},
		{
			name:        "long cluster name",
			docdbName:   "mydb",
			clusterName: "this-is-a-very-long-cluster-name-that-might-cause-issues",
			maxLength:   CNPG_MAX_CLUSTER_NAME_LENGTH,
		},
		{
			name:        "both names long",
			docdbName:   "long-documentdb-name-here",
			clusterName: "long-cluster-name-here-too",
			maxLength:   CNPG_MAX_CLUSTER_NAME_LENGTH,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateCNPGClusterName(tt.docdbName, tt.clusterName)

			if len(result) > tt.maxLength {
				t.Errorf("Generated name %q exceeds max length %d (got %d)", result, tt.maxLength, len(result))
			}

			if result == "" {
				t.Error("Generated name should not be empty")
			}
		})
	}

	// Test consistency - same inputs produce same outputs
	t.Run("consistency check", func(t *testing.T) {
		result1 := generateCNPGClusterName("test-db", "test-cluster")
		result2 := generateCNPGClusterName("test-db", "test-cluster")

		if result1 != result2 {
			t.Errorf("Inconsistent results: %q vs %q", result1, result2)
		}
	})

	// Test uniqueness - different inputs produce different outputs
	t.Run("uniqueness check", func(t *testing.T) {
		result1 := generateCNPGClusterName("db1", "cluster1")
		result2 := generateCNPGClusterName("db1", "cluster2")

		if result1 == result2 {
			t.Errorf("Expected different results for different clusters: %q vs %q", result1, result2)
		}
	})
}
