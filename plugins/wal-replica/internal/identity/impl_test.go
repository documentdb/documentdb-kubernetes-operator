// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package identity

import (
"context"
"testing"

"github.com/cloudnative-pg/cnpg-i/pkg/identity"
"github.com/documentdb/cnpg-i-wal-replica/pkg/metadata"
)

func TestGetPluginMetadata(t *testing.T) {
impl := Implementation{}
resp, err := impl.GetPluginMetadata(context.Background(), &identity.GetPluginMetadataRequest{})
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if resp.Name != metadata.PluginName {
t.Errorf("expected plugin name %q, got %q", metadata.PluginName, resp.Name)
}
}

func TestGetPluginCapabilities(t *testing.T) {
impl := Implementation{}
resp, err := impl.GetPluginCapabilities(context.Background(), &identity.GetPluginCapabilitiesRequest{})
if err != nil {
t.Fatalf("unexpected error: %v", err)
}

if len(resp.Capabilities) == 0 {
t.Fatal("expected at least one capability")
}

hasOperator := false
hasReconciler := false
for _, cap := range resp.Capabilities {
svc := cap.GetService()
if svc == nil {
continue
}
switch svc.Type {
case identity.PluginCapability_Service_TYPE_OPERATOR_SERVICE:
hasOperator = true
case identity.PluginCapability_Service_TYPE_RECONCILER_HOOKS:
hasReconciler = true
}
}

if !hasOperator {
t.Error("expected TYPE_OPERATOR_SERVICE capability")
}
if !hasReconciler {
t.Error("expected TYPE_RECONCILER_HOOKS capability")
}
}

func TestProbe(t *testing.T) {
impl := Implementation{}
resp, err := impl.Probe(context.Background(), &identity.ProbeRequest{})
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if !resp.Ready {
t.Error("expected probe to report ready")
}
}
