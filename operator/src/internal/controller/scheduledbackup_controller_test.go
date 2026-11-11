// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
)

var _ = Describe("ScheduledBackup Controller", func() {
	const (
		scheduledBackupName      = "test-scheduled-backup"
		scheduledBackupNamespace = "default"
		clusterName              = "test-cluster"
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
		Expect(dbv1.AddToScheme(scheme)).To(Succeed())
	})

	It("returns error for invalid cron schedule", func() {
		invalidSchedule := "invalid cron expression"
		cluster := &dbv1.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: scheduledBackupNamespace,
			},
		}
		scheduledBackup := &dbv1.ScheduledBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      scheduledBackupName,
				Namespace: scheduledBackupNamespace,
			},
			Spec: dbv1.ScheduledBackupSpec{
				Schedule: invalidSchedule,
				Cluster: cnpgv1.LocalObjectReference{
					Name: clusterName,
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(scheduledBackup, cluster).
			Build()

		reconciler := &ScheduledBackupReconciler{
			Client:   fakeClient,
			Scheme:   scheme,
			Recorder: recorder,
		}

		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      scheduledBackupName,
				Namespace: scheduledBackupNamespace,
			},
		})

		// expect err: invalid cron expression
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid cron expression"))
		Expect(result.Requeue).To(BeFalse())
	})
})
