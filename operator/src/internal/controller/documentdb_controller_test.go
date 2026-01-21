// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// parseExtensionVersions parses the output of pg_available_extensions query
// Returns defaultVersion, installedVersion, and a boolean indicating if parsing was successful
func parseExtensionVersions(output string) (defaultVersion, installedVersion string, ok bool) {
	return parseExtensionVersionsFromOutput(output)
}

var _ = Describe("DocumentDB Controller", func() {
	const (
		clusterName      = "test-cluster"
		clusterNamespace = "default"
	)

	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(dbpreview.AddToScheme(scheme)).To(Succeed())
		Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("updateExtensionImageIfNeeded", func() {
		It("should return false when current and desired images are the same", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeFalse())

			// Verify the cluster was not updated (image should remain the same)
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, client.ObjectKey{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v1.0.0"))
		})

		It("should update extension image and return true when current and desired images differ", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			// Verify the cluster was updated with the new image
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))
		})

		It("should return error when documentdb extension is not found in current cluster", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "other-extension",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "other/image:v1.0.0",
								},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			_, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("documentdb extension not found"))
		})

		It("should handle cluster with multiple extensions and update only documentdb", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "pg_stat_statements",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_stat_statements:v1.0.0",
								},
							},
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
							{
								Name: "pg_cron",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_cron:v1.0.0",
								},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "pg_stat_statements",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_stat_statements:v1.0.0",
								},
							},
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
							{
								Name: "pg_cron",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_cron:v1.0.0",
								},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			// Verify only documentdb extension was updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions).To(HaveLen(3))
			Expect(result.Spec.PostgresConfiguration.Extensions[0].Name).To(Equal("pg_stat_statements"))
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("postgres/pg_stat_statements:v1.0.0"))
			Expect(result.Spec.PostgresConfiguration.Extensions[1].Name).To(Equal("documentdb"))
			Expect(result.Spec.PostgresConfiguration.Extensions[1].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))
			Expect(result.Spec.PostgresConfiguration.Extensions[2].Name).To(Equal("pg_cron"))
			Expect(result.Spec.PostgresConfiguration.Extensions[2].ImageVolumeSource.Reference).To(Equal("postgres/pg_cron:v1.0.0"))
		})

		It("should return false when no extensions exist in both clusters", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			// Both clusters have no extensions, images are both empty strings, so they match
			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeFalse())
		})

		It("should handle documentdb extension as the only extension", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
								LdLibraryPath: []string{"lib"},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v3.0.0",
								},
								LdLibraryPath: []string{"lib"},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			// Verify the cluster was updated with the new image
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v3.0.0"))
			// Verify other fields are preserved
			Expect(result.Spec.PostgresConfiguration.Extensions[0].LdLibraryPath).To(Equal([]string{"lib"}))
		})

		It("should handle documentdb extension at the beginning of extensions list", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
							{
								Name: "pg_cron",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_cron:v1.0.0",
								},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
							{
								Name: "pg_cron",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_cron:v1.0.0",
								},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			// Verify the cluster was updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))
		})

		It("should handle documentdb extension at the end of extensions list", func() {
			currentCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "pg_cron",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_cron:v1.0.0",
								},
							},
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "pg_cron",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "postgres/pg_cron:v1.0.0",
								},
							},
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(currentCluster).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			updated, err := reconciler.updateExtensionImageIfNeeded(ctx, currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			// Verify the cluster was updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[1].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))
		})
	})

	Describe("upgradeDocumentDBExtensionIfNeeded", func() {
		It("should return nil when primary pod is not healthy", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary: "test-cluster-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{
						cnpgv1.PodHealthy: {"test-cluster-2", "test-cluster-3"}, // Primary not in healthy list
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.upgradeDocumentDBExtensionIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil when InstancesStatus is empty", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary:  "test-cluster-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.upgradeDocumentDBExtensionIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil and update image when image differs", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v1.0.0",
								},
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary: "test-cluster-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{
						cnpgv1.PodHealthy: {"test-cluster-1"},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
						},
					},
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			// Should update image and return nil (waiting for pod to become healthy)
			err := reconciler.upgradeDocumentDBExtensionIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify image was updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))
		})
	})

	Describe("parseExtensionVersionsFromOutput", func() {
		It("should parse valid output with matching versions", func() {
			output := ` default_version | installed_version 
-----------------+-------------------
 0.110-0         | 0.110-0
(1 row)`

			defaultVersion, installedVersion, ok := parseExtensionVersions(output)
			Expect(ok).To(BeTrue())
			Expect(defaultVersion).To(Equal("0.110-0"))
			Expect(installedVersion).To(Equal("0.110-0"))
		})

		It("should parse valid output with different versions", func() {
			output := ` default_version | installed_version 
-----------------+-------------------
 0.111-0         | 0.110-0
(1 row)`

			defaultVersion, installedVersion, ok := parseExtensionVersions(output)
			Expect(ok).To(BeTrue())
			Expect(defaultVersion).To(Equal("0.111-0"))
			Expect(installedVersion).To(Equal("0.110-0"))
		})

		It("should handle empty installed_version", func() {
			output := ` default_version | installed_version 
-----------------+-------------------
 0.110-0         | 
(1 row)`

			defaultVersion, installedVersion, ok := parseExtensionVersions(output)
			Expect(ok).To(BeTrue())
			Expect(defaultVersion).To(Equal("0.110-0"))
			Expect(installedVersion).To(Equal(""))
		})

		It("should return false for output with less than 3 lines", func() {
			output := ` default_version | installed_version 
-----------------+-------------------`

			_, _, ok := parseExtensionVersions(output)
			Expect(ok).To(BeFalse())
		})

		It("should return false for empty output", func() {
			output := ""

			_, _, ok := parseExtensionVersions(output)
			Expect(ok).To(BeFalse())
		})

		It("should return false for output with no pipe separator", func() {
			output := ` default_version   installed_version 
-----------------+-------------------
 0.110-0           0.110-0
(1 row)`

			_, _, ok := parseExtensionVersions(output)
			Expect(ok).To(BeFalse())
		})

		It("should return false for output with too many pipe separators", func() {
			output := ` default_version | installed_version | extra
-----------------+-------------------+------
 0.110-0         | 0.110-0           | data
(1 row)`

			_, _, ok := parseExtensionVersions(output)
			Expect(ok).To(BeFalse())
		})

		It("should handle semantic version strings", func() {
			output := ` default_version | installed_version 
-----------------+-------------------
 1.2.3-beta.1    | 1.2.2
(1 row)`

			defaultVersion, installedVersion, ok := parseExtensionVersions(output)
			Expect(ok).To(BeTrue())
			Expect(defaultVersion).To(Equal("1.2.3-beta.1"))
			Expect(installedVersion).To(Equal("1.2.2"))
		})

		It("should trim whitespace from versions", func() {
			output := ` default_version | installed_version 
-----------------+-------------------
   0.110-0       |    0.109-0   
(1 row)`

			defaultVersion, installedVersion, ok := parseExtensionVersions(output)
			Expect(ok).To(BeTrue())
			Expect(defaultVersion).To(Equal("0.110-0"))
			Expect(installedVersion).To(Equal("0.109-0"))
		})

		It("should handle output without row count footer", func() {
			output := ` default_version | installed_version 
-----------------+-------------------
 0.110-0         | 0.110-0`

			defaultVersion, installedVersion, ok := parseExtensionVersions(output)
			Expect(ok).To(BeTrue())
			Expect(defaultVersion).To(Equal("0.110-0"))
			Expect(installedVersion).To(Equal("0.110-0"))
		})
	})
})
