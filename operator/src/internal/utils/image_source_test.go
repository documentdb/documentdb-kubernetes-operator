// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package util

import "testing"

// TestImageRepoGettersDefault verifies the getters return the compiled-in
// defaults when their env overrides are unset, preserving existing behavior.
func TestImageRepoGettersDefault(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"ExtensionImageRepo", ExtensionImageRepo(), DOCUMENTDB_EXTENSION_IMAGE_REPO},
		{"GatewayImageRepo", GatewayImageRepo(), GATEWAY_IMAGE_REPO},
		{"OtelCollectorImage", OtelCollectorImage(), DEFAULT_OTEL_COLLECTOR_IMAGE},
		{"PostgresImage", PostgresImage(), ""},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestImageRepoGettersEnvOverride verifies each getter honors its env override.
func TestImageRepoGettersEnvOverride(t *testing.T) {
	t.Setenv(DOCUMENTDB_EXTENSION_IMAGE_REPO_ENV, "mcr.microsoft.com/documentdb/documentdb")
	t.Setenv(GATEWAY_IMAGE_REPO_ENV, "mcr.microsoft.com/documentdb/gateway")
	t.Setenv(OTEL_COLLECTOR_IMAGE_ENV, "mcr.microsoft.com/oss/otel/opentelemetry-collector-contrib:0.149.0")
	t.Setenv(POSTGRES_IMAGE_ENV, "mcr.microsoft.com/oss/cloudnative-pg/postgresql:17.6")

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"ExtensionImageRepo", ExtensionImageRepo(), "mcr.microsoft.com/documentdb/documentdb"},
		{"GatewayImageRepo", GatewayImageRepo(), "mcr.microsoft.com/documentdb/gateway"},
		{"OtelCollectorImage", OtelCollectorImage(), "mcr.microsoft.com/oss/otel/opentelemetry-collector-contrib:0.149.0"},
		{"PostgresImage", PostgresImage(), "mcr.microsoft.com/oss/cloudnative-pg/postgresql:17.6"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestCombinedImage(t *testing.T) {
	repo := DOCUMENTDB_COMBINED_IMAGE_REPO + ":18-"
	cases := []struct {
		name, explicit, spec, env, want string
	}{
		{"explicit versioned", "ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb:1.0.0", "0.117.0", "", repo + "1.0.0"},
		{"explicit with registry port", "localhost:5000/documentdb:0.117.0", "", "", repo + "0.117.0"},
		{"tag and digest ignores the tag, as admission does", "ghcr.io/x/documentdb:1.1.0@sha256:abc", "0.117.0", "", repo + "0.117.0"},
		{"explicit RC tag", "ghcr.io/x/documentdb:1.0-RC2", "", "", repo + "1.0-RC2"},
		{"explicit digest only uses spec version", "ghcr.io/x/documentdb@sha256:abc", "0.117.0", "", repo + "0.117.0"},
		{"explicit non-version tag uses env version", "ghcr.io/x/documentdb:latest", "", "1.0.0", repo + "1.0.0"},
		{"spec version", "", "0.117.0", "1.0.0", repo + "0.117.0"},
		{"env version", "", "", "0.117.0", repo + "0.117.0"},
		{"default", "", "", "", repo + DEFAULT_DOCUMENTDB_TAG},
	}
	for _, c := range cases {
		if got := CombinedImage(c.explicit, c.spec, c.env); got != c.want {
			t.Errorf("%s: CombinedImage(%q, %q, %q) = %q, want %q", c.name, c.explicit, c.spec, c.env, got, c.want)
		}
	}

	t.Setenv(DOCUMENTDB_COMBINED_IMAGE_REPO_ENV, "mcr.microsoft.com/documentdb/documentdb-combined")
	if got, want := CombinedImage("", "1.0.0", ""), "mcr.microsoft.com/documentdb/documentdb-combined:18-1.0.0"; got != want {
		t.Errorf("with repo override = %q, want %q", got, want)
	}
}
