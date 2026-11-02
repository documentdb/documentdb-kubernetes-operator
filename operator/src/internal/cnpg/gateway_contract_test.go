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

	"github.com/documentdb/documentdb-operator/internal/product"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func TestGatewayContractPluginParams(t *testing.T) {
	gatewayKeys := []string{
		util.PLUGIN_PARAM_GATEWAY_CONTAINER_NAME,
		util.PLUGIN_PARAM_GATEWAY_PORT,
		util.PLUGIN_PARAM_GATEWAY_ARGS,
		util.PLUGIN_PARAM_GATEWAY_USERNAME_ENV,
		util.PLUGIN_PARAM_GATEWAY_PASSWORD_ENV,
	}
	req := ctrl.Request{}
	req.Name, req.Namespace = "golden", "golden-ns"

	intent := product.DocumentDBAdapter{}.ToClusterIntent(goldenBaseDocumentDB())
	params := GetCnpgClusterSpecFromIntent(req, intent, "sa", "", true, logr.Discard()).Spec.Plugins[0].Parameters
	for _, key := range gatewayKeys {
		if _, ok := params[key]; ok {
			t.Errorf("DocumentDB profile emitted %q", key)
		}
	}

	intent.Product.Gateway = product.GatewayProfile{
		ContainerName: "pgcosmos-gateway",
		Port:          8081,
		Args:          []string{"--port", "8081"},
		UsernameEnv:   "COSMOS_USER",
		PasswordEnv:   "COSMOS_KEY",
	}
	params = GetCnpgClusterSpecFromIntent(req, intent, "sa", "", true, logr.Discard()).Spec.Plugins[0].Parameters
	want := map[string]string{
		util.PLUGIN_PARAM_GATEWAY_CONTAINER_NAME: "pgcosmos-gateway",
		util.PLUGIN_PARAM_GATEWAY_PORT:           "8081",
		util.PLUGIN_PARAM_GATEWAY_ARGS:           `["--port","8081"]`,
		util.PLUGIN_PARAM_GATEWAY_USERNAME_ENV:   "COSMOS_USER",
		util.PLUGIN_PARAM_GATEWAY_PASSWORD_ENV:   "COSMOS_KEY",
	}
	for k, v := range want {
		if params[k] != v {
			t.Errorf("%s = %q, want %q", k, params[k], v)
		}
	}
}

// TestGatewayContractSync drives a live cluster's gateway contract through
// add, change (including explicit empty args), and a return to the defaults.
func TestGatewayContractSync(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := cnpgv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	req := ctrl.Request{}
	req.Name, req.Namespace = "golden", "golden-ns"
	intent := product.DocumentDBAdapter{}.ToClusterIntent(goldenBaseDocumentDB())
	render := func(gw product.GatewayProfile) *cnpgv1.Cluster {
		in := intent
		in.Product.Gateway = gw
		return GetCnpgClusterSpecFromIntent(req, in, "sa", "", true, logr.Discard())
	}

	// Explicit sidecar-injector defaults render exactly like the DocumentDB
	// profile, as the injector's defaulting webhook would normalize them.
	explicitDefaults := render(product.GatewayProfile{
		ContainerName: "documentdb-gateway", Port: 10260, UsernameEnv: "USERNAME", PasswordEnv: "PASSWORD",
	})
	if !reflect.DeepEqual(explicitDefaults.Spec.Plugins, render(product.GatewayProfile{}).Spec.Plugins) {
		t.Fatalf("explicit defaults rendered %v", explicitDefaults.Spec.Plugins[0].Parameters)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(render(product.GatewayProfile{})).Build()
	key := client.ObjectKey{Name: "golden", Namespace: "golden-ns"}
	sync := func(desired *cnpgv1.Cluster) (*cnpgv1.Cluster, *cnpgv1.Cluster) {
		t.Helper()
		before := &cnpgv1.Cluster{}
		if err := c.Get(ctx, key, before); err != nil {
			t.Fatal(err)
		}
		if err := SyncCnpgCluster(ctx, c, before.DeepCopy(), desired, nil); err != nil {
			t.Fatal(err)
		}
		after := &cnpgv1.Cluster{}
		if err := c.Get(ctx, key, after); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{
			util.PLUGIN_PARAM_GATEWAY_CONTAINER_NAME, util.PLUGIN_PARAM_GATEWAY_PORT, util.PLUGIN_PARAM_GATEWAY_ARGS,
			util.PLUGIN_PARAM_GATEWAY_USERNAME_ENV, util.PLUGIN_PARAM_GATEWAY_PASSWORD_ENV,
		} {
			got, gotOK := after.Spec.Plugins[0].Parameters[k]
			want, wantOK := desired.Spec.Plugins[0].Parameters[k]
			if got != want || gotOK != wantOK {
				t.Errorf("%s = %q (set %t), want %q (set %t)", k, got, gotOK, want, wantOK)
			}
		}
		return before, after
	}
	restarted := func(before, after *cnpgv1.Cluster) bool {
		const a = "kubectl.kubernetes.io/restartedAt"
		return after.Annotations[a] != "" && after.Annotations[a] != before.Annotations[a]
	}

	custom := render(product.GatewayProfile{
		ContainerName: "pgcosmos-gateway", Port: 8081, Args: []string{"--port", "8081"},
		UsernameEnv: "COSMOS_USER", PasswordEnv: "COSMOS_KEY",
	})
	if before, after := sync(custom); !restarted(before, after) {
		t.Error("adding the gateway contract did not request a rolling restart")
	}

	changed := render(product.GatewayProfile{
		ContainerName: "pgcosmos-gateway-v2", Port: 8082, Args: []string{},
		UsernameEnv: "COSMOS_USER2", PasswordEnv: "COSMOS_KEY2",
	})
	if got := changed.Spec.Plugins[0].Parameters[util.PLUGIN_PARAM_GATEWAY_ARGS]; got != "[]" {
		t.Fatalf("explicit empty args rendered %q, want []", got)
	}
	if before, after := sync(changed); !restarted(before, after) {
		t.Error("changing the gateway contract did not request a rolling restart")
	}

	// Idempotent: re-syncing the same contract does not patch.
	if before, after := sync(changed); after.ResourceVersion != before.ResourceVersion {
		t.Error("re-syncing an unchanged gateway contract patched the cluster")
	}

	if before, after := sync(explicitDefaults); !restarted(before, after) {
		t.Error("returning to the default gateway contract did not request a rolling restart")
	}
	if before, after := sync(render(product.GatewayProfile{})); after.ResourceVersion != before.ResourceVersion {
		t.Error("re-syncing the default gateway contract patched the cluster")
	}
}
