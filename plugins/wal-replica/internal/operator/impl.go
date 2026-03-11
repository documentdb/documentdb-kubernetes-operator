// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
)

// Implementation is the implementation of the operator service
type Implementation struct {
	operator.OperatorServer
}

// GetCapabilities gets the capabilities of this operator hook
func (Implementation) GetCapabilities(
	context.Context,
	*operator.OperatorCapabilitiesRequest,
) (*operator.OperatorCapabilitiesResult, error) {
	return &operator.OperatorCapabilitiesResult{
		Capabilities: []*operator.OperatorCapability{
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_VALIDATE_CLUSTER_CREATE,
					},
				},
			},
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_VALIDATE_CLUSTER_CHANGE,
					},
				},
			},
			// TYPE_SET_STATUS_IN_CLUSTER is disabled due to an oscillation bug
			// where the enabled field alternates on every reconciliation cycle.
			// Re-enable once the root cause is identified and fixed upstream.
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						// NOTE: MutateCluster is not fully implemented on the CNPG operator
						// side as of v1.28. Defaults are applied via ApplyDefaults in the
						// reconciler as a workaround.
						Type: operator.OperatorCapability_RPC_TYPE_MUTATE_CLUSTER,
					},
				},
			},
		},
	}, nil
}
