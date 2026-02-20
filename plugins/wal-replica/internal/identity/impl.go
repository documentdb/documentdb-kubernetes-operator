// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package identity

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/identity"

	"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
)

// Implementation is the implementation of the identity service
type Implementation struct {
	identity.IdentityServer
}

// GetPluginMetadata implements the IdentityServer interface
func (Implementation) GetPluginMetadata(
	context.Context,
	*identity.GetPluginMetadataRequest,
) (*identity.GetPluginMetadataResponse, error) {
	return &metadata.Data, nil
}

// GetPluginCapabilities implements the IdentityServer interface
func (Implementation) GetPluginCapabilities(
	context.Context,
	*identity.GetPluginCapabilitiesRequest,
) (*identity.GetPluginCapabilitiesResponse, error) {
	return &identity.GetPluginCapabilitiesResponse{
		Capabilities: []*identity.PluginCapability{
			{ // TODO find out why this does nothing
				Type: &identity.PluginCapability_Service_{
					Service: &identity.PluginCapability_Service{
						Type: identity.PluginCapability_Service_TYPE_OPERATOR_SERVICE,
					},
				},
			},
			{
				Type: &identity.PluginCapability_Service_{
					Service: &identity.PluginCapability_Service{
						Type: identity.PluginCapability_Service_TYPE_RECONCILER_HOOKS,
					},
				},
			},
		},
	}, nil
}

// Probe implements the IdentityServer interface
func (Implementation) Probe(context.Context, *identity.ProbeRequest) (*identity.ProbeResponse, error) {
	return &identity.ProbeResponse{
		Ready: true,
	}, nil
}
