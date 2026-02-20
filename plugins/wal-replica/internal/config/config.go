// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/validation"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Plugin parameter keys
const (
	ImageParam           = "image"           // string
	ReplicationHostParam = "replicationHost" // primary host
	SynchronousParam     = "synchronous"     // enum: Active, Inactive, Unset
	WalDirectoryParam    = "walDirectory"    // directory where WAL is stored
	WalPVCSize           = "walPVCSize"      // Size of the PVC for WAL storage
)

// SynchronousMode represents the synchronous replication mode
type SynchronousMode string

const (
	SynchronousUnset    SynchronousMode = ""
	SynchronousActive   SynchronousMode = "active"
	SynchronousInactive SynchronousMode = "inactive"
)

const (
	defaultWalDir          = "/var/lib/postgresql/wal"
	defaultSynchronousMode = SynchronousInactive
)

// Configuration represents the plugin configuration parameters controlling the wal receiver pod
type Configuration struct {
	Image           string
	ReplicationHost string
	Synchronous     SynchronousMode
	WalDirectory    string
	WalPVCSize      string
}

// FromParameters builds a plugin configuration from the configuration parameters
func FromParameters(helper *common.Plugin) *Configuration {
	cfg := &Configuration{}
	cfg.Image = helper.Parameters[ImageParam]
	cfg.ReplicationHost = helper.Parameters[ReplicationHostParam]
	cfg.Synchronous = SynchronousMode(strings.ToLower(helper.Parameters[SynchronousParam]))
	cfg.WalDirectory = helper.Parameters[WalDirectoryParam]
	cfg.WalPVCSize = helper.Parameters[WalPVCSize]
	return cfg
}

// ValidateChanges validates the changes between the old configuration to the new configuration
func ValidateChanges(_ *Configuration, _ *Configuration, _ *common.Plugin) []*operator.ValidationError {
	return nil
}

// ToParameters serialize the configuration back to plugin parameters
func (c *Configuration) ToParameters() (map[string]string, error) {
	params := map[string]string{}
	params[ImageParam] = c.Image
	params[ReplicationHostParam] = c.ReplicationHost
	params[SynchronousParam] = string(c.Synchronous)
	params[WalDirectoryParam] = c.WalDirectory
	params[WalPVCSize] = c.WalPVCSize
	return params, nil
}

// ValidateParams ensures that the provided parameters are valid
func ValidateParams(helper *common.Plugin) []*operator.ValidationError {
	validationErrors := make([]*operator.ValidationError, 0)

	// If present, must be valid
	if raw, present := helper.Parameters[SynchronousParam]; present && raw != "" {
		switch SynchronousMode(strings.ToLower(raw)) {
		case SynchronousActive, SynchronousInactive:
			// valid value
		default:
			validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, SynchronousParam,
				fmt.Sprintf("Invalid value '%s'. Must be 'active' or 'inactive'", raw)))
		}
	}

	// If present, Wal size must be valid
	if raw, present := helper.Parameters[WalPVCSize]; present && raw != "" {
		if _, err := resource.ParseQuantity(raw); err != nil {
			validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, WalPVCSize, err.Error()))
		}
	}

	return validationErrors
}

// applyDefaults fills the configuration with the defaults
// We know that replicationhost and sync are valid already
func (c *Configuration) ApplyDefaults(cluster *cnpgv1.Cluster) {
	if c.Image == "" {
		c.Image = cluster.Status.Image
	}
	if c.ReplicationHost == "" {
		// Only doing reads, but want to make sure we get a primary
		c.ReplicationHost = cluster.Status.WriteService
	}
	if c.WalDirectory == "" {
		c.WalDirectory = defaultWalDir
	}
	if c.Synchronous == SynchronousUnset {
		c.Synchronous = defaultSynchronousMode
	}
	if c.WalPVCSize == "" {
		c.WalPVCSize = "10Gi"
	}
}
