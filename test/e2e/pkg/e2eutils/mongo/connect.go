// Package mongo — connect.go provides a high-level helper that opens a
// port-forward to a DocumentDB gateway Service, reads credentials from
// the standard "documentdb-credentials" secret in the CR's namespace,
// and returns a connected mongo-driver client wrapped in a [Handle]
// that also owns the port-forward lifetime.
//
// This helper intentionally lives outside pkg/e2eutils/fixtures to
// avoid an import cycle: fixtures creates the CR + secret; mongo is
// the pure data-plane helper callers reach for in `It` blocks.
package mongo

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	driver "go.mongodb.org/mongo-driver/v2/mongo"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/portforward"
)

// DefaultCredentialSecretName is the secret name the shared fixtures
// create to hold gateway credentials. Kept in sync with
// fixtures.DefaultCredentialSecretName; duplicated here to avoid a
// circular import.
const DefaultCredentialSecretName = "documentdb-credentials"

// Handle owns a live mongo-driver client plus the port-forward that
// backs it. Callers must invoke Close when done; failing to do so
// leaks a local port-forward goroutine.
type Handle struct {
	client *driver.Client
	stop   func() error
}

// Client returns the underlying mongo-driver client. Prefer Database
// for per-spec isolation.
func (h *Handle) Client() *driver.Client { return h.client }

// Database is a pass-through to the underlying driver client.
func (h *Handle) Database(name string) *driver.Database {
	return h.client.Database(name)
}

// Close disconnects the mongo client and tears down the port-forward.
// Safe to call on a nil handle. Returns the first non-nil error
// observed across (Disconnect, port-forward shutdown).
func (h *Handle) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	var derr error
	if h.client != nil {
		derr = h.client.Disconnect(ctx)
	}
	var serr error
	if h.stop != nil {
		serr = h.stop()
	}
	if derr != nil {
		return derr
	}
	return serr
}

// connectRetryTimeout bounds the post-port-forward ping/retry loop
// because forwardconnection's goroutine takes a brief moment to bind
// the chosen local port. 10s @ 100ms backoff absorbs ~100 attempts,
// which is plenty for the typical <1s bind delay without stretching
// the happy path.
const (
	connectRetryTimeout = 10 * time.Second
	connectRetryBackoff = 100 * time.Millisecond
)

// ConnectOption customises NewFromDocumentDB. Options are composable
// and apply in the order supplied; later options overwrite earlier
// ones for the same field.
type ConnectOption func(*connectConfig)

type connectConfig struct {
	rootCAs     *x509.CertPool
	caBundlePEM []byte
	serverName  string
	tlsInsecure bool
}

// WithRootCAs pins the trust store used for server-certificate
// verification to the given pool. Prefer this over WithCABundlePEM
// when you already have a *x509.CertPool assembled.
func WithRootCAs(pool *x509.CertPool) ConnectOption {
	return func(c *connectConfig) { c.rootCAs = pool; c.tlsInsecure = false }
}

// WithCABundlePEM pins the trust store to a CA bundle parsed from PEM
// bytes. Convenient for callers reading ca.crt out of a Secret.
func WithCABundlePEM(pem []byte) ConnectOption {
	return func(c *connectConfig) { c.caBundlePEM = pem; c.tlsInsecure = false }
}

// WithServerName overrides the TLS SNI + hostname-verification target.
// Use when connecting through a port-forward where Host is 127.0.0.1
// but the server certificate was issued for a Service DNS name.
func WithServerName(name string) ConnectOption {
	return func(c *connectConfig) { c.serverName = name }
}

// WithTLSInsecure turns off server-certificate verification. It is the
// default when no ConnectOption is supplied, preserving legacy
// behaviour; callers that want CA verification must pass WithRootCAs
// or WithCABundlePEM explicitly.
func WithTLSInsecure() ConnectOption {
	return func(c *connectConfig) {
		c.tlsInsecure = true
		c.rootCAs = nil
		c.caBundlePEM = nil
	}
}

// NewFromDocumentDB builds a connected Handle against the DocumentDB CR
// identified by (namespace, name). It:
//
//  1. Reads the CR and the "documentdb-credentials" secret from the
//     same namespace.
//  2. Picks a free local TCP port.
//  3. Opens a port-forward to the gateway Service via the portforward
//     helper (using OpenWithErr so teardown surfaces forwarder errors).
//  4. Connects the mongo-driver client with TLS; verification mode is
//     controlled by opts (default: InsecureSkipVerify for backwards
//     compatibility with the historical gateway self-signed cert).
//  5. Pings with retry until the port-forward is reachable or
//     connectRetryTimeout elapses.
func NewFromDocumentDB(
	ctx context.Context,
	env *environment.TestingEnvironment,
	namespace, name string,
	opts ...ConnectOption,
) (*Handle, error) {
	if env == nil || env.Client == nil {
		return nil, errors.New("mongo: NewFromDocumentDB requires a non-nil TestingEnvironment")
	}

	cfg := connectConfig{tlsInsecure: true}
	for _, o := range opts {
		o(&cfg)
	}

	dd := &previewv1.DocumentDB{}
	if err := env.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, dd); err != nil {
		return nil, fmt.Errorf("get DocumentDB %s/%s: %w", namespace, name, err)
	}

	user, pass, err := readCredentialSecret(ctx, env, namespace)
	if err != nil {
		return nil, err
	}

	lp, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("mongo: pick free port: %w", err)
	}

	stop, err := portforward.OpenWithErr(ctx, env, dd, lp)
	if err != nil {
		return nil, fmt.Errorf("mongo: open port-forward: %w", err)
	}

	c, err := NewClient(ctx, ClientOptions{
		Host:        "127.0.0.1",
		Port:        strconv.Itoa(lp),
		User:        user,
		Password:    pass,
		TLS:         true,
		TLSInsecure: cfg.tlsInsecure,
		RootCAs:     cfg.rootCAs,
		CABundlePEM: cfg.caBundlePEM,
		ServerName:  cfg.serverName,
		AuthDB:      "admin",
	})
	if err != nil {
		_ = stop()
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}

	// pingWithRetry owns the post-port-forward connection-refused
	// window. No pre-ping sleep is needed: the retry loop at
	// connectRetryBackoff cadence covers the forwarder bind delay.
	if err := pingWithRetry(ctx, c, connectRetryTimeout); err != nil {
		_ = c.Disconnect(ctx)
		_ = stop()
		return nil, fmt.Errorf("mongo: post-connect ping: %w", err)
	}

	return &Handle{client: c, stop: stop}, nil
}

// readCredentialSecret fetches username/password from the fixture
// credential secret. The secret is expected to have keys "username"
// and "password".
func readCredentialSecret(
	ctx context.Context,
	env *environment.TestingEnvironment,
	namespace string,
) (string, string, error) {
	sec := &corev1.Secret{}
	err := env.Client.Get(ctx, types.NamespacedName{
		Namespace: namespace, Name: DefaultCredentialSecretName,
	}, sec)
	if err != nil {
		return "", "", fmt.Errorf("get credential secret %s/%s: %w",
			namespace, DefaultCredentialSecretName, err)
	}
	u := string(sec.Data["username"])
	p := string(sec.Data["password"])
	if u == "" || p == "" {
		return "", "", fmt.Errorf("credential secret %s/%s missing username/password",
			namespace, DefaultCredentialSecretName)
	}
	return u, p, nil
}

// pickFreePort asks the kernel for an unused TCP port by binding ":0"
// and immediately closing the listener. There is a narrow race window
// between Close and the port-forward goroutine binding the same port;
// pingWithRetry absorbs that window without a fixed pre-ping sleep.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// pingWithRetry polls Ping until it succeeds or timeout elapses. The
// port-forward goroutine needs a moment to bind the local port, so the
// first few pings may fail with "connection refused". Short backoff
// (connectRetryBackoff) keeps the happy path fast while still covering
// slow CI nodes via the overall timeout budget.
func pingWithRetry(ctx context.Context, c *driver.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := c.Ping(pingCtx, nil)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			return fmt.Errorf("ping did not succeed within %s: %w", timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(connectRetryBackoff):
		}
	}
}
