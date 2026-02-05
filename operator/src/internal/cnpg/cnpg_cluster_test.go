// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
"testing"

cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
. "github.com/onsi/ginkgo/v2"
. "github.com/onsi/gomega"
metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
"k8s.io/utils/pointer"
ctrl "sigs.k8s.io/controller-runtime"
"sigs.k8s.io/controller-runtime/pkg/log/zap"

dbpreview "github.com/documentdb/documentdb-operator/api/preview"
util "github.com/documentdb/documentdb-operator/internal/utils"
)

var _ = Describe("getBootstrapConfiguration", func() {
var log = zap.New(zap.WriteTo(GinkgoWriter))

It("returns default bootstrap when no bootstrap is configured", func() {
documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{},
}

result := getBootstrapConfiguration(documentdb, true, log)
Expect(result).ToNot(BeNil())
Expect(result.InitDB).ToNot(BeNil())
Expect(result.InitDB.PostInitSQL).To(HaveLen(3))
Expect(result.InitDB.PostInitSQL[0]).To(Equal("CREATE EXTENSION documentdb CASCADE"))
Expect(result.Recovery).To(BeNil())
})

It("returns default bootstrap when not primary region", func() {
documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
Backup: cnpgv1.LocalObjectReference{
Name: "my-backup",
},
},
},
},
}

result := getBootstrapConfiguration(documentdb, false, log)
Expect(result).ToNot(BeNil())
Expect(result.InitDB).ToNot(BeNil())
Expect(result.Recovery).To(BeNil())
})

It("returns default bootstrap when recovery is not configured", func() {
documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Bootstrap: &dbpreview.BootstrapConfiguration{},
},
}

result := getBootstrapConfiguration(documentdb, true, log)
Expect(result).ToNot(BeNil())
Expect(result.InitDB).ToNot(BeNil())
Expect(result.Recovery).To(BeNil())
})

It("returns backup recovery when backup name is specified", func() {
backupName := "my-backup"
documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
Backup: cnpgv1.LocalObjectReference{
Name: backupName,
},
},
},
},
}

result := getBootstrapConfiguration(documentdb, true, log)
Expect(result).ToNot(BeNil())
Expect(result.Recovery).ToNot(BeNil())
Expect(result.Recovery.Backup).ToNot(BeNil())
Expect(result.Recovery.Backup.LocalObjectReference.Name).To(Equal(backupName))
Expect(result.Recovery.VolumeSnapshots).To(BeNil())
Expect(result.InitDB).To(BeNil())
})

It("returns PV recovery when PV name is specified", func() {
pvName := "my-pv"
documentdb := &dbpreview.DocumentDB{
ObjectMeta: metav1.ObjectMeta{
Name: "test-cluster",
},
Spec: dbpreview.DocumentDBSpec{
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
PersistentVolume: &dbpreview.PVRecoveryConfiguration{
Name: pvName,
},
},
},
},
}

result := getBootstrapConfiguration(documentdb, true, log)
Expect(result).ToNot(BeNil())
Expect(result.Recovery).ToNot(BeNil())
Expect(result.Recovery.VolumeSnapshots).ToNot(BeNil())
// Temp PVC name is based on documentdb name
Expect(result.Recovery.VolumeSnapshots.Storage.Name).To(Equal("test-cluster-pv-recovery-temp"))
Expect(result.Recovery.VolumeSnapshots.Storage.Kind).To(Equal("PersistentVolumeClaim"))
Expect(result.Recovery.VolumeSnapshots.Storage.APIGroup).To(Equal(pointer.String("")))
Expect(result.Recovery.Backup).To(BeNil())
Expect(result.InitDB).To(BeNil())
})

It("returns default bootstrap when backup name is empty", func() {
documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
Backup: cnpgv1.LocalObjectReference{
Name: "",
},
},
},
},
}

result := getBootstrapConfiguration(documentdb, true, log)
Expect(result).ToNot(BeNil())
Expect(result.InitDB).ToNot(BeNil())
Expect(result.Recovery).To(BeNil())
})

It("returns default bootstrap when PV name is empty", func() {
documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
PersistentVolume: &dbpreview.PVRecoveryConfiguration{
Name: "",
},
},
},
},
}

result := getBootstrapConfiguration(documentdb, true, log)
Expect(result).ToNot(BeNil())
Expect(result.InitDB).ToNot(BeNil())
Expect(result.Recovery).To(BeNil())
})
})

var _ = Describe("getDefaultBootstrapConfiguration", func() {
It("returns a bootstrap configuration with InitDB", func() {
result := getDefaultBootstrapConfiguration()
Expect(result).ToNot(BeNil())
Expect(result.InitDB).ToNot(BeNil())
Expect(result.Recovery).To(BeNil())
})

It("includes required PostInitSQL statements", func() {
result := getDefaultBootstrapConfiguration()
Expect(result.InitDB.PostInitSQL).To(HaveLen(3))
Expect(result.InitDB.PostInitSQL).To(ContainElement("CREATE EXTENSION documentdb CASCADE"))
Expect(result.InitDB.PostInitSQL).To(ContainElement("CREATE ROLE documentdb WITH LOGIN PASSWORD 'Admin100'"))
Expect(result.InitDB.PostInitSQL).To(ContainElement("ALTER ROLE documentdb WITH SUPERUSER CREATEDB CREATEROLE REPLICATION BYPASSRLS"))
})
})

var _ = Describe("GetCnpgClusterSpec", func() {
var log = zap.New(zap.WriteTo(GinkgoWriter))

It("creates a CNPG cluster spec with default bootstrap", func() {
req := ctrl.Request{}
req.Name = "test-cluster"
req.Namespace = "default"

documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
InstancesPerNode: 3,
Resource: dbpreview.Resource{
Storage: dbpreview.StorageConfiguration{
PvcSize: "10Gi",
},
},
},
}

result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "standard", true, log)
Expect(result).ToNot(BeNil())
Expect(result.Name).To(Equal("test-cluster"))
Expect(result.Namespace).To(Equal("default"))
Expect(int(result.Spec.Instances)).To(Equal(3))
Expect(result.Spec.Bootstrap).ToNot(BeNil())
Expect(result.Spec.Bootstrap.InitDB).ToNot(BeNil())
})

It("creates a CNPG cluster spec with backup recovery", func() {
req := ctrl.Request{}
req.Name = "test-cluster"
req.Namespace = "default"

documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
InstancesPerNode: 3,
Resource: dbpreview.Resource{
Storage: dbpreview.StorageConfiguration{
PvcSize: "10Gi",
},
},
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
Backup: cnpgv1.LocalObjectReference{
Name: "test-backup",
},
},
},
},
}

result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "standard", true, log)
Expect(result).ToNot(BeNil())
Expect(result.Spec.Bootstrap).ToNot(BeNil())
Expect(result.Spec.Bootstrap.Recovery).ToNot(BeNil())
Expect(result.Spec.Bootstrap.Recovery.Backup).ToNot(BeNil())
Expect(result.Spec.Bootstrap.Recovery.Backup.LocalObjectReference.Name).To(Equal("test-backup"))
})

It("creates a CNPG cluster spec with PV recovery", func() {
req := ctrl.Request{}
req.Name = "test-cluster"
req.Namespace = "default"

documentdb := &dbpreview.DocumentDB{
ObjectMeta: metav1.ObjectMeta{
Name: "test-cluster",
},
Spec: dbpreview.DocumentDBSpec{
InstancesPerNode: 3,
Resource: dbpreview.Resource{
Storage: dbpreview.StorageConfiguration{
PvcSize: "10Gi",
},
},
Bootstrap: &dbpreview.BootstrapConfiguration{
Recovery: &dbpreview.RecoveryConfiguration{
PersistentVolume: &dbpreview.PVRecoveryConfiguration{
Name: "test-pv",
},
},
},
},
}

result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "standard", true, log)
Expect(result).ToNot(BeNil())
Expect(result.Spec.Bootstrap).ToNot(BeNil())
Expect(result.Spec.Bootstrap.Recovery).ToNot(BeNil())
Expect(result.Spec.Bootstrap.Recovery.VolumeSnapshots).ToNot(BeNil())
// Temp PVC name is based on documentdb name
Expect(result.Spec.Bootstrap.Recovery.VolumeSnapshots.Storage.Name).To(Equal("test-cluster-pv-recovery-temp"))
Expect(result.Spec.Bootstrap.Recovery.VolumeSnapshots.Storage.Kind).To(Equal("PersistentVolumeClaim"))
})

It("uses specified storage class", func() {
req := ctrl.Request{}
req.Name = "test-cluster"
req.Namespace = "default"

documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
InstancesPerNode: 3,
Resource: dbpreview.Resource{
Storage: dbpreview.StorageConfiguration{
PvcSize: "10Gi",
},
},
},
}

result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "premium-storage", true, log)
Expect(result).ToNot(BeNil())
Expect(result.Spec.StorageConfiguration.StorageClass).ToNot(BeNil())
Expect(*result.Spec.StorageConfiguration.StorageClass).To(Equal("premium-storage"))
})

It("uses nil storage class when empty string is provided", func() {
req := ctrl.Request{}
req.Name = "test-cluster"
req.Namespace = "default"

documentdb := &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
InstancesPerNode: 3,
Resource: dbpreview.Resource{
Storage: dbpreview.StorageConfiguration{
PvcSize: "10Gi",
},
},
},
}

result := GetCnpgClusterSpec(req, documentdb, "postgres:16", "test-sa", "", true, log)
Expect(result).ToNot(BeNil())
Expect(result.Spec.StorageConfiguration.StorageClass).To(BeNil())
})
})

// Standard Go tests for additional coverage

func TestGetInheritedMetadataLabels(t *testing.T) {
tests := []struct {
name     string
appName  string
expected map[string]string
}{
{
name:    "standard app name",
appName: "my-documentdb",
expected: map[string]string{
util.LABEL_APP:          "my-documentdb",
util.LABEL_REPLICA_TYPE: "primary",
},
},
{
name:    "app name with special characters",
appName: "test-db-123",
expected: map[string]string{
util.LABEL_APP:          "test-db-123",
util.LABEL_REPLICA_TYPE: "primary",
},
},
{
name:    "empty app name",
appName: "",
expected: map[string]string{
util.LABEL_APP:          "",
util.LABEL_REPLICA_TYPE: "primary",
},
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
result := getInheritedMetadataLabels(tt.appName)

if result == nil {
t.Fatal("Expected non-nil result")
}

if result.Labels == nil {
t.Fatal("Expected non-nil labels map")
}

for key, expectedValue := range tt.expected {
if actualValue, exists := result.Labels[key]; !exists {
t.Errorf("Expected label %q to exist", key)
} else if actualValue != expectedValue {
t.Errorf("Expected label %q = %q, got %q", key, expectedValue, actualValue)
}
}
})
}
}

func TestGetMaxStopDelayOrDefault(t *testing.T) {
tests := []struct {
name       string
documentdb *dbpreview.DocumentDB
expected   int32
}{
{
name: "returns default when StopDelay is 0",
documentdb: &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Timeouts: dbpreview.Timeouts{
StopDelay: 0,
},
},
},
expected: util.CNPG_DEFAULT_STOP_DELAY,
},
{
name: "returns custom StopDelay when set",
documentdb: &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Timeouts: dbpreview.Timeouts{
StopDelay: 60,
},
},
},
expected: 60,
},
{
name: "returns max StopDelay",
documentdb: &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{
Timeouts: dbpreview.Timeouts{
StopDelay: 1800,
},
},
},
expected: 1800,
},
{
name: "returns default when Timeouts is empty",
documentdb: &dbpreview.DocumentDB{
Spec: dbpreview.DocumentDBSpec{},
},
expected: util.CNPG_DEFAULT_STOP_DELAY,
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
result := getMaxStopDelayOrDefault(tt.documentdb)

if result != tt.expected {
t.Errorf("Expected %d, got %d", tt.expected, result)
}
})
}
}
