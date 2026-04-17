// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package lifecycle implements the lifecycle hooks
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/documentdb/cnpg-i-sidecar-injector/internal/config"
	"github.com/documentdb/cnpg-i-sidecar-injector/internal/utils"
	"github.com/documentdb/cnpg-i-sidecar-injector/pkg/metadata"
)

// Implementation is the implementation of the lifecycle handler
type Implementation struct {
	lifecycle.UnimplementedOperatorLifecycleServer
}

// GetCapabilities exposes the lifecycle capabilities
func (impl Implementation) GetCapabilities(
	_ context.Context,
	_ *lifecycle.OperatorLifecycleCapabilitiesRequest,
) (*lifecycle.OperatorLifecycleCapabilitiesResponse, error) {
	return &lifecycle.OperatorLifecycleCapabilitiesResponse{
		LifecycleCapabilities: []*lifecycle.OperatorLifecycleCapabilities{
			{
				Group: "",
				Kind:  "Pod",
				OperationTypes: []*lifecycle.OperatorOperationType{
					{
						Type: lifecycle.OperatorOperationType_TYPE_CREATE,
					},
					{
						Type: lifecycle.OperatorOperationType_TYPE_PATCH,
					},
				},
			},
		},
	}, nil
}

// LifecycleHook is called by CNPG for Pod CREATE/PATCH/UPDATE operations
func (impl Implementation) LifecycleHook(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	kind, err := utils.GetKind(request.GetObjectDefinition())
	if err != nil {
		return nil, err
	}
	operation := request.GetOperationType().GetType().Enum()
	if operation == nil {
		return nil, errors.New("no operation set")
	}

	//nolint: gocritic
	switch kind {
	case "Pod":
		switch *operation {
		case lifecycle.OperatorOperationType_TYPE_CREATE, lifecycle.OperatorOperationType_TYPE_PATCH,
			lifecycle.OperatorOperationType_TYPE_UPDATE:
			return impl.reconcileMetadata(ctx, request)
		}
		// add any other custom logic to execute based on the operation
	}

	return &lifecycle.OperatorLifecycleResponse{}, nil
}

// reconcileMetadata mutates Pod metadata, injects sidecars, and applies labels/annotations.
func (impl Implementation) reconcileMetadata(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetClusterDefinition())
	if err != nil {
		return nil, err
	}

	// Initialize standard logger for debugging
	log.SetPrefix("[DocumentDB Sidecar Injector] ")

	// Debug: Log the full cluster definition to see what plugins are configured
	log.Printf("Cluster plugins configuration: %+v", cluster.Spec.Plugins)

	helper := common.NewPlugin(
		*cluster,
		metadata.PluginName,
	)

	// Debug logging for plugin parameters and metadata
	log.Printf("Plugin name being used: %s", metadata.PluginName)
	log.Printf("Plugin parameters received: %v, cluster: %s/%s",
		helper.Parameters, cluster.Namespace, cluster.Name)

	// Debug: Check if our plugin is found in the cluster's plugin list
	for i, plugin := range cluster.Spec.Plugins {
		log.Printf("Plugin[%d]: Name=%s, Enabled=%t, Parameters=%v",
			i, plugin.Name, *plugin.Enabled, plugin.Parameters)
	}

	configuration, valErrs := config.FromParameters(helper)
	if len(valErrs) > 0 {
		return nil, valErrs[0]
	}

	// Log the gateway image being used
	gatewayImageParam := helper.Parameters["gatewayImage"]
	if gatewayImageParam == "" {
		log.Printf("Using default gateway image: %s (no gatewayImage parameter provided)", configuration.GatewayImage)
	} else {
		log.Printf("Using configured gateway image: %s (parameter value: %s)", configuration.GatewayImage, gatewayImageParam)
	}

	pod, err := decoder.DecodePodJSON(request.GetObjectDefinition())
	if err != nil {
		return nil, err
	}

	mutatedPod := pod.DeepCopy()

	// Initialize environment variables for the gateway container
	envVars := []corev1.EnvVar{}

	// Add USERNAME and PASSWORD environment variables from secret defined in configuration
	credentialSecretName := configuration.DocumentDbCredentialSecret
	log.Printf("Adding USERNAME and PASSWORD environment variables from secret '%s'", credentialSecretName)
	envVars = append(envVars,
		corev1.EnvVar{
			Name: "USERNAME",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: credentialSecretName,
					},
					Key: "username",
				},
			},
		},
		corev1.EnvVar{
			Name: "PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: credentialSecretName,
					},
					Key: "password",
				},
			},
		},
	)

	// Initialize the sidecar container with configurable gateway image
	sidecar := &corev1.Container{
		Name:            "documentdb-gateway",
		Image:           configuration.GatewayImage,
		ImagePullPolicy: configuration.GatewayImagePullPolicy,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: 10260,
			},
		},
		Env: envVars,
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  pointer.Int64(1000),
			RunAsGroup: pointer.Int64(1000),
		},
	}

	// If TLS secret parameter provided, mount it at /tls
	// Track whether TLS secret is configured to augment container args later
	hasTLSSecret := false
	if tlsSecret, ok := helper.Parameters["gatewayTLSSecret"]; ok && tlsSecret != "" {
		// Append volume only if not already present
		found := false
		for _, v := range mutatedPod.Spec.Volumes {
			if v.Name == "gateway-tls" {
				found = true
				break
			}
		}
		if !found {
			mutatedPod.Spec.Volumes = append(mutatedPod.Spec.Volumes, corev1.Volume{
				Name: "gateway-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: tlsSecret},
				},
			})
		}
		// Add mount to sidecar container
		sidecar.VolumeMounts = append(sidecar.VolumeMounts, corev1.VolumeMount{Name: "gateway-tls", MountPath: "/tls", ReadOnly: true})
		// Provide env vars for gateway to load the mounted certificate and key
		// Most gateway images respect CERT_PATH and KEY_FILE; keep TLS_CERT_DIR for backward-compat
		sidecar.Env = append(sidecar.Env,
			corev1.EnvVar{Name: "TLS_CERT_DIR", Value: "/tls"},
			corev1.EnvVar{Name: "CERT_PATH", Value: "/tls/tls.crt"},
			corev1.EnvVar{Name: "KEY_FILE", Value: "/tls/tls.key"},
		)
		// Mark that TLS secret is present so we can also pass explicit CLI args
		hasTLSSecret = true
		log.Printf("Injected TLS secret volume for gateway: %s", tlsSecret)
	}

	// Build base args and append TLS file args if a TLS secret is configured
	args := []string{"--start-pg", "false", "--pg-port", "5432"}
	// Check if the pod has the label replication_cluster_type=replica

	// Check if the pod has the label replication_cluster_type=replica or is not a local primary
	if mutatedPod.Labels["replication_cluster_type"] == "replica" || cluster.Status.TargetPrimary != mutatedPod.Name {
		args = append([]string{"--create-user", "false"}, args...)
	} else {
		args = append([]string{"--create-user", "true"}, args...)
	}
	if hasTLSSecret {
		// Pass cert and key via CLI args to align with emulator_entrypoint.sh interface
		args = append(args, "--cert-path", "/tls/tls.crt", "--key-file", "/tls/tls.key")
	}
	sidecar.Args = args

	// Inject the sidecar container
	err = object.InjectPluginSidecar(mutatedPod, sidecar, false)
	if err != nil {
		return nil, err
	}

	// Inject OTel Collector sidecar when monitoring is enabled.
	// The sidecar is only injected when the operator passes otelCollectorImage
	// and otelConfigMapName parameters (i.e., monitoring.enabled is true).
	if configuration.OtelCollectorImage != "" && configuration.OtelConfigMapName != "" {
		log.Printf("Injecting OTel Collector sidecar with image: %s", configuration.OtelCollectorImage)

		// Add ConfigMap volume for operator-generated config files (static.yaml + dynamic.yaml)
		// Check for existing volume to be idempotent across CREATE and PATCH operations
		otelVolFound := false
		for _, v := range mutatedPod.Spec.Volumes {
			if v.Name == "otel-config" {
				otelVolFound = true
				break
			}
		}
		if !otelVolFound {
			mutatedPod.Spec.Volumes = append(mutatedPod.Spec.Volumes, corev1.Volume{
				Name: "otel-config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: configuration.OtelConfigMapName,
						},
					},
				},
			})
		}

		otelSidecar := &corev1.Container{
			Name:  "otel-collector",
			Image: configuration.OtelCollectorImage,
			Args: []string{
				"--config=file:/config/static.yaml",
				"--config=file:/config/dynamic.yaml",
			},
			// PGUSER and PGPASSWORD are sourced from the CNPG-managed application secret
			// ("<cluster>-app"). CNPG auto-creates this secret with "username" and "password"
			// keys for the application database user. The OTel Collector's sqlquery receiver
			// uses these credentials to connect to PostgreSQL and collect health metrics.
			Env: []corev1.EnvVar{
				{
					Name: "POD_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
						},
					},
				},
				{
					Name: "PGUSER",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cluster.Name + "-app",
							},
							Key: "username",
						},
					},
				},
				{
					Name: "PGPASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cluster.Name + "-app",
							},
							Key: "password",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "otel-config",
					MountPath: "/config",
					ReadOnly:  true,
				},
			},
		}

		// Expose Prometheus metrics port when configured
		if configuration.PrometheusPort > 0 {
			otelSidecar.Ports = append(otelSidecar.Ports, corev1.ContainerPort{
				Name:          "prom-metrics",
				ContainerPort: configuration.PrometheusPort,
				Protocol:      corev1.ProtocolTCP,
			})
			// Add Prometheus scrape annotations for auto-discovery
			if mutatedPod.Annotations == nil {
				mutatedPod.Annotations = map[string]string{}
			}
			mutatedPod.Annotations["prometheus.io/scrape"] = "true"
			mutatedPod.Annotations["prometheus.io/port"] = fmt.Sprintf("%d", configuration.PrometheusPort)
			mutatedPod.Annotations["prometheus.io/path"] = "/metrics"

			// Add readiness probe for Prometheus endpoint
			otelSidecar.ReadinessProbe = &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/metrics",
						Port: intstr.FromInt32(configuration.PrometheusPort),
					},
				},
				InitialDelaySeconds: 5,
				PeriodSeconds:       10,
			}
		}

		err = object.InjectPluginSidecar(mutatedPod, otelSidecar, false)
		if err != nil {
			return nil, err
		}

		// Set OTEL_EXPORTER_OTLP_ENDPOINT on the gateway container so it can
		// forward its own traces to the co-located OTel Collector sidecar.
		// Only set when the sidecar is present to avoid connection errors.
		for i := range mutatedPod.Spec.Containers {
			if mutatedPod.Spec.Containers[i].Name == "documentdb-gateway" {
				mutatedPod.Spec.Containers[i].Env = append(mutatedPod.Spec.Containers[i].Env, corev1.EnvVar{
					Name:  "OTEL_EXPORTER_OTLP_ENDPOINT",
					Value: "http://localhost:4317",
				})
				break
			}
		}

		log.Printf("OTel Collector sidecar injected successfully")
	}

	for key, value := range configuration.Labels {
		mutatedPod.Labels[key] = value
	}
	for key, value := range configuration.Annotations {
		mutatedPod.Annotations[key] = value
	}

	patch, err := object.CreatePatch(mutatedPod, pod)
	if err != nil {
		return nil, err
	}

	log.Printf("Generated patch: %s", string(patch))

	return &lifecycle.OperatorLifecycleResponse{
		JsonPatch: patch,
	}, nil
}
