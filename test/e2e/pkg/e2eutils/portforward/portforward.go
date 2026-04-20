// Package portforward is a thin wrapper around CNPG's
// tests/utils/forwardconnection helper, specialised for the DocumentDB
// gateway service.
//
// The DocumentDB operator creates a Service named
// "documentdb-service-<dd.Name>" in the same namespace as the CR, with
// a port named "gateway" targeting the gateway sidecar (default port
// 10260). This package opens a local port-forward to that service and
// returns a stop func the caller defers.
//
// Fallback note
//
// forwardconnection.NewDialerFromService is generic over service name
// and does NOT hardcode Postgres, despite the package's origin in the
// CNPG codebase. We therefore use the CNPG helper directly rather than
// reaching for client-go's portforward.PortForwarder. If a future CNPG
// release tightens the helper to Postgres-only semantics, this file is
// the single place to swap in a client-go implementation.
package portforward

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/forwardconnection"
	"github.com/onsi/ginkgo/v2"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

// GatewayPort is the default DocumentDB gateway TCP port inside the
// cluster. Mirrored from operator/src/internal/utils/constants.go so
// the E2E module does not depend on the operator's internal packages.
const GatewayPort = 10260

// ServiceNamePrefix mirrors DOCUMENTDB_SERVICE_PREFIX from the operator.
// The fully-qualified service name is ServiceNamePrefix + dd.Name,
// truncated to 63 characters to honour the Kubernetes DNS limit.
const ServiceNamePrefix = "documentdb-service-"

// GatewayServiceName returns the Service name the operator creates for
// the given DocumentDB CR.
func GatewayServiceName(dd *previewv1.DocumentDB) string {
	if dd == nil {
		return ""
	}
	name := ServiceNamePrefix + dd.Name
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// OpenWithErr establishes a port-forward from localPort on the caller's
// host to the DocumentDB gateway service backing dd. It returns a stop
// func that halts the forward and returns the final error reported by
// the forwarder goroutine (nil on clean shutdown). Callers MUST invoke
// stop exactly once; double-invocation is safe but only the first call
// returns the real error.
//
// If localPort is 0, a free port is picked by the kernel.
//
// Prefer OpenWithErr over Open for new call sites: exposing the
// forwarder error lets specs surface gateway-level disconnects instead
// of silently dropping them.
func OpenWithErr(
	ctx context.Context,
	env *environment.TestingEnvironment,
	dd *previewv1.DocumentDB,
	localPort int,
) (stop func() error, err error) {
	if env == nil {
		return nil, fmt.Errorf("OpenWithErr: env must not be nil")
	}
	if dd == nil {
		return nil, fmt.Errorf("OpenWithErr: dd must not be nil")
	}
	svcName := GatewayServiceName(dd)
	if svcName == "" {
		return nil, fmt.Errorf("OpenWithErr: could not derive gateway service name from %+v", dd)
	}

	dialer, _, err := forwardconnection.NewDialerFromService(
		ctx,
		env.Interface,
		env.RestClientConfig,
		dd.Namespace,
		svcName,
	)
	if err != nil {
		return nil, fmt.Errorf("building dialer for %s/%s: %w", dd.Namespace, svcName, err)
	}

	portMaps := []string{fmt.Sprintf("%d:%d", localPort, GatewayPort)}
	fc, err := forwardconnection.NewForwardConnection(dialer, portMaps, io.Discard, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("creating forward connection: %w", err)
	}

	fwdCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- fc.StartAndWait(fwdCtx) }()

	var stopped bool
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		// Drain the goroutine so callers see deterministic teardown.
		// context.Canceled is the expected shutdown signal and is
		// swallowed; everything else is surfaced.
		e := <-errCh
		if e != nil && !errors.Is(e, context.Canceled) {
			return e
		}
		return nil
	}
	return stop, nil
}

// Open is the backwards-compatible wrapper around OpenWithErr that
// returns a stop func() (no error). Any non-nil forwarder error
// observed at teardown is logged to GinkgoWriter so test failures are
// still traceable.
//
// New callers should prefer OpenWithErr; Open remains for pre-existing
// callers that cannot easily propagate the error (e.g., helpers that
// plug into DeferCleanup with a no-return func).
func Open(
	ctx context.Context,
	env *environment.TestingEnvironment,
	dd *previewv1.DocumentDB,
	localPort int,
) (stop func(), err error) {
	stopE, err := OpenWithErr(ctx, env, dd, localPort)
	if err != nil {
		return nil, err
	}
	return func() {
		if ferr := stopE(); ferr != nil {
			fmt.Fprintf(ginkgo.GinkgoWriter,
				"portforward: forwarder for %s/%s exited with error: %v\n",
				dd.Namespace, GatewayServiceName(dd), ferr)
		}
	}, nil
}
