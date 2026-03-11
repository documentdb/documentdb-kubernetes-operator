// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package reconciler

import (
"strings"
"testing"

cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

"github.com/documentdb/cnpg-i-wal-replica/internal/config"
)

func TestBuildWalReceiverCommand_CreateSlot(t *testing.T) {
cfg := &config.Configuration{
ReplicationHost: "test-cluster-rw",
Verbose:         true,
Compression:     0,
Synchronous:     config.SynchronousInactive,
}

cmd := buildWalReceiverCommand(cfg, "/var/lib/postgresql/wal", true)

if !strings.Contains(cmd, "pg_receivewal") {
t.Error("expected pg_receivewal in command")
}
if !strings.Contains(cmd, "--create-slot") {
t.Error("expected --create-slot flag")
}
if !strings.Contains(cmd, "--if-not-exists") {
t.Error("expected --if-not-exists flag")
}
if !strings.Contains(cmd, "--verbose") {
t.Error("expected --verbose flag")
}
if strings.Contains(cmd, "--synchronous") {
t.Error("did not expect --synchronous flag when inactive")
}
}

func TestBuildWalReceiverCommand_NoCreateSlot(t *testing.T) {
cfg := &config.Configuration{
ReplicationHost: "test-cluster-rw",
Verbose:         false,
Compression:     5,
Synchronous:     config.SynchronousActive,
}

cmd := buildWalReceiverCommand(cfg, "/wal", false)

if strings.Contains(cmd, "--create-slot") {
t.Error("should not have --create-slot when createSlot=false")
}
if strings.Contains(cmd, "--verbose") {
t.Error("should not have --verbose when verbose=false")
}
if !strings.Contains(cmd, "--synchronous") {
t.Error("expected --synchronous flag when active")
}
if !strings.Contains(cmd, "--compress 5") {
t.Error("expected --compress 5")
}
}

func TestBuildDeployment_Structure(t *testing.T) {
cluster := &cnpgv1.Cluster{
ObjectMeta: metav1.ObjectMeta{
Name:      "test-cluster",
Namespace: "default",
UID:       "test-uid-123",
},
TypeMeta: metav1.TypeMeta{
APIVersion: "postgresql.cnpg.io/v1",
Kind:       "Cluster",
},
Status: cnpgv1.ClusterStatus{
Certificates: cnpgv1.CertificatesStatus{
			CertificatesConfiguration: cnpgv1.CertificatesConfiguration{
ServerCASecret:       "test-cluster-ca",
ReplicationTLSSecret: "test-cluster-replication",
			},
},
},
}

cfg := &config.Configuration{
Image:           "postgres:16",
ReplicationHost: "test-cluster-rw",
WalDirectory:    "/var/lib/postgresql/wal",
WalPVCSize:      "10Gi",
Verbose:         true,
Compression:     0,
Synchronous:     config.SynchronousInactive,
}

ownerRef := buildOwnerReference(cluster)
dep := buildDeployment("test-cluster-wal-receiver", "default", cluster, cfg, ownerRef)

if dep.Name != "test-cluster-wal-receiver" {
t.Errorf("expected deployment name test-cluster-wal-receiver, got %q", dep.Name)
}
if dep.Namespace != "default" {
t.Errorf("expected namespace default, got %q", dep.Namespace)
}
if len(dep.OwnerReferences) != 1 {
t.Fatalf("expected 1 owner reference, got %d", len(dep.OwnerReferences))
}
if dep.OwnerReferences[0].Controller == nil || !*dep.OwnerReferences[0].Controller {
t.Error("expected Controller=true on OwnerReference")
}
if dep.OwnerReferences[0].BlockOwnerDeletion == nil || !*dep.OwnerReferences[0].BlockOwnerDeletion {
t.Error("expected BlockOwnerDeletion=true on OwnerReference")
}

// Check container
containers := dep.Spec.Template.Spec.Containers
if len(containers) != 1 {
t.Fatalf("expected 1 container, got %d", len(containers))
}
container := containers[0]

if container.Name != "wal-receiver" {
t.Errorf("expected container name wal-receiver, got %q", container.Name)
}
if container.Image != "postgres:16" {
t.Errorf("expected image postgres:16, got %q", container.Image)
}
if container.LivenessProbe == nil {
t.Error("expected liveness probe to be set")
}
if container.ReadinessProbe == nil {
t.Error("expected readiness probe to be set")
}

// Check volumes
volumes := dep.Spec.Template.Spec.Volumes
if len(volumes) != 3 {
t.Errorf("expected 3 volumes, got %d", len(volumes))
}

// Check security context
sc := dep.Spec.Template.Spec.SecurityContext
if sc == nil {
t.Fatal("expected security context")
}
if *sc.RunAsUser != 105 {
t.Errorf("expected RunAsUser=105, got %d", *sc.RunAsUser)
}
}

func TestBuildOwnerReference(t *testing.T) {
cluster := &cnpgv1.Cluster{
ObjectMeta: metav1.ObjectMeta{
Name: "test",
UID:  "uid-abc",
},
TypeMeta: metav1.TypeMeta{
APIVersion: "postgresql.cnpg.io/v1",
Kind:       "Cluster",
},
}

ref := buildOwnerReference(cluster)

if ref.Name != "test" {
t.Errorf("expected name test, got %q", ref.Name)
}
if ref.Controller == nil || !*ref.Controller {
t.Error("expected Controller=true")
}
if ref.BlockOwnerDeletion == nil || !*ref.BlockOwnerDeletion {
t.Error("expected BlockOwnerDeletion=true")
}
}

func TestBuildNonControllerOwnerReference(t *testing.T) {
cluster := &cnpgv1.Cluster{
ObjectMeta: metav1.ObjectMeta{
Name: "test",
UID:  "uid-abc",
},
TypeMeta: metav1.TypeMeta{
APIVersion: "postgresql.cnpg.io/v1",
Kind:       "Cluster",
},
}

ref := buildNonControllerOwnerReference(cluster)

if ref.Name != "test" {
t.Errorf("expected name test, got %q", ref.Name)
}
if ref.Controller == nil || *ref.Controller {
t.Error("expected Controller=false")
}
if ref.BlockOwnerDeletion == nil || !*ref.BlockOwnerDeletion {
t.Error("expected BlockOwnerDeletion=true")
}
}
