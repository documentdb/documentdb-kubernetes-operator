// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"context"
	"reflect"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func TestSyncMonitoringPasswordSecret(t *testing.T) {
	ctx := context.Background()
	req := ctrl.Request{}
	req.Name, req.Namespace = "acct", "ns"
	intent := pgcosmosIntent()
	intent.Product.Postgres.MonitoringPasswordSecret = ""
	render := func() *cnpgv1.Cluster {
		return GetCnpgClusterSpecFromIntent(req, intent, "acct-sa", "", true, logr.Discard())
	}
	current := render()
	customRole := cnpgv1.RoleConfiguration{Name: "custom_role", Login: true}
	current.Spec.Managed.Roles = append(current.Spec.Managed.Roles, customRole)
	s := runtime.NewScheme()
	if err := cnpgv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	patches := 0
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(current).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			patches++
			return c.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	for _, tc := range []struct {
		name, secret string
		enabled      bool
	}{
		{"add", "monitor-v1", true},
		{"update", "monitor-v2", true},
		{"remove", "", true},
		{"restore", "monitor-v3", true},
		{"disable monitoring", "monitor-v3", false},
		{"re-enable monitoring", "monitor-v3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent.Product.Postgres.MonitoringPasswordSecret = tc.secret
			intent.Monitoring.Enabled = tc.enabled
			desired := render()
			previousRestart := current.Annotations["kubectl.kubernetes.io/restartedAt"]
			if err := SyncCnpgCluster(ctx, c, current, desired, nil); err != nil {
				t.Fatal(err)
			}
			updated := &cnpgv1.Cluster{}
			if err := c.Get(ctx, req.NamespacedName, updated); err != nil {
				t.Fatal(err)
			}
			wantSecret := tc.secret
			if !tc.enabled {
				wantSecret = ""
			}
			if got := updated.Spec.Plugins[0].Parameters[util.PLUGIN_PARAM_OTEL_PASSWORD_SECRET]; got != wantSecret {
				t.Errorf("plugin password secret = %q, want %q", got, wantSecret)
			}
			if _, present := updated.Spec.Plugins[0].Parameters[util.PLUGIN_PARAM_OTEL_PASSWORD_SECRET]; wantSecret == "" && present {
				t.Error("removed password secret parameter must be absent")
			}
			roles := updated.Spec.Managed.Roles
			if len(roles) != 2 || !reflect.DeepEqual(roles[0], desired.Spec.Managed.Roles[0]) || !reflect.DeepEqual(roles[1], customRole) {
				t.Errorf("managed roles = %+v, want desired monitoring role and unchanged custom role", roles)
			}
			if !reflect.DeepEqual(updated.Spec.PostgresConfiguration.PgHBA, intent.Product.Postgres.PgHBA) {
				t.Errorf("SCRAM-only HBA was changed: %v", updated.Spec.PostgresConfiguration.PgHBA)
			}
			if updated.Annotations["kubectl.kubernetes.io/restartedAt"] == previousRestart {
				t.Error("changed collector credentials must request a rolling restart")
			}
			before := patches
			if err := SyncCnpgCluster(ctx, c, updated, desired, nil); err != nil {
				t.Fatal(err)
			}
			if patches != before {
				t.Error("unchanged credentials must not patch or request another restart")
			}
			current = updated
		})
	}
}
