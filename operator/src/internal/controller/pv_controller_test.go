// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var _ = Describe("PersistentVolume Controller", func() {
	const (
		pvName         = "test-pv"
		pvcName        = "test-pvc"
		clusterName    = "test-cluster"
		documentdbName = "test-documentdb"
		testNamespace  = "default"
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
		Expect(storagev1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("containsAllMountOptions", func() {
		It("returns true when all desired options are present", func() {
			current := []string{"nodev", "nosuid", "noexec", "rw"}
			desired := []string{"nodev", "nosuid", "noexec"}
			Expect(containsAllMountOptions(current, desired)).To(BeTrue())
		})

		It("returns false when some desired options are missing", func() {
			current := []string{"nodev", "rw"}
			desired := []string{"nodev", "nosuid", "noexec"}
			Expect(containsAllMountOptions(current, desired)).To(BeFalse())
		})

		It("returns true when desired is empty", func() {
			current := []string{"nodev", "rw"}
			desired := []string{}
			Expect(containsAllMountOptions(current, desired)).To(BeTrue())
		})

		It("returns false when current is empty but desired is not", func() {
			current := []string{}
			desired := []string{"nodev"}
			Expect(containsAllMountOptions(current, desired)).To(BeFalse())
		})
	})

	Describe("mergeMountOptions", func() {
		It("merges options without duplicates", func() {
			current := []string{"rw", "nodev"}
			desired := []string{"nodev", "nosuid", "noexec"}
			result := mergeMountOptions(current, desired)
			Expect(result).To(HaveLen(4))
			Expect(result).To(ContainElements("rw", "nodev", "nosuid", "noexec"))
		})

		It("handles empty current slice", func() {
			current := []string{}
			desired := []string{"nodev", "nosuid"}
			result := mergeMountOptions(current, desired)
			Expect(result).To(HaveLen(2))
			Expect(result).To(ContainElements("nodev", "nosuid"))
		})

		It("handles empty desired slice", func() {
			current := []string{"rw", "nodev"}
			desired := []string{}
			result := mergeMountOptions(current, desired)
			Expect(result).To(HaveLen(2))
			Expect(result).To(ContainElements("nodev", "rw"))
		})
	})

	Describe("isCNPGClusterOwnerRef", func() {
		It("returns true for valid CNPG Cluster owner reference", func() {
			ownerRef := metav1.OwnerReference{
				APIVersion: "postgresql.cnpg.io/v1",
				Kind:       "Cluster",
				Name:       "test-cluster",
			}
			Expect(isCNPGClusterOwnerRef(ownerRef)).To(BeTrue())
		})

		It("returns false for non-Cluster kind", func() {
			ownerRef := metav1.OwnerReference{
				APIVersion: "postgresql.cnpg.io/v1",
				Kind:       "Backup",
				Name:       "test-backup",
			}
			Expect(isCNPGClusterOwnerRef(ownerRef)).To(BeFalse())
		})

		It("returns false for non-CNPG API version", func() {
			ownerRef := metav1.OwnerReference{
				APIVersion: "apps/v1",
				Kind:       "Cluster",
				Name:       "test-cluster",
			}
			Expect(isCNPGClusterOwnerRef(ownerRef)).To(BeFalse())
		})
	})

	Describe("isOwnedByDocumentDB", func() {
		It("returns true when cluster is owned by the specified DocumentDB", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       documentdbName,
						},
					},
				},
			}
			Expect(isOwnedByDocumentDB(cluster, documentdbName)).To(BeTrue())
		})

		It("returns false when cluster is owned by a different DocumentDB", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       "other-documentdb",
						},
					},
				},
			}
			Expect(isOwnedByDocumentDB(cluster, documentdbName)).To(BeFalse())
		})

		It("returns false when cluster has no owner references", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
				},
			}
			Expect(isOwnedByDocumentDB(cluster, documentdbName)).To(BeFalse())
		})
	})

	Describe("getDesiredReclaimPolicy", func() {
		var reconciler *PersistentVolumeReconciler

		BeforeEach(func() {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler = &PersistentVolumeReconciler{Client: fakeClient}
		})

		It("returns Retain when spec specifies Retain", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}
			Expect(reconciler.getDesiredReclaimPolicy(documentdb)).To(Equal(corev1.PersistentVolumeReclaimRetain))
		})

		It("returns Delete when spec specifies Delete", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Delete",
						},
					},
				},
			}
			Expect(reconciler.getDesiredReclaimPolicy(documentdb)).To(Equal(corev1.PersistentVolumeReclaimDelete))
		})

		It("returns Retain when spec is empty (default)", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{},
					},
				},
			}
			Expect(reconciler.getDesiredReclaimPolicy(documentdb)).To(Equal(corev1.PersistentVolumeReclaimRetain))
		})

		It("returns Retain for unknown policy value", func() {
			documentdb := &dbpreview.DocumentDB{
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Unknown",
						},
					},
				},
			}
			Expect(reconciler.getDesiredReclaimPolicy(documentdb)).To(Equal(corev1.PersistentVolumeReclaimRetain))
		})
	})

	Describe("applyDesiredPVConfiguration", func() {
		var reconciler *PersistentVolumeReconciler

		BeforeEach(func() {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler = &PersistentVolumeReconciler{Client: fakeClient}
		})

		It("returns true and updates PV when reclaim policy differs", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					MountOptions:                  []string{"nodev", "noexec", "nosuid"},
				},
			}
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{Name: documentdbName},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}

			needsUpdate := reconciler.applyDesiredPVConfiguration(ctx, pv, documentdb)
			Expect(needsUpdate).To(BeTrue())
			Expect(pv.Spec.PersistentVolumeReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimRetain))
			Expect(pv.Labels[util.LabelCluster]).To(Equal(documentdbName))
		})

		It("returns true and updates PV when mount options are missing", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					MountOptions:                  []string{"rw"},
				},
			}
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{Name: documentdbName},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Delete",
						},
					},
				},
			}

			needsUpdate := reconciler.applyDesiredPVConfiguration(ctx, pv, documentdb)
			Expect(needsUpdate).To(BeTrue())
			Expect(pv.Spec.MountOptions).To(ContainElements("nodev", "noexec", "nosuid", "rw"))
			Expect(pv.Labels[util.LabelCluster]).To(Equal(documentdbName))
		})

		It("returns false when no changes are needed", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: pvName,
					Labels: map[string]string{
						util.LabelCluster: documentdbName,
					},
				},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
					MountOptions:                  []string{"nodev", "noexec", "nosuid"},
				},
			}
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{Name: documentdbName},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}

			needsUpdate := reconciler.applyDesiredPVConfiguration(ctx, pv, documentdb)
			Expect(needsUpdate).To(BeFalse())
		})
	})

	Describe("provisionerSupportsMountOptions", func() {
		It("returns true when PV has no storage class", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "",
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			Expect(reconciler.provisionerSupportsMountOptions(ctx, pv)).To(BeTrue())
		})

		It("returns true when StorageClass is not found", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "non-existent-sc",
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			Expect(reconciler.provisionerSupportsMountOptions(ctx, pv)).To(BeTrue())
		})

		It("returns false for kind local-path-provisioner", func() {
			storageClass := &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "local-path"},
				Provisioner: "rancher.io/local-path",
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "local-path",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(storageClass).
				Build()
			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			Expect(reconciler.provisionerSupportsMountOptions(ctx, pv)).To(BeFalse())
		})

		It("returns false for minikube hostpath provisioner", func() {
			storageClass := &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "standard"},
				Provisioner: "k8s.io/minikube-hostpath",
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "standard",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(storageClass).
				Build()
			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			Expect(reconciler.provisionerSupportsMountOptions(ctx, pv)).To(BeFalse())
		})

		It("returns true for Azure Disk provisioner", func() {
			storageClass := &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "managed-premium"},
				Provisioner: "disk.csi.azure.com",
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "managed-premium",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(storageClass).
				Build()
			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			Expect(reconciler.provisionerSupportsMountOptions(ctx, pv)).To(BeTrue())
		})

		It("returns true when Get StorageClass fails with non-NotFound error", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					StorageClassName: "some-sc",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*storagev1.StorageClass); ok {
							return fmt.Errorf("api timeout")
						}
						return client.Get(ctx, key, obj, opts...)
					},
				}).
				Build()
			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			// Should return true (safe default) when API error occurs
			Expect(reconciler.provisionerSupportsMountOptions(ctx, pv)).To(BeTrue())
		})
	})

	Describe("Reconcile", func() {
		It("skips PV without claimRef", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pv).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: pvName},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify PV was not modified
			updatedPV := &corev1.PersistentVolume{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: pvName}, updatedPV)).To(Succeed())
			Expect(updatedPV.Spec.MountOptions).To(BeEmpty())
		})

		It("skips PV not associated with DocumentDB", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: pvName,
				},
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					ClaimRef: &corev1.ObjectReference{
						Name:      pvcName,
						Namespace: testNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pv, pvc).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: pvName},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("updates PV when associated with DocumentDB and changes needed", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
					UID:       "documentdb-uid",
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}

			trueVal := true
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					UID:       "cluster-uid",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       documentdbName,
							UID:        "documentdb-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "postgresql.cnpg.io/v1",
							Kind:       "Cluster",
							Name:       clusterName,
							UID:        "cluster-uid",
							Controller: &trueVal,
						},
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: pvName,
				},
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					ClaimRef: &corev1.ObjectReference{
						Name:      pvcName,
						Namespace: testNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cluster, pvc, pv).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: pvName},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify PV was updated
			updatedPV := &corev1.PersistentVolume{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: pvName}, updatedPV)).To(Succeed())
			Expect(updatedPV.Spec.PersistentVolumeReclaimPolicy).To(Equal(corev1.PersistentVolumeReclaimRetain))
			Expect(updatedPV.Spec.MountOptions).To(ContainElements("nodev", "noexec", "nosuid"))
			Expect(updatedPV.Labels[util.LabelCluster]).To(Equal(documentdbName))
		})

		It("returns empty result when PV not found", func() {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: "non-existent-pv"},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("returns error when Get PV fails with non-NotFound error", func() {
			expectedErr := fmt.Errorf("api server unavailable")
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*corev1.PersistentVolume); ok {
							return expectedErr
						}
						return client.Get(ctx, key, obj, opts...)
					},
				}).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: pvName},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("api server unavailable"))
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("returns error when PV update fails", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
					UID:       "documentdb-uid",
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}

			trueVal := true
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					UID:       "cluster-uid",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       documentdbName,
							UID:        "documentdb-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "postgresql.cnpg.io/v1",
							Kind:       "Cluster",
							Name:       clusterName,
							UID:        "cluster-uid",
							Controller: &trueVal,
						},
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: pvName,
				},
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					ClaimRef: &corev1.ObjectReference{
						Name:      pvcName,
						Namespace: testNamespace,
					},
				},
			}

			expectedErr := fmt.Errorf("update conflict")
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cluster, pvc, pv).
				WithInterceptorFuncs(interceptor.Funcs{
					Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						if _, ok := obj.(*corev1.PersistentVolume); ok {
							return expectedErr
						}
						return client.Update(ctx, obj, opts...)
					},
				}).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: pvName},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("update conflict"))
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("returns error when findDocumentDBForPV fails due to PVC Get error", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					ClaimRef: &corev1.ObjectReference{
						Name:      pvcName,
						Namespace: testNamespace,
					},
				},
			}

			expectedErr := fmt.Errorf("permission denied")
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pv).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*corev1.PersistentVolumeClaim); ok {
							return expectedErr
						}
						return client.Get(ctx, key, obj, opts...)
					},
				}).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: pvName},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("permission denied"))
			Expect(result).To(Equal(ctrl.Result{}))
		})
	})

	Describe("findPVsForDocumentDB", func() {
		It("returns reconcile requests for PVs with matching documentdb.io/cluster label", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
					UID:       "documentdb-uid",
				},
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: pvName,
					Labels: map[string]string{
						util.LabelCluster: documentdbName,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			requests := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].Name).To(Equal(pvName))
		})

		It("returns empty when no PVs have matching label", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
				},
			}

			// PV labeled for a different DocumentDB cluster
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: pvName,
					Labels: map[string]string{
						util.LabelCluster: "different-cluster",
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pv).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			requests := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(requests).To(BeEmpty())
		})

		It("returns empty when object is not DocumentDB", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			requests := reconciler.findPVsForDocumentDB(ctx, pvc)
			Expect(requests).To(BeNil())
		})

		It("returns nil when listing PVs fails", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*corev1.PersistentVolumeList); ok {
							return fmt.Errorf("pv list error")
						}
						return client.List(ctx, list, opts...)
					},
				}).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			requests := reconciler.findPVsForDocumentDB(ctx, documentdb)
			Expect(requests).To(BeNil())
		})
	})

	Describe("pvPredicate", func() {
		var pred predicate.Predicate

		BeforeEach(func() {
			pred = pvPredicate()
		})

		Describe("CreateFunc", func() {
			It("returns true for bound PV with claimRef", func() {
				pv := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Spec: corev1.PersistentVolumeSpec{
						ClaimRef: &corev1.ObjectReference{Name: pvcName, Namespace: testNamespace},
					},
					Status: corev1.PersistentVolumeStatus{
						Phase: corev1.VolumeBound,
					},
				}
				e := event.CreateEvent{Object: pv}
				Expect(pred.Create(e)).To(BeTrue())
			})

			It("returns false for unbound PV", func() {
				pv := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Spec: corev1.PersistentVolumeSpec{
						ClaimRef: &corev1.ObjectReference{Name: pvcName, Namespace: testNamespace},
					},
					Status: corev1.PersistentVolumeStatus{
						Phase: corev1.VolumeAvailable,
					},
				}
				e := event.CreateEvent{Object: pv}
				Expect(pred.Create(e)).To(BeFalse())
			})

			It("returns false for PV without claimRef", func() {
				pv := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Status: corev1.PersistentVolumeStatus{
						Phase: corev1.VolumeBound,
					},
				}
				e := event.CreateEvent{Object: pv}
				Expect(pred.Create(e)).To(BeFalse())
			})

			It("returns false for non-PV object", func() {
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: testNamespace},
				}
				e := event.CreateEvent{Object: pvc}
				Expect(pred.Create(e)).To(BeFalse())
			})
		})

		Describe("UpdateFunc", func() {
			It("returns true for bound PV with claimRef", func() {
				oldPV := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Status: corev1.PersistentVolumeStatus{
						Phase: corev1.VolumeAvailable,
					},
				}
				newPV := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
					Spec: corev1.PersistentVolumeSpec{
						ClaimRef: &corev1.ObjectReference{Name: pvcName, Namespace: testNamespace},
					},
					Status: corev1.PersistentVolumeStatus{
						Phase: corev1.VolumeBound,
					},
				}
				e := event.UpdateEvent{ObjectOld: oldPV, ObjectNew: newPV}
				Expect(pred.Update(e)).To(BeTrue())
			})

			It("returns false for non-PV object", func() {
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: testNamespace},
				}
				e := event.UpdateEvent{ObjectOld: pvc, ObjectNew: pvc}
				Expect(pred.Update(e)).To(BeFalse())
			})
		})

		Describe("DeleteFunc", func() {
			It("always returns false", func() {
				pv := &corev1.PersistentVolume{
					ObjectMeta: metav1.ObjectMeta{Name: pvName},
				}
				e := event.DeleteEvent{Object: pv}
				Expect(pred.Delete(e)).To(BeFalse())
			})
		})
	})

	Describe("documentDBReclaimPolicyPredicate", func() {
		var pred predicate.Predicate

		BeforeEach(func() {
			pred = documentDBReclaimPolicyPredicate()
		})

		Describe("UpdateFunc", func() {
			It("returns true when reclaim policy changes", func() {
				oldDB := &dbpreview.DocumentDB{
					ObjectMeta: metav1.ObjectMeta{Name: documentdbName, Namespace: testNamespace},
					Spec: dbpreview.DocumentDBSpec{
						Resource: dbpreview.Resource{
							Storage: dbpreview.StorageConfiguration{
								PersistentVolumeReclaimPolicy: "Delete",
							},
						},
					},
				}
				newDB := &dbpreview.DocumentDB{
					ObjectMeta: metav1.ObjectMeta{Name: documentdbName, Namespace: testNamespace},
					Spec: dbpreview.DocumentDBSpec{
						Resource: dbpreview.Resource{
							Storage: dbpreview.StorageConfiguration{
								PersistentVolumeReclaimPolicy: "Retain",
							},
						},
					},
				}
				e := event.UpdateEvent{ObjectOld: oldDB, ObjectNew: newDB}
				Expect(pred.Update(e)).To(BeTrue())
			})

			It("returns false when reclaim policy unchanged", func() {
				oldDB := &dbpreview.DocumentDB{
					ObjectMeta: metav1.ObjectMeta{Name: documentdbName, Namespace: testNamespace},
					Spec: dbpreview.DocumentDBSpec{
						Resource: dbpreview.Resource{
							Storage: dbpreview.StorageConfiguration{
								PersistentVolumeReclaimPolicy: "Retain",
							},
						},
					},
				}
				newDB := &dbpreview.DocumentDB{
					ObjectMeta: metav1.ObjectMeta{Name: documentdbName, Namespace: testNamespace},
					Spec: dbpreview.DocumentDBSpec{
						Resource: dbpreview.Resource{
							Storage: dbpreview.StorageConfiguration{
								PersistentVolumeReclaimPolicy: "Retain",
							},
						},
					},
				}
				e := event.UpdateEvent{ObjectOld: oldDB, ObjectNew: newDB}
				Expect(pred.Update(e)).To(BeFalse())
			})

			It("returns false for non-DocumentDB objects", func() {
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: testNamespace},
				}
				e := event.UpdateEvent{ObjectOld: pvc, ObjectNew: pvc}
				Expect(pred.Update(e)).To(BeFalse())
			})
		})

		Describe("CreateFunc", func() {
			It("always returns false", func() {
				db := &dbpreview.DocumentDB{
					ObjectMeta: metav1.ObjectMeta{Name: documentdbName, Namespace: testNamespace},
				}
				e := event.CreateEvent{Object: db}
				Expect(pred.Create(e)).To(BeFalse())
			})
		})

		Describe("DeleteFunc", func() {
			It("always returns false", func() {
				db := &dbpreview.DocumentDB{
					ObjectMeta: metav1.ObjectMeta{Name: documentdbName, Namespace: testNamespace},
				}
				e := event.DeleteEvent{Object: db}
				Expect(pred.Delete(e)).To(BeFalse())
			})
		})
	})

	Describe("findDocumentDBForPV", func() {
		It("returns nil when PV has no claimRef", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.findDocumentDBForPV(ctx, pv)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("returns nil when PVC is not found", func() {
			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					ClaimRef: &corev1.ObjectReference{
						Name:      "non-existent-pvc",
						Namespace: testNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.findDocumentDBForPV(ctx, pv)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("returns nil when PVC has no CNPG Cluster owner", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
				},
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: &corev1.ObjectReference{
						Name:      pvcName,
						Namespace: testNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pvc).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.findDocumentDBForPV(ctx, pv)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("returns DocumentDB when full ownership chain exists", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
					UID:       "documentdb-uid",
				},
			}

			trueVal := true
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					UID:       "cluster-uid",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       documentdbName,
							UID:        "documentdb-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "postgresql.cnpg.io/v1",
							Kind:       "Cluster",
							Name:       clusterName,
							UID:        "cluster-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			pv := &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvName},
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: &corev1.ObjectReference{
						Name:      pvcName,
						Namespace: testNamespace,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, cluster, pvc).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result, err := reconciler.findDocumentDBForPV(ctx, pv)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())
			Expect(result.Name).To(Equal(documentdbName))
		})
	})

	Describe("findCNPGClusterOwner", func() {
		It("returns nil when PVC has no owner references", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result := reconciler.findCNPGClusterOwner(ctx, pvc)
			Expect(result).To(BeNil())
		})

		It("returns nil when owner is not a CNPG Cluster", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "StatefulSet",
							Name:       "some-statefulset",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result := reconciler.findCNPGClusterOwner(ctx, pvc)
			Expect(result).To(BeNil())
		})

		It("returns cluster when PVC is owned by CNPG Cluster", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					UID:       "cluster-uid",
				},
			}

			trueVal := true
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "postgresql.cnpg.io/v1",
							Kind:       "Cluster",
							Name:       clusterName,
							UID:        "cluster-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cluster).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result := reconciler.findCNPGClusterOwner(ctx, pvc)
			Expect(result).ToNot(BeNil())
			Expect(result.Name).To(Equal(clusterName))
		})

		It("returns nil and continues when Get CNPG Cluster fails with non-NotFound error", func() {
			trueVal := true
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "postgresql.cnpg.io/v1",
							Kind:       "Cluster",
							Name:       clusterName,
							UID:        "cluster-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*cnpgv1.Cluster); ok {
							return fmt.Errorf("api timeout")
						}
						return client.Get(ctx, key, obj, opts...)
					},
				}).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			// Should return nil and continue (error is logged but not returned)
			result := reconciler.findCNPGClusterOwner(ctx, pvc)
			Expect(result).To(BeNil())
		})
	})

	Describe("findDocumentDBOwner", func() {
		It("returns nil when cluster has no owner references", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result := reconciler.findDocumentDBOwner(ctx, cluster)
			Expect(result).To(BeNil())
		})

		It("returns nil when owner is not DocumentDB", func() {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "some-deployment",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result := reconciler.findDocumentDBOwner(ctx, cluster)
			Expect(result).To(BeNil())
		})

		It("returns DocumentDB when cluster is owned by DocumentDB", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentdbName,
					Namespace: testNamespace,
					UID:       "documentdb-uid",
				},
			}

			trueVal := true
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       documentdbName,
							UID:        "documentdb-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			result := reconciler.findDocumentDBOwner(ctx, cluster)
			Expect(result).ToNot(BeNil())
			Expect(result.Name).To(Equal(documentdbName))
		})

		It("returns nil and continues when Get DocumentDB fails with non-NotFound error", func() {
			trueVal := true
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: testNamespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "documentdb.io/v1",
							Kind:       "DocumentDB",
							Name:       documentdbName,
							UID:        "documentdb-uid",
							Controller: &trueVal,
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*dbpreview.DocumentDB); ok {
							return fmt.Errorf("api timeout")
						}
						return client.Get(ctx, key, obj, opts...)
					},
				}).
				Build()

			reconciler := &PersistentVolumeReconciler{Client: fakeClient}

			// Should return nil and continue (error is logged but not returned)
			result := reconciler.findDocumentDBOwner(ctx, cluster)
			Expect(result).To(BeNil())
		})
	})
})
