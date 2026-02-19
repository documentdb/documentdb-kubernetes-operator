// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

var _ = Describe("DocumentDB Controller", func() {
	const (
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
})
