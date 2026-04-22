// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// Environment variable names for long haul test configuration.
	EnvEnabled     = "LONGHAUL_ENABLED"
	EnvMaxDuration = "LONGHAUL_MAX_DURATION"
	EnvNamespace   = "LONGHAUL_NAMESPACE"
	EnvClusterName = "LONGHAUL_CLUSTER_NAME"
)

// Config holds all configuration for a long haul test run.
type Config struct {
	// MaxDuration is the maximum test duration. Zero means run until failure.
	// Requires explicit LONGHAUL_MAX_DURATION=0s to enable infinite runs.
	// Default: 30m (safe for local development).
	MaxDuration time.Duration

	// Namespace is the Kubernetes namespace of the target DocumentDB cluster.
	Namespace string

	// ClusterName is the name of the target DocumentDB cluster CR.
	ClusterName string
}

// DefaultConfig returns a Config with safe defaults for local development.
func DefaultConfig() Config {
	return Config{
		MaxDuration: 30 * time.Minute,
		Namespace:   "default",
		ClusterName: "",
	}
}

// LoadFromEnv loads configuration from environment variables,
// falling back to defaults for any unset variable.
func LoadFromEnv() (Config, error) {
	cfg := DefaultConfig()

	if v := os.Getenv(EnvMaxDuration); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvMaxDuration, v, err)
		}
		cfg.MaxDuration = d
	}

	if v := os.Getenv(EnvNamespace); v != "" {
		cfg.Namespace = v
	}

	if v := os.Getenv(EnvClusterName); v != "" {
		cfg.ClusterName = v
	}

	return cfg, nil
}

// Validate checks that the configuration is valid.
func (c *Config) Validate() error {
	if c.MaxDuration < 0 {
		return fmt.Errorf("max duration must not be negative, got %s", c.MaxDuration)
	}
	if c.Namespace == "" {
		return fmt.Errorf("namespace must not be empty")
	}
	if c.ClusterName == "" {
		return fmt.Errorf("cluster name must not be empty")
	}
	return nil
}

// IsEnabled returns true if the long haul test is explicitly enabled
// via the LONGHAUL_ENABLED environment variable.
func IsEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(EnvEnabled)))
	return v == "true" || v == "1" || v == "yes"
}
