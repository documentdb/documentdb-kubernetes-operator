// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/product"
)

var _ = Describe("GetCnpgClusterSpecFromIntent", func() {
	log := zap.New(zap.WriteTo(GinkgoWriter))

	newRequest := func() ctrl.Request {
		req := ctrl.Request{}
		req.Name = "test-cluster"
		req.Namespace = "default"
		return req
	}

	newDocumentDB := func() *dbpreview.DocumentDB {
		return &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				InstancesPerNode: 1,
				Resource: dbpreview.Resource{
					Storage: dbpreview.StorageConfiguration{PvcSize: "10Gi"},
				},
			},
		}
	}

	It("renders product-varying values from the intent", func() {
		intent := product.ClusterIntent{
			Images: product.Images{
				PostgresExtension: "reg/ext:test",
				Gateway:           "reg/gw:test",
			},
			CredentialSecret: "custom-secret",
			Plugins:          product.Plugins{SidecarInjectorName: "custom-injector.example.io"},
		}

		intent.Identity.Name = "test-cluster"
		intent.Identity.Namespace = "default"
		result := GetCnpgClusterSpecFromIntent(intent, "", true, log)

		Expect(result.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).To(Equal("reg/ext:test"))
		Expect(result.Spec.Plugins[0].Name).To(Equal("custom-injector.example.io"))
		Expect(result.Spec.Plugins[0].Parameters["gatewayImage"]).To(Equal("reg/gw:test"))
		Expect(result.Spec.Plugins[0].Parameters["documentDbCredentialSecret"]).To(Equal("custom-secret"))
	})

	It("matches the string wrapper when the intent is adapter-derived", func() {
		documentdb := newDocumentDB()
		intent := product.DocumentDBAdapter{}.ToClusterIntent(documentdb)

		intent.Identity.Name = "test-cluster"
		intent.Identity.Namespace = "default"

		fromIntent := GetCnpgClusterSpecFromIntent(intent, "", true, log)
		fromWrapper := GetCnpgClusterSpec(newRequest(), documentdb, "", "test-sa", "", true, log)

		Expect(fromIntent.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference).
			To(Equal(fromWrapper.Spec.PostgresConfiguration.Extensions[0].ImageVolumeSource.Reference))
		Expect(fromIntent.Spec.Plugins[0].Name).To(Equal(fromWrapper.Spec.Plugins[0].Name))
		Expect(fromIntent.Spec.Plugins[0].Parameters["gatewayImage"]).
			To(Equal(fromWrapper.Spec.Plugins[0].Parameters["gatewayImage"]))
	})
})
