// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/validation"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	corev1 "k8s.io/api/core/v1"
)

const (
	labelsParameter                     = "labels"
	annotationParameter                 = "annotations"
	gatewayImageParameter               = "gatewayImage"
	gatewayImagePullPolicyParameter     = "gatewayImagePullPolicy"
	documentDbCredentialSecretParameter = "documentDbCredentialSecret"
	otelCollectorImageParameter         = "otelCollectorImage"
	otelConfigMapNameParameter          = "otelConfigMapName"
	prometheusPortParameter             = "prometheusPort"
)

// Configuration represents the plugin configuration parameters
type Configuration struct {
	Labels                     map[string]string
	Annotations                map[string]string
	GatewayImage               string
	GatewayImagePullPolicy     corev1.PullPolicy
	DocumentDbCredentialSecret string
	OtelCollectorImage         string
	OtelConfigMapName          string
	PrometheusPort             int32
}

// FromParameters builds a plugin configuration from the configuration parameters
func FromParameters(
	helper *common.Plugin,
) (*Configuration, []*operator.ValidationError) {
	validationErrors := make([]*operator.ValidationError, 0)

	var labels map[string]string
	if helper.Parameters[labelsParameter] != "" {
		if err := json.Unmarshal([]byte(helper.Parameters[labelsParameter]), &labels); err != nil {
			validationErrors = append(
				validationErrors,
				validation.BuildErrorForParameter(helper, labelsParameter, err.Error()),
			)
		}
	}

	var annotations map[string]string
	if helper.Parameters[annotationParameter] != "" {
		if err := json.Unmarshal([]byte(helper.Parameters[annotationParameter]), &annotations); err != nil {
			validationErrors = append(
				validationErrors,
				validation.BuildErrorForParameter(helper, annotationParameter, err.Error()),
			)
		}
	}

	// Parse simple string parameters
	gatewayImage := helper.Parameters[gatewayImageParameter]
	credentialSecret := helper.Parameters[documentDbCredentialSecretParameter]
	pullPolicy := parsePullPolicy(helper.Parameters[gatewayImagePullPolicyParameter])

	var prometheusPort int32
	if portStr := helper.Parameters[prometheusPortParameter]; portStr != "" {
		p, err := strconv.ParseInt(portStr, 10, 32)
		if err != nil {
			validationErrors = append(
				validationErrors,
				validation.BuildErrorForParameter(helper, prometheusPortParameter, "invalid port number: "+err.Error()),
			)
		} else {
			prometheusPort = int32(p)
		}
	}

	configuration := &Configuration{
		Labels:                     labels,
		Annotations:                annotations,
		GatewayImage:               gatewayImage,
		GatewayImagePullPolicy:     pullPolicy,
		DocumentDbCredentialSecret: credentialSecret,
		OtelCollectorImage:         helper.Parameters[otelCollectorImageParameter],
		OtelConfigMapName:          helper.Parameters[otelConfigMapNameParameter],
		PrometheusPort:             prometheusPort,
	}

	configuration.applyDefaults()

	return configuration, validationErrors
}

// ValidateChanges validates the changes between the old configuration to the
// new configuration
func ValidateChanges(
	oldConfiguration *Configuration,
	newConfiguration *Configuration,
	helper *common.Plugin,
) []*operator.ValidationError {
	validationErrors := make([]*operator.ValidationError, 0)

	if !reflect.DeepEqual(oldConfiguration.Labels, newConfiguration.Labels) {
		validationErrors = append(
			validationErrors,
			validation.BuildErrorForParameter(helper, labelsParameter, "Labels cannot be changed"))
	}

	return validationErrors
}

// applyDefaults fills the configuration with the defaults
func (config *Configuration) applyDefaults() {
	if len(config.Labels) == 0 {
		config.Labels = map[string]string{
			"plugin-metadata": "default",
		}
	}
	if len(config.Annotations) == 0 {
		config.Annotations = map[string]string{
			"plugin-metadata": "default",
		}
	}
	// Set defaults
	if config.GatewayImage == "" {
		// NOTE: Keep in sync with operator/src/internal/utils/constants.go:DEFAULT_GATEWAY_IMAGE
		config.GatewayImage = "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway:0.109.0"
	}
	if config.GatewayImagePullPolicy == "" {
		config.GatewayImagePullPolicy = corev1.PullIfNotPresent
	}
	if config.DocumentDbCredentialSecret == "" {
		config.DocumentDbCredentialSecret = "documentdb-credentials"
	}
}

// parsePullPolicy converts a string to a corev1.PullPolicy.
// Returns empty string for unrecognized values; callers rely on applyDefaults() for the fallback.
func parsePullPolicy(value string) corev1.PullPolicy {
	switch corev1.PullPolicy(value) {
	case corev1.PullAlways, corev1.PullNever, corev1.PullIfNotPresent:
		return corev1.PullPolicy(value)
	default:
		return ""
	}
}

// ToParameters serialize the configuration to a map of plugin parameters
func (config *Configuration) ToParameters() (map[string]string, error) {
	result := make(map[string]string)
	serializedLabels, err := json.Marshal(config.Labels)
	if err != nil {
		return nil, err
	}
	serializedAnnotations, err := json.Marshal(config.Annotations)
	if err != nil {
		return nil, err
	}
	result[labelsParameter] = string(serializedLabels)
	result[annotationParameter] = string(serializedAnnotations)
	result[gatewayImageParameter] = config.GatewayImage
	result[gatewayImagePullPolicyParameter] = string(config.GatewayImagePullPolicy)
	result[documentDbCredentialSecretParameter] = config.DocumentDbCredentialSecret

	return result, nil
}
