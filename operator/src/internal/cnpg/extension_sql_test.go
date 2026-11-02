// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func TestExtensionLifecycleSQLUsesProductNames(t *testing.T) {
	cases := map[string]string{
		ExtensionVersionSQL("pgcosmos"):         "SELECT default_version, installed_version FROM pg_available_extensions WHERE name = 'pgcosmos'",
		ExtensionUpdateSQL("pgcosmos", ""):      `ALTER EXTENSION "pgcosmos" UPDATE`,
		ExtensionUpdateSQL("pgcosmos", "1.2-0"): `ALTER EXTENSION "pgcosmos" UPDATE TO '1.2-0'`,
		ReplicationRoleCheckSQL("cosmos_admin"): "SELECT 1 FROM pg_roles WHERE rolname = 'streaming_replica' AND pg_has_role('streaming_replica', 'cosmos_admin', 'USAGE');",
		ReplicationRoleGrantSQL("cosmos_admin"): `GRANT "cosmos_admin" TO streaming_replica;`,
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestExtensionLifecycleSQLQuotesProductNames(t *testing.T) {
	for _, tt := range []struct {
		name       string
		identifier string
		literal    string
	}{
		{name: "pg-cosmos", identifier: `"pg-cosmos"`, literal: "'pg-cosmos'"},
		{name: "PgCosmos", identifier: `"PgCosmos"`, literal: "'PgCosmos'"},
		{name: `pg"cosmos`, identifier: `"pg""cosmos"`, literal: `'pg"cosmos'`},
		{name: "pg'cosmos", identifier: `"pg'cosmos"`, literal: "'pg''cosmos'"},
		{name: "select", identifier: `"select"`, literal: "'select'"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := ExtensionUpdateSQL(tt.name, ""), "ALTER EXTENSION "+tt.identifier+" UPDATE"; got != want {
				t.Errorf("default upgrade = %q, want %q", got, want)
			}
			if got, want := ExtensionUpdateSQL(tt.name, "1.2-0"), "ALTER EXTENSION "+tt.identifier+" UPDATE TO '1.2-0'"; got != want {
				t.Errorf("targeted upgrade = %q, want %q", got, want)
			}
			if got, want := ReplicationRoleGrantSQL(tt.name), "GRANT "+tt.identifier+" TO streaming_replica;"; got != want {
				t.Errorf("replication grant = %q, want %q", got, want)
			}
			if got, want := ExtensionVersionSQL(tt.name), "SELECT default_version, installed_version FROM pg_available_extensions WHERE name = "+tt.literal; got != want {
				t.Errorf("extension lookup = %q, want %q", got, want)
			}
			if got, want := ReplicationRoleCheckSQL(tt.name), "SELECT 1 FROM pg_roles WHERE rolname = 'streaming_replica' AND pg_has_role('streaming_replica', "+tt.literal+", 'USAGE');"; got != want {
				t.Errorf("replication role lookup = %q, want %q", got, want)
			}
		})
	}
}

func TestClusterImageReaders(t *testing.T) {
	cluster := &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{
		PostgresConfiguration: cnpgv1.PostgresConfiguration{Extensions: []cnpgv1.ExtensionConfiguration{
			{Name: "pgcosmos", ImageVolumeSource: corev1.ImageVolumeSource{Reference: "reg/pgcosmos:1"}},
		}},
		Plugins: []cnpgv1.PluginConfiguration{{Parameters: map[string]string{util.PLUGIN_PARAM_GATEWAY_IMAGE: "reg/gw:1"}}},
	}}
	if got := ExtensionImage(cluster, "pgcosmos"); got != "reg/pgcosmos:1" {
		t.Errorf("ExtensionImage = %q", got)
	}
	if got := ExtensionImage(cluster, "documentdb"); got != "" {
		t.Errorf("ExtensionImage(other) = %q, want empty", got)
	}
	if got := GatewayImage(cluster); got != "reg/gw:1" {
		t.Errorf("GatewayImage = %q", got)
	}
	if got := GatewayImage(&cnpgv1.Cluster{}); got != "" {
		t.Errorf("GatewayImage(no plugins) = %q, want empty", got)
	}
}
