// Package testenv constructs a CloudNative-PG *environment.TestingEnvironment
// pre-configured for the DocumentDB E2E suite.
//
// Upstream CNPG's NewTestingEnvironment only registers the
// volumesnapshot and prometheus-operator scheme groups. DocumentDB specs
// additionally need:
//
//   - CloudNative-PG's apiv1 (Cluster, Backup, ScheduledBackup, Pooler, …)
//   - k8s.io client-go scheme (core/v1, apps/v1, …)
//   - the DocumentDB operator preview API (documentdb.io/preview)
//
// NewDocumentDBTestingEnvironment registers those groups onto the shared
// scheme and rebuilds env.Client so it can Get/List/Watch DocumentDB CRs.
//
// Phase-0 note: CNPG's NewTestingEnvironment parses POSTGRES_IMG with
// Masterminds/semver. If the tag is not semver-parseable (e.g. "latest")
// it returns an error. We default POSTGRES_IMG=busybox:17.2 when the
// variable is unset so the suite can boot without CNPG postgres images.
package testenv

import (
	"context"
	"fmt"
	"os"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/environment"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

// DefaultOperatorNamespace is the namespace the DocumentDB operator is
// deployed into by the Helm chart and by the suite's CI fixtures.
const DefaultOperatorNamespace = "documentdb-operator"

// DefaultPostgresImage is the placeholder image used to satisfy CNPG's
// semver parsing when the caller does not care about the Postgres image
// (DocumentDB specs never launch raw CNPG clusters from this env).
const DefaultPostgresImage = "busybox:17.2"

// postgresImgEnv is the environment variable consulted by the upstream
// CNPG testing environment constructor.
const postgresImgEnv = "POSTGRES_IMG"

// NewDocumentDBTestingEnvironment returns a CNPG *TestingEnvironment with
// the CloudNative-PG apiv1, client-go and DocumentDB preview schemes
// registered and env.Client rebuilt against that scheme. The supplied
// context is stored on the returned environment for callers that need it.
func NewDocumentDBTestingEnvironment(ctx context.Context) (*environment.TestingEnvironment, error) {
	if _, ok := os.LookupEnv(postgresImgEnv); !ok {
		if err := os.Setenv(postgresImgEnv, DefaultPostgresImage); err != nil {
			return nil, fmt.Errorf("setting %s: %w", postgresImgEnv, err)
		}
	}

	env, err := environment.NewTestingEnvironment()
	if err != nil {
		return nil, fmt.Errorf("creating CNPG testing environment: %w", err)
	}

	utilruntime.Must(cnpgv1.AddToScheme(env.Scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(env.Scheme))
	utilruntime.Must(previewv1.AddToScheme(env.Scheme))

	c, err := client.New(env.RestClientConfig, client.Options{Scheme: env.Scheme})
	if err != nil {
		return nil, fmt.Errorf("rebuilding controller-runtime client with DocumentDB scheme: %w", err)
	}
	env.Client = c
	if ctx != nil {
		env.Ctx = ctx
	}
	return env, nil
}

// DefaultDocumentDBScheme returns a fresh scheme with the same group
// registrations applied by NewDocumentDBTestingEnvironment. It is useful
// for unit tests that construct a fake client without spinning up the
// full TestingEnvironment.
func DefaultDocumentDBScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := cnpgv1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding cnpg apiv1 to scheme: %w", err)
	}
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding client-go scheme: %w", err)
	}
	if err := previewv1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding documentdb preview scheme: %w", err)
	}
	return s, nil
}
