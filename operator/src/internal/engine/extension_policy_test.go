// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/documentdb/documentdb-operator/internal/product"
)

type extensionPolicyAdapter struct {
	product.DocumentDBAdapter
	format product.ExtensionVersionFormat
}

func (a extensionPolicyAdapter) Profile() product.ProductProfile {
	p := product.DocumentDBProfile()
	p.Name = "Widget"
	p.Postgres.Extension.Name = "widget"
	p.Postgres.ExtensionVersionFormat = a.format
	return p
}

func TestHandleExtensionUpgradeVersionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		format                         product.ExtensionVersionFormat
		installed, binary, requested   string
		wantSQL, wantSchema, wantEvent string
	}{
		{
			name: "dotted auto", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", requested: "auto",
			wantSQL: `ALTER EXTENSION "widget" UPDATE`, wantSchema: "1.3.0",
		},
		{
			name: "dotted explicit", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", requested: "1.3.0",
			wantSQL: `ALTER EXTENSION "widget" UPDATE TO '1.3.0'`, wantSchema: "1.3.0",
		},
		{
			name:      "hyphen auto",
			installed: "1.2-3", binary: "1.3-0", requested: "auto",
			wantSQL: `ALTER EXTENSION "widget" UPDATE`, wantSchema: "1.3.0",
		},
		{
			name:      "hyphen explicit",
			installed: "1.2-3", binary: "1.3-0", requested: "1.3.0",
			wantSQL: `ALTER EXTENSION "widget" UPDATE TO '1.3-0'`, wantSchema: "1.3.0",
		},
		{
			name: "dotted two phase", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", wantSchema: "1.2.3",
			wantEvent: `binary version is 1.3.0, schema is at 1.2.3. Set spec.schemaVersion to "1.3.0"`,
		},
		{
			name:      "hyphen two phase",
			installed: "1.2-3", binary: "1.3-0", wantSchema: "1.2.3",
			wantEvent: `binary version is 1.3.0, schema is at 1.2.3. Set spec.schemaVersion to "1.3.0"`,
		},
		{
			name: "dotted rollback", format: product.ExtensionVersionSemver,
			installed: "1.3.0", binary: "1.2.3", requested: "auto",
			wantSchema: "1.3.0", wantEvent: "ExtensionRollback",
		},
		{
			name: "dotted target beyond binary", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", requested: "1.4.0", wantSchema: "1.2.3",
		},
		{
			name: "dotted target already installed", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", requested: "1.2.3", wantSchema: "1.2.3",
		},
		{
			name: "dotted prerelease explicit", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", requested: "1.3.0-rc.1",
			wantSQL: `ALTER EXTENSION "widget" UPDATE TO '1.3.0-rc.1'`, wantSchema: "1.3.0-rc.1",
		},
		{
			name: "dotted prerelease two phase", format: product.ExtensionVersionSemver,
			installed: "1.3.0-rc.1", binary: "1.3.0", wantSchema: "1.3.0-rc.1",
			wantEvent: `binary version is 1.3.0, schema is at 1.3.0-rc.1`,
		},
		{
			name:      "hyphen policy still rejects dotted versions",
			installed: "1.2.3", binary: "1.3.0", requested: "auto", wantSchema: "1.2.3",
		},
		{
			name: "dotted invalid target", format: product.ExtensionVersionSemver,
			installed: "1.2.3", binary: "1.3.0", requested: "not-a-version", wantSchema: "1.2.3",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := reconcilableCluster()
			db.Spec.SchemaVersion = tc.requested
			c := newFakeClient(t, s, nil, db)
			r, rec := newClusterReconciler(c, s, &sqlRecorder{})
			r.Adapter = extensionPolicyAdapter{format: tc.format}
			var updates []string
			r.SQLExecutor = func(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
				if strings.Contains(sql, "pg_available_extensions") {
					return "default_version | installed_version\n---+---\n" + tc.binary + " | " + tc.installed + "\n(1 row)", nil
				}
				updates = append(updates, sql)
				return "", nil
			}
			cluster := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{
				CurrentPrimary:  "db-1",
				InstancesStatus: map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"db-1"}},
			}}
			if err := r.HandleExtensionUpgrade(context.Background(), cluster, db); err != nil {
				t.Fatal(err)
			}
			if tc.wantSQL == "" {
				if len(updates) != 0 {
					t.Fatalf("unexpected upgrade: %q", updates)
				}
			} else if len(updates) != 1 || updates[0] != tc.wantSQL {
				t.Fatalf("upgrade SQL = %q, want %q", updates, tc.wantSQL)
			}
			if got := getDocumentDB(t, c).Status.SchemaVersion; got != tc.wantSchema {
				t.Errorf("schema status = %q, want %q", got, tc.wantSchema)
			}
			if events := drainEvents(rec); tc.wantEvent != "" && !hasEvent(events, tc.wantEvent) {
				t.Errorf("events = %q, want %q", events, tc.wantEvent)
			}
		})
	}
}
