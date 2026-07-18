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
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	labelsParameter                     = "labels"
	annotationParameter                 = "annotations"
	gatewayImageParameter               = "gatewayImage"
	gatewayImagePullPolicyParameter     = "gatewayImagePullPolicy"
	gatewayMemoryRequestParameter       = "gatewayMemoryRequest"
	gatewayMemoryLimitParameter         = "gatewayMemoryLimit"
	gatewayCPURequestParameter          = "gatewayCpuRequest"
	gatewayCPULimitParameter            = "gatewayCpuLimit"
	documentDbCredentialSecretParameter = "documentDbCredentialSecret"
	otelCollectorImageParameter         = "otelCollectorImage"
	otelConfigMapNameParameter          = "otelConfigMapName"
	otelConfigHashParameter             = "otelConfigHash"
	otelMonitorSecretParameter          = "otelMonitorSecret"
	otelMemoryRequestParameter          = "otelMemoryRequest"
	otelMemoryLimitParameter            = "otelMemoryLimit"
	otelCPURequestParameter             = "otelCpuRequest"
	otelCPULimitParameter               = "otelCpuLimit"
	prometheusPortParameter             = "prometheusPort"
)

// Configuration represents the plugin configuration parameters
type Configuration struct {
	Labels                     map[string]string
	Annotations                map[string]string
	GatewayImage               string
	GatewayImagePullPolicy     corev1.PullPolicy
	GatewayMemoryRequest       string
	GatewayMemoryLimit         string
	GatewayCPURequest          string
	GatewayCPULimit            string
	DocumentDbCredentialSecret string
	OtelCollectorImage         string
	OtelConfigMapName          string
	OtelMonitorSecret          string
	OTelMemoryRequest          string
	OTelMemoryLimit            string
	OTelCPURequest             string
	OTelCPULimit               string
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
	otelCollectorImage := helper.Parameters[otelCollectorImageParameter]
	otelConfigMapName := helper.Parameters[otelConfigMapNameParameter]
	otelMonitorSecret := helper.Parameters[otelMonitorSecretParameter]
	validateQuantityParameters(helper, &validationErrors,
		gatewayMemoryRequestParameter,
		gatewayMemoryLimitParameter,
		gatewayCPURequestParameter,
		gatewayCPULimitParameter,
		otelMemoryRequestParameter,
		otelMemoryLimitParameter,
		otelCPURequestParameter,
		otelCPULimitParameter,
	)

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

	requiredOtelParameters := []string{
		otelCollectorImageParameter,
		otelConfigMapNameParameter,
		otelMonitorSecretParameter,
	}
	otelConfigured := helper.Parameters[prometheusPortParameter] != "" ||
		helper.Parameters[otelConfigHashParameter] != ""
	for _, parameter := range requiredOtelParameters {
		otelConfigured = otelConfigured || helper.Parameters[parameter] != ""
	}
	if otelConfigured {
		for _, parameter := range requiredOtelParameters {
			if helper.Parameters[parameter] == "" {
				validationErrors = append(
					validationErrors,
					validation.BuildErrorForParameter(
						helper,
						parameter,
						"required when any OTel sidecar parameter is configured",
					),
				)
			}
		}
	}

	configuration := &Configuration{
		Labels:                     labels,
		Annotations:                annotations,
		GatewayImage:               gatewayImage,
		GatewayImagePullPolicy:     pullPolicy,
		GatewayMemoryRequest:       helper.Parameters[gatewayMemoryRequestParameter],
		GatewayMemoryLimit:         helper.Parameters[gatewayMemoryLimitParameter],
		GatewayCPURequest:          helper.Parameters[gatewayCPURequestParameter],
		GatewayCPULimit:            helper.Parameters[gatewayCPULimitParameter],
		DocumentDbCredentialSecret: credentialSecret,
		OtelCollectorImage:         otelCollectorImage,
		OtelConfigMapName:          otelConfigMapName,
		OtelMonitorSecret:          otelMonitorSecret,
		OTelMemoryRequest:          helper.Parameters[otelMemoryRequestParameter],
		OTelMemoryLimit:            helper.Parameters[otelMemoryLimitParameter],
		OTelCPURequest:             helper.Parameters[otelCPURequestParameter],
		OTelCPULimit:               helper.Parameters[otelCPULimitParameter],
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

func validateQuantityParameters(
	helper *common.Plugin,
	validationErrors *[]*operator.ValidationError,
	parameters ...string,
) {
	for _, parameter := range parameters {
		value := helper.Parameters[parameter]
		if value == "" {
			continue
		}
		if _, err := resource.ParseQuantity(value); err != nil {
			*validationErrors = append(
				*validationErrors,
				validation.BuildErrorForParameter(helper, parameter, "invalid resource quantity: "+err.Error()),
			)
		}
	}
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
		config.GatewayImage = "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway:0.110.0"
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
	// Omit empty optional resource params to avoid noisy defaulting diffs.
	setIfNotEmpty := func(key, val string) {
		if val != "" {
			result[key] = val
		}
	}
	setIfNotEmpty(gatewayMemoryRequestParameter, config.GatewayMemoryRequest)
	setIfNotEmpty(gatewayMemoryLimitParameter, config.GatewayMemoryLimit)
	setIfNotEmpty(gatewayCPURequestParameter, config.GatewayCPURequest)
	setIfNotEmpty(gatewayCPULimitParameter, config.GatewayCPULimit)
	result[documentDbCredentialSecretParameter] = config.DocumentDbCredentialSecret
	setIfNotEmpty(otelMemoryRequestParameter, config.OTelMemoryRequest)
	setIfNotEmpty(otelMemoryLimitParameter, config.OTelMemoryLimit)
	setIfNotEmpty(otelCPURequestParameter, config.OTelCPURequest)
	setIfNotEmpty(otelCPULimitParameter, config.OTelCPULimit)

	return result, nil
}
