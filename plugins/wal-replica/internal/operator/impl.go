// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
)

// Implementation is the implementation of the identity service
type Implementation struct {
	operator.OperatorServer
}

// GetCapabilities gets the capabilities of this operator lifecycle hook
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
			/* TODO re-add if we need status or can figure out the oscillation bug
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_SET_STATUS_IN_CLUSTER,
					},
				},
			},
			*/
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_MUTATE_CLUSTER,
					},
				},
			},
		},
	}, nil
}
