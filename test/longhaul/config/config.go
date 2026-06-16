// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// Environment variable names for long haul test configuration.
	EnvEnabled     = "LONGHAUL_ENABLED"
	EnvMaxDuration = "LONGHAUL_MAX_DURATION"
	EnvNamespace   = "LONGHAUL_NAMESPACE"
	EnvClusterName = "LONGHAUL_CLUSTER_NAME"

	// Workload and operation tuning.
	EnvMongoURI        = "LONGHAUL_MONGO_URI"
	EnvNumWriters      = "LONGHAUL_NUM_WRITERS"
	EnvNumVerifiers    = "LONGHAUL_NUM_VERIFIERS"
	EnvOpCooldown      = "LONGHAUL_OP_COOLDOWN"
	EnvRecoveryTimeout = "LONGHAUL_RECOVERY_TIMEOUT"
	EnvSteadyStateWait = "LONGHAUL_STEADY_STATE_WAIT"
	// Scale operation bounds. The DocumentDB CRD hard-caps spec.nodeCount=1,
	// so the scale dimension actually exercised is spec.instancesPerNode (1-3).
	EnvMinInstances = "LONGHAUL_MIN_INSTANCES"
	EnvMaxInstances = "LONGHAUL_MAX_INSTANCES"

	// Observability and reporting.
	EnvReportInterval = "LONGHAUL_REPORT_INTERVAL"
)

// Config holds all configuration for a long haul test run.
type Config struct {
	// MaxDuration is the maximum test duration. Zero means run until failure.
	MaxDuration time.Duration

	// Namespace is the Kubernetes namespace of the target DocumentDB cluster.
	Namespace string

	// ClusterName is the name of the target DocumentDB cluster CR.
	ClusterName string

	// MongoURI is the MongoDB connection string for data-plane workload.
	MongoURI string

	// NumWriters is the number of concurrent writer goroutines.
	NumWriters int

	// NumVerifiers is the number of concurrent verifier goroutines.
	NumVerifiers int

	// OpCooldown is the minimum time between disruptive operations.
	OpCooldown time.Duration

	// RecoveryTimeout is the max time to wait for cluster recovery after an operation.
	RecoveryTimeout time.Duration

	// SteadyStateWait is how long the cluster must be healthy before an operation fires.
	SteadyStateWait time.Duration

	// MinInstances is the minimum spec.instancesPerNode for scale-down.
	// CRD lower bound is 1.
	MinInstances int

	// MaxInstances is the maximum spec.instancesPerNode for scale-up.
	// CRD upper bound is 3.
	MaxInstances int

	// ReportInterval is how often checkpoint reports are generated.
	ReportInterval time.Duration
}

// DefaultConfig returns a Config with safe defaults for local development.
func DefaultConfig() Config {
	return Config{
		MaxDuration:     30 * time.Minute,
		Namespace:       "default",
		ClusterName:     "",
		MongoURI:        "",
		NumWriters:      5,
		NumVerifiers:    2,
		OpCooldown:      5 * time.Minute,
		RecoveryTimeout: 5 * time.Minute,
		SteadyStateWait: 60 * time.Second,
		MinInstances:    1,
		MaxInstances:    3,
		ReportInterval:  1 * time.Hour,
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

	if v := os.Getenv(EnvMongoURI); v != "" {
		cfg.MongoURI = v
	}

	if v := os.Getenv(EnvNumWriters); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvNumWriters, v, err)
		}
		cfg.NumWriters = n
	}

	if v := os.Getenv(EnvNumVerifiers); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvNumVerifiers, v, err)
		}
		cfg.NumVerifiers = n
	}

	if v := os.Getenv(EnvOpCooldown); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvOpCooldown, v, err)
		}
		cfg.OpCooldown = d
	}

	if v := os.Getenv(EnvRecoveryTimeout); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvRecoveryTimeout, v, err)
		}
		cfg.RecoveryTimeout = d
	}

	if v := os.Getenv(EnvSteadyStateWait); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvSteadyStateWait, v, err)
		}
		cfg.SteadyStateWait = d
	}

	if v := os.Getenv(EnvMinInstances); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvMinInstances, v, err)
		}
		cfg.MinInstances = n
	}

	if v := os.Getenv(EnvMaxInstances); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvMaxInstances, v, err)
		}
		cfg.MaxInstances = n
	}

	if v := os.Getenv(EnvReportInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s=%q: %w", EnvReportInterval, v, err)
		}
		cfg.ReportInterval = d
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
	if c.NumWriters < 1 {
		return fmt.Errorf("num writers must be at least 1, got %d", c.NumWriters)
	}
	if c.NumVerifiers < 1 {
		return fmt.Errorf("num verifiers must be at least 1, got %d", c.NumVerifiers)
	}
	if c.OpCooldown < 0 {
		return fmt.Errorf("operation cooldown must not be negative, got %s", c.OpCooldown)
	}
	if c.RecoveryTimeout <= 0 {
		return fmt.Errorf("recovery timeout must be positive, got %s", c.RecoveryTimeout)
	}
	if c.MinInstances < 1 {
		return fmt.Errorf("min instances must be at least 1, got %d", c.MinInstances)
	}
	if c.MaxInstances > 3 {
		return fmt.Errorf("max instances must not exceed 3 (CRD upper bound for spec.instancesPerNode), got %d", c.MaxInstances)
	}
	if c.MaxInstances < c.MinInstances {
		return fmt.Errorf("max instances (%d) must be >= min instances (%d)", c.MaxInstances, c.MinInstances)
	}
	return nil
}

// IsEnabled returns true if the long haul test is explicitly enabled
// via the LONGHAUL_ENABLED environment variable.
func IsEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(EnvEnabled)))
	return v == "true" || v == "1" || v == "yes"
}
