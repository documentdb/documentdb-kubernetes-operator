// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package core_test

import (
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/documentdb/documentdb-operator/pkg/core"
)

// Example renders a CNPG Cluster for a product other than DocumentDB.
func Example() {
	intent := core.ClusterIntent{
		Images:                core.Images{PostgresExtension: "reg/pgcosmos:1", Gateway: "reg/pgcosmos-gateway:1"},
		Topology:              core.Topology{Instances: 1},
		Storage:               core.Storage{PvcSize: "10Gi"},
		Identity:              core.Identity{Name: "acct", APIVersion: "cosmos.azure.com/v1", Kind: "CosmosAccount"},
		CredentialSecret:      "cosmos-credentials",
		SidecarInjectorPlugin: "sidecar.cosmos.azure.com",
		Product: core.ProductProfile{
			Name: "Cosmos DB",
			Postgres: core.PostgresProfile{
				Extension:           core.ExtensionSpec{Name: "pgcosmos"},
				ProtectedParameters: map[string]string{"pgcosmos.mode": "operator"},
			},
			Gateway: core.GatewayProfile{ContainerName: "pgcosmos-gateway"},
		},
	}
	req := ctrl.Request{}
	req.Name, req.Namespace = "acct", "ns"

	cluster := core.RenderCluster(req, intent, "acct-sa", "", true, logr.Discard())
	fmt.Println(cluster.Spec.PostgresConfiguration.Extensions[0].Name)
	fmt.Println(cluster.Spec.PostgresConfiguration.Parameters["pgcosmos.mode"])
	fmt.Println(core.ExtensionUpdateSQL("pgcosmos", ""))
	// Output:
	// pgcosmos
	// operator
	// ALTER EXTENSION "pgcosmos" UPDATE
}

// ExampleNewReplicationContext builds the contexts a product's
// ReplicationHooks returns for a primary awaiting promotion and a replica.
func ExampleNewReplicationContext() {
	promoting := core.NewReplicationContext(core.ReplicationPrimary, "account-east", "account-west")
	fmt.Println(promoting.IsPrimary(), promoting.EndpointEnabled())

	replica := core.NewReplicationContext(core.ReplicationReplica, "", "")
	replica.PrimaryCNPGClusterName = "account-east"
	replica.OtherCNPGClusterNames = []string{"account-east"}
	fmt.Println(replica.IsPrimary(), replica.IsReplicating(), replica.GetReplicationSource())
	// Output:
	// true false
	// false true account-east
}
