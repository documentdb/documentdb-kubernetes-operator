// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func buildFakeClient(objs ...runtime.Object) *fake.ClientBuilder {
	scheme := runtime.NewScheme()
	Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())

	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithRuntimeObjects(objs...)
	}
	return builder
}

func baseCluster(name, namespace string) *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			StorageConfiguration: cnpgv1.StorageConfiguration{
				Size: "10Gi",
			},
			LogLevel:     "info",
			MaxStopDelay: 30,
			Affinity:     cnpgv1.AffinityConfiguration{},
			PostgresConfiguration: cnpgv1.PostgresConfiguration{
				Extensions: []cnpgv1.ExtensionConfiguration{
					{
						Name: "documentdb",
						ImageVolumeSource: corev1.ImageVolumeSource{
							Reference: "ghcr.io/documentdb/documentdb:0.109.0",
						},
					},
				},
				Parameters: map[string]string{
					"cron.database_name":    "postgres",
					"max_replication_slots": "10",
					"max_wal_senders":       "10",
				},
			},
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name:    util.DEFAULT_SIDECAR_INJECTOR_PLUGIN,
					Enabled: pointer.Bool(true),
					Parameters: map[string]string{
						"gatewayImage":               "ghcr.io/documentdb/gateway:0.109.0",
						"documentDbCredentialSecret": "documentdb-credentials",
					},
				},
			},
		},
	}
}

var _ = Describe("SyncCnpgCluster", func() {
	const namespace = "test-ns"

	It("does nothing when current matches desired", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())
	})

	It("detects extension image changes", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()
		desired.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference = "ghcr.io/documentdb/documentdb:0.110.0"

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("ghcr.io/documentdb/documentdb:0.110.0"))
	})

	It("detects gateway image changes", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()
		desired.Spec.Plugins[0].Parameters["gatewayImage"] = "ghcr.io/documentdb/gateway:0.110.0"

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.Plugins[0].Parameters["gatewayImage"]).To(Equal("ghcr.io/documentdb/gateway:0.110.0"))
	})

	It("patches plugin parameters (TLS secret sync)", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()
		desired.Spec.Plugins[0].Parameters["gatewayTLSSecret"] = "my-tls-secret"

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.Plugins[0].Parameters["gatewayTLSSecret"]).To(Equal("my-tls-secret"))

		// Should also have restart annotation since plugin params changed
		Expect(updated.Annotations).To(HaveKey("kubectl.kubernetes.io/restartedAt"))
	})

	It("re-enables a disabled plugin", func() {
		current := baseCluster("test-cluster", namespace)
		current.Spec.Plugins[0].Enabled = pointer.Bool(false)
		desired := current.DeepCopy()
		desired.Spec.Plugins[0].Enabled = pointer.Bool(true)

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(*updated.Spec.Plugins[0].Enabled).To(BeTrue())
		Expect(updated.Annotations).To(HaveKey("kubectl.kubernetes.io/restartedAt"))
	})

	It("re-enables a plugin with nil Enabled field", func() {
		current := baseCluster("test-cluster", namespace)
		current.Spec.Plugins[0].Enabled = nil
		desired := current.DeepCopy()
		desired.Spec.Plugins[0].Enabled = pointer.Bool(true)

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(*updated.Spec.Plugins[0].Enabled).To(BeTrue())
	})

	It("does not add restart annotation when extension image changes", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()
		desired.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference = "ghcr.io/documentdb/documentdb:0.110.0"
		desired.Spec.Plugins[0].Parameters["gatewayImage"] = "ghcr.io/documentdb/gateway:0.110.0"

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("ghcr.io/documentdb/documentdb:0.110.0"))
		Expect(updated.Spec.Plugins[0].Parameters["gatewayImage"]).To(Equal("ghcr.io/documentdb/gateway:0.110.0"))
		// No restart annotation — CNPG handles restart for extension changes
		Expect(updated.Annotations).ToNot(HaveKey("kubectl.kubernetes.io/restartedAt"))
	})

	It("applies extra patch operations", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()

		extraOps := []JSONPatch{
			{
				Op:    PatchOpReplace,
				Path:  "/spec/instances",
				Value: 3,
			},
		}

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, extraOps)

		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.Instances).To(Equal(3))
	})

	It("returns error when documentdb extension is not found in current cluster", func() {
		current := baseCluster("test-cluster", namespace)
		current.Spec.PostgresConfiguration.Extensions = nil // no extensions

		desired := baseCluster("test-cluster", namespace)
		desired.Spec.PostgresConfiguration.Extensions = []cnpgv1.ExtensionConfiguration{
			{
				Name: "documentdb",
				ImageVolumeSource: corev1.ImageVolumeSource{
					Reference: "ghcr.io/documentdb/documentdb:0.110.0",
				},
			},
		}

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("documentdb extension not found"))
	})

	It("skips plugin sync when desired has no plugins", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()
		desired.Spec.Plugins = nil

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("skips plugin sync when plugin not found in current cluster", func() {
		current := baseCluster("test-cluster", namespace)
		current.Spec.Plugins = nil // no plugins in current

		desired := baseCluster("test-cluster", namespace)
		desired.Spec.Plugins[0].Parameters["gatewayImage"] = "ghcr.io/documentdb/gateway:0.110.0"

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("handles gateway and TLS changes together", func() {
		current := baseCluster("test-cluster", namespace)
		desired := current.DeepCopy()
		desired.Spec.Plugins[0].Parameters["gatewayImage"] = "ghcr.io/documentdb/gateway:0.110.0"
		desired.Spec.Plugins[0].Parameters["gatewayTLSSecret"] = "new-tls-secret"

		c := buildFakeClient(current).Build()
		err := SyncCnpgCluster(context.Background(), c, current, desired, nil)
		Expect(err).ToNot(HaveOccurred())

		updated := &cnpgv1.Cluster{}
		Expect(c.Get(context.Background(), types.NamespacedName{Name: "test-cluster", Namespace: namespace}, updated)).To(Succeed())
		Expect(updated.Spec.Plugins[0].Parameters["gatewayImage"]).To(Equal("ghcr.io/documentdb/gateway:0.110.0"))
		Expect(updated.Spec.Plugins[0].Parameters["gatewayTLSSecret"]).To(Equal("new-tls-secret"))
		// Restart annotation because gateway updated (no extension change)
		Expect(updated.Annotations).To(HaveKey("kubectl.kubernetes.io/restartedAt"))
	})
})

var _ = Describe("Helper functions", func() {
	It("findExtensionImage returns -1 for cluster without extensions", func() {
		cluster := &cnpgv1.Cluster{
			Spec: cnpgv1.ClusterSpec{
				PostgresConfiguration: cnpgv1.PostgresConfiguration{},
			},
		}
		idx, img := findExtensionImage(cluster)
		Expect(idx).To(Equal(-1))
		Expect(img).To(BeEmpty())
	})

	It("findPlugin returns -1 when plugin not found", func() {
		cluster := &cnpgv1.Cluster{
			Spec: cnpgv1.ClusterSpec{
				Plugins: []cnpgv1.PluginConfiguration{
					{Name: "other-plugin"},
				},
			},
		}
		idx, plugin := findPlugin(cluster, "my-plugin")
		Expect(idx).To(Equal(-1))
		Expect(plugin).To(BeNil())
	})

	It("findPlugin returns correct index and plugin", func() {
		cluster := &cnpgv1.Cluster{
			Spec: cnpgv1.ClusterSpec{
				Plugins: []cnpgv1.PluginConfiguration{
					{Name: "plugin-a"},
					{Name: "plugin-b"},
				},
			},
		}
		idx, plugin := findPlugin(cluster, "plugin-b")
		Expect(idx).To(Equal(1))
		Expect(plugin).ToNot(BeNil())
		Expect(plugin.Name).To(Equal("plugin-b"))
	})

	It("getParam returns empty for nil map", func() {
		Expect(getParam(nil, "key")).To(BeEmpty())
	})

	It("getParam returns value for existing key", func() {
		m := map[string]string{"key": "value"}
		Expect(getParam(m, "key")).To(Equal("value"))
	})

	It("getParam returns empty for missing key", func() {
		m := map[string]string{"other": "value"}
		Expect(getParam(m, "key")).To(BeEmpty())
	})
})
