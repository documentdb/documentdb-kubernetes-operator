// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"
	"encoding/json"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	cnpgoperator "github.com/cloudnative-pg/cnpg-i/pkg/operator"

	"github.com/documentdb/cnpg-i-sidecar-injector/pkg/metadata"
)

func TestValidateClusterChangeRetainsParameterErrors(t *testing.T) {
	oldCluster := &cnpgv1.Cluster{}
	newCluster := &cnpgv1.Cluster{
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: metadata.PluginName,
					Parameters: map[string]string{
						"otelCollectorImage": "otel/opentelemetry-collector-contrib:test",
					},
				},
			},
		},
	}

	oldJSON, err := json.Marshal(oldCluster)
	if err != nil {
		t.Fatalf("marshal old cluster: %v", err)
	}
	newJSON, err := json.Marshal(newCluster)
	if err != nil {
		t.Fatalf("marshal new cluster: %v", err)
	}

	result, err := (Implementation{}).ValidateClusterChange(
		context.Background(),
		&cnpgoperator.OperatorValidateClusterChangeRequest{
			OldCluster: oldJSON,
			NewCluster: newJSON,
		},
	)
	if err != nil {
		t.Fatalf("ValidateClusterChange() error: %v", err)
	}
	if got, want := len(result.ValidationErrors), 2; got != want {
		t.Fatalf("validation errors = %d, want %d: %v", got, want, result.ValidationErrors)
	}
}
