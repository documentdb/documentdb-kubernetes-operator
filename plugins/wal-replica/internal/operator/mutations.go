// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"
	"fmt"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/documentdb/cnpg-i-wal-replica/internal/config"
	"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
)

// MutateCluster is called to mutate a cluster with the defaulting webhook.
// NOTE: MutateCluster is not fully implemented on the CNPG operator side as of CNPG 1.28.
// See: https://github.com/documentdb/documentdb-kubernetes-operator/pull/74#issuecomment-3518389125
// Defaults are applied via ApplyDefaults in the reconciler as a workaround.
func (Implementation) MutateCluster(
	ctx context.Context,
	request *operator.OperatorMutateClusterRequest,
) (*operator.OperatorMutateClusterResult, error) {
	logger := log.FromContext(ctx).WithName("MutateCluster")
	logger.Warning("MutateCluster hook invoked")
	cluster, err := decoder.DecodeClusterLenient(request.GetDefinition())
	if err != nil {
		return nil, err
	}

	helper := common.NewPlugin(
		*cluster,
		metadata.PluginName,
	)

	cfg, valErrs := config.FromParameters(helper)
	if len(valErrs) > 0 {
		return nil, fmt.Errorf("invalid plugin configuration: %s", valErrs[0].Message)
	}

	mutatedCluster := cluster.DeepCopy()
	if helper.PluginIndex >= 0 {
		if mutatedCluster.Spec.Plugins[helper.PluginIndex].Parameters == nil {
			mutatedCluster.Spec.Plugins[helper.PluginIndex].Parameters = make(map[string]string)
		}
		cfg.ApplyDefaults(cluster)

		mutatedCluster.Spec.Plugins[helper.PluginIndex].Parameters, err = cfg.ToParameters()
		if err != nil {
			return nil, err
		}
	} else {
		logger.Info("Plugin not found in the cluster, skipping mutation", "plugin", metadata.PluginName)
	}

	logger.Info("Mutated cluster", "cluster", mutatedCluster)
	patch, err := object.CreatePatch(cluster, mutatedCluster)
	if err != nil {
		return nil, err
	}

	return &operator.OperatorMutateClusterResult{
		JsonPatch: patch,
	}, nil
}
