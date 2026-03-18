// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package config

import (
"testing"

cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/common"
metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
"k8s.io/utils/pointer"
)

func newHelper(params map[string]string) *common.Plugin {
cluster := cnpgv1.Cluster{
ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
Spec: cnpgv1.ClusterSpec{
Plugins: []cnpgv1.PluginConfiguration{
{
Name:       "cnpg-i-wal-replica.documentdb.io",
Enabled:    pointer.Bool(true),
Parameters: params,
},
},
},
}
return common.NewPlugin(cluster, "cnpg-i-wal-replica.documentdb.io")
}

func TestFromParameters_Defaults(t *testing.T) {
helper := newHelper(map[string]string{})
cfg, errs := FromParameters(helper)

if len(errs) != 0 {
t.Fatalf("expected no validation errors, got %d", len(errs))
}
if cfg.Verbose != true {
t.Errorf("expected verbose=true by default, got %v", cfg.Verbose)
}
if cfg.Compression != 0 {
t.Errorf("expected compression=0 by default, got %d", cfg.Compression)
}
if cfg.Image != "" {
t.Errorf("expected empty image before ApplyDefaults, got %q", cfg.Image)
}
}

func TestFromParameters_WithValues(t *testing.T) {
helper := newHelper(map[string]string{
ImageParam:           "postgres:16",
ReplicationHostParam: "my-cluster-rw",
SynchronousParam:     "Active",
WalDirectoryParam:    "/custom/wal",
WalPVCSizeParam:      "50Gi",
VerboseParam:         "false",
CompressionParam:     "5",
})
cfg, errs := FromParameters(helper)

if len(errs) != 0 {
t.Fatalf("expected no validation errors, got %d", len(errs))
}
if cfg.Image != "postgres:16" {
t.Errorf("expected image postgres:16, got %q", cfg.Image)
}
if cfg.Synchronous != SynchronousActive {
t.Errorf("expected synchronous=active, got %q", cfg.Synchronous)
}
if cfg.WalPVCSize != "50Gi" {
t.Errorf("expected walPVCSize=50Gi, got %q", cfg.WalPVCSize)
}
if cfg.Verbose != false {
t.Errorf("expected verbose=false, got %v", cfg.Verbose)
}
if cfg.Compression != 5 {
t.Errorf("expected compression=5, got %d", cfg.Compression)
}
}

func TestFromParameters_InvalidSynchronous(t *testing.T) {
helper := newHelper(map[string]string{
SynchronousParam: "bogus",
})
_, errs := FromParameters(helper)

if len(errs) != 1 {
t.Fatalf("expected 1 validation error, got %d", len(errs))
}
}

func TestFromParameters_InvalidCompression(t *testing.T) {
tests := []struct {
name  string
value string
}{
{"not a number", "abc"},
{"too high", "10"},
{"negative", "-1"},
}
for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
helper := newHelper(map[string]string{
CompressionParam: tt.value,
})
_, errs := FromParameters(helper)
if len(errs) != 1 {
t.Fatalf("expected 1 validation error for %q, got %d", tt.value, len(errs))
}
})
}
}

func TestFromParameters_InvalidVerbose(t *testing.T) {
helper := newHelper(map[string]string{
VerboseParam: "yes",
})
_, errs := FromParameters(helper)

if len(errs) != 1 {
t.Fatalf("expected 1 validation error, got %d", len(errs))
}
}

func TestFromParameters_InvalidWalPVCSize(t *testing.T) {
helper := newHelper(map[string]string{
WalPVCSizeParam: "not-a-quantity",
})
_, errs := FromParameters(helper)

if len(errs) != 1 {
t.Fatalf("expected 1 validation error, got %d", len(errs))
}
}

func TestApplyDefaults(t *testing.T) {
cfg := &Configuration{}
cluster := &cnpgv1.Cluster{
Status: cnpgv1.ClusterStatus{
Image:        "postgres:16-default",
WriteService: "test-cluster-rw.default.svc",
},
}

cfg.ApplyDefaults(cluster)

if cfg.Image != "postgres:16-default" {
t.Errorf("expected default image, got %q", cfg.Image)
}
if cfg.ReplicationHost != "test-cluster-rw.default.svc" {
t.Errorf("expected default replicationHost, got %q", cfg.ReplicationHost)
}
if cfg.WalDirectory != defaultWalDir {
t.Errorf("expected default walDirectory, got %q", cfg.WalDirectory)
}
if cfg.Synchronous != defaultSynchronousMode {
t.Errorf("expected default synchronous, got %q", cfg.Synchronous)
}
if cfg.WalPVCSize != defaultWalPVCSize {
t.Errorf("expected default walPVCSize, got %q", cfg.WalPVCSize)
}
}

func TestApplyDefaults_NoOverwrite(t *testing.T) {
cfg := &Configuration{
Image:           "custom:latest",
ReplicationHost: "custom-host",
WalDirectory:    "/custom",
Synchronous:     SynchronousActive,
WalPVCSize:      "100Gi",
}
cluster := &cnpgv1.Cluster{
Status: cnpgv1.ClusterStatus{
Image:        "should-not-be-used",
WriteService: "should-not-be-used",
},
}

cfg.ApplyDefaults(cluster)

if cfg.Image != "custom:latest" {
t.Errorf("expected custom image preserved, got %q", cfg.Image)
}
if cfg.ReplicationHost != "custom-host" {
t.Errorf("expected custom host preserved, got %q", cfg.ReplicationHost)
}
if cfg.WalPVCSize != "100Gi" {
t.Errorf("expected custom PVC size preserved, got %q", cfg.WalPVCSize)
}
}

func TestToParameters_Roundtrip(t *testing.T) {
original := &Configuration{
Image:           "postgres:16",
ReplicationHost: "host-rw",
Synchronous:     SynchronousActive,
WalDirectory:    "/wal",
WalPVCSize:      "20Gi",
Verbose:         true,
Compression:     3,
}

params, err := original.ToParameters()
if err != nil {
t.Fatalf("ToParameters failed: %v", err)
}

helper := newHelper(params)
roundtripped, errs := FromParameters(helper)
if len(errs) != 0 {
t.Fatalf("unexpected validation errors: %v", errs)
}

if roundtripped.Image != original.Image {
t.Errorf("image mismatch: %q vs %q", roundtripped.Image, original.Image)
}
if roundtripped.Synchronous != original.Synchronous {
t.Errorf("synchronous mismatch: %q vs %q", roundtripped.Synchronous, original.Synchronous)
}
if roundtripped.Verbose != original.Verbose {
t.Errorf("verbose mismatch: %v vs %v", roundtripped.Verbose, original.Verbose)
}
if roundtripped.Compression != original.Compression {
t.Errorf("compression mismatch: %d vs %d", roundtripped.Compression, original.Compression)
}
}
