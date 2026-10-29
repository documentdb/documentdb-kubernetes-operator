// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

// TestDocumentDBAdapterExtensionImage covers the extension-image resolution
// priority: explicit image, then spec version, then the change-stream override,
// then the product default.
func TestDocumentDBAdapterExtensionImage(t *testing.T) {
	a := DocumentDBAdapter{}
	tests := []struct {
		name string
		spec dbv1.DocumentDBSpec
		want string
	}{
		{"explicit image overrides feature gate", dbv1.DocumentDBSpec{
			Image:        &dbv1.ImageSpec{DocumentDB: "custom-registry/custom-image:v1"},
			FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: true},
		}, "custom-registry/custom-image:v1"},
		{"documentDBVersion resolves image", dbv1.DocumentDBSpec{DocumentDBVersion: "1.2.3"}, util.DOCUMENTDB_EXTENSION_IMAGE_REPO + ":1.2.3"},
		{"explicit overrides documentDBVersion", dbv1.DocumentDBSpec{
			Image:             &dbv1.ImageSpec{DocumentDB: "custom-registry/custom-image:v1"},
			DocumentDBVersion: "1.2.3",
		}, "custom-registry/custom-image:v1"},
		{"changestream enabled", dbv1.DocumentDBSpec{FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: true}}, util.CHANGESTREAM_DOCUMENTDB_IMAGE},
		{"changestream disabled falls through to default", dbv1.DocumentDBSpec{FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: false}}, util.DEFAULT_DOCUMENTDB_IMAGE},
		{"default when no overrides", dbv1.DocumentDBSpec{}, util.DEFAULT_DOCUMENTDB_IMAGE},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.ExtensionImage(&dbv1.DocumentDB{Spec: tt.spec}); got != tt.want {
				t.Errorf("ExtensionImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDocumentDBAdapterGatewayImage covers the gateway-image resolution priority.
func TestDocumentDBAdapterGatewayImage(t *testing.T) {
	a := DocumentDBAdapter{}
	tests := []struct {
		name string
		spec dbv1.DocumentDBSpec
		want string
	}{
		{"default when no overrides", dbv1.DocumentDBSpec{}, util.DEFAULT_GATEWAY_IMAGE},
		{"explicit image takes precedence", dbv1.DocumentDBSpec{
			Image:        &dbv1.ImageSpec{Gateway: "custom-registry/custom-gateway:v1"},
			FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: true},
		}, "custom-registry/custom-gateway:v1"},
		{"documentDBVersion resolves image", dbv1.DocumentDBSpec{DocumentDBVersion: "1.2.3"}, util.GATEWAY_IMAGE_REPO + ":1.2.3"},
		{"explicit overrides documentDBVersion", dbv1.DocumentDBSpec{
			Image:             &dbv1.ImageSpec{Gateway: "custom-registry/custom-gateway:v1"},
			DocumentDBVersion: "1.2.3",
		}, "custom-registry/custom-gateway:v1"},
		{"changestream enabled", dbv1.DocumentDBSpec{FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: true}}, util.CHANGESTREAM_GATEWAY_IMAGE},
		{"changestream disabled", dbv1.DocumentDBSpec{FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: false}}, util.DEFAULT_GATEWAY_IMAGE},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.GatewayImage(&dbv1.DocumentDB{Spec: tt.spec}); got != tt.want {
				t.Errorf("GatewayImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDocumentDBAdapterImageResolutionEnvVar confirms the DOCUMENTDB_VERSION env
// fallback flows through the profile-driven resolver.
func TestDocumentDBAdapterImageResolutionEnvVar(t *testing.T) {
	t.Setenv(util.DOCUMENTDB_VERSION_ENV, "0.200.0")
	a := DocumentDBAdapter{}
	db := &dbv1.DocumentDB{Spec: dbv1.DocumentDBSpec{}}

	if got, want := a.ExtensionImage(db), util.DOCUMENTDB_EXTENSION_IMAGE_REPO+":0.200.0"; got != want {
		t.Errorf("ExtensionImage() = %q, want %q", got, want)
	}
	if got, want := a.GatewayImage(db), util.GATEWAY_IMAGE_REPO+":0.200.0"; got != want {
		t.Errorf("GatewayImage() = %q, want %q", got, want)
	}
}

// TestDocumentDBAdapterRepoOverrideFlowsToDefault confirms that a repository env
// override is honored on the default-tag path (no explicit image and no version),
// so a mirrored registry stays consistent for the default image too.
func TestDocumentDBAdapterRepoOverrideFlowsToDefault(t *testing.T) {
	t.Setenv(util.DOCUMENTDB_EXTENSION_IMAGE_REPO_ENV, "mcr.microsoft.com/documentdb/documentdb")
	t.Setenv(util.GATEWAY_IMAGE_REPO_ENV, "mcr.microsoft.com/documentdb/gateway")
	a := DocumentDBAdapter{}
	db := &dbv1.DocumentDB{Spec: dbv1.DocumentDBSpec{}}

	if got, want := a.ExtensionImage(db), "mcr.microsoft.com/documentdb/documentdb:"+util.DEFAULT_DOCUMENTDB_TAG; got != want {
		t.Errorf("ExtensionImage() = %q, want %q", got, want)
	}
	if got, want := a.GatewayImage(db), "mcr.microsoft.com/documentdb/gateway:"+util.DEFAULT_DOCUMENTDB_TAG; got != want {
		t.Errorf("GatewayImage() = %q, want %q", got, want)
	}
}

// TestDocumentDBAdapterPostgresImage covers the base PostgreSQL operand image
// resolution: a CR-pinned image wins, else the operator-level POSTGRES_IMAGE
// default, else empty (defer to CloudNativePG's built-in operand default).
func TestDocumentDBAdapterPostgresImage(t *testing.T) {
	a := DocumentDBAdapter{}

	t.Run("empty when unset defers to CNPG", func(t *testing.T) {
		got := a.ToClusterIntent(&dbv1.DocumentDB{Spec: dbv1.DocumentDBSpec{}}).Images.Postgres
		if got != "" {
			t.Errorf("Postgres = %q, want empty", got)
		}
	})

	t.Run("operator default from env", func(t *testing.T) {
		t.Setenv(util.POSTGRES_IMAGE_ENV, "mcr.microsoft.com/oss/cloudnative-pg/postgresql:17.6")
		got := a.ToClusterIntent(&dbv1.DocumentDB{Spec: dbv1.DocumentDBSpec{}}).Images.Postgres
		if want := "mcr.microsoft.com/oss/cloudnative-pg/postgresql:17.6"; got != want {
			t.Errorf("Postgres = %q, want %q", got, want)
		}
	})

	t.Run("CR pin overrides operator default", func(t *testing.T) {
		t.Setenv(util.POSTGRES_IMAGE_ENV, "mcr.microsoft.com/oss/cloudnative-pg/postgresql:17.6")
		db := &dbv1.DocumentDB{Spec: dbv1.DocumentDBSpec{
			Image: &dbv1.ImageSpec{Postgres: "custom-registry/pg:16"},
		}}
		if got, want := a.ToClusterIntent(db).Images.Postgres, "custom-registry/pg:16"; got != want {
			t.Errorf("Postgres = %q, want %q", got, want)
		}
	})
}

// TestDocumentDBAdapterRendererInputs covers the per-instance renderer inputs the
// adapter maps from the custom resource onto the intent.
func TestDocumentDBAdapterRendererInputs(t *testing.T) {
	t.Setenv(util.DOCUMENTDB_IMAGE_PULL_POLICY_ENV, "bogus")
	t.Setenv(util.GATEWAY_IMAGE_PULL_POLICY_ENV, "Never")
	certs := &cnpgv1.CertificatesConfiguration{ServerTLSSecret: "pg-tls"}
	db := &dbv1.DocumentDB{Spec: dbv1.DocumentDBSpec{
		Resource: dbv1.Resource{
			Memory:  "8Gi",
			CPU:     "2",
			Gateway: &dbv1.ComponentResources{Memory: "1Gi"},
		},
		Monitoring:   &dbv1.MonitoringSpec{Enabled: true},
		LogLevel:     "debug",
		Timeouts:     dbv1.Timeouts{StopDelay: 90},
		Postgres:     &dbv1.PostgresSpec{Parameters: map[string]string{"work_mem": "8MB"}},
		FeatureGates: map[string]bool{dbv1.FeatureGateChangeStreams: true},
		TLS:          &dbv1.TLSConfiguration{Postgres: certs},
	}}
	db.Status.TLS = &dbv1.TLSStatus{Ready: false, SecretName: "gw-tls"}

	intent := DocumentDBAdapter{}.ToClusterIntent(db)

	if intent.Resources.Memory != "8Gi" || intent.Resources.CPU != "2" ||
		intent.Resources.Gateway == nil || intent.Resources.Gateway.Memory != "1Gi" || intent.Resources.Database != nil {
		t.Errorf("Resources = %+v", intent.Resources)
	}
	if intent.Monitoring != db.Spec.Monitoring {
		t.Errorf("Monitoring not passed through")
	}
	if intent.Postgres.LogLevel != "debug" || intent.Postgres.StopDelay != 90 || intent.Postgres.Parameters["work_mem"] != "8MB" {
		t.Errorf("Postgres = %+v", intent.Postgres)
	}
	if !intent.FeatureGates.ChangeStreams || intent.FeatureGates.IOUring {
		t.Errorf("FeatureGates = %+v", intent.FeatureGates)
	}
	if intent.TLS.GatewaySecret != "" {
		t.Errorf("gateway TLS secret must stay empty until TLS is ready, got %q", intent.TLS.GatewaySecret)
	}
	if intent.TLS.Postgres != certs {
		t.Errorf("Postgres certificates not passed through")
	}
	if intent.Images.PostgresExtensionPullPolicy != "" || intent.Images.GatewayPullPolicy != "Never" {
		t.Errorf("pull policies = %q/%q", intent.Images.PostgresExtensionPullPolicy, intent.Images.GatewayPullPolicy)
	}

	db.Status.TLS.Ready = true
	if got := (DocumentDBAdapter{}).ToClusterIntent(db).TLS.GatewaySecret; got != "gw-tls" {
		t.Errorf("ready gateway TLS secret = %q, want gw-tls", got)
	}
}
