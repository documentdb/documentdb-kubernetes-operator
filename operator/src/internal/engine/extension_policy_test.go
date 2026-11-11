// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/documentdb/documentdb-operator/internal/cnpg"
	"github.com/documentdb/documentdb-operator/internal/product"
)

type extensionPolicyAdapter struct {
	product.DocumentDBAdapter
	format       product.ExtensionVersionFormat
	dependencies []string
}

func (a extensionPolicyAdapter) Profile() product.ProductProfile {
	p := product.DocumentDBProfile()
	p.Name = "Widget"
	p.Postgres.Extension.Name = "widget"
	p.Postgres.ExtensionVersionFormat = a.format
	p.Postgres.ExtensionDependencies = a.dependencies
	return p
}

func TestHandleExtensionUpgradeVersionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		format                         product.ExtensionVersionFormat
		dependencies                   []string
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
		{
			name: "dotted auto with ordered dependencies", format: product.ExtensionVersionSemver,
			dependencies: []string{"widget-types", "widget_core"},
			installed:    "1.2.3", binary: "1.3.0", requested: "auto", wantSchema: "1.3.0",
			wantSQL: `BEGIN; ALTER EXTENSION "widget-types" UPDATE TO '1.3.0'; ALTER EXTENSION "widget_core" UPDATE TO '1.3.0'; ALTER EXTENSION "widget" UPDATE TO '1.3.0'; COMMIT;`,
		},
		{
			name: "dotted prerelease with ordered dependencies", format: product.ExtensionVersionSemver,
			dependencies: []string{"widget-types", "widget_core"},
			installed:    "1.2.3", binary: "1.3.0", requested: "1.3.0-rc.1", wantSchema: "1.3.0-rc.1",
			wantSQL: `BEGIN; ALTER EXTENSION "widget-types" UPDATE TO '1.3.0-rc.1'; ALTER EXTENSION "widget_core" UPDATE TO '1.3.0-rc.1'; ALTER EXTENSION "widget" UPDATE TO '1.3.0-rc.1'; COMMIT;`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestScheme(t)
			db := reconcilableCluster()
			db.Spec.SchemaVersion = tc.requested
			c := newFakeClient(t, s, nil, db)
			r, rec := newClusterReconciler(c, s, &sqlRecorder{})
			r.Adapter = extensionPolicyAdapter{format: tc.format, dependencies: tc.dependencies}
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

func TestHandleExtensionUpgradeDependencies(t *testing.T) {
	const (
		bothSQL = `BEGIN; ALTER EXTENSION "documentdb_core" UPDATE TO '1.0-0'; ALTER EXTENSION "documentdb" UPDATE TO '1.0-0'; COMMIT;`
		coreSQL = `BEGIN; ALTER EXTENSION "documentdb_core" UPDATE TO '1.0-0'; COMMIT;`
		mainSQL = `BEGIN; ALTER EXTENSION "documentdb" UPDATE TO '1.0-0'; COMMIT;`
	)
	for _, tc := range []struct {
		name, requested, installed, binary string
		coreInstalled, coreBinary          string
		coreOutput, wantSQL, wantErr       string
		queryErr, updateErr                bool
	}{
		{name: "auto upgrades core first", requested: "auto", wantSQL: bothSQL},
		{name: "explicit upgrades core first", requested: "1.0.0", wantSQL: bothSQL},
		{
			name:      "explicit intermediate target does not use dependency default",
			requested: "1.0.0", binary: "1.1-0", coreBinary: "1.1-0", wantSQL: bothSQL,
		},
		{name: "core already current", requested: "auto", coreInstalled: "1.0-0", wantSQL: mainSQL},
		{name: "repair auto", requested: "auto", installed: "1.0-0", wantSQL: coreSQL},
		{name: "repair explicit", requested: "1.0.0", installed: "1.0-0", wantSQL: coreSQL},
		{
			name: "repair explicit below binary", requested: "1.0.0", installed: "1.0-0",
			binary: "1.1-0", coreBinary: "1.1-0", wantSQL: coreSQL,
		},
		{name: "already current", requested: "auto", installed: "1.0-0", coreInstalled: "1.0-0"},
		{name: "two phase leaves both unchanged"},
		{name: "two phase does not repair without authorization", installed: "1.0-0"},
		{name: "older explicit target does not downgrade", requested: "0.116.0"},
		{name: "invalid explicit target does not repair", requested: "invalid", installed: "1.0-0"},
		{name: "target above binary does not repair", requested: "1.1.0", installed: "1.0-0"},
		{name: "binary rollback does not upgrade dependencies", requested: "auto", installed: "1.1-0"},
		{
			name: "dependency downgrade rejected", requested: "auto",
			coreInstalled: "1.1-0", coreBinary: "1.1-0", wantErr: "cannot downgrade dependency documentdb_core",
		},
		{
			name: "dependency binary too old", requested: "auto",
			coreBinary: "0.117-0", wantErr: "binary offers 0.117-0",
		},
		{
			name: "invalid dependency binary", requested: "auto",
			coreBinary: "invalid", wantErr: "failed to compare documentdb_core dependency binary version",
		},
		{
			name: "invalid dependency installed version", requested: "auto",
			coreInstalled: "invalid", wantErr: "failed to compare documentdb_core dependency installed version",
		},
		{
			name: "missing dependency", requested: "auto",
			coreOutput: "(0 rows)", wantErr: "extension versions missing or invalid",
		},
		{
			name: "uninstalled dependency", requested: "auto",
			coreOutput: "default_version | installed_version\n---+---\n1.0-0 | \n(1 row)",
			wantErr:    "extension versions missing or invalid",
		},
		{
			name: "dependency query failure", requested: "auto",
			queryErr: true, wantErr: "failed to check documentdb_core extension versions",
		},
		{
			name: "transaction failure does not advance schema status", requested: "auto",
			updateErr: true, wantSQL: bothSQL, wantErr: "failed to run ALTER EXTENSION documentdb UPDATE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installed, binary := tc.installed, tc.binary
			coreInstalled, coreBinary := tc.coreInstalled, tc.coreBinary
			if installed == "" {
				installed = "0.117-0"
			}
			if binary == "" {
				binary = "1.0-0"
			}
			if coreInstalled == "" {
				coreInstalled = "0.117-0"
			}
			if coreBinary == "" {
				coreBinary = "1.0-0"
			}
			versions := func(binary, installed string) string {
				return fmt.Sprintf("default_version | installed_version\n---+---\n%s | %s\n(1 row)", binary, installed)
			}
			s := newTestScheme(t)
			db := reconcilableCluster()
			db.Spec.SchemaVersion = tc.requested
			c := newFakeClient(t, s, nil, db)
			r, _ := newClusterReconciler(c, s, &sqlRecorder{})
			var updates []string
			r.SQLExecutor = func(_ context.Context, _ *cnpgv1.Cluster, sql string) (string, error) {
				switch sql {
				case cnpg.ExtensionVersionSQL("documentdb"):
					return versions(binary, installed), nil
				case cnpg.ExtensionVersionSQL("documentdb_core"):
					if tc.queryErr {
						return "", errInjected
					}
					if tc.coreOutput != "" {
						return tc.coreOutput, nil
					}
					return versions(coreBinary, coreInstalled), nil
				default:
					if !strings.HasPrefix(sql, "BEGIN;") {
						t.Fatalf("unexpected SQL: %s", sql)
					}
					updates = append(updates, sql)
					if tc.updateErr {
						return "", errInjected
					}
					installed, coreInstalled = "1.0-0", "1.0-0"
					return "", nil
				}
			}
			cluster := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{
				CurrentPrimary:  "db-1",
				InstancesStatus: map[cnpgv1.PodStatus][]string{cnpgv1.PodHealthy: {"db-1"}},
			}}
			wantSchema := r.extensionVersionToSemver(installed)
			err := r.HandleExtensionUpgrade(context.Background(), cluster, db)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.wantSQL == "" {
				if len(updates) != 0 {
					t.Fatalf("unexpected updates: %q", updates)
				}
			} else if len(updates) != 1 || updates[0] != tc.wantSQL {
				t.Fatalf("updates = %q, want %q", updates, tc.wantSQL)
			}
			if tc.wantSQL != "" && tc.wantErr == "" {
				wantSchema = "1.0.0"
				if err := r.HandleExtensionUpgrade(context.Background(), cluster, db); err != nil {
					t.Fatalf("repeat reconcile: %v", err)
				}
				if len(updates) != 1 {
					t.Fatalf("repeat reconcile ran another upgrade: %q", updates)
				}
			}
			if got := getDocumentDB(t, c).Status.SchemaVersion; got != wantSchema {
				t.Errorf("schema status = %q, want %q", got, wantSchema)
			}
		})
	}
}
