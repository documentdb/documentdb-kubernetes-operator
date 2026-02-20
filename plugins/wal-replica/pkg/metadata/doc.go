// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package metadata contains the metadata of this plugin
package metadata

import "github.com/cloudnative-pg/cnpg-i/pkg/identity"

// PluginName is the name of the plugin
const PluginName = "cnpg-i-wal-replica.documentdb.io"

// Data is the metadata of this plugin
var Data = identity.GetPluginMetadataResponse{
	Name:          PluginName,
	Version:       "0.1.0",
	DisplayName:   "WAL Replica Pod Manager",
	ProjectUrl:    "https://github.com/documentdb/cnpg-i-wal-replica",
	RepositoryUrl: "https://github.com/documentdb/cnpg-i-wal-replica",
	License:       "MIT",
	LicenseUrl:    "https://github.com/documentdb/cnpg-i-wal-replica/LICENSE",
	Maturity:      "alpha",
}
