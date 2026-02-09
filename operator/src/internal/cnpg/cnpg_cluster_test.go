// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

var _ = Describe("GetCnpgClusterSpec", func() {
	const (
		docdbName      = "test-docdb"
		docdbNamespace = "default"
		testImage      = "test-image:latest"
	)

	var (
		req ctrl.Request
	)

	newTestDocumentDB := func(featureGates map[string]bool) *dbpreview.DocumentDB {
		return &dbpreview.DocumentDB{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "documentdb.io/v1alpha1",
				Kind:       "DocumentDB",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      docdbName,
				Namespace: docdbNamespace,
			},
			Spec: dbpreview.DocumentDBSpec{
				NodeCount:        1,
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{
						PvcSize: "10Gi",
					},
				},
				FeatureGates: featureGates,
			},
		}
	}

	BeforeEach(func() {
		req = ctrl.Request{}
		req.Name = docdbName
		req.Namespace = docdbNamespace
	})

	Describe("wal_level parameter", func() {
		Context("when featureGates is nil", func() {
			It("does not include wal_level in Parameters", func() {
				documentdb := newTestDocumentDB(nil)
				cluster := GetCnpgClusterSpec(req, documentdb, testImage, docdbName, "", true, logr.Discard())

				_, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
				Expect(exists).To(BeFalse())
			})
		})

		Context("when featureGates is an empty map", func() {
			It("does not include wal_level in Parameters", func() {
				documentdb := newTestDocumentDB(map[string]bool{})
				cluster := GetCnpgClusterSpec(req, documentdb, testImage, docdbName, "", true, logr.Discard())

				_, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
				Expect(exists).To(BeFalse())
			})
		})

		Context("when ChangeStreams feature gate is enabled", func() {
			It("sets wal_level to logical", func() {
				documentdb := newTestDocumentDB(map[string]bool{
					dbpreview.FeatureGateChangeStreams: true,
				})
				cluster := GetCnpgClusterSpec(req, documentdb, testImage, docdbName, "", true, logr.Discard())

				walLevel, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
				Expect(exists).To(BeTrue())
				Expect(walLevel).To(Equal("logical"))
			})
		})

		Context("when ChangeStreams feature gate is explicitly disabled", func() {
			It("does not include wal_level in Parameters", func() {
				documentdb := newTestDocumentDB(map[string]bool{
					dbpreview.FeatureGateChangeStreams: false,
				})
				cluster := GetCnpgClusterSpec(req, documentdb, testImage, docdbName, "", true, logr.Discard())

				_, exists := cluster.Spec.PostgresConfiguration.Parameters["wal_level"]
				Expect(exists).To(BeFalse())
			})
		})
	})

	Describe("default PostgreSQL parameters", func() {
		It("always includes cron.database_name, max_replication_slots, and max_wal_senders", func() {
			documentdb := newTestDocumentDB(nil)
			cluster := GetCnpgClusterSpec(req, documentdb, testImage, docdbName, "", true, logr.Discard())

			params := cluster.Spec.PostgresConfiguration.Parameters
			Expect(params).To(HaveKeyWithValue("cron.database_name", "postgres"))
			Expect(params).To(HaveKeyWithValue("max_replication_slots", "10"))
			Expect(params).To(HaveKeyWithValue("max_wal_senders", "10"))
		})
	})
})
