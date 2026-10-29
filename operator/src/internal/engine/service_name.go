// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"slices"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/internal/product"
)

var certificateGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}

// gatewayServiceName returns the gateway Service name for obj and keeps it
// stable for the life of the custom resource.
//
// A name that fits 63 characters is always prefix+name. A longer name is
// chosen once and recorded in the ServiceNameAnnotation, so deleting and
// recreating the Service (or the operator restarting) does not change the
// hostname clients use. The first choice keeps the plainly truncated name an
// earlier release used when this cluster still shows it in use (a Service or
// managed gateway Certificate it controls) and no other resource controls a
// Service by that name; otherwise it is the hashed name. A recorded legacy
// name is dropped in favor of the hashed name once another resource controls
// that Service. Managed certificates follow the returned name (see
// CertificateReconciler), so a change never leaves TLS without a matching SAN.
func gatewayServiceName(ctx context.Context, c client.Client, naming product.NamingProfile, obj client.Object) (string, error) {
	hashed := naming.ServiceName(obj.GetName())
	if len(naming.ServicePrefix+obj.GetName()) <= 63 {
		return hashed, nil
	}
	legacy, hasLegacy := naming.LegacyServiceName(obj.GetName())
	key := naming.ServiceNameAnnotation()
	recorded := obj.GetAnnotations()[key]
	if recorded == hashed {
		return hashed, nil
	}

	chosen := hashed
	// Reconstruct lost annotations from the actual endpoint before consulting
	// a certificate, whose add-only SANs may retain both historical names.
	hashedService := &corev1.Service{}
	if recorded != legacy {
		if err := c.Get(ctx, types.NamespacedName{Name: hashed, Namespace: obj.GetNamespace()}, hashedService); err != nil && !apierrors.IsNotFound(err) {
			return "", err
		}
	}
	if hasLegacy && !controlledBy(hashedService, obj) {
		available, inUse, err := legacyServiceNameState(ctx, c, obj, legacy)
		if err != nil {
			return "", err
		}
		if available && (recorded == legacy || inUse) {
			chosen = legacy
		}
	}
	if recorded == chosen {
		return chosen, nil
	}

	// Record the choice. Values other than the two valid names, and a legacy
	// name another resource now holds, are replaced here. The optimistic lock
	// makes concurrent reconcilers agree: a loser requeues and reads the
	// winner's choice.
	before, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return chosen, nil
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[key] = chosen
	obj.SetAnnotations(annotations)
	if err := c.Patch(ctx, obj, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return "", err
	}
	return chosen, nil
}

// legacyServiceNameState reports whether the legacy Service name is available
// to obj (no other resource controls a Service by that name) and whether obj
// shows it in use: it controls that Service, or a managed gateway Certificate
// listing it. The Certificate is read as unstructured so callers need not
// register cert-manager types, and a missing cert-manager CRD counts as none.
func legacyServiceNameState(ctx context.Context, c client.Reader, obj client.Object, legacy string) (available, inUse bool, err error) {
	ns := obj.GetNamespace()
	svc := &corev1.Service{}
	switch err := c.Get(ctx, types.NamespacedName{Name: legacy, Namespace: ns}, svc); {
	case err == nil:
		if ref := metav1.GetControllerOf(svc); ref != nil && ref.UID != obj.GetUID() {
			return false, false, nil
		}
		if controlledBy(svc, obj) {
			return true, true, nil
		}
	case !apierrors.IsNotFound(err):
		return false, false, err
	}

	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(certificateGVK)
	switch err := c.Get(ctx, types.NamespacedName{Name: obj.GetName() + "-gateway-cert", Namespace: ns}, cert); {
	case err == nil:
		dnsNames, _, _ := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
		return true, controlledBy(cert, obj) && slices.Contains(dnsNames, legacy), nil
	case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
		return true, false, nil
	default:
		return false, false, err
	}
}

func controlledBy(o, owner metav1.Object) bool {
	ref := metav1.GetControllerOf(o)
	return ref != nil && ref.UID == owner.GetUID()
}

// gatewayServiceTLSReady holds a long-name endpoint while its managed
// certificate is being migrated. Reading unstructured keeps the cluster
// reconciler usable without registering cert-manager types.
func gatewayServiceTLSReady(ctx context.Context, c client.Reader, obj client.Object, serviceName string) (bool, error) {
	raw := &unstructured.Unstructured{}
	raw.SetGroupVersionKind(certificateGVK)
	if err := c.Get(ctx, types.NamespacedName{Name: obj.GetName() + "-gateway-cert", Namespace: obj.GetNamespace()}, raw); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return true, nil
		}
		return false, err
	}
	cert := &cmapi.Certificate{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, cert); err != nil {
		return false, err
	}
	if ref := metav1.GetControllerOf(cert); ref != nil && ref.UID != obj.GetUID() {
		return false, nil
	}
	for _, name := range []string{serviceName, serviceName + "." + obj.GetNamespace(), serviceName + "." + obj.GetNamespace() + ".svc"} {
		if !slices.Contains(cert.Spec.DNSNames, name) {
			return false, nil
		}
	}
	for _, condition := range cert.Status.Conditions {
		if certificateReady(cert, condition) {
			return true, nil
		}
	}
	return false, nil
}
