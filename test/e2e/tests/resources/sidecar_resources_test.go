package resources

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"github.com/documentdb/documentdb-operator/test/e2e"
)

// These specs validate the pod memory carve-out (sidecar resource isolation).
// With spec.resource.memory treated as the total pod envelope, the operator
// reserves memory for the gateway (default 18.75%, here 192Mi of a 1Gi envelope)
// and — when monitoring is enabled — the OTel collector (default memory limit
// 128Mi, CPU 50m request / 200m limit), and gives PostgreSQL the remainder. Each sidecar is Guaranteed (request==limit)
// so a leak is OOM-isolated to its own container.
//
// For a 1Gi (1024Mi) envelope:
//
//	gateway  = 18.75% × 1024Mi          = 192Mi   (request == limit)
//	otel     = 48Mi request / 128Mi limit; cpu 50m req / 200m limit (monitoring on)
//	postgres = 1024 − 192            = 832Mi   (monitoring off)
//	postgres = 1024 − 192 − 128      = 704Mi   (monitoring on)
const (
	podEnvelope         = "1Gi"
	wantGatewayMem      = "192Mi"
	wantPostgresNoMon   = "832Mi"
	wantPostgresWithMon = "704Mi"
	wantOTelMemRequest  = "48Mi"
	wantOTelMemLimit    = "128Mi"
	wantOTelCPURequest  = "50m"
	wantOTelCPULimit    = "200m"
)

var _ = Describe("Sidecar memory carve-out",
	Label(e2e.ResourcesLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("reserves gateway memory and gives PostgreSQL the remainder (monitoring off)",
			func() {
				env := e2e.SuiteEnv()
				Expect(env).ToNot(BeNil())
				c := env.Client

				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
				DeferCleanup(cancel)

				cr, cleanup := setupFreshCluster(ctx, c, "carveout-nomon", podEnvelope, false)
				DeferCleanup(cleanup)

				pod, err := getInstancePod(ctx, c, cr.Namespace, cr.Name)
				Expect(err).ToNot(HaveOccurred())

				gw := containerByName(pod, gatewayContainerName)
				Expect(gw).ToNot(BeNil(), "gateway container present")
				assertGuaranteedMemory(gw, wantGatewayMem)

				pg := containerByName(pod, postgresContainerName)
				Expect(pg).ToNot(BeNil(), "postgres container present")
				assertGuaranteedMemory(pg, wantPostgresNoMon)

				Expect(containerByName(pod, otelContainerName)).To(BeNil(),
					"otel collector should be absent when monitoring is disabled")
			})

		It("additionally reserves OTel collector memory when monitoring is enabled",
			func() {
				env := e2e.SuiteEnv()
				Expect(env).ToNot(BeNil())
				c := env.Client

				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
				DeferCleanup(cancel)

				cr, cleanup := setupFreshCluster(ctx, c, "carveout-mon", podEnvelope, true)
				DeferCleanup(cleanup)

				pod, err := getInstancePod(ctx, c, cr.Namespace, cr.Name)
				Expect(err).ToNot(HaveOccurred())

				gw := containerByName(pod, gatewayContainerName)
				Expect(gw).ToNot(BeNil(), "gateway container present")
				assertGuaranteedMemory(gw, wantGatewayMem)

				otel := containerByName(pod, otelContainerName)
				Expect(otel).ToNot(BeNil(), "otel collector container present")
				Expect(quantityEqual(otel.Resources.Requests.Memory(), wantOTelMemRequest)).To(BeTrue(),
					"otel memory request = %s, want %s", otel.Resources.Requests.Memory(), wantOTelMemRequest)
				Expect(quantityEqual(otel.Resources.Limits.Memory(), wantOTelMemLimit)).To(BeTrue(),
					"otel memory limit = %s, want %s", otel.Resources.Limits.Memory(), wantOTelMemLimit)
				Expect(quantityEqual(otel.Resources.Requests.Cpu(), wantOTelCPURequest)).To(BeTrue(),
					"otel cpu request = %s, want %s", otel.Resources.Requests.Cpu(), wantOTelCPURequest)
				Expect(quantityEqual(otel.Resources.Limits.Cpu(), wantOTelCPULimit)).To(BeTrue(),
					"otel cpu limit = %s, want %s", otel.Resources.Limits.Cpu(), wantOTelCPULimit)
				Expect(hasEnv(otel, "GOMEMLIMIT")).To(BeTrue(),
					"otel collector should have a GOMEMLIMIT env var")

				pg := containerByName(pod, postgresContainerName)
				Expect(pg).ToNot(BeNil(), "postgres container present")
				assertGuaranteedMemory(pg, wantPostgresWithMon)
			})

		It("derives the envelope from per-container memory when the envelope is omitted",
			func() {
				env := e2e.SuiteEnv()
				Expect(env).ToNot(BeNil())
				c := env.Client

				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
				DeferCleanup(cancel)

				// No spec.resource.memory; gateway + database memory set explicitly.
				cr, cleanup := setupExplicitCluster(ctx, c, "carveout-explicit", "256Mi", "1Gi")
				DeferCleanup(cleanup)

				pod, err := getInstancePod(ctx, c, cr.Namespace, cr.Name)
				Expect(err).ToNot(HaveOccurred())

				gw := containerByName(pod, gatewayContainerName)
				Expect(gw).ToNot(BeNil(), "gateway container present")
				assertGuaranteedMemory(gw, "256Mi")

				pg := containerByName(pod, postgresContainerName)
				Expect(pg).ToNot(BeNil(), "postgres container present")
				assertGuaranteedMemory(pg, "1Gi")
			})
	})

// assertGuaranteedMemory asserts the container's memory request and limit both
// equal want (Guaranteed-class for memory).
func assertGuaranteedMemory(ctr *corev1.Container, want string) {
	GinkgoHelper()
	Expect(quantityEqual(ctr.Resources.Limits.Memory(), want)).To(BeTrue(),
		"%s memory limit = %s, want %s", ctr.Name, ctr.Resources.Limits.Memory(), want)
	Expect(quantityEqual(ctr.Resources.Requests.Memory(), want)).To(BeTrue(),
		"%s memory request = %s, want %s", ctr.Name, ctr.Resources.Requests.Memory(), want)
}

func hasEnv(ctr *corev1.Container, name string) bool {
	for _, e := range ctr.Env {
		if e.Name == name {
			return true
		}
	}
	return false
}
