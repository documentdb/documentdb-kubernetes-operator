// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// pgcosmosIntent is a synthetic second product used to prove the renderer has
// no DocumentDB-specific knowledge beyond documented wire contracts.
func pgcosmosIntent() product.ClusterIntent {
	return product.ClusterIntent{
		Images: product.Images{
			PostgresExtension: "reg/pgcosmos:1",
			Gateway:           "reg/pgcosmos-gateway:1",
			Postgres:          "reg/postgres:17",
		},
		Topology:              product.Topology{Instances: 3},
		Storage:               product.Storage{PvcSize: "20Gi"},
		Resources:             product.Resources{Memory: "4Gi", CPU: "2"},
		Monitoring:            &dbv1.MonitoringSpec{Enabled: true},
		TLS:                   product.TLS{GatewaySecret: "acct-tls"},
		Identity:              product.Identity{Name: "acct", UID: "uid-1", APIVersion: "cosmos.azure.com/v1", Kind: "CosmosAccount"},
		Postgres:              product.Postgres{Parameters: map[string]string{"work_mem": "8MB", "pgcosmos.locked": "user"}},
		CredentialSecret:      "cosmos-credentials",
		SidecarInjectorPlugin: "sidecar.cosmos.azure.com",
		WALReplicaPlugin:      "wal.cosmos.azure.com",
		Product: product.ProductProfile{
			Name: "Cosmos DB",
			Postgres: product.PostgresProfile{
				Extension: product.ExtensionSpec{
					Name:                 "pgcosmos",
					DynamicLibraryPath:   []string{"lib"},
					ExtensionControlPath: []string{"share"},
				},
				AdditionalLibraries:      []string{"pgcosmos_core"},
				BootstrapSQL:             []string{"CREATE EXTENSION pgcosmos CASCADE"},
				PgHBA:                    []string{"host all all 0.0.0.0/0 scram-sha-256"},
				MonitoringPasswordSecret: "cosmos-monitor-password",
				ProtectedParameters:      map[string]string{"pgcosmos.locked": "operator"},
				ReplicationAdminRole:     "cosmos_admin",
			},
			Gateway: product.GatewayProfile{ContainerName: "pgcosmos-gateway", Port: 8081},
		},
	}
}

func TestSecondProductRendersWithoutDocumentDBLiterals(t *testing.T) {
	req := ctrl.Request{}
	req.Name, req.Namespace = "acct", "ns"
	intent := pgcosmosIntent()
	cluster := GetCnpgClusterSpecFromIntent(req, intent, "acct-sa", "", true, logr.Discard())

	raw, err := json.Marshal(cluster)
	if err != nil {
		t.Fatal(err)
	}
	// documentDbCredentialSecret is the sidecar plugin's parameter key, a
	// documented wire contract shared by every product.
	scrubbed := regexp.MustCompile(regexp.QuoteMeta(util.PLUGIN_PARAM_CREDENTIAL_SECRET)).ReplaceAll(raw, nil)
	if loc := regexp.MustCompile(`(?i)documentdb`).FindIndex(scrubbed); loc != nil {
		lo, hi := max(loc[0]-80, 0), min(loc[1]+80, len(scrubbed))
		t.Fatalf("rendered pgcosmos cluster contains a DocumentDB literal: ...%s...", scrubbed[lo:hi])
	}

	pg := cluster.Spec.PostgresConfiguration
	if len(pg.Extensions) != 1 || pg.Extensions[0].Name != "pgcosmos" || pg.Extensions[0].ImageVolumeSource.Reference != "reg/pgcosmos:1" {
		t.Errorf("extensions = %+v", pg.Extensions)
	}
	if !slices.Contains(pg.AdditionalLibraries, "pgcosmos_core") {
		t.Errorf("additional libraries = %v", pg.AdditionalLibraries)
	}
	if !slices.Equal(pg.PgHBA, intent.Product.Postgres.PgHBA) {
		t.Errorf("pg_hba = %v", pg.PgHBA)
	}
	if got := pg.Parameters["pgcosmos.locked"]; got != "operator" {
		t.Errorf("protected product GUC = %q, want operator", got)
	}
	if got := pg.Parameters["work_mem"]; got != "8MB" {
		t.Errorf("user GUC = %q, want 8MB", got)
	}
	if _, ok := pg.Parameters["cron.database_name"]; ok {
		t.Error("DocumentDB-only cron.database_name leaked into pgcosmos parameters")
	}
	if cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.InitDB == nil ||
		len(cluster.Spec.Bootstrap.InitDB.PostInitSQL) == 0 ||
		cluster.Spec.Bootstrap.InitDB.PostInitSQL[0] != "CREATE EXTENSION pgcosmos CASCADE" {
		t.Errorf("bootstrap = %+v", cluster.Spec.Bootstrap)
	}

	if len(cluster.Spec.Plugins) == 0 || cluster.Spec.Plugins[0].Name != "sidecar.cosmos.azure.com" {
		t.Fatalf("plugins = %+v", cluster.Spec.Plugins)
	}
	params := cluster.Spec.Plugins[0].Parameters
	for k, want := range map[string]string{
		util.PLUGIN_PARAM_GATEWAY_IMAGE:          "reg/pgcosmos-gateway:1",
		util.PLUGIN_PARAM_CREDENTIAL_SECRET:      "cosmos-credentials",
		util.PLUGIN_PARAM_GATEWAY_TLS_SECRET:     "acct-tls",
		util.PLUGIN_PARAM_GATEWAY_CONTAINER_NAME: "pgcosmos-gateway",
		util.PLUGIN_PARAM_GATEWAY_PORT:           "8081",
		util.PLUGIN_PARAM_OTEL_PASSWORD_SECRET:   "cosmos-monitor-password",
	} {
		if params[k] != want {
			t.Errorf("plugin param %s = %q, want %q", k, params[k], want)
		}
	}
	role := cluster.Spec.Managed.Roles[0]
	if role.Name != otelcfg.MonitorRoleName || !role.Login || role.DisablePassword ||
		role.PasswordSecret == nil || role.PasswordSecret.Name != intent.Product.Postgres.MonitoringPasswordSecret {
		t.Errorf("SCRAM monitoring role = %+v", role)
	}

	if len(cluster.OwnerReferences) != 1 || cluster.OwnerReferences[0].Kind != "CosmosAccount" {
		t.Errorf("owner references = %+v", cluster.OwnerReferences)
	}
}

func TestSecondProductResourceValidationUsesProductPath(t *testing.T) {
	res := product.Resources{Memory: "not-a-quantity"}
	errs := ValidateResourcesFor(res, false, field.NewPath("spec", "compute"), DefaultSplitConfig())
	if len(errs) == 0 {
		t.Fatal("expected a validation error")
	}
	for _, e := range errs {
		if regexp.MustCompile(`^spec\.compute`).FindString(e.Field) == "" {
			t.Errorf("error field %q is not under spec.compute", e.Field)
		}
	}
}

// TestSecondProductOtelConfigHash checks that the rendered config hash is the
// hash of the collector config generated with the product's metric naming.
func TestSecondProductOtelConfigHash(t *testing.T) {
	req := ctrl.Request{}
	req.Name, req.Namespace = "acct", "ns"
	intent := pgcosmosIntent()
	intent.Product.Naming = product.NamingProfile{
		MetricClusterAttribute: "cosmos.cluster", OperatorName: "cosmos-operator", HealthMetricName: "cosmos.postgres.up",
	}
	data, err := otelcfg.GenerateConfigMapDataWithOptions(OTelOptions(intent.Product), "acct", "ns", intent.Monitoring)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data["static.yaml"], "metric_name: cosmos.postgres.up\n") {
		t.Fatalf("static.yaml:\n%s", data["static.yaml"])
	}
	got := GetCnpgClusterSpecFromIntent(req, intent, "acct-sa", "", true, logr.Discard()).Spec.Plugins[0].Parameters[util.PLUGIN_PARAM_OTEL_CONFIG_HASH]
	if want := otelcfg.HashConfigMapData(data); got != want {
		t.Fatalf("otelConfigHash = %q, want %q", got, want)
	}

	intent.Product.Naming.HealthMetricName = ""
	if def := GetCnpgClusterSpecFromIntent(req, intent, "acct-sa", "", true, logr.Discard()).Spec.Plugins[0].Parameters[util.PLUGIN_PARAM_OTEL_CONFIG_HASH]; def == got {
		t.Fatal("the health metric name does not affect the config hash")
	}
}
