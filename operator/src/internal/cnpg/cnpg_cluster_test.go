// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func TestGetCnpgClusterSpec(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))

	tests := []struct {
		name              string
		documentdb        *dbpreview.DocumentDB
		documentdbImage   string
		serviceAccount    string
		storageClass      string
		isPrimaryRegion   bool
		expectedInstances int
		expectedImageName string
	}{
		{
			name: "basic cluster creation with primary region",
			documentdb: &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-db",
					Namespace: "default",
					UID:       types.UID("test-uid-123"),
				},
				Spec: dbpreview.DocumentDBSpec{
					NodeCount:        1,
					InstancesPerNode: 2,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:      "10Gi",
							StorageClass: "standard",
						},
					},
				},
			},
			documentdbImage:   "test-image:v1",
			serviceAccount:    "test-sa",
			storageClass:      "standard",
			isPrimaryRegion:   true,
			expectedInstances: 2,
			expectedImageName: "test-image:v1",
		},
		{
			name: "cluster with custom sidecar plugin",
			documentdb: &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-db",
					Namespace: "production",
					UID:       types.UID("custom-uid-456"),
				},
				Spec: dbpreview.DocumentDBSpec{
					NodeCount:                 1,
					InstancesPerNode:          3,
					SidecarInjectorPluginName: "custom-sidecar-plugin.example.io",
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "20Gi",
						},
					},
				},
			},
			documentdbImage:   "custom-image:v2",
			serviceAccount:    "custom-sa",
			storageClass:      "",
			isPrimaryRegion:   true,
			expectedInstances: 3,
			expectedImageName: "custom-image:v2",
		},
		{
			name: "cluster with TLS ready status",
			documentdb: &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tls-db",
					Namespace: "secure",
					UID:       types.UID("tls-uid-789"),
				},
				Spec: dbpreview.DocumentDBSpec{
					NodeCount:        1,
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "5Gi",
						},
					},
				},
				Status: dbpreview.DocumentDBStatus{
					TLS: &dbpreview.TLSStatus{
						Ready:      true,
						SecretName: "tls-secret",
					},
				},
			},
			documentdbImage:   "tls-image:v1",
			serviceAccount:    "tls-sa",
			storageClass:      "fast-storage",
			isPrimaryRegion:   true,
			expectedInstances: 1,
			expectedImageName: "tls-image:v1",
		},
		{
			name: "cluster with custom gateway image",
			documentdb: &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gateway-db",
					Namespace: "default",
					UID:       types.UID("gateway-uid-101"),
				},
				Spec: dbpreview.DocumentDBSpec{
					NodeCount:        1,
					InstancesPerNode: 1,
					GatewayImage:     "custom-gateway:v3",
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "10Gi",
						},
					},
				},
			},
			documentdbImage:   "documentdb-image:v1",
			serviceAccount:    "gateway-sa",
			storageClass:      "",
			isPrimaryRegion:   true,
			expectedInstances: 1,
			expectedImageName: "documentdb-image:v1",
		},
		{
			name: "cluster with nil TLS status",
			documentdb: &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nil-tls-db",
					Namespace: "default",
					UID:       types.UID("nil-tls-uid-201"),
				},
				Spec: dbpreview.DocumentDBSpec{
					NodeCount:        1,
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "10Gi",
						},
					},
				},
				Status: dbpreview.DocumentDBStatus{
					TLS: nil, // explicitly nil TLS status
				},
			},
			documentdbImage:   "documentdb-image:v1",
			serviceAccount:    "nil-tls-sa",
			storageClass:      "",
			isPrimaryRegion:   true,
			expectedInstances: 1,
			expectedImageName: "documentdb-image:v1",
		},
		{
			name: "cluster as replica region (isPrimaryRegion=false)",
			documentdb: &dbpreview.DocumentDB{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "documentdb.io/preview",
					Kind:       "DocumentDB",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "replica-db",
					Namespace: "replica-ns",
					UID:       types.UID("replica-uid-301"),
				},
				Spec: dbpreview.DocumentDBSpec{
					NodeCount:        1,
					InstancesPerNode: 2,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "15Gi",
						},
					},
				},
			},
			documentdbImage:   "replica-image:v1",
			serviceAccount:    "replica-sa",
			storageClass:      "fast-storage",
			isPrimaryRegion:   false, // replica region
			expectedInstances: 2,
			expectedImageName: "replica-image:v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.documentdb.Name,
					Namespace: tt.documentdb.Namespace,
				},
			}

			cluster := GetCnpgClusterSpec(req, tt.documentdb, tt.documentdbImage, tt.serviceAccount, tt.storageClass, tt.isPrimaryRegion, logger)

			// Verify basic fields
			if cluster.Name != tt.documentdb.Name {
				t.Errorf("Expected cluster name %q, got %q", tt.documentdb.Name, cluster.Name)
			}
			if cluster.Namespace != tt.documentdb.Namespace {
				t.Errorf("Expected cluster namespace %q, got %q", tt.documentdb.Namespace, cluster.Namespace)
			}

			// Verify instances
			if cluster.Spec.Instances != tt.expectedInstances {
				t.Errorf("Expected %d instances, got %d", tt.expectedInstances, cluster.Spec.Instances)
			}

			// Verify image name
			if cluster.Spec.ImageName != tt.expectedImageName {
				t.Errorf("Expected image name %q, got %q", tt.expectedImageName, cluster.Spec.ImageName)
			}

			// Verify storage configuration
			if cluster.Spec.StorageConfiguration.Size != tt.documentdb.Spec.Resource.Storage.PvcSize {
				t.Errorf("Expected storage size %q, got %q", tt.documentdb.Spec.Resource.Storage.PvcSize, cluster.Spec.StorageConfiguration.Size)
			}

			// Verify storage class
			if tt.storageClass != "" {
				if cluster.Spec.StorageConfiguration.StorageClass == nil || *cluster.Spec.StorageConfiguration.StorageClass != tt.storageClass {
					t.Errorf("Expected storage class %q, got %v", tt.storageClass, cluster.Spec.StorageConfiguration.StorageClass)
				}
			} else {
				if cluster.Spec.StorageConfiguration.StorageClass != nil {
					t.Errorf("Expected nil storage class, got %q", *cluster.Spec.StorageConfiguration.StorageClass)
				}
			}

			// Verify owner references
			if len(cluster.OwnerReferences) != 1 {
				t.Errorf("Expected 1 owner reference, got %d", len(cluster.OwnerReferences))
			} else {
				owner := cluster.OwnerReferences[0]
				if owner.Name != tt.documentdb.Name {
					t.Errorf("Expected owner name %q, got %q", tt.documentdb.Name, owner.Name)
				}
				if owner.Kind != "DocumentDB" {
					t.Errorf("Expected owner kind 'DocumentDB', got %q", owner.Kind)
				}
			}

			// Verify plugins are configured
			if len(cluster.Spec.Plugins) == 0 {
				t.Error("Expected at least one plugin to be configured")
			}

			// Verify bootstrap configuration exists
			if cluster.Spec.Bootstrap == nil {
				t.Error("Expected bootstrap configuration to be set")
			}

			// Verify inherited metadata labels
			if cluster.Spec.InheritedMetadata == nil || cluster.Spec.InheritedMetadata.Labels == nil {
				t.Error("Expected inherited metadata labels to be set")
			} else {
				if cluster.Spec.InheritedMetadata.Labels[util.LABEL_APP] != tt.documentdb.Name {
					t.Errorf("Expected app label %q, got %q", tt.documentdb.Name, cluster.Spec.InheritedMetadata.Labels[util.LABEL_APP])
				}
			}

			// Verify TLS secret parameter when TLS is ready
			if tt.documentdb.Status.TLS != nil && tt.documentdb.Status.TLS.Ready {
				found := false
				for _, p := range cluster.Spec.Plugins {
					if secretName, ok := p.Parameters["gatewayTLSSecret"]; ok {
						found = true
						if secretName != tt.documentdb.Status.TLS.SecretName {
							t.Errorf("Expected gatewayTLSSecret %q, got %q", tt.documentdb.Status.TLS.SecretName, secretName)
						}
					}
				}
				if !found {
					t.Error("Expected gatewayTLSSecret parameter in plugin configuration")
				}
			}
		})
	}
}

func TestGetInheritedMetadataLabels(t *testing.T) {
	tests := []struct {
		name     string
		appName  string
		expected map[string]string
	}{
		{
			name:    "standard app name",
			appName: "my-documentdb",
			expected: map[string]string{
				util.LABEL_APP:          "my-documentdb",
				util.LABEL_REPLICA_TYPE: "primary",
			},
		},
		{
			name:    "app name with special characters",
			appName: "test-db-123",
			expected: map[string]string{
				util.LABEL_APP:          "test-db-123",
				util.LABEL_REPLICA_TYPE: "primary",
			},
		},
		{
			name:    "empty app name",
			appName: "",
			expected: map[string]string{
				util.LABEL_APP:          "",
				util.LABEL_REPLICA_TYPE: "primary",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInheritedMetadataLabels(tt.appName)

			if result == nil {
				t.Fatal("Expected non-nil result")
			}

			if result.Labels == nil {
				t.Fatal("Expected non-nil labels map")
			}

			for key, expectedValue := range tt.expected {
				if actualValue, exists := result.Labels[key]; !exists {
					t.Errorf("Expected label %q to exist", key)
				} else if actualValue != expectedValue {
					t.Errorf("Expected label %q = %q, got %q", key, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestGetBootstrapConfiguration(t *testing.T) {
	logger := zap.New(zap.UseDevMode(true))

	tests := []struct {
		name            string
		documentdb      *dbpreview.DocumentDB
		isPrimaryRegion bool
		expectRecovery  bool
		expectInitDB    bool
	}{
		{
			name: "primary region without recovery - uses InitDB",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			},
			isPrimaryRegion: true,
			expectRecovery:  false,
			expectInitDB:    true,
		},
		{
			name: "primary region with recovery from backup",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
						},
					},
				},
			},
			isPrimaryRegion: true,
			expectRecovery:  true,
			expectInitDB:    false,
		},
		{
			name: "non-primary region ignores recovery",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
						},
					},
				},
			},
			isPrimaryRegion: false,
			expectRecovery:  false,
			expectInitDB:    true,
		},
		{
			name: "empty backup name - uses InitDB",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							Backup: cnpgv1.LocalObjectReference{Name: ""},
						},
					},
				},
			},
			isPrimaryRegion: true,
			expectRecovery:  false,
			expectInitDB:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getBootstrapConfiguration(tt.documentdb, tt.isPrimaryRegion, logger)

			if result == nil {
				t.Fatal("Expected non-nil bootstrap configuration")
			}

			if tt.expectRecovery {
				if result.Recovery == nil {
					t.Error("Expected Recovery to be configured")
				}
				if result.InitDB != nil {
					t.Error("Expected InitDB to be nil when Recovery is used")
				}
			}

			if tt.expectInitDB {
				if result.InitDB == nil {
					t.Error("Expected InitDB to be configured")
				} else {
					// Verify PostInitSQL contains expected SQL statements
					if len(result.InitDB.PostInitSQL) == 0 {
						t.Error("Expected PostInitSQL to have SQL statements")
					}
				}
				if result.Recovery != nil {
					t.Error("Expected Recovery to be nil when InitDB is used")
				}
			}
		})
	}
}

func TestGetMaxStopDelayOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		documentdb *dbpreview.DocumentDB
		expected   int32
	}{
		{
			name: "returns default when StopDelay is 0",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Timeouts: dbpreview.Timeouts{
						StopDelay: 0,
					},
				},
			},
			expected: util.CNPG_DEFAULT_STOP_DELAY,
		},
		{
			name: "returns custom StopDelay when set",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Timeouts: dbpreview.Timeouts{
						StopDelay: 60,
					},
				},
			},
			expected: 60,
		},
		{
			name: "returns max StopDelay",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Timeouts: dbpreview.Timeouts{
						StopDelay: 1800,
					},
				},
			},
			expected: 1800,
		},
		{
			name: "returns default when Timeouts is empty",
			documentdb: &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{},
			},
			expected: util.CNPG_DEFAULT_STOP_DELAY,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMaxStopDelayOrDefault(tt.documentdb)

			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}
