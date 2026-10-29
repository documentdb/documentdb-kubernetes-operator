// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	otelcfg "github.com/documentdb/documentdb-operator/internal/otel"
	"github.com/documentdb/documentdb-operator/internal/product"
)

// noMonitoringSchemasAdapter is DocumentDB with a profile that declares no
// monitoring schemas.
type noMonitoringSchemasAdapter struct{ product.DocumentDBAdapter }

func (a noMonitoringSchemasAdapter) Profile() product.ProductProfile {
	p := product.DocumentDBProfile()
	p.Postgres.MonitoringSchemas = nil
	return p
}

func (a noMonitoringSchemasAdapter) Intent(db *dbpreview.DocumentDB) product.ClusterIntent {
	intent := a.DocumentDBAdapter.Intent(db)
	intent.Product = a.Profile()
	return intent
}

// TestClusterReconcileSkipsMonitoringGrantsWithoutSchemas checks that a product
// without MonitoringSchemas runs no monitoring-role SQL, and that disabling
// monitoring declares the role absent instead of retaining it.
func TestClusterReconcileSkipsMonitoringGrantsWithoutSchemas(t *testing.T) {
	for _, monitoring := range []bool{true, false} {
		t.Run(fmt.Sprintf("monitoring=%t", monitoring), func(t *testing.T) {
			s := newTestScheme(t)
			db := reconcilableCluster()
			db.Spec.ExposeViaService.ServiceType = ""
			seed := db.DeepCopy()
			seed.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: true}
			cluster := healthyCNPGCluster(t, s, seed)
			db.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: monitoring}
			c := newFakeClient(t, s, nil, db, cluster)
			sql := &sqlRecorder{roleOut: "(1 row)"}
			r, _ := newClusterReconciler(c, s, sql)
			r.Adapter = noMonitoringSchemasAdapter{}

			if res, err := r.Reconcile(context.Background(), clusterKey); err != nil || res != (ctrl.Result{}) {
				t.Fatalf("expected converged reconcile, got %v, %v", res, err)
			}
			if sql.ran(otelcfg.MonitorRoleName) {
				t.Fatalf("monitoring-role SQL ran without monitoring schemas: %q", sql.commands)
			}

			updated := &cnpgv1.Cluster{}
			if err := c.Get(context.Background(), clusterKey.NamespacedName, updated); err != nil {
				t.Fatal(err)
			}
			want := cnpgv1.EnsurePresent
			if !monitoring {
				want = cnpgv1.EnsureAbsent
			}
			for _, role := range updated.Spec.Managed.Roles {
				if role.Name == otelcfg.MonitorRoleName {
					if role.Ensure != want {
						t.Fatalf("monitoring role ensure = %q, want %q", role.Ensure, want)
					}
					return
				}
			}
			t.Fatal("the CNPG Cluster does not declare the OTel monitoring role")
		})
	}
}
