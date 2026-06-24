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

func TestComputeResourceSplit_EnvelopeOmittedAllExplicit(t *testing.T) {
	cfg := prodSplitConfig()
	// No envelope; gateway + database fully specified for both dims.
	d := ddbWithMemory("", false)
	d.Spec.Resource.CPU = ""
	d.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "512Mi", CPU: "500m"}
	d.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "4Gi", CPU: "3"}

	s := ComputeResourceSplit(d, cfg)
	if s.Gateway.MemoryLimit != "512Mi" || s.Gateway.CPULimit != "500m" {
		t.Errorf("gateway = %+v, want 512Mi/500m", s.Gateway)
	}
	if s.Postgres.MemoryLimit != "4Gi" || s.Postgres.CPULimit != "3" {
		t.Errorf("postgres = %+v, want 4Gi/3", s.Postgres)
	}
	if s.PostgresMemoryBytes != 4*1024*1024*1024 {
		t.Errorf("postgres bytes = %d, want 4Gi", s.PostgresMemoryBytes)
	}
}

func TestComputeResourceSplit_CPUCarvedWithMonitoring(t *testing.T) {
	cfg := prodSplitConfig()
	d := ddbWithMemory("8Gi", true)
	d.Spec.Resource.CPU = "4"
	s := ComputeResourceSplit(d, cfg)
	// otel cpu reservation defaults to 50m; postgres = 4 - 50m = 3950m.
	if s.OTel.CPURequest != "50m" {
		t.Errorf("otel cpu = %q, want 50m", s.OTel.CPURequest)
	}
	if s.Postgres.CPULimit != "3950m" {
		t.Errorf("postgres cpu = %q, want 3950m (4 - 50m otel)", s.Postgres.CPULimit)
	}
}

func TestValidateResources(t *testing.T) {
	cfg := prodSplitConfig()
	mk := func(memEnv, cpuEnv string, monitoring bool) *dbpreview.DocumentDB {
		d := ddbWithMemory(memEnv, monitoring)
		d.Spec.Resource.CPU = cpuEnv
		return d
	}

	// 1. Envelope set, valid -> no errors.
	if errs := ValidateResources(mk("16Gi", "", false), cfg); len(errs) != 0 {
		t.Errorf("envelope-set valid: unexpected errors %v", errs)
	}

	// 2. Envelope omitted, gateway+database memory set -> ok.
	d := mk("", "", false)
	d.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "512Mi"}
	d.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "4Gi"}
	if errs := ValidateResources(d, cfg); len(errs) != 0 {
		t.Errorf("omitted+fully-specified memory: unexpected errors %v", errs)
	}

	// 3. Envelope omitted, only gateway memory set -> error.
	d = mk("", "", false)
	d.Spec.Resource.Gateway = &dbpreview.ComponentResources{Memory: "512Mi"}
	if errs := ValidateResources(d, cfg); len(errs) == 0 {
		t.Errorf("omitted+partial memory: expected an error")
	}

	// 4. Nothing set -> no error (unmanaged).
	if errs := ValidateResources(mk("", "", false), cfg); len(errs) != 0 {
		t.Errorf("unmanaged: unexpected errors %v", errs)
	}

	// 5. Envelope set but explicit database memory exceeds it -> error.
	d = mk("4Gi", "", false)
	d.Spec.Resource.Database = &dbpreview.ComponentResources{Memory: "8Gi"}
	if errs := ValidateResources(d, cfg); len(errs) == 0 {
		t.Errorf("oversubscribed memory: expected an error")
	}

	// 6. Tiny envelope: gateway(18.75% of 100Mi) + otel(128Mi) reservations
	// exceed the 100Mi envelope, leaving nothing for postgres -> error.
	if errs := ValidateResources(mk("100Mi", "", true), cfg); len(errs) == 0 {
		t.Errorf("reservations exceed envelope: expected an error")
	}

	// 7. CPU omitted + only database.cpu set (gateway.cpu unset) -> error (symmetric rule).
	d = mk("", "", false)
	d.Spec.Resource.Database = &dbpreview.ComponentResources{CPU: "2"}
	if errs := ValidateResources(d, cfg); len(errs) == 0 {
		t.Errorf("omitted+partial cpu: expected an error")
	}

	// 8. CPU omitted + gateway.cpu + database.cpu set -> ok.
	d = mk("", "", false)
	d.Spec.Resource.Gateway = &dbpreview.ComponentResources{CPU: "500m"}
	d.Spec.Resource.Database = &dbpreview.ComponentResources{CPU: "2"}
	if errs := ValidateResources(d, cfg); len(errs) != 0 {
		t.Errorf("omitted+fully-specified cpu: unexpected errors %v", errs)
	}
}
