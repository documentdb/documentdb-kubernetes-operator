// Package mongo provides thin helpers for the DocumentDB E2E suite to
// connect to a DocumentDB gateway endpoint using the official
// mongo-driver/v2 client. It is intentionally minimal: URI construction
// with proper credential escaping, connect/ping, seeding, counting, and
// database cleanup.
package mongo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// DefaultConnectTimeout is applied to mongo.Connect when the caller does
// not provide a deadline on the context.
const DefaultConnectTimeout = 10 * time.Second

// ClientOptions describes the parameters required to reach a DocumentDB
// gateway. All fields are required except TLSInsecure (ignored when TLS
// is false) and AuthDB (defaults to "admin").
type ClientOptions struct {
	// Host is the DocumentDB gateway hostname or IP.
	Host string
	// Port is the DocumentDB gateway TCP port.
	Port string
	// User is the plain (un-escaped) username.
	User string
	// Password is the plain (un-escaped) password.
	Password string
	// TLS toggles transport TLS on the connection.
	TLS bool
	// TLSInsecure skips certificate verification when TLS is true. It is
	// only appropriate for tests against self-signed certificates that
	// are not trusted via RootCAs. Mutually exclusive in practice with
	// RootCAs/CABundlePEM: if both are set, RootCAs wins and
	// InsecureSkipVerify is not applied.
	TLSInsecure bool
	// RootCAs, when non-nil and TLS is true, is used as the trust store
	// for server-certificate verification. Takes precedence over
	// CABundlePEM if both are set.
	RootCAs *x509.CertPool
	// CABundlePEM, when non-empty and RootCAs is nil, is parsed into a
	// one-off CertPool used as the trust store for server-certificate
	// verification. Convenience for callers that already have the PEM
	// bytes (e.g., from a kubernetes.io/tls Secret).
	CABundlePEM []byte
	// ServerName is the expected hostname presented by the server for
	// SNI + hostname verification. Defaults to Host when empty. Set
	// explicitly when connecting through a port-forward (where Host is
	// 127.0.0.1 but the cert is issued for a Service DNS name).
	ServerName string
	// AuthDB is the authentication database (authSource). Defaults to
	// "admin" when empty.
	AuthDB string
}

// BuildURI constructs the mongodb:// URI that NewClient would use. It is
// exported to make credential escaping, TLS flag, and authSource
// behaviour directly unit-testable without spinning up a server.
func BuildURI(opts ClientOptions) (string, error) {
	if opts.Host == "" {
		return "", errors.New("mongo: Host is required")
	}
	if opts.Port == "" {
		return "", errors.New("mongo: Port is required")
	}
	if opts.User == "" {
		return "", errors.New("mongo: User is required")
	}
	authDB := opts.AuthDB
	if authDB == "" {
		authDB = "admin"
	}
	u := url.QueryEscape(opts.User)
	p := url.QueryEscape(opts.Password)
	tlsFlag := "false"
	if opts.TLS {
		tlsFlag = "true"
	}
	// authSource is a URL query parameter; url.QueryEscape keeps it safe
	// for names containing reserved characters.
	return fmt.Sprintf(
		"mongodb://%s:%s@%s:%s/?tls=%s&authSource=%s",
		u, p, opts.Host, opts.Port, tlsFlag, url.QueryEscape(authDB),
	), nil
}

// NewClient builds a connected *mongo.Client against the endpoint
// described by opts. The caller owns the returned client and is
// responsible for calling Disconnect.
//
// Connect time is bounded by DefaultConnectTimeout via the driver's
// SetConnectTimeout option. mongo-driver/v2 Connect is lazy, so
// callers who need a post-connect round-trip must call Ping (or
// pingWithRetry from connect.go) themselves.
func NewClient(_ context.Context, opts ClientOptions) (*mongo.Client, error) {
	uri, err := BuildURI(opts)
	if err != nil {
		return nil, err
	}
	co := options.Client().ApplyURI(uri).SetConnectTimeout(DefaultConnectTimeout)
	if opts.TLS {
		tlsCfg, terr := buildTLSConfig(opts)
		if terr != nil {
			return nil, terr
		}
		if tlsCfg != nil {
			co.SetTLSConfig(tlsCfg)
		}
	}
	c, err := mongo.Connect(co)
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}
	return c, nil
}

// buildTLSConfig assembles a *tls.Config for the driver. Priority:
//
//  1. RootCAs, if non-nil — use as trust store.
//  2. CABundlePEM, if non-empty — parse into a fresh pool.
//  3. TLSInsecure — skip verification entirely.
//
// Returns (nil, nil) when TLS is on but none of the above are set; the
// driver then falls back to the system trust store (default behaviour).
// ServerName is propagated when set so callers can overcome SNI
// mismatch in port-forward scenarios.
func buildTLSConfig(opts ClientOptions) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if opts.ServerName != "" {
		cfg.ServerName = opts.ServerName
	}
	switch {
	case opts.RootCAs != nil:
		cfg.RootCAs = opts.RootCAs
		return cfg, nil
	case len(opts.CABundlePEM) > 0:
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(opts.CABundlePEM) {
			return nil, errors.New("mongo: CABundlePEM contained no parseable certificates")
		}
		cfg.RootCAs = pool
		return cfg, nil
	case opts.TLSInsecure:
		cfg.InsecureSkipVerify = true //nolint:gosec // tests only, self-signed gateway
		return cfg, nil
	}
	// TLS on, no CA and not insecure: return a minimal config that
	// still honours a user-supplied ServerName but otherwise defers to
	// the driver/system trust store.
	if cfg.ServerName != "" {
		return cfg, nil
	}
	return nil, nil
}

// Ping issues a server-selection + hello roundtrip, using the context
// for cancellation/deadline propagation.
func Ping(ctx context.Context, c *mongo.Client) error {
	if c == nil {
		return errors.New("mongo: nil client")
	}
	if err := c.Ping(ctx, nil); err != nil {
		return fmt.Errorf("mongo: ping: %w", err)
	}
	return nil
}

// Seed inserts docs into db.coll via InsertMany and returns the number
// of documents accepted by the server.
func Seed(ctx context.Context, c *mongo.Client, db, coll string, docs []bson.M) (int, error) {
	if c == nil {
		return 0, errors.New("mongo: nil client")
	}
	if len(docs) == 0 {
		return 0, nil
	}
	anyDocs := make([]any, len(docs))
	for i := range docs {
		anyDocs[i] = docs[i]
	}
	res, err := c.Database(db).Collection(coll).InsertMany(ctx, anyDocs)
	if err != nil {
		return 0, fmt.Errorf("mongo: seed %s.%s: %w", db, coll, err)
	}
	return len(res.InsertedIDs), nil
}

// Count returns the number of documents in db.coll matching filter.
func Count(ctx context.Context, c *mongo.Client, db, coll string, filter bson.M) (int64, error) {
	if c == nil {
		return 0, errors.New("mongo: nil client")
	}
	if filter == nil {
		filter = bson.M{}
	}
	n, err := c.Database(db).Collection(coll).CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo: count %s.%s: %w", db, coll, err)
	}
	return n, nil
}

// DropDatabase drops the named database. A nil client returns an error.
func DropDatabase(ctx context.Context, c *mongo.Client, db string) error {
	if c == nil {
		return errors.New("mongo: nil client")
	}
	if err := c.Database(db).Drop(ctx); err != nil {
		return fmt.Errorf("mongo: drop %s: %w", db, err)
	}
	return nil
}
