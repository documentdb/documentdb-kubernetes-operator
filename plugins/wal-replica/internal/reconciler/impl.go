// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reconciler

import (
	"context"
	"encoding/json"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	"github.com/cloudnative-pg/machinery/pkg/log"
)

// Implementation is the implementation of the identity service
type Implementation struct {
	reconciler.UnimplementedReconcilerHooksServer
}

// GetCapabilities gets the capabilities of this operator lifecycle hook
func (Implementation) GetCapabilities(
	context.Context,
	*reconciler.ReconcilerHooksCapabilitiesRequest,
) (*reconciler.ReconcilerHooksCapabilitiesResult, error) {
	return &reconciler.ReconcilerHooksCapabilitiesResult{
		ReconcilerCapabilities: []*reconciler.ReconcilerHooksCapability{
			{
				Kind: reconciler.ReconcilerHooksCapability_KIND_CLUSTER,
			},
		},
	}, nil
}

func (Implementation) Post(ctx context.Context, req *reconciler.ReconcilerHooksRequest) (*reconciler.ReconcilerHooksResult, error) {
	logger := log.FromContext(ctx).WithName("PostReconcilerHook")
	cluster := &cnpgv1.Cluster{}
	if err := json.Unmarshal(req.GetResourceDefinition(), cluster); err != nil {
		logger.Error(err, "while decoding the cluster")
		return nil, err
	}
	logger.Info("Post called for ", "cluster", cluster)

	if err := CreateWalReplica(ctx, cluster); err != nil {
		logger.Error(err, "while creating the wal replica")
		return nil, err
	}

	return &reconciler.ReconcilerHooksResult{Behavior: reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE}, nil
}

func (Implementation) Pre(ctx context.Context, req *reconciler.ReconcilerHooksRequest) (*reconciler.ReconcilerHooksResult, error) {
	// NOOP
	return &reconciler.ReconcilerHooksResult{}, nil
}
