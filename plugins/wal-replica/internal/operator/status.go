// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operator

import (
	"context"
	"encoding/json"
	"errors"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/clusterstatus"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
)

type Status struct {
	Enabled bool `json:"enabled"`
}

func (Implementation) SetStatusInCluster(
	ctx context.Context,
	req *operator.SetStatusInClusterRequest,
) (*operator.SetStatusInClusterResponse, error) {
	logger := log.FromContext(ctx).WithName("SetStatusInCluster")

	cluster, err := decoder.DecodeClusterLenient(req.GetCluster())
	if err != nil {
		return nil, err
	}

	// TODO remove
	logger.Debug("Debug worked?")

	plg := common.NewPlugin(*cluster, metadata.PluginName)

	// Find the status for our plugin
	var pluginEntry *cnpgv1.PluginStatus
	for idx, entry := range plg.Cluster.Status.PluginStatus {
		if metadata.PluginName == entry.Name {
			pluginEntry = &plg.Cluster.Status.PluginStatus[idx]
			break
		}
	}

	if pluginEntry == nil {
		err := errors.New("plugin entry not found in the cluster status")
		logger.Error(err, "while fetching the plugin status", "plugin", metadata.PluginName)
		return nil, errors.New("plugin entry not found")
	}

	if plg.PluginIndex < 0 {
		logger.Info("Plugin not being used, setting disabled status")
		return clusterstatus.NewSetStatusInClusterResponseBuilder().JSONStatusResponse(Status{Enabled: false})
	}

	var status Status
	if pluginEntry.Status != "" {
		if err := json.Unmarshal([]byte(pluginEntry.Status), &status); err != nil {
			logger.Error(err, "while unmarshalling plugin status",
				"entry", pluginEntry)
			return nil, err
		}
	}

	logger.Info("debug status snapshot",
		"resourceVersion", cluster.ResourceVersion,
		"pluginIndex", plg.PluginIndex,
		"rawPluginStatus", pluginEntry.Status,
		"decodedEnabled", status.Enabled,
	)

	if status.Enabled {
		logger.Info("plugin is enabled, no action taken")
		//return clusterstatus.NewSetStatusInClusterResponseBuilder().NoOpResponse(), nil
		return clusterstatus.NewSetStatusInClusterResponseBuilder().JSONStatusResponse(Status{Enabled: true})
	}

	// TODO uncomment this line when the `enabled` field stops alternating constantly
	logger.Info("setting enabled plugin status")

	return clusterstatus.NewSetStatusInClusterResponseBuilder().JSONStatusResponse(Status{Enabled: true})
}
