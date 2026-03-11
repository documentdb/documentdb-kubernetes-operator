// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
"fmt"
"strconv"
"strings"

cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/validation"
"github.com/cloudnative-pg/cnpg-i/pkg/operator"
"k8s.io/apimachinery/pkg/api/resource"
)

// Plugin parameter keys
const (
ImageParam           = "image"           // string: container image for the WAL receiver
ReplicationHostParam = "replicationHost" // string: primary host for WAL streaming
SynchronousParam     = "synchronous"     // enum: active, inactive
WalDirectoryParam    = "walDirectory"    // string: directory where WAL files are stored
WalPVCSizeParam      = "walPVCSize"      // string: PVC size (e.g., "10Gi")
VerboseParam         = "verbose"         // bool string: enable verbose pg_receivewal output
CompressionParam     = "compression"     // int string: compression level (0=disabled)
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
defaultWalPVCSize      = "10Gi"
defaultVerbose         = true
defaultCompression     = 0
)

// Configuration represents the plugin configuration parameters controlling the wal receiver pod
type Configuration struct {
Image           string
ReplicationHost string
Synchronous     SynchronousMode
WalDirectory    string
WalPVCSize      string
Verbose         bool
Compression     int
}

// FromParameters builds a plugin configuration from the configuration parameters.
// Returns the configuration and any validation errors encountered during parsing.
func FromParameters(helper *common.Plugin) (*Configuration, []*operator.ValidationError) {
validationErrors := ValidateParams(helper)

cfg := &Configuration{}
cfg.Image = helper.Parameters[ImageParam]
cfg.ReplicationHost = helper.Parameters[ReplicationHostParam]
cfg.Synchronous = SynchronousMode(strings.ToLower(helper.Parameters[SynchronousParam]))
cfg.WalDirectory = helper.Parameters[WalDirectoryParam]
cfg.WalPVCSize = helper.Parameters[WalPVCSizeParam]

if raw, ok := helper.Parameters[VerboseParam]; ok && raw != "" {
cfg.Verbose = strings.EqualFold(raw, "true")
} else {
cfg.Verbose = defaultVerbose
}

if raw, ok := helper.Parameters[CompressionParam]; ok && raw != "" {
if v, err := strconv.Atoi(raw); err == nil {
cfg.Compression = v
}
}

return cfg, validationErrors
}

// ValidateChanges validates the changes between the old configuration to the new configuration
func ValidateChanges(_ *Configuration, _ *Configuration, _ *common.Plugin) []*operator.ValidationError {
return nil
}

// ToParameters serializes the configuration back to plugin parameters
func (c *Configuration) ToParameters() (map[string]string, error) {
params := map[string]string{
ImageParam:           c.Image,
ReplicationHostParam: c.ReplicationHost,
SynchronousParam:     string(c.Synchronous),
WalDirectoryParam:    c.WalDirectory,
WalPVCSizeParam:      c.WalPVCSize,
VerboseParam:         strconv.FormatBool(c.Verbose),
CompressionParam:     strconv.Itoa(c.Compression),
}
return params, nil
}

// ValidateParams ensures that the provided parameters are valid
func ValidateParams(helper *common.Plugin) []*operator.ValidationError {
validationErrors := make([]*operator.ValidationError, 0)

if raw, present := helper.Parameters[SynchronousParam]; present && raw != "" {
switch SynchronousMode(strings.ToLower(raw)) {
case SynchronousActive, SynchronousInactive:
// valid
default:
validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, SynchronousParam,
fmt.Sprintf("invalid value '%s'; must be 'active' or 'inactive'", raw)))
}
}

if raw, present := helper.Parameters[WalPVCSizeParam]; present && raw != "" {
if _, err := resource.ParseQuantity(raw); err != nil {
validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, WalPVCSizeParam, err.Error()))
}
}

if raw, present := helper.Parameters[VerboseParam]; present && raw != "" {
if !strings.EqualFold(raw, "true") && !strings.EqualFold(raw, "false") {
validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, VerboseParam,
fmt.Sprintf("invalid value '%s'; must be 'true' or 'false'", raw)))
}
}

if raw, present := helper.Parameters[CompressionParam]; present && raw != "" {
v, err := strconv.Atoi(raw)
if err != nil {
validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, CompressionParam,
fmt.Sprintf("invalid value '%s'; must be an integer", raw)))
} else if v < 0 || v > 9 {
validationErrors = append(validationErrors, validation.BuildErrorForParameter(helper, CompressionParam,
fmt.Sprintf("invalid value '%d'; must be between 0 and 9", v)))
}
}

return validationErrors
}

// ApplyDefaults fills the configuration with default values
func (c *Configuration) ApplyDefaults(cluster *cnpgv1.Cluster) {
if c.Image == "" {
c.Image = cluster.Status.Image
}
if c.ReplicationHost == "" {
c.ReplicationHost = cluster.Status.WriteService
}
if c.WalDirectory == "" {
c.WalDirectory = defaultWalDir
}
if c.Synchronous == SynchronousUnset {
c.Synchronous = defaultSynchronousMode
}
if c.WalPVCSize == "" {
c.WalPVCSize = defaultWalPVCSize
}
}
