// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/documentdb/cnpg-i-wal-replica/internal/config"
	"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
)

// MutateCluster is called to mutate a cluster with the defaulting webhook.
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

	config := config.FromParameters(helper)
	mutatedCluster := cluster.DeepCopy()
	if helper.PluginIndex >= 0 {
		if mutatedCluster.Spec.Plugins[helper.PluginIndex].Parameters == nil {
			mutatedCluster.Spec.Plugins[helper.PluginIndex].Parameters = make(map[string]string)
		}
		config.ApplyDefaults(cluster)

		mutatedCluster.Spec.Plugins[helper.PluginIndex].Parameters, err = config.ToParameters()
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
