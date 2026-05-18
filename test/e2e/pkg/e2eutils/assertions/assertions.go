// Package assertions returns checker closures for use with Gomega's
// Eventually / Consistently. Each helper yields a `func() error` so it
// can be awaited with `Eventually(fn, timeout, poll).Should(Succeed())`
// without this package pulling in ginkgo or gomega itself.
package assertions

import (
	"context"
	"fmt"
	"regexp"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgclusterutils "github.com/cloudnative-pg/cloudnative-pg/tests/utils/clusterutils"
	preview "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	documentdbutil "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/documentdb"
)

// runningStatus aliases the canonical ReadyStatus constant exported by
// the documentdb helper package so all sibling helpers share a single
// source of truth for the "DocumentDB is healthy" sentinel.
const runningStatus = documentdbutil.ReadyStatus

// clusterNameFor returns the CNPG Cluster name that backs the given
// DocumentDB. For single-cluster (non-replicated) deployments this
// matches the DocumentDB name; replicated clusters use
// `<documentdb>-<member>` but are out of scope here (see AssertPrimary*
// variants that accept an explicit cluster name).
func clusterNameFor(dd *preview.DocumentDB) string {
	return dd.Name
}

// getDocumentDB is a small helper shared by assertions that need to
// read a DocumentDB by key.
func getDocumentDB(ctx context.Context, c client.Client, key client.ObjectKey) (*preview.DocumentDB, error) {
	dd := &preview.DocumentDB{}
	if err := c.Get(ctx, key, dd); err != nil {
		return nil, fmt.Errorf("get DocumentDB %s: %w", key, err)
	}
	return dd, nil
}

// AssertDocumentDBReady returns a checker that succeeds when the
// DocumentDB identified by key reports Status.Status == runningStatus.
// Any other value (including "" for a freshly-created object) yields
// a non-nil error so Eventually will keep polling.
func AssertDocumentDBReady(ctx context.Context, c client.Client, key client.ObjectKey) func() error {
	return func() error {
		dd, err := getDocumentDB(ctx, c, key)
		if err != nil {
			return err
		}
		if dd.Status.Status != runningStatus {
			return fmt.Errorf("DocumentDB %s status=%q, want %q",
				key, dd.Status.Status, runningStatus)
		}
		return nil
	}
}

// AssertInstanceCount returns a checker that succeeds when the CNPG
// Cluster backing the DocumentDB reports Status.ReadyInstances == want.
// This is the canonical signal for "scale operation completed": the
// DocumentDB spec alone does not expose a live instance count.
func AssertInstanceCount(ctx context.Context, c client.Client, key client.ObjectKey, want int) func() error {
	return func() error {
		dd, err := getDocumentDB(ctx, c, key)
		if err != nil {
			return err
		}
		cluster := &cnpgv1.Cluster{}
		ck := client.ObjectKey{Namespace: key.Namespace, Name: clusterNameFor(dd)}
		if err := c.Get(ctx, ck, cluster); err != nil {
			return fmt.Errorf("get CNPG Cluster %s: %w", ck, err)
		}
		if cluster.Status.ReadyInstances != want {
			return fmt.Errorf("CNPG Cluster %s readyInstances=%d, want %d",
				ck, cluster.Status.ReadyInstances, want)
		}
		return nil
	}
}

// AssertPrimaryUnchanged returns a checker that succeeds when the
// CNPG primary pod name still matches initialPrimary. It is intended
// for Consistently() checks during operations that must not trigger a
// failover (e.g. PVC resize).
func AssertPrimaryUnchanged(ctx context.Context, c client.Client, key client.ObjectKey, initialPrimary string) func() error {
	return func() error {
		dd, err := getDocumentDB(ctx, c, key)
		if err != nil {
			return err
		}
		pod, err := cnpgclusterutils.GetPrimary(ctx, c, key.Namespace, clusterNameFor(dd))
		if err != nil {
			return fmt.Errorf("get primary for %s: %w", key, err)
		}
		if pod == nil || pod.Name == "" {
			return fmt.Errorf("no primary pod found for %s", key)
		}
		if pod.Name != initialPrimary {
			return fmt.Errorf("primary changed: want %s, got %s", initialPrimary, pod.Name)
		}
		return nil
	}
}

// AssertPVCCount returns a checker that succeeds when the count of
// PersistentVolumeClaims in ns matching labelSelector equals want.
// labelSelector follows the standard Kubernetes selector syntax and
// must parse cleanly or the checker returns an error on every call.
func AssertPVCCount(ctx context.Context, c client.Client, ns, labelSelector string, want int) func() error {
	sel, selErr := labels.Parse(labelSelector)
	return func() error {
		if selErr != nil {
			return fmt.Errorf("parse selector %q: %w", labelSelector, selErr)
		}
		pvcs := &corev1.PersistentVolumeClaimList{}
		if err := c.List(ctx, pvcs, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: sel}); err != nil {
			return fmt.Errorf("list PVCs in %s: %w", ns, err)
		}
		if got := len(pvcs.Items); got != want {
			return fmt.Errorf("PVC count in %s (%s): got %d, want %d",
				ns, labelSelector, got, want)
		}
		return nil
	}
}

// AssertTLSSecretReady returns a checker that succeeds when the named
// secret exists in ns and contains non-empty tls.crt and tls.key
// entries (the canonical keys for a kubernetes.io/tls Secret).
func AssertTLSSecretReady(ctx context.Context, c client.Client, ns, secretName string) func() error {
	return func() error {
		s := &corev1.Secret{}
		key := client.ObjectKey{Namespace: ns, Name: secretName}
		if err := c.Get(ctx, key, s); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("TLS secret %s not found", key)
			}
			return fmt.Errorf("get TLS secret %s: %w", key, err)
		}
		if len(s.Data[corev1.TLSCertKey]) == 0 {
			return fmt.Errorf("TLS secret %s missing %s", key, corev1.TLSCertKey)
		}
		if len(s.Data[corev1.TLSPrivateKeyKey]) == 0 {
			return fmt.Errorf("TLS secret %s missing %s", key, corev1.TLSPrivateKeyKey)
		}
		return nil
	}
}

// AssertServiceType returns a checker that succeeds when the named
// Service exists in ns and its spec.type equals want.
func AssertServiceType(ctx context.Context, c client.Client, ns, svcName string, want corev1.ServiceType) func() error {
	return func() error {
		svc := &corev1.Service{}
		key := client.ObjectKey{Namespace: ns, Name: svcName}
		if err := c.Get(ctx, key, svc); err != nil {
			return fmt.Errorf("get Service %s: %w", key, err)
		}
		if svc.Spec.Type != want {
			return fmt.Errorf("Service %s type=%s, want %s", key, svc.Spec.Type, want)
		}
		return nil
	}
}

// AssertConnectionStringMatches returns a checker that succeeds when
// the DocumentDB's Status.ConnectionString is non-empty and matches
// the supplied regular expression. Regex compilation errors surface on
// every invocation so bad test input fails fast in Eventually.
func AssertConnectionStringMatches(ctx context.Context, c client.Client, key client.ObjectKey, regex string) func() error {
	re, reErr := regexp.Compile(regex)
	return func() error {
		if reErr != nil {
			return fmt.Errorf("compile regex %q: %w", regex, reErr)
		}
		dd, err := getDocumentDB(ctx, c, key)
		if err != nil {
			return err
		}
		cs := dd.Status.ConnectionString
		if cs == "" {
			return fmt.Errorf("DocumentDB %s has empty connectionString", key)
		}
		if !re.MatchString(cs) {
			return fmt.Errorf("DocumentDB %s connectionString %q does not match %q",
				key, cs, regex)
		}
		return nil
	}
}
