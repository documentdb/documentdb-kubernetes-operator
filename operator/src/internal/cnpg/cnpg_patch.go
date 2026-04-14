// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

// JSONPatch represents a single JSON Patch (RFC 6902) operation.
type JSONPatch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

const (
	// JSON Patch operations
	PatchOpReplace = "replace"
	PatchOpAdd     = "add"
	PatchOpRemove  = "remove"

	// JSON Patch paths — replication
	PatchPathReplicaCluster    = "/spec/replica"
	PatchPathPostgresConfig    = "/spec/postgresql"
	PatchPathPostgresConfigSyn = "/spec/postgresql/synchronous"
	PatchPathInstances         = "/spec/instances"
	PatchPathPlugins           = "/spec/plugins"
	PatchPathReplicationSlots  = "/spec/replicationSlots"
	PatchPathExternalClusters  = "/spec/externalClusters"
	PatchPathManagedServices   = "/spec/managed/services/additional"
	PatchPathSynchronous       = "/spec/postgresql/synchronous"
	PatchPathBootstrap         = "/spec/bootstrap"

	// JSON Patch path format strings for image upgrades (require fmt.Sprintf with index)
	PatchPathExtensionImageFmt     = "/spec/postgresql/extensions/%d/image/reference"
	PatchPathPluginGatewayImageFmt = "/spec/plugins/%d/parameters/gatewayImage"

	// JSON Patch path for restart annotation.
	// The '/' in the annotation key is escaped as '~1' per RFC 6901 (JSON Pointer).
	PatchPathRestartAnnotation = "/metadata/annotations/kubectl.kubernetes.io~1restartedAt"
)
