// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// SyncCnpgCluster compares the current and desired CNPG Cluster specs and patches
// all fields in a single atomic JSON Patch operation. This is the single entry point
// for ALL CNPG spec mutations (images + plugin params + replication).
//
// Only mutable plugin parameters are synced: gatewayImage and gatewayTLSSecret.
// Other parameters (e.g., documentDbCredentialSecret) are set at cluster creation
// and do not change during the lifecycle of a DocumentDB resource.
//
// extraOps are additional patch operations (e.g., replication changes) to include
// in the same atomic patch.
func SyncCnpgCluster(
	ctx context.Context,
	c client.Client,
	current, desired *cnpgv1.Cluster,
	extraOps []JSONPatch,
) error {
	logger := log.FromContext(ctx)

	var patchOps []JSONPatch
	extensionUpdated := false
	gatewayUpdated := false

	// Extension image
	currentExtIndex, currentExtImage := findExtensionImage(current)
	_, desiredExtImage := findExtensionImage(desired)
	if currentExtImage != desiredExtImage {
		if currentExtIndex == -1 {
			return fmt.Errorf("documentdb extension not found in current CNPG cluster spec")
		}
		patchOps = append(patchOps, JSONPatch{
			Op:    PatchOpReplace,
			Path:  fmt.Sprintf(PatchPathExtensionImageFmt, currentExtIndex),
			Value: desiredExtImage,
		})
		extensionUpdated = true
	}

	// Gateway image and plugin parameters share the same plugin lookup
	pluginParamsChanged := false
	if len(desired.Spec.Plugins) > 0 {
		desiredPlugin := desired.Spec.Plugins[0]
		pluginIdx, currentPlugin := findPlugin(current, desiredPlugin.Name)
		if pluginIdx != -1 {
			// Gateway image
			desiredGwImage := getParam(desiredPlugin.Parameters, "gatewayImage")
			currentGwImage := getParam(currentPlugin.Parameters, "gatewayImage")
			if desiredGwImage != "" && currentGwImage != desiredGwImage {
				patchOps = append(patchOps, JSONPatch{
					Op:    PatchOpReplace,
					Path:  fmt.Sprintf(PatchPathPluginGatewayImageFmt, pluginIdx),
					Value: desiredGwImage,
				})
				gatewayUpdated = true
			}

			// Ensure plugin is enabled
			if currentPlugin.Enabled == nil || !*currentPlugin.Enabled {
				patchOps = append(patchOps, JSONPatch{
					Op:    PatchOpReplace,
					Path:  fmt.Sprintf("/spec/plugins/%d/enabled", pluginIdx),
					Value: true,
				})
				pluginParamsChanged = true
			}

			// TLS secret (only synced when BuildDesiredCnpgCluster sets it, i.e. TLS is ready)
			desiredTLS := getParam(desiredPlugin.Parameters, "gatewayTLSSecret")
			currentTLS := getParam(currentPlugin.Parameters, "gatewayTLSSecret")
			if desiredTLS != "" && currentTLS != desiredTLS {
				patchOps = append(patchOps, JSONPatch{
					Op:    PatchOpReplace,
					Path:  fmt.Sprintf("/spec/plugins/%d/parameters/gatewayTLSSecret", pluginIdx),
					Value: desiredTLS,
				})
				pluginParamsChanged = true
			}
		}
	}

	// Extra operations (e.g., replication changes)
	patchOps = append(patchOps, extraOps...)

	// CNPG auto-restarts pods when extension image changes (ImageVolume PodSpec divergence),
	// but NOT for plugin parameter or gateway-only changes. Include a restart annotation
	// in the same atomic patch to avoid partial-apply state where the spec is updated but
	// the restart annotation is never applied if a subsequent reconcile no-ops the spec diff.
	needsRestart := !extensionUpdated && (gatewayUpdated || pluginParamsChanged)
	if needsRestart {
		// Ensure the annotations map exists before adding a key into it.
		// JSON Patch "add" requires the parent path to exist.
		if current.Annotations == nil {
			patchOps = append(patchOps, JSONPatch{
				Op:    PatchOpAdd,
				Path:  "/metadata/annotations",
				Value: map[string]string{
					"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339Nano),
				},
			})
		} else {
			patchOps = append(patchOps, JSONPatch{
				Op:    PatchOpAdd,
				Path:  PatchPathRestartAnnotation,
				Value: time.Now().Format(time.RFC3339Nano),
			})
		}
	}

	if len(patchOps) == 0 {
		return nil
	}

	// Apply all patches atomically
	patchBytes, err := json.Marshal(patchOps)
	if err != nil {
		return fmt.Errorf("failed to marshal sync patch: %w", err)
	}
	if err := c.Patch(ctx, current, client.RawPatch(types.JSONPatchType, patchBytes)); err != nil {
		return fmt.Errorf("failed to patch CNPG cluster: %w", err)
	}

	if needsRestart {
		logger.Info("Added restart annotation for non-extension update", "clusterName", current.Name)
	}

	return nil
}

// findExtensionImage returns the index and image reference for the documentdb extension.
func findExtensionImage(cluster *cnpgv1.Cluster) (int, string) {
	for i, ext := range cluster.Spec.PostgresConfiguration.Extensions {
		if ext.Name == "documentdb" {
			return i, ext.ImageVolumeSource.Reference
		}
	}
	return -1, ""
}

// findPlugin returns the index and plugin config for a named plugin, or -1 if not found.
func findPlugin(cluster *cnpgv1.Cluster, name string) (int, *cnpgv1.PluginConfiguration) {
	for i := range cluster.Spec.Plugins {
		if cluster.Spec.Plugins[i].Name == name {
			return i, &cluster.Spec.Plugins[i]
		}
	}
	return -1, nil
}

// getParam safely retrieves a value from a map, returning "" if the map is nil.
func getParam(params map[string]string, key string) string {
	if params == nil {
		return ""
	}
	return params[key]
}
