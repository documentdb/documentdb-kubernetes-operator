// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"testing"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// prodSplitConfig mirrors the documented production defaults (18.75%, cap 32Gi,
// otel 48Mi/128Mi) without depending on environment variables.
func prodSplitConfig() SplitConfig {
	return SplitConfig{
		GatewayMemoryFraction: 0.1875,
		GatewayMemoryCapBytes: 32 * 1024 * 1024 * 1024, // 32Gi
		OTelMemoryRequest:     "48Mi",
		OTelMemoryLimit:       "128Mi",
		OTelCPURequest:        "50m",
	}
}

func ddbWithMemory(mem string, monitoring bool) *dbpreview.DocumentDB {
	d := &dbpreview.DocumentDB{}
	d.Spec.Resource.Memory = mem
	if monitoring {
		d.Spec.Monitoring = &dbpreview.MonitoringSpec{Enabled: true}
	}
	return d
}

func TestComputeResourceSplit_ProductionRows(t *testing.T) {
	// (SKU, envelope, expected gateway, expected db remainder) — monitoring OFF.
	cases := []struct {
		name     string
		envelope string
		wantGW   string
		wantDB   string
	}{
		{"M20", "4Gi", "768Mi", "3328Mi"},
		{"M50", "32Gi", "6Gi", "26Gi"},
		{"M60", "64Gi", "12Gi", "52Gi"},
		{"M200-capped", "256Gi", "32Gi", "224Gi"},
	}
	cfg := prodSplitConfig()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ComputeResourceSplit(ddbWithMemory(tc.envelope, false), cfg)
			if s.Gateway.MemoryLimit != tc.wantGW {
				t.Errorf("gateway memory = %q, want %q", s.Gateway.MemoryLimit, tc.wantGW)
			}
			if s.Gateway.MemoryRequest != s.Gateway.MemoryLimit {
				t.Errorf("gateway request %q != limit %q (want Guaranteed)", s.Gateway.MemoryRequest, s.Gateway.MemoryLimit)
			}
			if s.Postgres.MemoryLimit != tc.wantDB {
				t.Errorf("postgres memory = %q, want %q", s.Postgres.MemoryLimit, tc.wantDB)
			}
			if s.MonitoringEnabled {
				t.Errorf("monitoring should be disabled")
			}
			if s.OTel.MemoryLimit != "" {
				t.Errorf("otel should be empty when monitoring disabled, got %q", s.OTel.MemoryLimit)
			}
		})
	}
}

func TestComputeResourceSplit_MonitoringCarvesOTel(t *testing.T) {
	cfg := prodSplitConfig()
	s := ComputeResourceSplit(ddbWithMemory("16Gi", true), cfg)

	if !s.MonitoringEnabled {
		t.Fatalf("monitoring should be enabled")
	}
	// 16Gi gateway = 18.75% = 3Gi.
	if s.Gateway.MemoryLimit != "3Gi" {
		t.Errorf("gateway = %q, want 3Gi", s.Gateway.MemoryLimit)
	}
	if s.OTel.MemoryRequest != "48Mi" || s.OTel.MemoryLimit != "128Mi" {
		t.Errorf("otel req/limit = %q/%q, want 48Mi/128Mi", s.OTel.MemoryRequest, s.OTel.MemoryLimit)
	}
	// db = 16Gi - 3Gi - 128Mi = 13Gi - 128Mi = 13312Mi - 128Mi = 13184Mi.
	if s.Postgres.MemoryLimit != "13184Mi" {
		t.Errorf("postgres = %q, want 13184Mi", s.Postgres.MemoryLimit)
	}
	wantBytes := int64(13184) * 1024 * 1024
	if s.PostgresMemoryBytes != wantBytes {
		t.Errorf("PostgresMemoryBytes = %d, want %d", s.PostgresMemoryBytes, wantBytes)
	}
}

func TestComputeResourceSplit_ExplicitOverridesWin(t *testing.T) {
	cfg := prodSplitConfig()
	d := ddbWithMemory("16Gi", true)
	d.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "2Gi", CPU: "1"}
	d.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "10Gi"}
	d.Spec.Resource.OTel = &dbpreview.ComponentResources{Memory: "256Mi"}

	s := ComputeResourceSplit(d, cfg)

	if s.Gateway.MemoryLimit != "2Gi" || s.Gateway.MemoryRequest != "2Gi" {
		t.Errorf("gateway override not applied: %+v", s.Gateway)
	}
	if s.Gateway.CPULimit != "1" || s.Gateway.CPURequest != "1" {
		t.Errorf("gateway cpu override not applied: %+v", s.Gateway)
	}
	if s.OTel.MemoryLimit != "256Mi" || s.OTel.MemoryRequest != "256Mi" {
		t.Errorf("otel override not applied: %+v", s.OTel)
	}
	if s.Postgres.MemoryLimit != "10Gi" {
		t.Errorf("database override not applied: %q", s.Postgres.MemoryLimit)
	}
}

func TestComputeResourceSplit_UnsetMemoryNoCarveOut(t *testing.T) {
	cfg := prodSplitConfig()
	// No envelope memory set -> no automatic carve-out (legacy behavior).
	s := ComputeResourceSplit(ddbWithMemory("", false), cfg)
	if s.Gateway.MemoryLimit != "" || s.Postgres.MemoryLimit != "" {
		t.Errorf("expected no memory set, got gw=%q pg=%q", s.Gateway.MemoryLimit, s.Postgres.MemoryLimit)
	}
	if s.PostgresMemoryBytes != 0 {
		t.Errorf("expected 0 postgres bytes, got %d", s.PostgresMemoryBytes)
	}
}

func TestComputeResourceSplit_CPUFromEnvelope(t *testing.T) {
	cfg := prodSplitConfig()
	d := ddbWithMemory("8Gi", false)
	d.Spec.Resource.CPU = "4"
	s := ComputeResourceSplit(d, cfg)
	if s.Postgres.CPULimit != "4" || s.Postgres.CPURequest != "4" {
		t.Errorf("postgres cpu = %q/%q, want 4/4", s.Postgres.CPURequest, s.Postgres.CPULimit)
	}
}

func TestComputeResourceSplit_GatewayCPULimitDefault(t *testing.T) {
	cfg := prodSplitConfig()
	cfg.GatewayCPULimit = "2"
	d := ddbWithMemory("8Gi", false)
	s := ComputeResourceSplit(d, cfg)
	if s.Gateway.CPULimit != "2" || s.Gateway.CPURequest != "2" {
		t.Errorf("gateway cpu = %q/%q, want 2/2", s.Gateway.CPURequest, s.Gateway.CPULimit)
	}
}
