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
	otelPasswordSecretParameter         = "otelPasswordSecret"
	otelMemoryRequestParameter          = "otelMemoryRequest"
	otelMemoryLimitParameter            = "otelMemoryLimit"
	otelCPURequestParameter             = "otelCpuRequest"
	otelCPULimitParameter               = "otelCpuLimit"
	prometheusPortParameter             = "prometheusPort"

	// Gateway contract parameters. They are optional and default to the
	// DocumentDB gateway so other products can reuse this injector with a
	// different gateway binary.
	gatewayContainerNameParameter = "gatewayContainerName"
	gatewayPortParameter          = "gatewayPort"
	gatewayArgsParameter          = "gatewayArgs"
	gatewayUsernameEnvParameter   = "gatewayUsernameEnv"
	gatewayPasswordEnvParameter   = "gatewayPasswordEnv"
)

// Defaults for the gateway contract (the DocumentDB gateway).
const (
	DefaultGatewayContainerName = "documentdb-gateway"
	DefaultGatewayPort          = int32(10260)
	DefaultGatewayUsernameEnv   = "USERNAME"
	DefaultGatewayPasswordEnv   = "PASSWORD"
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
	OtelConfigHash             string
	// OtelPasswordSecret optionally supplies otel_monitor's password key.
	OtelPasswordSecret string
	OTelMemoryRequest  string
	OTelMemoryLimit    string
	OTelCPURequest     string
	OTelCPULimit       string
	PrometheusPort     int32
	// GatewayContainerName and GatewayPort name and expose the gateway sidecar.
	GatewayContainerName string
	GatewayPort          int32
	// GatewayArgs, when non-nil, replaces the DocumentDB gateway arguments
	// (--create-user/--start-pg/--pg-port and the TLS file flags) verbatim.
	GatewayArgs []string
	// GatewayUsernameEnv and GatewayPasswordEnv are the env var names the
	// credential secret's username/password keys are exposed as.
	GatewayUsernameEnv string
	GatewayPasswordEnv string
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

	var gatewayArgs []string
	if raw := helper.Parameters[gatewayArgsParameter]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &gatewayArgs); err != nil {
			validationErrors = append(
				validationErrors,
				validation.BuildErrorForParameter(helper, gatewayArgsParameter, "must be a JSON array of strings: "+err.Error()),
			)
		}
	}

	var gatewayPort int32
	if portStr := helper.Parameters[gatewayPortParameter]; portStr != "" {
		p, err := strconv.ParseInt(portStr, 10, 32)
		if err != nil || p <= 0 || p > 65535 {
			validationErrors = append(
				validationErrors,
				validation.BuildErrorForParameter(helper, gatewayPortParameter, "invalid port number: "+portStr),
			)
		} else {
			gatewayPort = int32(p)
		}
	}

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
	}
	otelParameters := append([]string{
		prometheusPortParameter,
		otelConfigHashParameter,
		otelPasswordSecretParameter,
		otelMemoryRequestParameter,
		otelMemoryLimitParameter,
		otelCPURequestParameter,
		otelCPULimitParameter,
	}, requiredOtelParameters...)
	otelConfigured := false
	for _, parameter := range otelParameters {
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
		OtelConfigHash:             helper.Parameters[otelConfigHashParameter],
		OtelPasswordSecret:         helper.Parameters[otelPasswordSecretParameter],
		OTelMemoryRequest:          helper.Parameters[otelMemoryRequestParameter],
		OTelMemoryLimit:            helper.Parameters[otelMemoryLimitParameter],
		OTelCPURequest:             helper.Parameters[otelCPURequestParameter],
		OTelCPULimit:               helper.Parameters[otelCPULimitParameter],
		PrometheusPort:             prometheusPort,
		GatewayContainerName:       helper.Parameters[gatewayContainerNameParameter],
		GatewayPort:                gatewayPort,
		GatewayArgs:                gatewayArgs,
		GatewayUsernameEnv:         helper.Parameters[gatewayUsernameEnvParameter],
		GatewayPasswordEnv:         helper.Parameters[gatewayPasswordEnvParameter],
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
		config.GatewayImage = "ghcr.io/documentdb/documentdb-kubernetes-operator/gateway:1.0.0"
	}
	if config.GatewayImagePullPolicy == "" {
		config.GatewayImagePullPolicy = corev1.PullIfNotPresent
	}
	if config.DocumentDbCredentialSecret == "" {
		config.DocumentDbCredentialSecret = "documentdb-credentials"
	}
	if config.GatewayContainerName == "" {
		config.GatewayContainerName = DefaultGatewayContainerName
	}
	if config.GatewayPort == 0 {
		config.GatewayPort = DefaultGatewayPort
	}
	if config.GatewayUsernameEnv == "" {
		config.GatewayUsernameEnv = DefaultGatewayUsernameEnv
	}
	if config.GatewayPasswordEnv == "" {
		config.GatewayPasswordEnv = DefaultGatewayPasswordEnv
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
	setIfNotEmpty(otelCollectorImageParameter, config.OtelCollectorImage)
	setIfNotEmpty(otelConfigMapNameParameter, config.OtelConfigMapName)
	setIfNotEmpty(otelConfigHashParameter, config.OtelConfigHash)
	setIfNotEmpty(otelPasswordSecretParameter, config.OtelPasswordSecret)
	setIfNotEmpty(otelMemoryRequestParameter, config.OTelMemoryRequest)
	setIfNotEmpty(otelMemoryLimitParameter, config.OTelMemoryLimit)
	setIfNotEmpty(otelCPURequestParameter, config.OTelCPURequest)
	setIfNotEmpty(otelCPULimitParameter, config.OTelCPULimit)
	if config.PrometheusPort > 0 {
		result[prometheusPortParameter] = strconv.FormatInt(int64(config.PrometheusPort), 10)
	}
	// Gateway contract parameters are only serialized when they differ from
	// the DocumentDB defaults, keeping existing parameter maps unchanged.
	if config.GatewayContainerName != DefaultGatewayContainerName {
		setIfNotEmpty(gatewayContainerNameParameter, config.GatewayContainerName)
	}
	if config.GatewayPort > 0 && config.GatewayPort != DefaultGatewayPort {
		result[gatewayPortParameter] = strconv.FormatInt(int64(config.GatewayPort), 10)
	}
	if config.GatewayArgs != nil {
		serializedArgs, err := json.Marshal(config.GatewayArgs)
		if err != nil {
			return nil, err
		}
		result[gatewayArgsParameter] = string(serializedArgs)
	}
	if config.GatewayUsernameEnv != DefaultGatewayUsernameEnv {
		setIfNotEmpty(gatewayUsernameEnvParameter, config.GatewayUsernameEnv)
	}
	if config.GatewayPasswordEnv != DefaultGatewayPasswordEnv {
		setIfNotEmpty(gatewayPasswordEnvParameter, config.GatewayPasswordEnv)
	}

	return result, nil
}
