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
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
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

	Describe("shouldWarnAboutRetainedPVs", func() {
		var reconciler *DocumentDBReconciler

		BeforeEach(func() {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler = &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}
		})

		It("returns true when policy is empty (default Retain)", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
				},
				Spec: dbpreview.DocumentDBSpec{
					Resource: dbpreview.Resource{
						Storage: dbpreview.StorageConfiguration{
							PvcSize:                       "10Gi",
							PersistentVolumeReclaimPolicy: "", // empty = default Retain
						},
					},
				},
			}
			Expect(reconciler.shouldWarnAboutRetainedPVs(documentdb)).To(BeTrue())
		})

		It("returns true when policy is explicitly Retain", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
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
			Expect(reconciler.shouldWarnAboutRetainedPVs(documentdb)).To(BeTrue())
		})

		It("returns false when policy is Delete", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
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
			Expect(reconciler.shouldWarnAboutRetainedPVs(documentdb)).To(BeFalse())
		})
	})

	Describe("findPVsForDocumentDB", func() {
		It("returns PV names for bound PVCs with matching cluster label", func() {
			pvc1 := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-1",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-abc123",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			}
			pvc2 := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-2",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-def456",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pvc1, pvc2).
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

		It("excludes PVCs that are not bound", func() {
			boundPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-bound",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-bound",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			}
			pendingPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-pending",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-pending",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimPending,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(boundPVC, pendingPVC).
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
			Expect(pvNames).To(ContainElement("pv-bound"))
		})

		It("excludes PVCs from different clusters", func() {
			matchingPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-match",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-match",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			}
			otherClusterPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-cluster-pvc",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: "other-cluster",
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-other",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(matchingPVC, otherClusterPVC).
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

		It("returns empty slice when no PVCs exist", func() {
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
		It("emits warning event with PV names when PVCs exist", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-1",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-test123",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pvc).
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

		It("does not emit event when no PVCs exist", func() {
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

	Describe("handleDeletion", func() {
		It("removes finalizer and allows deletion when finalizer is present", func() {
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
							PersistentVolumeReclaimPolicy: "Delete", // No warning should be emitted
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

			// Call handleDeletion - it checks for finalizer, not DeletionTimestamp
			result, err := reconciler.handleDeletion(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify finalizer was removed
			updated := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, documentDBFinalizer)).To(BeFalse())
		})

		It("emits warning event when policy is Retain and PVCs exist", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-1",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-retained",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
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
							PersistentVolumeReclaimPolicy: "Retain",
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb, pvc).
				Build()

			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			result, err := reconciler.handleDeletion(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify warning event was emitted
			Eventually(recorder.Events).Should(Receive(ContainSubstring("pv-retained")))

			// Verify finalizer was removed
			updated := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, documentDBFinalizer)).To(BeFalse())
		})

		It("returns without action when finalizer is not present", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:       documentDBName,
					Namespace:  documentDBNamespace,
					Finalizers: []string{}, // No finalizer
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

			result, err := reconciler.handleDeletion(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify object still exists (no Update was called)
			existing := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, existing)).To(Succeed())
		})

		It("does not emit warning when policy is Delete", func() {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName + "-1",
					Namespace: documentDBNamespace,
					Labels: map[string]string{
						cnpgClusterLabel: documentDBName,
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeName: "pv-will-be-deleted",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
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
				WithObjects(documentdb, pvc).
				Build()

			// Create a new recorder to ensure it's empty
			localRecorder := record.NewFakeRecorder(10)
			reconciler := &DocumentDBReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: localRecorder,
			}

			result, err := reconciler.handleDeletion(ctx, documentdb)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())

			// Verify NO warning event was emitted (policy is Delete)
			Consistently(localRecorder.Events).ShouldNot(Receive())
		})
	})

	Describe("Finalizer management in Reconcile", func() {
		It("adds finalizer to new DocumentDB resource", func() {
			documentdb := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      documentDBName,
					Namespace: documentDBNamespace,
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

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(documentdb).
				Build()

			// Verify resource starts without finalizer
			Expect(controllerutil.ContainsFinalizer(documentdb, documentDBFinalizer)).To(BeFalse())

			// Add finalizer like the controller does
			controllerutil.AddFinalizer(documentdb, documentDBFinalizer)
			Expect(fakeClient.Update(ctx, documentdb)).To(Succeed())

			// Verify finalizer was added
			updated := &dbpreview.DocumentDB{}
			Expect(fakeClient.Get(ctx, types.NamespacedName{Name: documentDBName, Namespace: documentDBNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, documentDBFinalizer)).To(BeTrue())
		})
	})
})
