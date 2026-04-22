// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package controller

import (
	"context"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// CertificateReconciler manages certificate lifecycle for DocumentDB components.
// Today it provisions gateway TLS assets; future work can layer in additional surfaces.
type CertificateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=documentdb.io,resources=dbs,verbs=get;list;watch
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates;issuers,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates/status;issuers/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *CertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ddb := &dbpreview.DocumentDB{}
	if err := r.Get(ctx, req.NamespacedName, ddb); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	res, err := r.reconcileCertificates(ctx, ddb)
	if err != nil {
		logger.Error(err, "failed to reconcile certificate resources")
	}
	return res, err
}

func (r *CertificateReconciler) reconcileCertificates(ctx context.Context, ddb *dbpreview.DocumentDB) (ctrl.Result, error) {
	if ddb.Spec.TLS == nil || ddb.Spec.TLS.Gateway == nil {
		return ctrl.Result{}, nil
	}

	gatewayCfg := ddb.Spec.TLS.Gateway

	// Empty mode defaults to SelfSigned for security - TLS is always enabled
	mode := gatewayCfg.Mode
	if mode == "" {
		mode = "SelfSigned"
	}

	if ddb.Status.TLS == nil {
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Ready = false
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	switch mode {
	case "SelfSigned":
		return r.ensureSelfSignedCert(ctx, ddb)
	case "Provided":
		return r.ensureProvidedSecret(ctx, ddb)
	case "CertManager":
		return r.ensureCertManagerManagedCert(ctx, ddb)
	default:
		return ctrl.Result{}, nil
	}
}

func (r *CertificateReconciler) ensureProvidedSecret(ctx context.Context, ddb *dbpreview.DocumentDB) (ctrl.Result, error) {
	gatewayCfg := ddb.Spec.TLS.Gateway
	if gatewayCfg == nil || gatewayCfg.Provided == nil || gatewayCfg.Provided.SecretName == "" {
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Message = "Provided TLS secret name missing"
			status.Ready = false
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: gatewayCfg.Provided.SecretName, Namespace: ddb.Namespace}, secret); err != nil {
		if errors.IsNotFound(err) {
			if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
				status.Ready = false
				status.SecretName = gatewayCfg.Provided.SecretName
				status.Message = "Waiting for provided TLS secret"
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
		}
		return ctrl.Result{}, err
	}

	if _, crtOk := secret.Data["tls.crt"]; !crtOk {
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.Message = "Provided secret missing tls.crt"
			status.SecretName = gatewayCfg.Provided.SecretName
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}
	if _, keyOk := secret.Data["tls.key"]; !keyOk {
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.Message = "Provided secret missing tls.key"
			status.SecretName = gatewayCfg.Provided.SecretName
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
		status.Ready = true
		status.SecretName = gatewayCfg.Provided.SecretName
		status.Message = "Using provided TLS secret"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CertificateReconciler) ensureCertManagerManagedCert(ctx context.Context, ddb *dbpreview.DocumentDB) (ctrl.Result, error) {
	gatewayCfg := ddb.Spec.TLS.Gateway
	if gatewayCfg == nil || gatewayCfg.CertManager == nil {
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.Message = "CertManager configuration missing"
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	cmCfg := gatewayCfg.CertManager

	issuerRef := cmmeta.ObjectReference{Name: cmCfg.IssuerRef.Name}
	if cmCfg.IssuerRef.Kind != "" {
		issuerRef.Kind = cmCfg.IssuerRef.Kind
	} else {
		issuerRef.Kind = "Issuer"
	}
	if cmCfg.IssuerRef.Group != "" {
		issuerRef.Group = cmCfg.IssuerRef.Group
	} else {
		issuerRef.Group = "cert-manager.io"
	}

	secretName := cmCfg.SecretName
	if secretName == "" {
		secretName = ddb.Name + "-gateway-cert-tls"
	}

	serviceBase := util.DOCUMENTDB_SERVICE_PREFIX + ddb.Name
	baseDNS := []string{serviceBase, serviceBase + "." + ddb.Namespace, serviceBase + "." + ddb.Namespace + ".svc"}
	dnsSet := map[string]struct{}{}
	finalDNS := []string{}
	for _, n := range cmCfg.DNSNames {
		if _, ok := dnsSet[n]; !ok && n != "" {
			dnsSet[n] = struct{}{}
			finalDNS = append(finalDNS, n)
		}
	}
	for _, n := range baseDNS {
		if _, ok := dnsSet[n]; !ok {
			dnsSet[n] = struct{}{}
			finalDNS = append(finalDNS, n)
		}
	}

	certName := ddb.Name + "-gateway-cert"
	cert := &cmapi.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: certName, Namespace: ddb.Namespace}, cert); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		cert = &cmapi.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: certName, Namespace: ddb.Namespace},
			Spec: cmapi.CertificateSpec{
				SecretName:  secretName,
				DNSNames:    finalDNS,
				IssuerRef:   issuerRef,
				Duration:    &metav1.Duration{Duration: 90 * 24 * time.Hour},
				RenewBefore: &metav1.Duration{Duration: 15 * 24 * time.Hour},
				Usages:      []cmapi.KeyUsage{cmapi.UsageServerAuth},
			},
		}
		if err := controllerutil.SetControllerReference(ddb, cert, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.SecretName = secretName
			status.Message = "Creating cert-manager certificate"
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	for _, cond := range cert.Status.Conditions {
		if cond.Type == cmapi.CertificateConditionReady && cond.Status == cmmeta.ConditionTrue {
			if !ddb.Status.TLS.Ready {
				if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
					status.Ready = true
					status.SecretName = cert.Spec.SecretName
					status.Message = "Gateway TLS certificate ready (cert-manager)"
				}); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}
	}

	if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
		status.Ready = false
		status.SecretName = cert.Spec.SecretName
		status.Message = "Waiting for cert-manager certificate to become ready"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
}

func (r *CertificateReconciler) ensureSelfSignedCert(ctx context.Context, ddb *dbpreview.DocumentDB) (ctrl.Result, error) {
	namespace := ddb.Namespace
	issuerName := ddb.Name + "-gateway-selfsigned"
	certName := ddb.Name + "-gateway-cert"
	secretName := certName + "-tls"

	issuer := &cmapi.Issuer{}
	if err := r.Get(ctx, types.NamespacedName{Name: issuerName, Namespace: namespace}, issuer); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		issuer = &cmapi.Issuer{
			ObjectMeta: metav1.ObjectMeta{Name: issuerName, Namespace: namespace},
			Spec:       cmapi.IssuerSpec{IssuerConfig: cmapi.IssuerConfig{SelfSigned: &cmapi.SelfSignedIssuer{}}},
		}
		if err := controllerutil.SetControllerReference(ddb, issuer, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, issuer); err != nil {
			return ctrl.Result{}, err
		}
	}

	serviceBase := util.DOCUMENTDB_SERVICE_PREFIX + ddb.Name
	dnsNames := []string{
		serviceBase,
		serviceBase + "." + namespace,
		serviceBase + "." + namespace + ".svc",
	}

	cert := &cmapi.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: certName, Namespace: namespace}, cert); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		cert = &cmapi.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: certName, Namespace: namespace},
			Spec: cmapi.CertificateSpec{
				SecretName:  secretName,
				Duration:    &metav1.Duration{Duration: 90 * 24 * time.Hour},
				RenewBefore: &metav1.Duration{Duration: 15 * 24 * time.Hour},
				DNSNames:    dnsNames,
				IssuerRef:   cmmeta.ObjectReference{Name: issuerName, Kind: "Issuer", Group: "cert-manager.io"},
				Usages:      []cmapi.KeyUsage{cmapi.UsageServerAuth},
			},
		}
		if err := controllerutil.SetControllerReference(ddb, cert, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.SecretName = secretName
			status.Message = "Creating self-signed certificate"
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	for _, cond := range cert.Status.Conditions {
		if cond.Type == cmapi.CertificateConditionReady && cond.Status == cmmeta.ConditionTrue {
			if !ddb.Status.TLS.Ready {
				if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
					status.Ready = true
					status.SecretName = cert.Spec.SecretName
					status.Message = "Gateway TLS certificate ready"
				}); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}
	}

	if err := r.updateTLSStatus(ctx, ddb, func(status *dbpreview.TLSStatus) {
		status.Ready = false
		status.SecretName = cert.Spec.SecretName
		status.Message = "Waiting for gateway TLS certificate to become ready"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
}

func (r *CertificateReconciler) updateTLSStatus(ctx context.Context, ddb *dbpreview.DocumentDB, mutate func(*dbpreview.TLSStatus)) error {
	key := types.NamespacedName{Name: ddb.Name, Namespace: ddb.Namespace}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &dbpreview.DocumentDB{}
		if err := r.Get(ctx, key, current); err != nil {
			return err
		}
		if current.Status.TLS == nil {
			current.Status.TLS = &dbpreview.TLSStatus{}
		}
		mutate(current.Status.TLS)
		if err := r.Status().Update(ctx, current); err != nil {
			return err
		}
		ddb.Status = current.Status
		return nil
	})
}

func (r *CertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbpreview.DocumentDB{}).
		Owns(&cmapi.Certificate{}).
		Owns(&cmapi.Issuer{}).
		Named("certificate-controller").
		Complete(r)
}
