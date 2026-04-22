package status

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/assertions"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/portforward"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/timeouts"
)

// DocumentDB status — ConnectionString.
//
// The operator publishes a `mongodb://` URI in status.connectionString
// once the gateway Service and credential secret are ready. This spec
// has three layers, ordered cheapest-first so the failure surface is
// well-separated:
//
//  1. Shape — the string matches `^mongodb://` and carries a non-empty
//     host component. Catches "field unset" and "scheme drift".
//
//  2. Semantic — the string names the expected credential secret,
//     targets the default gateway port, and carries every Mongo URI
//     query param the Go/JS/Python driver needs (directConnection,
//     authMechanism=SCRAM-SHA-256, tls, replicaSet, and
//     tlsAllowInvalidCertificates correlated with status.TLS.Ready).
//     Catches operator regressions that rewrite GenerateConnectionString
//     in util.go.
//
//  3. Live — open a real port-forward to the gateway Service, read the
//     credential secret, and Ping via mongo-driver/v2. Proves the
//     (port + params) the operator published actually reach a working
//     endpoint, independent of the string's literal host (which is the
//     cluster-internal Service ClusterIP and so only dialable from
//     outside the cluster via port-forward).
//
// Why we do not shell-eval the string
//
// status.connectionString contains `$(kubectl get secret …)` subshells
// in userinfo so that a human can paste it into a terminal and have
// credentials auto-resolve. Running `bash -c "echo <str>"` in-test to
// exercise that roundtrip would require `kubectl` + a valid kubeconfig
// in the Ginkgo process, conflate shell and driver failure modes, and
// not work on runners without bash — we have none today, but locking
// ourselves to bash for a status assertion is a poor tradeoff. The
// string-level assertion on the secret-name reference (below) is the
// high-signal subset of that approach at a fraction of the cost.
//
// This spec runs against the session-scoped shared RO fixture so it
// adds negligible time to the suite.
var _ = Describe("DocumentDB status — connectionString",
	Label(e2e.StatusLabel), e2e.MediumLevelLabel,
	func() {
		BeforeEach(func() { e2e.SkipUnlessLevel(e2e.Medium) })

		It("publishes a valid, dialable mongodb:// URI", func() {
			env := e2e.SuiteEnv()
			Expect(env).ToNot(BeNil())
			c := env.Client

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			DeferCleanup(cancel)

			handle, err := fixtures.GetOrCreateSharedRO(ctx, c)
			Expect(err).ToNot(HaveOccurred())

			key := client.ObjectKey{Namespace: handle.Namespace(), Name: handle.Name()}

			// Layer 1: shape assertion via the shared helper, eventually-polled
			// because the operator may publish the string a reconcile or two
			// after the CR flips Ready.
			By("asserting status.connectionString matches ^mongodb://")
			Eventually(
				assertions.AssertConnectionStringMatches(ctx, c, key, `^mongodb://`),
				timeouts.For(timeouts.DocumentDBReady),
				timeouts.PollInterval(timeouts.DocumentDBReady),
			).Should(Succeed())

			dd, err := handle.GetCR(ctx, c)
			Expect(err).ToNot(HaveOccurred())
			connStr := dd.Status.ConnectionString
			Expect(connStr).ToNot(BeEmpty(),
				"status.connectionString must be populated on a Ready DocumentDB")

			// Layer 2a: credential-secret reference. Catches operator typos
			// and regressions that ignore spec.documentDbCredentialSecret.
			// expectedSecret mirrors utils.GenerateConnectionString's
			// fallback: spec override wins, else the default secret name.
			expectedSecret := dd.Spec.DocumentDbCredentialSecret
			if expectedSecret == "" {
				expectedSecret = mongo.DefaultCredentialSecretName
			}
			By(fmt.Sprintf("asserting connection string references secret %q", expectedSecret))
			// The secret name appears twice inside `kubectl get secret <name> -n <ns>`
			// subshells — one substring match is sufficient.
			Expect(connStr).To(ContainSubstring("secret "+expectedSecret+" "),
				"connection string must reference credential secret %q; got: %s",
				expectedSecret, connStr)

			// Layer 2b: extract host:port and query params. We cannot use
			// url.Parse on the full string because userinfo contains
			// `$(kubectl ... | base64 -d)` which is not a valid URL
			// userinfo. Strip userinfo with a regex that matches up to
			// the LAST '@' before the first '/' — Mongo's default URI
			// grammar guarantees userinfo does not contain '/'.
			By("parsing host:port and query params from the published URI")
			re := regexp.MustCompile(`^mongodb://.*@(?P<hostport>[^/]+)/\?(?P<query>.+)$`)
			m := re.FindStringSubmatch(connStr)
			Expect(m).ToNot(BeNil(),
				"connection string must be of form mongodb://<userinfo>@<host:port>/?<query>; got: %s",
				connStr)
			hostport := m[1]
			rawQuery := m[2]

			host, port, err := splitHostPort(hostport)
			Expect(err).ToNot(HaveOccurred(),
				"host:port segment must split cleanly; got %q", hostport)
			Expect(host).ToNot(BeEmpty(), "host component must not be empty")
			Expect(port).To(Equal(portforward.GatewayPort),
				"connection string port must equal the default gateway port (%d); got %d",
				portforward.GatewayPort, port)

			// Layer 2c: required query parameters. Each catches a distinct
			// regression in GenerateConnectionString: missing
			// directConnection breaks replica-set discovery through the
			// gateway; missing authMechanism breaks SCRAM; missing tls or
			// replicaSet breaks drivers that refuse to infer defaults.
			By("asserting required Mongo URI query parameters are present")
			qv, err := url.ParseQuery(rawQuery)
			Expect(err).ToNot(HaveOccurred(), "query must parse: %q", rawQuery)
			Expect(qv.Get("directConnection")).To(Equal("true"),
				"connection string must set directConnection=true")
			Expect(qv.Get("authMechanism")).To(Equal("SCRAM-SHA-256"),
				"connection string must set authMechanism=SCRAM-SHA-256")
			Expect(qv.Get("tls")).To(Equal("true"),
				"connection string must set tls=true (gateway is TLS-only)")
			Expect(qv.Get("replicaSet")).To(Equal("rs0"),
				"connection string must set replicaSet=rs0")

			// Layer 2d: TLS trust flag correlates with status.TLS.Ready.
			// GenerateConnectionString appends tlsAllowInvalidCertificates=true
			// exactly when the CR is NOT in a "trust-ready" state
			// (status.TLS nil or not Ready). Inverting this flag would
			// either leak self-signed exposure into production or break
			// connections to trusted CAs; both are silent footguns without
			// this assertion.
			trustReady := dd.Status.TLS != nil && dd.Status.TLS.Ready
			if trustReady {
				Expect(qv.Has("tlsAllowInvalidCertificates")).To(BeFalse(),
					"with status.TLS.Ready=true the connection string must NOT set tlsAllowInvalidCertificates")
			} else {
				Expect(qv.Get("tlsAllowInvalidCertificates")).To(Equal("true"),
					"with status.TLS.Ready=false the connection string must set tlsAllowInvalidCertificates=true")
			}

			// Layer 3: live Ping through the same (port + params + secret)
			// contract. NewFromDocumentDB opens a port-forward to the
			// gateway Service, reads the credential secret, dials with
			// TLS+InsecureSkipVerify (matching tlsAllowInvalidCertificates
			// behaviour for this spec's shared self-signed fixture), and
			// Pings. Any mismatch between the published string's port /
			// secret-name and what actually serves traffic surfaces here
			// as a connect or auth failure.
			By("dialing the gateway via port-forward and running Ping")
			dialCtx, dialCancel := context.WithTimeout(ctx, timeouts.For(timeouts.MongoConnect))
			DeferCleanup(dialCancel)
			mh, err := mongo.NewFromDocumentDB(dialCtx, env, dd.Namespace, dd.Name,
				mongo.WithTLSInsecure())
			Expect(err).ToNot(HaveOccurred(),
				"must be able to dial + Ping using the contract described by status.connectionString")
			DeferCleanup(func() {
				closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer closeCancel()
				_ = mh.Close(closeCtx)
			})
		})
	})

// splitHostPort splits a "host:port" segment where the port is
// numeric. We avoid net.SplitHostPort only because the host in this
// spec is a ClusterIP and so unambiguously not an IPv6 literal — a
// focused parser makes the "port drift" failure message more direct.
func splitHostPort(hostport string) (host string, port int, err error) {
	for i := len(hostport) - 1; i >= 0; i-- {
		if hostport[i] == ':' {
			p, perr := strconv.Atoi(hostport[i+1:])
			if perr != nil {
				return "", 0, fmt.Errorf("port segment not numeric: %q", hostport[i+1:])
			}
			return hostport[:i], p, nil
		}
	}
	return "", 0, fmt.Errorf("host:port missing ':' separator: %q", hostport)
}
