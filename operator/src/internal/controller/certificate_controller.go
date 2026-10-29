// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/engine"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// CertificateReconciler manages certificate lifecycle for DocumentDB components.
// Today it provisions gateway TLS assets; future work can layer in additional surfaces.
type CertificateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=documentdb.io,resources=dbs,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates;issuers,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates/status;issuers/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *CertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.engine().Reconcile(ctx, req)
}

func (r *CertificateReconciler) engine() *engine.CertificateReconciler[*dbpreview.DocumentDB] {
	return &engine.CertificateReconciler[*dbpreview.DocumentDB]{Client: r.Client, Scheme: r.Scheme, Adapter: product.DocumentDBAdapter{}}
}

func (r *CertificateReconciler) reconcileCertificates(ctx context.Context, ddb *dbpreview.DocumentDB) (ctrl.Result, error) {
	return r.engine().ReconcileCertificates(ctx, ddb)
}

func (r *CertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbpreview.DocumentDB{}).
		Owns(&cmapi.Certificate{}).
		Owns(&cmapi.Issuer{}).
		Named("certificate-controller").
		Complete(r)
}
