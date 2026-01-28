// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import (
	"testing"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGenerateServiceName(t *testing.T) {
	tests := []struct {
		name           string
		docdbName      string
		sourceCluster  string
		targetCluster  string
		resourceGroup  string
		expectedLength int
		description    string
	}{
		{
			name:           "short resource group",
			docdbName:      "mydb",
			sourceCluster:  "us-east",
			targetCluster:  "us-west",
			resourceGroup:  "rg1",
			expectedLength: 20, // hash string length (8 hex chars from 32-bit hash)
			description:    "Short resource group should return full hash string",
		},
		{
			name:           "empty resource group",
			docdbName:      "testdb",
			sourceCluster:  "eastus",
			targetCluster:  "westus",
			resourceGroup:  "",
			expectedLength: 23, // full hash length
			description:    "Empty resource group should return full hash string",
		},
		{
			name:           "long resource group name requiring truncation",
			docdbName:      "database",
			sourceCluster:  "eastus",
			targetCluster:  "westus",
			resourceGroup:  "very-long-resource-group-name-that-exceeds-normal-limits",
			expectedLength: 6, // 63 - 56 - 1 = 6
			description:    "Long resource group names will cause hash truncation",
		},
		{
			name:           "resource group at boundary",
			docdbName:      "db",
			sourceCluster:  "source",
			targetCluster:  "target",
			resourceGroup:  "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghij",
			expectedLength: 0, // 63 - 62 - 1 = 0
			description:    "Resource group at 62 chars leaves no space for hash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateServiceName(tt.docdbName, tt.sourceCluster, tt.targetCluster, tt.resourceGroup)

			// Verify the result matches expected length
			if len(result) != tt.expectedLength {
				t.Errorf("generateServiceName(%q, %q, %q, %q) returned length %d; expected %d\nDescription: %s\nResult: %q",
					tt.docdbName, tt.sourceCluster, tt.targetCluster, tt.resourceGroup, len(result), tt.expectedLength, tt.description, result)
			}

			// Verify result + resourceGroup doesn't exceed 63 chars (with hyphen)
			totalLength := len(result) + len(tt.resourceGroup)
			if len(tt.resourceGroup) > 0 {
				totalLength++ // account for hyphen
			}
			if totalLength > 63 {
				t.Errorf("generateServiceName(%q, %q, %q, %q) would exceed 63 chars when combined with resource group: result=%q (len=%d), resourceGroup=%q (len=%d), total=%d",
					tt.docdbName, tt.sourceCluster, tt.targetCluster, tt.resourceGroup, result, len(result), tt.resourceGroup, len(tt.resourceGroup), totalLength)
			}
		})

		// Test consistency - same inputs should produce same output
		t.Run(tt.name+" consistency check", func(t *testing.T) {
			result1 := generateServiceName(tt.docdbName, tt.sourceCluster, tt.targetCluster, tt.resourceGroup)
			result2 := generateServiceName(tt.docdbName, tt.sourceCluster, tt.targetCluster, tt.resourceGroup)

			if result1 != result2 {
				t.Errorf("generateServiceName produced inconsistent results: %q vs %q", result1, result2)
			}
		})
	}
}

func TestGenerateConnectionString(t *testing.T) {
	tests := []struct {
		name           string
		documentdb     *dbpreview.DocumentDB
		serviceIp      string
		trustTLS       bool
		expectedPrefix string
		expectedSuffix string
		description    string
	}{
		{
			name: "default secret with untrusted TLS",
			documentdb: &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-db",
					Namespace: "test-namespace",
				},
				Spec: dbpreview.DocumentDBSpec{
					DocumentDbCredentialSecret: "",
				},
			},
			serviceIp:      "192.168.1.100",
			trustTLS:       false,
			expectedPrefix: "mongodb://$(kubectl get secret documentdb-credentials -n test-namespace -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret documentdb-credentials -n test-namespace -o jsonpath='{.data.password}' | base64 -d)@192.168.1.100:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true",
			expectedSuffix: "&tlsAllowInvalidCertificates=true&replicaSet=rs0",
			description:    "When no secret is specified, should use default secret and include tlsAllowInvalidCertificates",
		},
		{
			name: "custom secret with trusted TLS",
			documentdb: &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-db",
					Namespace: "test-namespace",
				},
				Spec: dbpreview.DocumentDBSpec{
					DocumentDbCredentialSecret: "custom-secret",
				},
			},
			serviceIp:      "10.0.0.50",
			trustTLS:       true,
			expectedPrefix: "mongodb://$(kubectl get secret custom-secret -n test-namespace -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret custom-secret -n test-namespace -o jsonpath='{.data.password}' | base64 -d)@10.0.0.50:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true",
			expectedSuffix: "&replicaSet=rs0",
			description:    "When trustTLS is true, should not include tlsAllowInvalidCertificates",
		},
		{
			name: "hostname instead of IP with untrusted TLS",
			documentdb: &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prod-db",
					Namespace: "production",
				},
				Spec: dbpreview.DocumentDBSpec{
					DocumentDbCredentialSecret: "prod-credentials",
				},
			},
			serviceIp:      "documentdb.example.com",
			trustTLS:       false,
			expectedPrefix: "mongodb://$(kubectl get secret prod-credentials -n production -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret prod-credentials -n production -o jsonpath='{.data.password}' | base64 -d)@documentdb.example.com:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true",
			expectedSuffix: "&tlsAllowInvalidCertificates=true&replicaSet=rs0",
			description:    "Should work with hostname/FQDN instead of IP address",
		},
		{
			name: "IPv6 address with trusted TLS",
			documentdb: &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ipv6-db",
					Namespace: "default",
				},
				Spec: dbpreview.DocumentDBSpec{
					DocumentDbCredentialSecret: "ipv6-secret",
				},
			},
			serviceIp:      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			trustTLS:       true,
			expectedPrefix: "mongodb://$(kubectl get secret ipv6-secret -n default -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret ipv6-secret -n default -o jsonpath='{.data.password}' | base64 -d)@2001:0db8:85a3:0000:0000:8a2e:0370:7334:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true",
			expectedSuffix: "&replicaSet=rs0",
			description:    "Should support IPv6 addresses",
		},
		{
			name: "different namespace with custom secret",
			documentdb: &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cross-ns-db",
					Namespace: "app-namespace",
				},
				Spec: dbpreview.DocumentDBSpec{
					DocumentDbCredentialSecret: "app-secret",
				},
			},
			serviceIp:      "172.16.0.10",
			trustTLS:       false,
			expectedPrefix: "mongodb://$(kubectl get secret app-secret -n app-namespace -o jsonpath='{.data.username}' | base64 -d):$(kubectl get secret app-secret -n app-namespace -o jsonpath='{.data.password}' | base64 -d)@172.16.0.10:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true",
			expectedSuffix: "&tlsAllowInvalidCertificates=true&replicaSet=rs0",
			description:    "Should correctly use the DocumentDB instance's namespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateConnectionString(tt.documentdb, tt.serviceIp, tt.trustTLS)

			// Verify the connection string starts with expected prefix
			expectedFull := tt.expectedPrefix + tt.expectedSuffix
			if result != expectedFull {
				t.Errorf("GenerateConnectionString() = %q; expected %q\nDescription: %s",
					result, expectedFull, tt.description)
			}

			// Verify essential components are present
			if result == "" {
				t.Error("GenerateConnectionString() returned empty string")
			}

			// Verify it contains mongodb://
			if len(result) < 10 || result[:10] != "mongodb://" {
				t.Errorf("Connection string should start with 'mongodb://', got: %q", result[:10])
			}

			// Verify TLS parameter is present
			if !contains(result, "tls=true") {
				t.Error("Connection string should contain 'tls=true'")
			}

			// Verify SCRAM-SHA-256 auth mechanism
			if !contains(result, "authMechanism=SCRAM-SHA-256") {
				t.Error("Connection string should contain 'authMechanism=SCRAM-SHA-256'")
			}

			// Verify replicaSet parameter
			if !contains(result, "replicaSet=rs0") {
				t.Error("Connection string should contain 'replicaSet=rs0'")
			}

			// Verify tlsAllowInvalidCertificates based on trustTLS
			if tt.trustTLS {
				if contains(result, "tlsAllowInvalidCertificates") {
					t.Error("Connection string should NOT contain 'tlsAllowInvalidCertificates' when trustTLS is true")
				}
			} else {
				if !contains(result, "tlsAllowInvalidCertificates=true") {
					t.Error("Connection string should contain 'tlsAllowInvalidCertificates=true' when trustTLS is false")
				}
			}

			// Verify service IP is in the connection string
			if !contains(result, tt.serviceIp) {
				t.Errorf("Connection string should contain service IP/hostname %q", tt.serviceIp)
			}

			// Verify namespace is used correctly
			if !contains(result, tt.documentdb.Namespace) {
				t.Errorf("Connection string should contain namespace %q", tt.documentdb.Namespace)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGetDocumentDBServiceDefinition_CNPGLabels(t *testing.T) {
	tests := []struct {
		name             string
		documentDBName   string
		endpointEnabled  bool
		serviceType      corev1.ServiceType
		expectedSelector map[string]string
		description      string
	}{
		{
			name:            "endpoint disabled - should have disabled selector",
			documentDBName:  "test-documentdb",
			endpointEnabled: false,
			serviceType:     corev1.ServiceTypeLoadBalancer,
			expectedSelector: map[string]string{
				"disabled": "true",
			},
			description: "When endpoint is disabled, service should have disabled selector",
		},
		{
			name:            "endpoint enabled with LoadBalancer - should use CNPG labels",
			documentDBName:  "test-documentdb",
			endpointEnabled: true,
			serviceType:     corev1.ServiceTypeLoadBalancer,
			expectedSelector: map[string]string{
				"app":                  "test-documentdb",
				"cnpg.io/instanceRole": "primary",
			},
			description: "When endpoint is enabled, service should use CNPG labels for failover support",
		},
		{
			name:            "endpoint enabled with ClusterIP - should use CNPG labels",
			documentDBName:  "test-documentdb",
			endpointEnabled: true,
			serviceType:     corev1.ServiceTypeClusterIP,
			expectedSelector: map[string]string{
				"app":                  "test-documentdb",
				"cnpg.io/instanceRole": "primary",
			},
			description: "Service type should not affect selector labels",
		},
		{
			name:            "different documentdb name - should reflect in cluster label",
			documentDBName:  "my-db-cluster",
			endpointEnabled: true,
			serviceType:     corev1.ServiceTypeLoadBalancer,
			expectedSelector: map[string]string{
				"app":                  "my-db-cluster",
				"cnpg.io/instanceRole": "primary",
			},
			description: "Cluster label should match DocumentDB instance name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock DocumentDB instance
			documentdb := &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.documentDBName,
					Namespace: "test-namespace",
					UID:       types.UID("test-uid-123"),
				},
			}

			// Create a mock ReplicationContext
			replicationContext := &ReplicationContext{
				CNPGClusterName: tt.documentDBName,
				Environment:     "test",
				state:           NoReplication, // This will make EndpointEnabled() return true
			}

			// If endpoint should be disabled, set a different state
			if !tt.endpointEnabled {
				replicationContext.state = Primary
				replicationContext.currentLocalPrimary = "different-primary"
				replicationContext.targetLocalPrimary = "target-primary"
			}

			// Generate the service definition
			service := GetDocumentDBServiceDefinition(documentdb, replicationContext, "test-namespace", tt.serviceType)

			// Verify the selector matches expected values
			if len(service.Spec.Selector) != len(tt.expectedSelector) {
				t.Errorf("Expected selector to have %d labels, got %d. Expected: %v, Got: %v",
					len(tt.expectedSelector), len(service.Spec.Selector), tt.expectedSelector, service.Spec.Selector)
			}

			for key, expectedValue := range tt.expectedSelector {
				if actualValue, exists := service.Spec.Selector[key]; !exists {
					t.Errorf("Expected selector to contain key %q, but it was missing. Selector: %v", key, service.Spec.Selector)
				} else if actualValue != expectedValue {
					t.Errorf("Expected selector[%q] = %q, got %q", key, expectedValue, actualValue)
				}
			}

			// Verify other service properties
			if service.Name == "" {
				t.Error("Service name should not be empty")
			}

			if service.Namespace != "test-namespace" {
				t.Errorf("Expected service namespace to be 'test-namespace', got %q", service.Namespace)
			}

			if service.Spec.Type != tt.serviceType {
				t.Errorf("Expected service type to be %v, got %v", tt.serviceType, service.Spec.Type)
			}

			// Verify owner reference is set correctly
			if len(service.OwnerReferences) != 1 {
				t.Errorf("Expected 1 owner reference, got %d", len(service.OwnerReferences))
			} else {
				ownerRef := service.OwnerReferences[0]
				if ownerRef.Name != tt.documentDBName {
					t.Errorf("Expected owner reference name to be %q, got %q", tt.documentDBName, ownerRef.Name)
				}
				if ownerRef.Kind != "DocumentDB" {
					t.Errorf("Expected owner reference kind to be 'DocumentDB', got %q", ownerRef.Kind)
				}
			}

			t.Logf("✅ %s: %s", tt.name, tt.description)
		})
	}
}
