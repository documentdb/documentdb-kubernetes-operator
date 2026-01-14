// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

var _ = Describe("PVC Controller", func() {
	const (
		pvcName         = "test-pvc"
		pvcNamespace    = "default"
		clusterName     = "test-cluster"
		cnpgClusterName = "test-cnpg-cluster"
	)

	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		// Register required schemes
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(dbpreview.AddToScheme(scheme)).To(Succeed())
		Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
	})

	// Helper function to create a PVC with optional labels and owner references
	createPVC := func(name, namespace string, labels map[string]string, ownerRefs []metav1.OwnerReference) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       namespace,
				Labels:          labels,
				OwnerReferences: ownerRefs,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
		}
	}

	// Helper function to create a CNPG cluster with optional owner references
	createCNPGCluster := func(name, namespace string, ownerRefs []metav1.OwnerReference) *cnpgv1.Cluster {
		return &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       namespace,
				UID:             types.UID("cnpg-" + name + "-uid"),
				OwnerReferences: ownerRefs,
			},
		}
	}

	// Helper function to create a DocumentDB cluster
	createDocumentDB := func(name, namespace string) *dbpreview.DocumentDB {
		return &dbpreview.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				UID:       types.UID("documentdb-" + name + "-uid"),
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
		}
	}

	// Helper function to create an owner reference
	createOwnerRef := func(apiVersion, kind, name string, uid types.UID) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion: apiVersion,
			Kind:       kind,
			Name:       name,
			UID:        uid,
			Controller: func() *bool { b := true; return &b }(),
		}
	}

	// Helper function to reconcile and verify no error
	reconcileAndExpectSuccess := func(reconciler *PVCReconciler, name, namespace string) {
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			},
		}
		result, err := reconciler.Reconcile(ctx, req)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
	}

	// Helper function to verify PVC label state
	verifyPVCLabel := func(fakeClient client.Client, name, namespace string, shouldHaveLabel bool, expectedClusterName string) {
		updated := &corev1.PersistentVolumeClaim{}
		Expect(fakeClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, updated)).To(Succeed())
		if shouldHaveLabel {
			Expect(updated.Labels).ToNot(BeNil())
			Expect(updated.Labels["documentdb.io/cluster"]).To(Equal(expectedClusterName))
		} else {
			_, hasLabel := updated.Labels["documentdb.io/cluster"]
			Expect(hasLabel).To(BeFalse())
		}
	}

	Describe("Reconcile", func() {
		Context("when PVC not found", func() {
			It("should handle PVC not found gracefully", func() {
				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					Build()

				reconciler := &PVCReconciler{
					Client: fakeClient,
				}

				req := ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      "non-existent-pvc",
						Namespace: pvcNamespace,
					},
				}

				result, err := reconciler.Reconcile(ctx, req)
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal(ctrl.Result{}))
			})
		})

		Context("when PVC already has documentdb.io/cluster label", func() {
			It("should handle PVC with documentdb.io/cluster label", func() {
				documentdb := createDocumentDB(clusterName, pvcNamespace)
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentdb, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, true, clusterName)
			})
		})

		Context("when PVC has no documentdb.io/cluster label", func() {
			It("should not add label when PVC has no owner references", func() {
				pvc := createPVC(pvcName, pvcNamespace, nil, nil)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, false, "")
			})

			It("should not add label when PVC has owner but owner is not CNPG Cluster", func() {
				ownerRef := createOwnerRef("apps/v1", "StatefulSet", "test-statefulset", types.UID("statefulset-uid-123"))
				pvc := createPVC(pvcName, pvcNamespace, nil, []metav1.OwnerReference{ownerRef})

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, false, "")
			})

			It("should not add label when PVC owner is CNPG Cluster but CNPG Cluster has no owner", func() {
				cnpgCluster := createCNPGCluster(cnpgClusterName, pvcNamespace, nil)
				cnpgOwnerRef := createOwnerRef("postgresql.cnpg.io/v1", "Cluster", cnpgClusterName, cnpgCluster.UID)
				pvc := createPVC(pvcName, pvcNamespace, nil, []metav1.OwnerReference{cnpgOwnerRef})

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(cnpgCluster, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, false, "")
			})

			It("should not add label when PVC owner is CNPG Cluster but CNPG owner is not DocumentDB", func() {
				deploymentOwnerRef := createOwnerRef("apps/v1", "Deployment", "some-deployment", types.UID("deployment-uid-789"))
				cnpgCluster := createCNPGCluster(cnpgClusterName, pvcNamespace, []metav1.OwnerReference{deploymentOwnerRef})
				cnpgOwnerRef := createOwnerRef("postgresql.cnpg.io/v1", "Cluster", cnpgClusterName, cnpgCluster.UID)
				pvc := createPVC(pvcName, pvcNamespace, nil, []metav1.OwnerReference{cnpgOwnerRef})

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(cnpgCluster, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, false, "")
			})

			It("should add label when PVC owner is CNPG Cluster and CNPG owner is DocumentDB", func() {
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDBOwnerRef := createOwnerRef("documentdb.io/v1alpha1", "DocumentDB", clusterName, documentDB.UID)
				cnpgCluster := createCNPGCluster(cnpgClusterName, pvcNamespace, []metav1.OwnerReference{documentDBOwnerRef})
				cnpgOwnerRef := createOwnerRef("postgresql.cnpg.io/v1", "Cluster", cnpgClusterName, cnpgCluster.UID)
				pvc := createPVC(pvcName, pvcNamespace, nil, []metav1.OwnerReference{cnpgOwnerRef})

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, cnpgCluster, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, true, clusterName)
			})

			It("should handle error gracefully when CNPG Cluster does not exist", func() {
				cnpgOwnerRef := createOwnerRef("postgresql.cnpg.io/v1", "Cluster", "non-existent-cnpg", types.UID("cnpg-uid-456"))
				pvc := createPVC(pvcName, pvcNamespace, nil, []metav1.OwnerReference{cnpgOwnerRef})

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)
				verifyPVCLabel(fakeClient, pvcName, pvcNamespace, false, "")
			})
		})
	})

	Describe("findPVCsForCluster", func() {
		It("should return reconcile requests for all PVCs matching cluster label", func() {
			pvc1 := createPVC("pvc-1", pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
			pvc2 := createPVC("pvc-2", pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
			pvc3 := createPVC("pvc-3", pvcNamespace, map[string]string{"documentdb.io/cluster": "different-cluster"}, nil)
			cluster := createDocumentDB(clusterName, pvcNamespace)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pvc1, pvc2, pvc3, cluster).
				Build()

			reconciler := &PVCReconciler{Client: fakeClient}
			requests := reconciler.findPVCsForCluster(ctx, cluster)

			Expect(len(requests)).To(Equal(2))
			Expect(requests).To(ContainElement(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "pvc-1",
					Namespace: pvcNamespace,
				},
			}))
			Expect(requests).To(ContainElement(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "pvc-2",
					Namespace: pvcNamespace,
				},
			}))
		})

		It("should return empty list when no PVCs match cluster label", func() {
			cluster := createDocumentDB(clusterName, pvcNamespace)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster).
				Build()

			reconciler := &PVCReconciler{Client: fakeClient}
			requests := reconciler.findPVCsForCluster(ctx, cluster)

			Expect(len(requests)).To(Equal(0))
		})

		It("should return empty list on error listing PVCs", func() {
			cluster := createDocumentDB(clusterName, pvcNamespace)

			// Create client without PVC scheme to simulate error
			limitedScheme := runtime.NewScheme()
			Expect(dbpreview.AddToScheme(limitedScheme)).To(Succeed())

			fakeClient := fake.NewClientBuilder().
				WithScheme(limitedScheme).
				WithObjects(cluster).
				Build()

			reconciler := &PVCReconciler{Client: fakeClient}
			requests := reconciler.findPVCsForCluster(ctx, cluster)

			Expect(len(requests)).To(Equal(0))
		})
	})

	Describe("Reconcile - Retention Annotation Management", func() {
		Context("when DocumentDB does not exist and PVC has no annotation", func() {
			It("should set default value 7 when no cluster and no annotation", func() {
				// Create PVC with documentdb.io/cluster label but no retention annotation
				// The cluster referenced does not exist
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify annotation was set to default value 7
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Annotations).ToNot(BeNil())
				Expect(updated.Annotations["documentdb.io/pvc-retention-days"]).To(Equal("7"))
			})
		})

		Context("when DocumentDB does not exist but PVC has annotation", func() {
			It("should do nothing when no cluster but annotation exists", func() {
				// Create PVC with documentdb.io/cluster label and existing retention annotation
				// The cluster referenced does not exist
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
				pvc.Annotations = map[string]string{
					"documentdb.io/pvc-retention-days": "10",
					"custom-annotation":                "custom-value",
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify annotations remain unchanged
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Annotations).ToNot(BeNil())
				Expect(updated.Annotations["documentdb.io/pvc-retention-days"]).To(Equal("10"))
				Expect(updated.Annotations["custom-annotation"]).To(Equal("custom-value"))
			})
		})

		Context("when DocumentDB exists but PVC has no annotation", func() {
			It("should set annotation from cluster when no annotation exists", func() {
				// Create DocumentDB with retention period
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDB.Spec.Resource.Storage.PvcRetentionDays = 14

				// Create PVC with documentdb.io/cluster label but no retention annotation
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify annotation was added
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Annotations).ToNot(BeNil())
				Expect(updated.Annotations["documentdb.io/pvc-retention-days"]).To(Equal("14"))
			})
		})

		Context("when DocumentDB exists and PVC has annotation", func() {
			It("should update annotation when value differs from cluster", func() {
				// Create DocumentDB with retention period
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDB.Spec.Resource.Storage.PvcRetentionDays = 14

				// Create PVC with old retention value
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
				pvc.Annotations = map[string]string{
					"documentdb.io/pvc-retention-days": "7",
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify annotation was updated
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Annotations).ToNot(BeNil())
				Expect(updated.Annotations["documentdb.io/pvc-retention-days"]).To(Equal("14"))
			})

			It("should not modify annotation when value matches cluster", func() {
				// Create DocumentDB with retention period
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

				// Create PVC with correct retention value
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
				pvc.Annotations = map[string]string{
					"documentdb.io/pvc-retention-days": "7",
					"custom-annotation":                "custom-value",
				}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify annotations remain unchanged
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Annotations).ToNot(BeNil())
				Expect(updated.Annotations["documentdb.io/pvc-retention-days"]).To(Equal("7"))
				Expect(updated.Annotations["custom-annotation"]).To(Equal("custom-value"))
			})
		})

		Context("when retention period is zero", func() {
			It("should set annotation to zero", func() {
				// Create DocumentDB with zero retention period (retain forever)
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDB.Spec.Resource.Storage.PvcRetentionDays = 0

				// Create PVC with documentdb.io/cluster label
				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify annotation was set to zero
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Annotations).ToNot(BeNil())
				Expect(updated.Annotations["documentdb.io/pvc-retention-days"]).To(Equal("0"))
			})
		})
	})

	Describe("Finalizer Management", func() {
		Context("when PVC is not deleted (deletionTimestamp is null)", func() {
			It("should add finalizer if not exists", func() {
				// Create DocumentDB and PVC without finalizer
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
				// No finalizers initially

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify finalizer was added
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Finalizers).To(ContainElement(PVCFinalizerName))
			})

			It("should do nothing if finalizer already exists", func() {
				// Create DocumentDB and PVC with finalizer already present
				documentDB := createDocumentDB(clusterName, pvcNamespace)
				documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

				pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
				pvc.Finalizers = []string{PVCFinalizerName, "some-other-finalizer"}

				fakeClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(documentDB, pvc).
					Build()

				reconciler := &PVCReconciler{Client: fakeClient}
				reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

				// Verify finalizers remain unchanged
				updated := &corev1.PersistentVolumeClaim{}
				Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
				Expect(updated.Finalizers).To(Equal([]string{PVCFinalizerName, "some-other-finalizer"}))
			})
		})

		Context("when PVC is deleted (deletionTimestamp is not null)", func() {
			Context("and retention period has not been exceeded", func() {
				It("should add finalizer if not exists and requeue after retention period", func() {
					// Create DocumentDB
					documentDB := createDocumentDB(clusterName, pvcNamespace)
					documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

					// Create PVC with deletionTimestamp (being deleted)
					// Must have at least one finalizer for Kubernetes to accept deletionTimestamp
					pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
					now := metav1.Now()
					pvc.DeletionTimestamp = &now
					pvc.Finalizers = []string{"some-other-finalizer"} // Need at least one finalizer
					pvc.Annotations = map[string]string{
						"documentdb.io/pvc-retention-days": "7",
					}

					fakeClient := fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(documentDB, pvc).
						Build()

					reconciler := &PVCReconciler{Client: fakeClient}
					req := ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name:      pvcName,
							Namespace: pvcNamespace,
						},
					}
					result, err := reconciler.Reconcile(ctx, req)
					Expect(err).ToNot(HaveOccurred())
					// Should requeue to check retention expiration later
					Expect(result.Requeue).To(BeFalse())
					Expect(result.RequeueAfter).To(BeNumerically(">", 0))

					// Verify our finalizer was added
					updated := &corev1.PersistentVolumeClaim{}
					Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
					Expect(updated.Finalizers).To(ContainElement(PVCFinalizerName))
					Expect(updated.Finalizers).To(ContainElement("some-other-finalizer"))
				})

				It("should keep finalizer and requeue after retention period if finalizer already exists", func() {
					// Create DocumentDB
					documentDB := createDocumentDB(clusterName, pvcNamespace)
					documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

					// Create PVC with deletionTimestamp and existing finalizer
					pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
					now := metav1.Now()
					pvc.DeletionTimestamp = &now
					pvc.Finalizers = []string{PVCFinalizerName, "another-finalizer"}
					pvc.Annotations = map[string]string{
						"documentdb.io/pvc-retention-days": "7",
					}

					fakeClient := fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(documentDB, pvc).
						Build()

					reconciler := &PVCReconciler{Client: fakeClient}
					req := ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name:      pvcName,
							Namespace: pvcNamespace,
						},
					}
					result, err := reconciler.Reconcile(ctx, req)
					Expect(err).ToNot(HaveOccurred())
					// Should requeue to check retention expiration later
					Expect(result.Requeue).To(BeFalse())
					Expect(result.RequeueAfter).To(BeNumerically(">", 0))

					// Verify finalizers remain unchanged
					updated := &corev1.PersistentVolumeClaim{}
					Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
					Expect(updated.Finalizers).To(Equal([]string{PVCFinalizerName, "another-finalizer"}))
				})
			})

			Context("and retention period has been exceeded", func() {
				It("should remove finalizer if exists", func() {
					// Create DocumentDB
					documentDB := createDocumentDB(clusterName, pvcNamespace)
					documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

					// Create PVC with deletionTimestamp from 10 days ago (exceeded 7 day retention)
					pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
					tenDaysAgo := metav1.NewTime(time.Now().AddDate(0, 0, -10))
					pvc.DeletionTimestamp = &tenDaysAgo
					pvc.Finalizers = []string{PVCFinalizerName, "another-finalizer"}
					pvc.Annotations = map[string]string{
						"documentdb.io/pvc-retention-days": "7",
					}

					fakeClient := fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(documentDB, pvc).
						Build()

					reconciler := &PVCReconciler{Client: fakeClient}
					reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

					// Verify finalizer was removed
					updated := &corev1.PersistentVolumeClaim{}
					Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
					Expect(updated.Finalizers).ToNot(ContainElement(PVCFinalizerName))
					Expect(updated.Finalizers).To(Equal([]string{"another-finalizer"}))
				})

				It("should do nothing if finalizer does not exist", func() {
					// Create DocumentDB
					documentDB := createDocumentDB(clusterName, pvcNamespace)
					documentDB.Spec.Resource.Storage.PvcRetentionDays = 7

					// Create PVC with deletionTimestamp from 10 days ago (exceeded 7 day retention)
					pvc := createPVC(pvcName, pvcNamespace, map[string]string{"documentdb.io/cluster": clusterName}, nil)
					tenDaysAgo := metav1.NewTime(time.Now().AddDate(0, 0, -10))
					pvc.DeletionTimestamp = &tenDaysAgo
					pvc.Finalizers = []string{"another-finalizer"}
					pvc.Annotations = map[string]string{
						"documentdb.io/pvc-retention-days": "7",
					}

					fakeClient := fake.NewClientBuilder().
						WithScheme(scheme).
						WithObjects(documentDB, pvc).
						Build()

					reconciler := &PVCReconciler{Client: fakeClient}
					reconcileAndExpectSuccess(reconciler, pvcName, pvcNamespace)

					// Verify finalizers remain unchanged (no PVC finalizer to remove)
					updated := &corev1.PersistentVolumeClaim{}
					Expect(fakeClient.Get(ctx, client.ObjectKey{Name: pvcName, Namespace: pvcNamespace}, updated)).To(Succeed())
					Expect(updated.Finalizers).To(Equal([]string{"another-finalizer"}))
					Expect(updated.Finalizers).ToNot(ContainElement(PVCFinalizerName))
				})
			})
		})
	})

	Describe("ClusterRetentionChangedPredicate", func() {
		It("should return true when PvcRetentionDays changes", func() {
			oldCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 7,
						},
					},
				},
			}

			newCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 14,
						},
					},
				},
			}

			predicate := ClusterRetentionChangedPredicate()
			updateEvent := event.UpdateEvent{
				ObjectOld: oldCluster,
				ObjectNew: newCluster,
			}

			Expect(predicate.Update(updateEvent)).To(BeTrue())
		})

		It("should return false when PvcRetentionDays does not change", func() {
			oldCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 7,
						},
					},
				},
			}

			newCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "20Gi", // Different field changed
							PvcRetentionDays: 7,      // Same value
						},
					},
				},
			}

			predicate := ClusterRetentionChangedPredicate()
			updateEvent := event.UpdateEvent{
				ObjectOld: oldCluster,
				ObjectNew: newCluster,
			}

			Expect(predicate.Update(updateEvent)).To(BeFalse())
		})

		It("should return false when PvcRetentionDays is the same (both zero)", func() {
			oldCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 0,
						},
					},
				},
			}

			newCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 0,
						},
					},
				},
			}

			predicate := ClusterRetentionChangedPredicate()
			updateEvent := event.UpdateEvent{
				ObjectOld: oldCluster,
				ObjectNew: newCluster,
			}

			Expect(predicate.Update(updateEvent)).To(BeFalse())
		})

		It("should return true when PvcRetentionDays changes from 0 to non-zero", func() {
			oldCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 0,
						},
					},
				},
			}

			newCluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: pvcNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:          "10Gi",
							PvcRetentionDays: 30,
						},
					},
				},
			}

			predicate := ClusterRetentionChangedPredicate()
			updateEvent := event.UpdateEvent{
				ObjectOld: oldCluster,
				ObjectNew: newCluster,
			}

			Expect(predicate.Update(updateEvent)).To(BeTrue())
		})
	})
})
