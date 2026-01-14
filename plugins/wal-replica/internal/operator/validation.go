// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/documentdb/cnpg-i-wal-replica/internal/config"
	"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
)

// ValidateClusterCreate validates a cluster that is being created,
// Should validate all plugin parameters
func (Implementation) ValidateClusterCreate(
	ctx context.Context,
	request *operator.OperatorValidateClusterCreateRequest,
) (*operator.OperatorValidateClusterCreateResult, error) {
	logger := log.FromContext(ctx).WithName("ValidateClusterCreate")
	logger.Info("ValidateClusterCreate called")
	cluster, err := decoder.DecodeClusterLenient(request.GetDefinition())
	if err != nil {
		return nil, err
	}

	result := &operator.OperatorValidateClusterCreateResult{}

	helper := common.NewPlugin(
		*cluster,
		metadata.PluginName,
	)

	result.ValidationErrors = config.ValidateParams(helper)

	return result, nil
}

// ValidateClusterChange validates a cluster that is being changed
func (Implementation) ValidateClusterChange(
	ctx context.Context,
	request *operator.OperatorValidateClusterChangeRequest,
) (*operator.OperatorValidateClusterChangeResult, error) {
	logger := log.FromContext(ctx).WithName("ValidateClusterChange")
	logger.Info("ValidateClusterChange called")
	result := &operator.OperatorValidateClusterChangeResult{}

	oldCluster, err := decoder.DecodeClusterLenient(request.GetOldCluster())
	if err != nil {
		return nil, err
	}

	newCluster, err := decoder.DecodeClusterLenient(request.GetNewCluster())
	if err != nil {
		return nil, err
	}

	oldClusterHelper := common.NewPlugin(
		*oldCluster,
		metadata.PluginName,
	)

	newClusterHelper := common.NewPlugin(
		*newCluster,
		metadata.PluginName,
	)

	newConfiguration := config.FromParameters(newClusterHelper)
	oldConfiguration := config.FromParameters(oldClusterHelper)
	result.ValidationErrors = config.ValidateChanges(oldConfiguration, newConfiguration, newClusterHelper)

	return result, nil
}
