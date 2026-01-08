// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

var _ = Describe("bootstrap recovery validation", func() {
	var v *DocumentDBWebhook

	BeforeEach(func() {
		v = &DocumentDBWebhook{}
	})

	It("doesn't complain if there isn't a bootstrap configuration", func() {
		documentdb := &dbpreview.DocumentDB{}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(BeEmpty())
	})

	It("doesn't complain if there isn't a recovery configuration", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{},
			},
		}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(BeEmpty())
	})

	It("doesn't complain if only backup recovery is specified", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "my-backup",
						},
					},
				},
			},
		}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(BeEmpty())
	})

	It("doesn't complain if only PVC recovery is specified", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						PVC: cnpgv1.LocalObjectReference{
							Name: "my-pvc",
						},
					},
				},
			},
		}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(BeEmpty())
	})

	It("complains if both backup and PVC recovery are specified", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "my-backup",
						},
						PVC: cnpgv1.LocalObjectReference{
							Name: "my-pvc",
						},
					},
				},
			},
		}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(HaveLen(1))
		Expect(result[0].Error()).To(ContainSubstring("cannot specify both backup and PVC recovery"))
	})

	It("doesn't complain if backup name is empty and PVC is specified", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "",
						},
						PVC: cnpgv1.LocalObjectReference{
							Name: "my-pvc",
						},
					},
				},
			},
		}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(BeEmpty())
	})

	It("doesn't complain if PVC name is empty and backup is specified", func() {
		documentdb := &dbpreview.DocumentDB{
			Spec: dbpreview.DocumentDBSpec{
				Bootstrap: &dbpreview.BootstrapConfiguration{
					Recovery: &dbpreview.RecoveryConfiguration{
						Backup: cnpgv1.LocalObjectReference{
							Name: "my-backup",
						},
						PVC: cnpgv1.LocalObjectReference{
							Name: "",
						},
					},
				},
			},
		}
		result := v.validateBootstrapRecovery(documentdb)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("DocumentDB webhook", func() {
	var v *DocumentDBWebhook

	BeforeEach(func() {
		v = &DocumentDBWebhook{}
	})

	Context("validate method", func() {
		It("returns no errors for a valid DocumentDB with no bootstrap", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: dbpreview.DocumentDBSpec{},
			}
			result := v.validate(documentdb)
			Expect(result).To(BeEmpty())
		})

		It("returns errors when both backup and PVC recovery are specified", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: dbpreview.DocumentDBSpec{
					Bootstrap: &dbpreview.BootstrapConfiguration{
						Recovery: &dbpreview.RecoveryConfiguration{
							Backup: cnpgv1.LocalObjectReference{
								Name: "my-backup",
							},
							PVC: cnpgv1.LocalObjectReference{
								Name: "my-pvc",
							},
						},
					},
				},
			}
			result := v.validate(documentdb)
			Expect(result).To(HaveLen(1))
		})
	})
})
