package exposure

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/portforward"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// hasLoadBalancerController probes the target cluster by creating a
// throwaway LoadBalancer Service and polling briefly for an external
// address. The probe uses a short timeout so environments without a
// working LB controller skip fast rather than failing the spec. The
// probe namespace is the default namespace; the Service is deleted
// before the function returns regardless of the outcome.
func hasLoadBalancerController(ctx context.Context, c client.Client, timeout time.Duration) (bool, error) {
	probeName := fmt.Sprintf("e2e-lb-probe-%d", time.Now().UnixNano())
	probeNS := "default"
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      probeName,
			Namespace: probeNS,
			Labels: map[string]string{
				"e2e.documentdb.io/probe": "loadbalancer",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{"app.kubernetes.io/name": "nonexistent-e2e-probe"},
			Ports: []corev1.ServicePort{{
				Name:       "probe",
				Port:       80,
				TargetPort: intstr.FromInt(80),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if err := c.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("create LB probe: %w", err)
	}
	defer func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Delete(delCtx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: probeName, Namespace: probeNS},
		})
	}()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got := &corev1.Service{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: probeNS, Name: probeName}, got); err == nil {
			for _, ing := range got.Status.LoadBalancer.Ingress {
				if ing.IP != "" || ing.Hostname != "" {
					return true, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return false, nil
}

// DocumentDB exposure — LoadBalancer.
//
// Requires a working LoadBalancer controller in the target cluster
// (kind + MetalLB, a cloud-provider LB, etc.). When no external address
// is assigned to a probe Service within ~30s, the spec skips rather than
// fails so unconfigured environments do not poison the run.
var _ = Describe("DocumentDB exposure — LoadBalancer",
	Label(e2e.ExposureLabel, e2e.NeedsMetalLBLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() {
			e2e.SkipUnlessLevel(e2e.Medium)
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil())
			probeCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			ok, err := hasLoadBalancerController(probeCtx, env.Client, 30*time.Second)
			Expect(err).ToNot(HaveOccurred())
			if !ok {
				Skip("no LoadBalancer controller in cluster — probe service acquired no external address within 30s")
			}
		})

		It("provisions a LoadBalancer Service with an external address", func() {
			env := e2e.SuiteEnv()
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
			DeferCleanup(cancel)

			dd, cleanup := setupFreshCluster(ctx, c, "expose-lb",
				[]string{"exposure_loadbalancer"},
				map[string]string{"EXPOSURE_TYPE": "LoadBalancer"},
			)
			DeferCleanup(cleanup)

			// 1. Spec round-trip.
			Expect(dd.Spec.ExposeViaService.ServiceType).To(Equal("LoadBalancer"))

			// 2. Service type is LoadBalancer.
			svcName := portforward.GatewayServiceName(dd)
			Eventually(assertions.AssertServiceType(ctx, c, dd.Namespace, svcName, corev1.ServiceTypeLoadBalancer),
				timeouts.For(timeouts.ServiceReady), timeouts.PollInterval(timeouts.ServiceReady)).
				Should(Succeed())

			// 3. External address is eventually assigned.
			Eventually(func() error {
				svc := &corev1.Service{}
				if err := c.Get(ctx, client.ObjectKey{Namespace: dd.Namespace, Name: svcName}, svc); err != nil {
					return err
				}
				for _, ing := range svc.Status.LoadBalancer.Ingress {
					if ing.IP != "" || ing.Hostname != "" {
						return nil
					}
				}
				return fmt.Errorf("Service %s/%s has no external address yet", dd.Namespace, svcName)
			}, timeouts.For(timeouts.ServiceReady), timeouts.PollInterval(timeouts.ServiceReady)).
				Should(Succeed())
		})
	})
