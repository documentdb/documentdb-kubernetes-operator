// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"slices"
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
	"github.com/documentdb/documentdb-operator/internal/product"
)

// CertificateReconciler provisions the gateway TLS certificate of a product
// cluster: a self-signed cert-manager Issuer and Certificate (default), a
// user-provided Secret, or a cert-manager Certificate from a user Issuer.
type CertificateReconciler[T client.Object] struct {
	client.Client
	Scheme  *runtime.Scheme
	Adapter product.ClusterAdapter[T]
}

// Reconcile reconciles the gateway TLS assets of the custom resource.
func (r *CertificateReconciler[T]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	obj := r.Adapter.New()
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	res, err := r.ReconcileCertificates(ctx, obj)
	if err != nil {
		logger.Error(err, "failed to reconcile certificate resources")
	}
	return res, err
}

// ReconcileCertificates reconciles gateway TLS for obj by mode.
func (r *CertificateReconciler[T]) ReconcileCertificates(ctx context.Context, obj T) (ctrl.Result, error) {
	// TLS is always enabled. When tls or tls.gateway is unset, default to SelfSigned
	// so the operator provisions a managed cert (issue #356 - no plaintext path).
	gatewayCfg := r.Adapter.Spec(obj).GatewayTLS
	if gatewayCfg == nil {
		gatewayCfg = &dbpreview.GatewayTLS{Mode: "SelfSigned"}
	}

	// Empty mode defaults to SelfSigned for security - TLS is always enabled
	mode := gatewayCfg.Mode
	if mode == "" {
		mode = "SelfSigned"
	}

	if r.Adapter.Status(obj).TLS == nil {
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Ready = false
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	switch mode {
	case "SelfSigned":
		return r.ensureSelfSignedCert(ctx, obj)
	case "Provided":
		return r.ensureProvidedSecret(ctx, obj)
	case "CertManager":
		return r.ensureCertManagerManagedCert(ctx, obj)
	default:
		// Unknown/legacy mode (e.g. "Disabled" from a pre-#357 resource still in etcd
		// — apiserver does not re-validate stored objects against the trimmed enum).
		// Fail-safe to SelfSigned so the gateway never serves plaintext.
		log.FromContext(ctx).Info(
			"unsupported TLS gateway mode; defaulting to SelfSigned to preserve no-plaintext invariant",
			"requestedMode", mode,
			"hint", "Update spec.tls.gateway.mode to SelfSigned, CertManager, or Provided.",
		)
		return r.ensureSelfSignedCert(ctx, obj)
	}
}

func (r *CertificateReconciler[T]) ensureProvidedSecret(ctx context.Context, obj T) (ctrl.Result, error) {
	gatewayCfg := r.Adapter.Spec(obj).GatewayTLS
	if gatewayCfg == nil || gatewayCfg.Provided == nil || gatewayCfg.Provided.SecretName == "" {
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Message = "Provided TLS secret name missing"
			status.Ready = false
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: gatewayCfg.Provided.SecretName, Namespace: obj.GetNamespace()}, secret); err != nil {
		if errors.IsNotFound(err) {
			if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
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
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.Message = "Provided secret missing tls.crt"
			status.SecretName = gatewayCfg.Provided.SecretName
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}
	if _, keyOk := secret.Data["tls.key"]; !keyOk {
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.Message = "Provided secret missing tls.key"
			status.SecretName = gatewayCfg.Provided.SecretName
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}

	if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
		status.Ready = true
		status.SecretName = gatewayCfg.Provided.SecretName
		status.Message = "Using provided TLS secret"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CertificateReconciler[T]) ensureCertManagerManagedCert(ctx context.Context, obj T) (ctrl.Result, error) {
	gatewayCfg := r.Adapter.Spec(obj).GatewayTLS
	if gatewayCfg == nil || gatewayCfg.CertManager == nil {
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
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
		secretName = obj.GetName() + "-gateway-cert-tls"
	}

	serviceBase, err := gatewayServiceName(ctx, r.Client, r.Adapter.Profile().Naming, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	baseDNS := []string{serviceBase, serviceBase + "." + obj.GetNamespace(), serviceBase + "." + obj.GetNamespace() + ".svc"}
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

	certName := obj.GetName() + "-gateway-cert"
	cert := &cmapi.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: certName, Namespace: obj.GetNamespace()}, cert); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		cert = &cmapi.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: certName, Namespace: obj.GetNamespace()},
			Spec: cmapi.CertificateSpec{
				SecretName:  secretName,
				DNSNames:    finalDNS,
				IssuerRef:   issuerRef,
				Duration:    &metav1.Duration{Duration: 90 * 24 * time.Hour},
				RenewBefore: &metav1.Duration{Duration: 15 * 24 * time.Hour},
				Usages:      []cmapi.KeyUsage{cmapi.UsageServerAuth},
			},
		}
		if err := controllerutil.SetControllerReference(obj, cert, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.SecretName = secretName
			status.Message = "Creating cert-manager certificate"
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}
	if res, done, err := r.ensureServiceDNSNames(ctx, obj, cert, baseDNS); done {
		return res, err
	}

	for _, cond := range cert.Status.Conditions {
		if certificateReady(cert, cond) {
			if !r.Adapter.Status(obj).TLS.Ready {
				if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
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

	if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
		status.Ready = false
		status.SecretName = cert.Spec.SecretName
		status.Message = "Waiting for cert-manager certificate to become ready"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
}

func (r *CertificateReconciler[T]) ensureSelfSignedCert(ctx context.Context, obj T) (ctrl.Result, error) {
	namespace := obj.GetNamespace()
	issuerName := obj.GetName() + "-gateway-selfsigned"
	certName := obj.GetName() + "-gateway-cert"
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
		if err := controllerutil.SetControllerReference(obj, issuer, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, issuer); err != nil {
			return ctrl.Result{}, err
		}
	}

	serviceBase, err := gatewayServiceName(ctx, r.Client, r.Adapter.Profile().Naming, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
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
		if err := controllerutil.SetControllerReference(obj, cert, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.SecretName = secretName
			status.Message = "Creating self-signed certificate"
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
	}
	if res, done, err := r.ensureServiceDNSNames(ctx, obj, cert, dnsNames); done {
		return res, err
	}

	for _, cond := range cert.Status.Conditions {
		if certificateReady(cert, cond) {
			if !r.Adapter.Status(obj).TLS.Ready {
				if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
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

	if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
		status.Ready = false
		status.SecretName = cert.Spec.SecretName
		status.Message = "Waiting for gateway TLS certificate to become ready"
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: RequeueAfterShort}, nil
}

// ensureServiceDNSNames adds to an existing managed Certificate any gateway
// Service DNS name it lacks, for example after the Service name changes. It
// only adds names, so hostnames clients already use stay valid. A Certificate
// controlled by another resource is neither modified nor used; one with no
// controller is used as before. done reports that the caller should return
// with the given result. Readiness checks require a Ready condition observed
// for the Certificate's current generation, so the added names are issued
// before TLS is reported ready again.
func (r *CertificateReconciler[T]) ensureServiceDNSNames(ctx context.Context, obj T, cert *cmapi.Certificate, serviceDNS []string) (ctrl.Result, bool, error) {
	if ref := metav1.GetControllerOf(cert); ref != nil && ref.UID != obj.GetUID() {
		// Left by a deleted custom resource of the same name; wait for
		// garbage collection instead of using its status or Secret.
		if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
			status.Ready = false
			status.Message = "Waiting for a gateway certificate owned by another resource to be removed"
		}); err != nil {
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterShort}, true, nil
	}
	if !controlledBy(cert, obj) {
		return ctrl.Result{}, false, nil
	}
	var missing []string
	for _, n := range serviceDNS {
		if !slices.Contains(cert.Spec.DNSNames, n) {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return ctrl.Result{}, false, nil
	}
	before := cert.DeepCopy()
	cert.Spec.DNSNames = append(cert.Spec.DNSNames, missing...)
	if err := r.Patch(ctx, cert, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, true, err
	}
	if err := r.updateTLSStatus(ctx, obj, func(status *dbpreview.TLSStatus) {
		status.Ready = false
		status.SecretName = cert.Spec.SecretName
		status.Message = "Updating gateway certificate DNS names"
	}); err != nil {
		return ctrl.Result{}, true, err
	}
	return ctrl.Result{RequeueAfter: RequeueAfterShort}, true, nil
}

// certificateReady reports whether cond is a Ready=True condition for cert's
// current generation, so a spec change (such as added DNS names) is issued
// before TLS is reported ready.
func certificateReady(cert *cmapi.Certificate, cond cmapi.CertificateCondition) bool {
	return cond.Type == cmapi.CertificateConditionReady && cond.Status == cmmeta.ConditionTrue &&
		cond.ObservedGeneration >= cert.Generation
}

func (r *CertificateReconciler[T]) updateTLSStatus(ctx context.Context, obj T, mutate func(*dbpreview.TLSStatus)) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := r.Adapter.New()
		if err := r.Get(ctx, key, current); err != nil {
			return err
		}
		status := r.Adapter.Status(current)
		if status.TLS == nil {
			status.TLS = &dbpreview.TLSStatus{}
		}
		mutate(status.TLS)
		r.Adapter.SetStatus(current, status)
		if err := r.Status().Update(ctx, current); err != nil {
			return err
		}
		r.Adapter.SetStatus(obj, r.Adapter.Status(current))
		// The version and annotations must come from the same snapshot:
		// advancing only the version would let a later naming patch overwrite
		// a choice recorded by another reconciler.
		obj.SetAnnotations(current.GetAnnotations())
		obj.SetResourceVersion(current.GetResourceVersion())
		return nil
	})
}

// SetupWithManager registers the reconciler as controller name.
func (r *CertificateReconciler[T]) SetupWithManager(mgr ctrl.Manager, name string) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.Adapter.New()).
		Owns(&cmapi.Certificate{}).
		Owns(&cmapi.Issuer{}).
		Named(name).
		Complete(r)
}
