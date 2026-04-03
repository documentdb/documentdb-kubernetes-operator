// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// parseExtensionVersions parses the output of pg_available_extensions query
// Returns defaultVersion, installedVersion, and a boolean indicating if parsing was successful
func parseExtensionVersions(output string) (defaultVersion, installedVersion string, ok bool) {
	return parseExtensionVersionsFromOutput(output)
}

var _ = Describe("DocumentDB Controller", func() {
	const (
		clusterName         = "test-cluster"
		clusterNamespace    = "default"
		documentDBName      = "test-documentdb"
		documentDBNamespace = "default"
	)

	var (
		ctx      context.Context
		scheme   *runtime.Scheme
		recorder *record.FakeRecorder
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		recorder = record.NewFakeRecorder(10)
		Expect(dbpreview.AddToScheme(scheme)).To(Succeed())
		Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("buildImagePatchOps", func() {
		It("should return 0 ops when both extension and gateway images are the same", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(BeEmpty())
			Expect(extUpdated).To(BeFalse())
			Expect(gwUpdated).To(BeFalse())
		})

		It("should return 1 op when only extension image differs", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(HaveLen(1))
			Expect(patchOps[0].Op).To(Equal("replace"))
			Expect(patchOps[0].Path).To(Equal("/spec/postgresql/extensions/0/image/reference"))
			Expect(patchOps[0].Value).To(Equal("documentdb/documentdb:v2.0.0"))
			Expect(extUpdated).To(BeTrue())
			Expect(gwUpdated).To(BeFalse())
		})

		It("should return 1 op when only gateway image differs", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(HaveLen(1))
			Expect(patchOps[0].Op).To(Equal("replace"))
			Expect(patchOps[0].Path).To(Equal("/spec/plugins/0/parameters/gatewayImage"))
			Expect(patchOps[0].Value).To(Equal("gateway:v2.0.0"))
			Expect(extUpdated).To(BeFalse())
			Expect(gwUpdated).To(BeTrue())
		})

		It("should return 2 ops when both extension and gateway images differ", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(HaveLen(2))
			// First op: extension image
			Expect(patchOps[0].Op).To(Equal("replace"))
			Expect(patchOps[0].Path).To(Equal("/spec/postgresql/extensions/0/image/reference"))
			Expect(patchOps[0].Value).To(Equal("documentdb/documentdb:v2.0.0"))
			// Second op: gateway image
			Expect(patchOps[1].Op).To(Equal("replace"))
			Expect(patchOps[1].Path).To(Equal("/spec/plugins/0/parameters/gatewayImage"))
			Expect(patchOps[1].Value).To(Equal("gateway:v2.0.0"))
			Expect(extUpdated).To(BeTrue())
			Expect(gwUpdated).To(BeTrue())
		})

		It("should return error when documentdb extension is not found in current cluster", func() {
			currentCluster := &cnpgv1.Cluster{
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

			_, _, _, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("documentdb extension not found"))
		})

		It("should use correct index when documentdb is not the first extension", func() {
			currentCluster := &cnpgv1.Cluster{
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

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(HaveLen(1))
			Expect(patchOps[0].Path).To(Equal("/spec/postgresql/extensions/1/image/reference"))
			Expect(patchOps[0].Value).To(Equal("documentdb/documentdb:v2.0.0"))
			Expect(extUpdated).To(BeTrue())
			Expect(gwUpdated).To(BeFalse())
		})

		It("should skip gateway check when plugin is not found in current cluster", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{}, // No plugins
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(BeEmpty())
			Expect(extUpdated).To(BeFalse())
			Expect(gwUpdated).To(BeFalse())
		})

		It("should skip gateway check when current plugin has nil Parameters", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name:       "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: nil,
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(HaveLen(1))
			Expect(patchOps[0].Path).To(Equal("/spec/plugins/0/parameters/gatewayImage"))
			Expect(patchOps[0].Value).To(Equal("gateway:v2.0.0"))
			Expect(extUpdated).To(BeFalse())
			Expect(gwUpdated).To(BeTrue())
		})

		It("should return 0 ops when no extensions exist in either cluster", func() {
			currentCluster := &cnpgv1.Cluster{
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
				Spec: cnpgv1.ClusterSpec{
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(BeEmpty())
			Expect(extUpdated).To(BeFalse())
			Expect(gwUpdated).To(BeFalse())
		})

		It("should use correct plugin index when sidecar plugin is not the first plugin", func() {
			currentCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-wal-replica.documentdb.io",
							Parameters: map[string]string{
								"someParam": "value",
							},
						},
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
			}

			desiredCluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
							},
						},
					},
				},
			}

			patchOps, extUpdated, gwUpdated, err := buildImagePatchOps(currentCluster, desiredCluster)
			Expect(err).ToNot(HaveOccurred())
			Expect(patchOps).To(HaveLen(1))
			Expect(patchOps[0].Path).To(Equal("/spec/plugins/1/parameters/gatewayImage"))
			Expect(patchOps[0].Value).To(Equal("gateway:v2.0.0"))
			Expect(extUpdated).To(BeFalse())
			Expect(gwUpdated).To(BeTrue())
		})
	})

	Describe("upgradeDocumentDBIfNeeded", func() {
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

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
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

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
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
			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify image was updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))
		})

		It("should update image status fields after patching CNPG cluster", func() {
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "documentdb-sidecar",
							Parameters: map[string]string{
								"gatewayImage": "documentdb/gateway:v1.0.0",
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "documentdb-sidecar",
							Parameters: map[string]string{
								"gatewayImage": "documentdb/gateway:v2.0.0",
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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify image status fields were updated on the DocumentDB resource
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.DocumentDBImage).To(Equal("documentdb/documentdb:v2.0.0"))
			Expect(updatedDB.Status.GatewayImage).To(Equal("documentdb/gateway:v2.0.0"))
		})

		It("should return error when DocumentDB resource cannot be refetched", func() {
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
			}

			desiredCluster := cluster.DeepCopy()

			// DocumentDB resource does NOT exist in the fake client — refetch will fail
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "non-existent-documentdb",
					Namespace: clusterNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to refetch DocumentDB resource"))
		})

		It("should return error when documentdb extension is missing from current cluster", func() {
			cluster := &cnpgv1.Cluster{
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

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to build image patch operations"))
		})

		It("should add restart annotation for gateway-only update", func() {
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
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
									Reference: "documentdb/documentdb:v1.0.0", // Same extension image
								},
							},
						},
					},
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0", // Only gateway differs
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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify gateway image was updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())

			// Verify restart annotation was added for gateway-only update
			Expect(result.Annotations).To(HaveKey("kubectl.kubernetes.io/restartedAt"))

			// Verify gateway image status was updated
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.GatewayImage).To(Equal("gateway:v2.0.0"))
		})

		It("should not add restart annotation when extension image also changes", func() {
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
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
									Reference: "documentdb/documentdb:v2.0.0", // Both differ
								},
							},
						},
					},
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0", // Both differ
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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify images were updated
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v2.0.0"))

			// Restart annotation should NOT be added when extension also changes
			// (CNPG handles restart via ImageVolume PodSpec divergence)
			Expect(result.Annotations).ToNot(HaveKey("kubectl.kubernetes.io/restartedAt"))
		})

		It("should return nil when images match and primary is not healthy", func() {
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary: "test-cluster-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{
						cnpgv1.PodHealthy: {"test-cluster-2"}, // Primary NOT healthy
					},
				},
			}

			desiredCluster := cluster.DeepCopy()

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

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Image status should still be updated even though primary isn't healthy
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.DocumentDBImage).To(Equal("documentdb/documentdb:v1.0.0"))
			Expect(updatedDB.Status.GatewayImage).To(Equal("gateway:v1.0.0"))
		})

		It("should update stale image status when images already match", func() {
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
									Reference: "documentdb/documentdb:v2.0.0",
								},
							},
						},
					},
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary:  "test-cluster-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{},
				},
			}

			desiredCluster := cluster.DeepCopy()

			// Status has stale/old image values
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
				Status: dbpreview.DocumentDBStatus{
					DocumentDBImage: "documentdb/documentdb:v1.0.0",
					GatewayImage:    "gateway:v1.0.0",
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

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify stale status was corrected
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.DocumentDBImage).To(Equal("documentdb/documentdb:v2.0.0"))
			Expect(updatedDB.Status.GatewayImage).To(Equal("gateway:v2.0.0"))
		})

		It("should not patch cluster when images already match and no instances exist", func() {
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
					CurrentPrimary:  "",
					InstancesStatus: map[cnpgv1.PodStatus][]string{},
				},
			}

			desiredCluster := cluster.DeepCopy()

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
				Status: dbpreview.DocumentDBStatus{
					DocumentDBImage: "documentdb/documentdb:v1.0.0",
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

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Cluster should remain unchanged (no patch applied)
			result := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, result)).To(Succeed())
			Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("documentdb/documentdb:v1.0.0"))
		})

		// --- SQL-path tests (Steps 4–7) using injectable SQLExecutor ---

		It("should return error when SQL version check fails", func() {
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

			desiredCluster := cluster.DeepCopy()

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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
					return "", fmt.Errorf("connection refused")
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to check documentdb extension versions"))
		})

		It("should return nil when SQL output is unparseable", func() {
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

			desiredCluster := cluster.DeepCopy()

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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
					return "(0 rows)", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil when installed version is empty", func() {
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

			desiredCluster := cluster.DeepCopy()

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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
					return " default_version | installed_version \n-----------------+-------------------\n 0.110-0         |                   \n", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil and update status when versions match (no upgrade needed)", func() {
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

			desiredCluster := cluster.DeepCopy()

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
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
					return " default_version | installed_version \n-----------------+-------------------\n 0.110-0         | 0.110-0           \n", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Status should reflect the installed version as semver
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.SchemaVersion).To(Equal("0.110.0"))
		})

		It("should emit warning event on rollback detection and skip ALTER EXTENSION", func() {
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

			desiredCluster := cluster.DeepCopy()

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

			sqlCallCount := 0
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
					sqlCallCount++
					// Binary offers 0.109-0, but installed schema is 0.110-0 → rollback
					return " default_version | installed_version \n-----------------+-------------------\n 0.109-0         | 0.110-0           \n", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Only the version-check SQL should have been called (no ALTER EXTENSION)
			Expect(sqlCallCount).To(Equal(1))

			// Verify warning event was emitted
			Expect(recorder.Events).To(HaveLen(1))
			event := <-recorder.Events
			Expect(event).To(ContainSubstring("ExtensionRollback"))
			Expect(event).To(ContainSubstring("0.109-0"))
			Expect(event).To(ContainSubstring("0.110-0"))

			// Status should still reflect the installed version
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.SchemaVersion).To(Equal("0.110.0"))
		})

		It("should run ALTER EXTENSION and update status on successful upgrade", func() {
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

			desiredCluster := cluster.DeepCopy()

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

			sqlCalls := []string{}
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
					sqlCalls = append(sqlCalls, sql)
					if len(sqlCalls) == 1 {
						// First call: version check — installed 0.109-0, default 0.110-0
						return " default_version | installed_version \n-----------------+-------------------\n 0.110-0         | 0.109-0           \n", nil
					}
					// Second call: ALTER EXTENSION
					return "ALTER EXTENSION", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Verify both SQL calls were made
			Expect(sqlCalls).To(HaveLen(2))
			Expect(sqlCalls[0]).To(ContainSubstring("pg_available_extensions"))
			Expect(sqlCalls[1]).To(Equal("ALTER EXTENSION documentdb UPDATE"))

			// Status should reflect the upgraded version (default version as semver)
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.SchemaVersion).To(Equal("0.110.0"))
		})

		It("should return error when ALTER EXTENSION fails", func() {
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

			desiredCluster := cluster.DeepCopy()

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

			callCount := 0
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
					callCount++
					if callCount == 1 {
						// Version check: upgrade needed
						return " default_version | installed_version \n-----------------+-------------------\n 0.110-0         | 0.109-0           \n", nil
					}
					// ALTER EXTENSION fails
					return "", fmt.Errorf("permission denied")
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to run ALTER EXTENSION documentdb UPDATE"))
		})

		It("should skip ALTER EXTENSION when version comparison fails", func() {
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

			desiredCluster := cluster.DeepCopy()

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

			sqlCallCount := 0
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
					sqlCallCount++
					// Return invalid version format that will fail CompareExtensionVersions
					return " default_version | installed_version \n-----------------+-------------------\n invalid         | 0.110-0           \n", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Only version-check call, no ALTER EXTENSION (skipped as safety measure)
			Expect(sqlCallCount).To(Equal(1))
		})

		It("should return error when CNPG cluster patch fails for image update", func() {
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
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						return fmt.Errorf("patch conflict")
					},
				}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to patch CNPG cluster with new images"))
		})

		It("should return error when restart annotation patch fails for gateway-only update", func() {
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v1.0.0",
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "cnpg-i-sidecar-injector.documentdb.io",
							Parameters: map[string]string{
								"gatewayImage": "gateway:v2.0.0",
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

			patchCallCount := 0
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						patchCallCount++
						if patchCallCount == 1 {
							// First patch (JSONPatch for gateway image) succeeds
							return c.Patch(ctx, obj, patch, opts...)
						}
						// Second patch (MergePatch for restart annotation) fails
						return fmt.Errorf("restart annotation patch failed")
					},
				}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add restart annotation for gateway update"))
		})

		It("should log error but return nil when updateImageStatus fails after patching", func() {
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
				WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						return c.Patch(ctx, obj, patch, opts...)
					},
					SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return fmt.Errorf("status update failed")
					},
				}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			// Should return nil because updateImageStatus error is only logged, not returned
			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should log error but continue when updateImageStatus fails in step 2b", func() {
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

			desiredCluster := cluster.DeepCopy()

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
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return fmt.Errorf("status update failed")
					},
				}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			// images match, primary not healthy => hits step 2b updateImageStatus (fails) then returns nil
			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when DocumentDB status update fails in version sync", func() {
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

			desiredCluster := cluster.DeepCopy()

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
				Status: dbpreview.DocumentDBStatus{
					// Images match cluster so updateImageStatus is a no-op
					DocumentDBImage: "documentdb/documentdb:v1.0.0",
					// SchemaVersion is stale — triggers step 5 status update
					SchemaVersion: "0.109.0",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return fmt.Errorf("status update conflict")
					},
				}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
					// versions match, but SchemaVersion is stale
					return " default_version | installed_version \n-----------------+-------------------\n 0.110-0         | 0.110-0           \n", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to update DocumentDB status with schema version"))
		})

		It("should return error when status update fails after ALTER EXTENSION upgrade", func() {
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

			desiredCluster := cluster.DeepCopy()

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-documentdb",
					Namespace: clusterNamespace,
				},
				Status: dbpreview.DocumentDBStatus{
					// Images match cluster so updateImageStatus is a no-op
					DocumentDBImage: "documentdb/documentdb:v1.0.0",
					// Version matches installed so step 5 is a no-op
					SchemaVersion: "0.109.0",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster, documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return fmt.Errorf("status update conflict")
					},
				}).
				Build()

			sqlCalls := []string{}
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
				SQLExecutor: func(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
					sqlCalls = append(sqlCalls, sql)
					if len(sqlCalls) == 1 {
						// default > installed → triggers ALTER EXTENSION
						return " default_version | installed_version \n-----------------+-------------------\n 0.110-0         | 0.109-0           \n", nil
					}
					return "ALTER EXTENSION", nil
				},
			}

			err := reconciler.upgradeDocumentDBIfNeeded(ctx, cluster, desiredCluster, documentdb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to update DocumentDB status after schema upgrade"))
			Expect(sqlCalls).To(HaveLen(2))
		})
	})

	Describe("updateImageStatus", func() {
		It("should set DocumentDBImage and GatewayImage from cluster spec", func() {
			cluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "documentdb-sidecar",
							Parameters: map[string]string{
								"gatewayImage": "documentdb/gateway:v1.0.0",
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
				WithObjects(documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.updateImageStatus(ctx, documentdb, cluster)
			Expect(err).ToNot(HaveOccurred())

			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.DocumentDBImage).To(Equal("documentdb/documentdb:v1.0.0"))
			Expect(updatedDB.Status.GatewayImage).To(Equal("documentdb/gateway:v1.0.0"))
		})

		It("should be a no-op when status fields already match", func() {
			cluster := &cnpgv1.Cluster{
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
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: "documentdb-sidecar",
							Parameters: map[string]string{
								"gatewayImage": "documentdb/gateway:v1.0.0",
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
				Status: dbpreview.DocumentDBStatus{
					DocumentDBImage: "documentdb/documentdb:v1.0.0",
					GatewayImage:    "documentdb/gateway:v1.0.0",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.updateImageStatus(ctx, documentdb, cluster)
			Expect(err).ToNot(HaveOccurred())

			// Status should remain unchanged
			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.DocumentDBImage).To(Equal("documentdb/documentdb:v1.0.0"))
			Expect(updatedDB.Status.GatewayImage).To(Equal("documentdb/gateway:v1.0.0"))
		})

		It("should handle cluster with no gateway plugin", func() {
			cluster := &cnpgv1.Cluster{
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
				WithObjects(documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.updateImageStatus(ctx, documentdb, cluster)
			Expect(err).ToNot(HaveOccurred())

			updatedDB := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "test-documentdb", Namespace: clusterNamespace}, updatedDB)).To(Succeed())
			Expect(updatedDB.Status.DocumentDBImage).To(Equal("documentdb/documentdb:v1.0.0"))
			Expect(updatedDB.Status.GatewayImage).To(Equal(""))
		})

		It("should return error when status update fails", func() {
			cluster := &cnpgv1.Cluster{
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
				Status: dbpreview.DocumentDBStatus{
					// Different from cluster → triggers status update
					DocumentDBImage: "documentdb/documentdb:v1.0.0",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						return fmt.Errorf("status update failed")
					},
				}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.updateImageStatus(ctx, documentdb, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to update DocumentDB image status"))
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

	Describe("findPVsForDocumentDB", func() {
		It("returns PV names for PVs with matching documentdb.io/cluster label", func() {
			pv1 := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-abc123",
					Labels: map[string]string{
						util.LabelCluster:   documentDBName,
						util.LabelNamespace: documentDBNamespace,
					},
				},
			}
			pv2 := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-def456",
					Labels: map[string]string{
						util.LabelCluster:   documentDBName,
						util.LabelNamespace: documentDBNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pv1, pv2).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			pvNames, err := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(pvNames).To(HaveLen(2))
			Expect(pvNames).To(ContainElements("pv-abc123", "pv-def456"))
		})

		It("excludes PVs labeled for a different DocumentDB cluster", func() {
			matchingPV := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-match",
					Labels: map[string]string{
						util.LabelCluster:   documentDBName,
						util.LabelNamespace: documentDBNamespace,
					},
				},
			}
			otherPV := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-other",
					Labels: map[string]string{
						util.LabelCluster:   "other-cluster",
						util.LabelNamespace: documentDBNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(matchingPV, otherPV).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			pvNames, err := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(pvNames).To(HaveLen(1))
			Expect(pvNames).To(ContainElement("pv-match"))
		})

		It("excludes PVs with same cluster name but different namespace", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-other-ns",
					Labels: map[string]string{
						util.LabelCluster:   documentDBName,
						util.LabelNamespace: "other-namespace",
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pv).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			pvNames, err := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(pvNames).To(BeEmpty())
		})

		It("returns empty slice when no PVs have the label", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			pvNames, err := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(pvNames).To(BeEmpty())
		})
	})

	Describe("emitPVRetentionWarning", func() {
		It("emits warning event with PV names when labeled PVs exist", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-test123",
					Labels: map[string]string{
						util.LabelCluster:   documentDBName,
						util.LabelNamespace: documentDBNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pv).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			err := reconciler.emitPVRetentionWarning(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// Check that an event was recorded
			Eventually(recorder.Events).Should(Receive(ContainSubstring("PVsRetained")))
		})

		It("does not emit event when no labeled PVs exist", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			err := reconciler.emitPVRetentionWarning(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())

			// No event should be recorded
			Consistently(recorder.Events).ShouldNot(Receive())
		})

		It("does not panic when Recorder is nil", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: nil, // No recorder
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			}

			err := reconciler.emitPVRetentionWarning(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("reconcileFinalizer", func() {
		It("adds finalizer when not present and object is not being deleted", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:       documentDBName,
					Namespace:  documentDBNamespace,
					Finalizers: []string{}, // No finalizer
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:                       "10Gi",
							PersistentVolumeReclaimPolicy: "Delete",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			// Call reconcileFinalizer - should add finalizer since object is not being deleted
			done, result, err := reconciler.reconcileFinalizer(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(result.Requeue).To(BeTrue())

			// Verify finalizer was added
			updated := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, documentDBFinalizer)).To(BeTrue())
		})

		It("continues reconciliation when finalizer is present and object is not being deleted", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:       documentDBName,
					Namespace:  documentDBNamespace,
					Finalizers: []string{documentDBFinalizer}, // Finalizer present
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:                       "10Gi",
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			// Call reconcileFinalizer - should continue since finalizer is present and not deleting
			done, result, err := reconciler.reconcileFinalizer(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(done).To(BeFalse()) // Should continue reconciliation
			Expect(result.Requeue).To(BeFalse())

			// Verify finalizer is still present
			updated := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, documentDBFinalizer)).To(BeTrue())
		})

		It("does not emit warning when policy is Delete", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pv-will-be-deleted",
					Labels: map[string]string{
						util.LabelCluster:   documentDBName,
						util.LabelNamespace: documentDBNamespace,
					},
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:       documentDBName,
					Namespace:  documentDBNamespace,
					Finalizers: []string{documentDBFinalizer},
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:                       "10Gi",
							PersistentVolumeReclaimPolicy: "Delete",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv).
				Build()

			// Create a new recorder to verify no events are emitted during this test
			localRecorder := record.NewFakeRecorder(10)
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: localRecorder,
			}

			_, result, err := reconciler.reconcileFinalizer(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify NO warning event was emitted (policy is Delete)
			Consistently(localRecorder.Events).ShouldNot(Receive())
		})
	})

	Describe("reconcilePVRecovery", func() {
		It("returns immediately when PV recovery is not configured", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					// No bootstrap.recovery.persistentVolume configured
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("returns error when PV does not exist", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "non-existent-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			_, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("returns error when PV is Bound (not available for recovery)", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "bound-pv",
				},
				Spec: corev1.PersistentVolumeSpec{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound, // Not available
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "bound-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			_, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be Available or Released for recovery"))
		})

		It("clears claimRef and requeues when PV is Released with claimRef", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "released-pv",
				},
				Spec: corev1.PersistentVolumeSpec{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					ClaimRef: &corev1.ObjectReference{
						Name:      "old-pvc",
						Namespace: documentDBNamespace,
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeReleased,
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "released-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(RequeueAfterShort))

			// Verify claimRef was cleared
			updatedPV := &corev1.PersistentVolume{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: "released-pv"}, updatedPV)).To(Succeed())
			Expect(updatedPV.Spec.ClaimRef).To(BeNil())
		})

		It("creates temp PVC when PV is Available", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "available-pv",
				},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "standard",
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeAvailable,
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
					UID:       "test-uid",
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "available-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(RequeueAfterShort))

			// Verify temp PVC was created
			tempPVC := &corev1.PersistentVolumeClaim{}
			tempPVCName := documentDBName + "-pv-recovery-temp"
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: tempPVCName, Namespace: documentDBNamespace}, tempPVC)).To(Succeed())
			Expect(tempPVC.Spec.VolumeName).To(Equal("available-pv"))
		})

		It("waits for temp PVC to bind when it exists but is not bound", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "available-pv",
				},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "standard",
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeAvailable,
				},
			}

			tempPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-pv-recovery-temp",
					Namespace: documentDBNamespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "available-pv",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimPending, // Not yet bound
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "available-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv, tempPVC).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(RequeueAfterShort))
		})

		It("proceeds when temp PVC is bound", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "available-pv",
				},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "standard",
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeAvailable,
				},
			}

			tempPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-pv-recovery-temp",
					Namespace: documentDBNamespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "available-pv",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound, // Bound and ready
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "available-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv, tempPVC).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(result.RequeueAfter).To(BeZero())
		})

		It("deletes temp PVC when CNPG cluster is healthy", func() {
			cnpgCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Status: cnpgv1.ClusterStatus{
					Phase: "Cluster in healthy state",
				},
			}

			tempPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-pv-recovery-temp",
					Namespace: documentDBNamespace,
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "some-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cnpgCluster, tempPVC).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify temp PVC was deleted
			deletedPVC := &corev1.PersistentVolumeClaim{}
			err = fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName + "-pv-recovery-temp", Namespace: documentDBNamespace}, deletedPVC)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("does not delete temp PVC when CNPG cluster exists but is not healthy", func() {
			cnpgCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Status: cnpgv1.ClusterStatus{
					Phase: "Cluster is initializing",
				},
			}

			tempPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-pv-recovery-temp",
					Namespace: documentDBNamespace,
				},
			}

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							PersistentVolume: &dbpreview.PVRecoveryConfiguration{
								Name: "some-pv",
							},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cnpgCluster, tempPVC).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.reconcilePVRecovery(ctx, documentdb, documentDBNamespace, documentDBName)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify temp PVC still exists
			existingPVC := &corev1.PersistentVolumeClaim{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName + "-pv-recovery-temp", Namespace: documentDBNamespace}, existingPVC)).To(Succeed())
		})
	})

	Describe("SetupWithManager", func() {
		It("should initialize SQLExecutor before manager registration", func() {
			reconciler := &DocumentDBReconciler{
				Clientset: kubefake.NewSimpleClientset(),
			}

			// Passing nil manager: initialization runs first, then builder fails
			err := reconciler.SetupWithManager(nil)
			Expect(err).To(HaveOccurred())

			// Verify defaults were initialized
			Expect(reconciler.SQLExecutor).ToNot(BeNil())
		})

		It("should return error when Clientset is nil and no custom SQLExecutor is set", func() {
			reconciler := &DocumentDBReconciler{}
			err := reconciler.SetupWithManager(nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Clientset must be configured"))
		})

		It("should not override pre-set SQLExecutor", func() {
			customExecutor := func(_ context.Context, _ *cnpgv1.Cluster, _ string) (string, error) {
				return "custom", nil
			}

			reconciler := &DocumentDBReconciler{
				SQLExecutor: customExecutor,
				Clientset:   kubefake.NewSimpleClientset(),
			}

			// Passing nil manager: initialization runs, then builder fails
			err := reconciler.SetupWithManager(nil)
			Expect(err).To(HaveOccurred())

			// Verify custom SQLExecutor was NOT overridden
			output, _ := reconciler.SQLExecutor(context.Background(), nil, "")
			Expect(output).To(Equal("custom"))
		})

		It("should return error when K8s version validation fails", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "1", Minor: "34"}

			reconciler := &DocumentDBReconciler{
				Clientset: clientset,
			}

			err := reconciler.SetupWithManager(nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not supported"))
		})
	})

	Describe("Reconcile", func() {
		It("should reconcile a DocumentDB with existing CNPG cluster through the full path", func() {
			// Register rbacv1 for Role/RoleBinding creation in EnsureServiceAccountRoleAndRoleBinding
			Expect(rbacv1.AddToScheme(scheme)).To(Succeed())

			// Create DocumentDB with finalizer already present (skip finalizer-add requeue)
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:       documentDBName,
					Namespace:  documentDBNamespace,
					Finalizers: []string{documentDBFinalizer},
				},
				Spec: dbpreview.DocumentDBSpec{
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "1Gi",
						},
					},
				},
			}

			// Create existing CNPG cluster with matching images (no upgrade needed)
			cnpgCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: util.DEFAULT_DOCUMENTDB_IMAGE,
								},
							},
						},
					},
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: util.DEFAULT_SIDECAR_INJECTOR_PLUGIN,
							Parameters: map[string]string{
								"gatewayImage":               util.DEFAULT_GATEWAY_IMAGE,
								"documentDbCredentialSecret": util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET,
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary: documentDBName + "-1",
					TargetPrimary:  documentDBName + "-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{
						cnpgv1.PodHealthy: {documentDBName + "-1"},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cnpgCluster).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			// Mock SQLExecutor: permissions already granted, extension versions match
			sqlExecutor := func(_ context.Context, _ *cnpgv1.Cluster, cmd string) (string, error) {
				if strings.Contains(cmd, "pg_roles") {
					return "(1 row)", nil
				}
				if strings.Contains(cmd, "pg_available_extensions") {
					return " default_version | installed_version\n" +
						"-----------------+-------------------\n" +
						" 0.110-0         | 0.110-0\n(1 row)", nil
				}
				return "", nil
			}

			reconciler := &DocumentDBReconciler{
				Client:      fakeClient,
				Scheme:      scheme,
				Recorder:    recorder,
				SQLExecutor: sqlExecutor,
			}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should add restart annotation when TLS secret name changes", func() {
			Expect(rbacv1.AddToScheme(scheme)).To(Succeed())

			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:       documentDBName,
					Namespace:  documentDBNamespace,
					Finalizers: []string{documentDBFinalizer},
				},
				Spec: dbpreview.DocumentDBSpec{
					InstancesPerNode: 1,
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize: "1Gi",
						},
					},
				},
				Status: dbpreview.DocumentDBStatus{
					TLS: &dbpreview.TLSStatus{
						Ready:      true,
						SecretName: "new-tls-secret",
					},
				},
			}

			cnpgCluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: cnpgv1.ClusterSpec{
					Instances: 1,
					PostgresConfiguration: cnpgv1.PostgresConfiguration{
						Extensions: []cnpgv1.ExtensionConfiguration{
							{
								Name: "documentdb",
								ImageVolumeSource: corev1.ImageVolumeSource{
									Reference: util.DEFAULT_DOCUMENTDB_IMAGE,
								},
							},
						},
					},
					Plugins: []cnpgv1.PluginConfiguration{
						{
							Name: util.DEFAULT_SIDECAR_INJECTOR_PLUGIN,
							Parameters: map[string]string{
								"gatewayImage":               util.DEFAULT_GATEWAY_IMAGE,
								"documentDbCredentialSecret": util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET,
								"gatewayTLSSecret":           "old-tls-secret",
							},
						},
					},
				},
				Status: cnpgv1.ClusterStatus{
					CurrentPrimary: documentDBName + "-1",
					TargetPrimary:  documentDBName + "-1",
					InstancesStatus: map[cnpgv1.PodStatus][]string{
						cnpgv1.PodHealthy: {documentDBName + "-1"},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cnpgCluster).
				WithStatusSubresource(&dbpreview.DocumentDB{}).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(RequeueAfterShort))

			// Verify CNPG cluster was updated with new TLS secret and restart annotation
			updatedCluster := &cnpgv1.Cluster{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, updatedCluster)).To(Succeed())
			Expect(updatedCluster.Spec.Plugins[0].Parameters["gatewayTLSSecret"]).To(Equal("new-tls-secret"))
			Expect(updatedCluster.Annotations).To(HaveKey("kubectl.kubernetes.io/restartedAt"))
		})
	})

	Describe("validateK8sVersion", func() {
		It("should return nil for K8s >= 1.35", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "1", Minor: "35"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			Expect(reconciler.validateK8sVersion()).To(Succeed())
		})

		It("should return error for K8s < 1.35", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "1", Minor: "34"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			err := reconciler.validateK8sVersion()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not supported"))
			Expect(err.Error()).To(ContainSubstring("1.35"))
		})

		It("should return nil for K8s 1.36+", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "1", Minor: "36"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			Expect(reconciler.validateK8sVersion()).To(Succeed())
		})

		It("should return nil for future major versions (e.g. K8s 2.0)", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "2", Minor: "0"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			Expect(reconciler.validateK8sVersion()).To(Succeed())
		})

		It("should handle minor version with + suffix", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "1", Minor: "35+"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			Expect(reconciler.validateK8sVersion()).To(Succeed())
		})

		It("should return error when ServerVersion fails", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, fmt.Errorf("connection refused")
			})

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			err := reconciler.validateK8sVersion()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to detect"))
		})

		It("should return error when minor version is not a number", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "1", Minor: "abc"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			err := reconciler.validateK8sVersion()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse"))
		})

		It("should return error when major version is not a number", func() {
			clientset := kubefake.NewSimpleClientset()
			fakeDisc, ok := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			Expect(ok).To(BeTrue())
			fakeDisc.FakedServerVersion = &version.Info{Major: "abc", Minor: "35"}

			reconciler := &DocumentDBReconciler{Clientset: clientset}
			err := reconciler.validateK8sVersion()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse Kubernetes major version"))
		})
	})
})
