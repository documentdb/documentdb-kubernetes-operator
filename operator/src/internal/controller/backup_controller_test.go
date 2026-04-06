// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/telemetry"
)

var _ = Describe("Backup Controller", func() {
	const (
		backupName      = "test-backup"
		backupNamespace = "default"
		clusterName     = "test-cluster"
	)

	var (
		ctx      context.Context
		scheme   *runtime.Scheme
		recorder record.EventRecorder
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		recorder = record.NewFakeRecorder(10)
		// register both preview and CNPG types used by the controller
		Expect(dbpreview.AddToScheme(scheme)).To(Succeed())
		Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("createCNPGBackup", func() {
		It("creates a CNPG Backup with expected spec and owner reference and requeues", func() {
			// fake client + reconciler
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			reconciler := &BackupReconciler{
				Telemetry: telemetry.NoopBackupTelemetry{},
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			// input dbpreview Backup
			backup := &dbpreview.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Spec: dbpreview.BackupSpec{
					Cluster: cnpgv1.LocalObjectReference{Name: clusterName},
				},
			}

			// Create the associated DocumentDB cluster
			cluster := &dbpreview.DocumentDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: backupNamespace,
				},
			}
			Expect(fakeClient.Create(ctx, cluster)).To(Succeed())

			// Call under test
			res, err := reconciler.createCNPGBackup(ctx, backup, cluster)
			Expect(err).ToNot(HaveOccurred())
			// controller uses a 5s requeue
			Expect(res.RequeueAfter).To(Equal(5 * time.Second))

			// Verify only one CNPG Backup exists in the fake client
			cnpgBackupList := &cnpgv1.BackupList{}
			Expect(fakeClient.List(ctx, cnpgBackupList)).To(Succeed())
			Expect(len(cnpgBackupList.Items)).To(Equal(1))
			cnpgBackup := &cnpgBackupList.Items[0]

			Expect(cnpgBackup.Name).To(Equal(backupName))
			Expect(cnpgBackup.Namespace).To(Equal(backupNamespace))

			// Check spec fields
			Expect(cnpgBackup.Spec.Method).To(Equal(cnpgv1.BackupMethodVolumeSnapshot))
			Expect(cnpgBackup.Spec.Cluster.Name).To(Equal(clusterName))

			// Owner reference should reference the dbpreview Backup (by name)
			Expect(len(cnpgBackup.OwnerReferences)).To(Equal(1))
			ownerReference := cnpgBackup.OwnerReferences[0]
			Expect(ownerReference.Name).To(Equal(backup.Name))
			Expect(ownerReference.Controller).ToNot(BeNil())
			Expect(*ownerReference.Controller).To(BeTrue())
		})
	})

	Describe("updateBackupStatus", func() {
		It("requeues until expiration time when CNPG Backup phase is Completed", func() {
			backup := &dbpreview.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Spec: dbpreview.BackupSpec{
					Cluster: cnpgv1.LocalObjectReference{Name: clusterName},
				},
				Status: dbpreview.BackupStatus{
					Phase: cnpgv1.BackupPhasePending,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(backup).
				WithStatusSubresource(&dbpreview.Backup{}).
				Build()

			reconciler := &BackupReconciler{
				Telemetry: telemetry.NoopBackupTelemetry{},
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			now := time.Now().UTC()
			cnpgBackup := &cnpgv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Status: cnpgv1.BackupStatus{
					Phase:     cnpgv1.BackupPhaseCompleted,
					StartedAt: &metav1.Time{Time: now.Add(-time.Minute)},
					StoppedAt: &metav1.Time{Time: now},
				},
			}

			res, err := reconciler.updateBackupStatus(ctx, backup, cnpgBackup, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.RequeueAfter).NotTo(Equal(0))

			// Verify status was updated with times
			updated := &dbpreview.Backup{}
			Expect(fakeClient.Get(ctx, client.ObjectKey{Name: backupName, Namespace: backupNamespace}, updated)).To(Succeed())
			Expect(string(updated.Status.Phase)).To(Equal(string(cnpgv1.BackupPhaseCompleted)))
			Expect(updated.Status.StartedAt).ToNot(BeNil())
			Expect(updated.Status.StoppedAt).ToNot(BeNil())
			Expect(updated.Status.StartedAt.Time.Unix()).To(Equal(cnpgBackup.Status.StartedAt.Time.Unix()))
			Expect(updated.Status.StoppedAt.Time.Unix()).To(Equal(cnpgBackup.Status.StoppedAt.Time.Unix()))
		})

		It("stops reconciling (returns zero result) when CNPG Backup phase is Failed", func() {
			backup := &dbpreview.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Spec: dbpreview.BackupSpec{
					Cluster: cnpgv1.LocalObjectReference{Name: clusterName},
				},
				Status: dbpreview.BackupStatus{
					Phase:     cnpgv1.BackupPhaseStarted,
					StartedAt: &metav1.Time{Time: time.Now().UTC().Add(-5 * time.Minute)},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(backup).
				WithStatusSubresource(&dbpreview.Backup{}).
				Build()

			reconciler := &BackupReconciler{
				Telemetry: telemetry.NoopBackupTelemetry{},
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: recorder,
			}

			startTime := time.Now().UTC().Add(-10 * time.Minute)
			stopTime := time.Now().UTC()
			cnpgBackup := &cnpgv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Status: cnpgv1.BackupStatus{
					Phase:     cnpgv1.BackupPhaseFailed,
					StartedAt: &metav1.Time{Time: startTime},
					StoppedAt: &metav1.Time{Time: stopTime},
					Error:     "connection timeout",
				},
			}

			res, err := reconciler.updateBackupStatus(ctx, backup, cnpgBackup, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(res.RequeueAfter).NotTo(Equal(0))

			// Verify status was updated with error
			updated := &dbpreview.Backup{}
			Expect(fakeClient.Get(ctx, client.ObjectKey{Name: backupName, Namespace: backupNamespace}, updated)).To(Succeed())
			Expect(string(updated.Status.Phase)).To(Equal(string(cnpgv1.BackupPhaseFailed)))
			Expect(updated.Status.Message).To(Equal("connection timeout"))
			Expect(updated.Status.StartedAt).ToNot(BeNil())
			Expect(updated.Status.StoppedAt).ToNot(BeNil())
			Expect(updated.Status.StartedAt.Time.Unix()).To(Equal(startTime.Unix()))
			Expect(updated.Status.StoppedAt.Time.Unix()).To(Equal(stopTime.Unix()))
		})

		It("does not update status when phase hasn't changed", func() {
			backup := &dbpreview.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Spec: dbpreview.BackupSpec{
					Cluster: cnpgv1.LocalObjectReference{Name: clusterName},
				},
				Status: dbpreview.BackupStatus{
					Phase: cnpgv1.BackupPhaseRunning,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(backup).
				WithStatusSubresource(&dbpreview.Backup{}).
				Build()

			reconciler := &BackupReconciler{
				Telemetry: telemetry.NoopBackupTelemetry{},
				Client: fakeClient,
				Scheme: scheme,
			}

			// CNPG Backup has same phase
			cnpgBackup := &cnpgv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      backupName,
					Namespace: backupNamespace,
				},
				Status: cnpgv1.BackupStatus{
					Phase: cnpgv1.BackupPhaseRunning,
				},
			}

			res, err := reconciler.updateBackupStatus(ctx, backup, cnpgBackup, nil)
			Expect(err).ToNot(HaveOccurred())
			// Still in progress, requeue
			Expect(res.RequeueAfter).To(Equal(10 * time.Second))

			// Phase should remain unchanged
			updated := &dbpreview.Backup{}
			Expect(fakeClient.Get(ctx, client.ObjectKey{Name: backupName, Namespace: backupNamespace}, updated)).To(Succeed())
			Expect(string(updated.Status.Phase)).To(Equal(string(cnpgv1.BackupPhaseRunning)))
		})
	})
})
