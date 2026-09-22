// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	util "github.com/documentdb/documentdb-operator/internal/utils"
)

func TestToClusterIntentDefaults(t *testing.T) {
	a := DocumentDBAdapter{}
	db := &dbpreview.DocumentDB{Spec: dbpreview.DocumentDBSpec{}}

	intent := a.ToClusterIntent(db)

	if intent.Images.PostgresExtension != util.DEFAULT_DOCUMENTDB_IMAGE {
		t.Errorf("Images.PostgresExtension = %q, want %q", intent.Images.PostgresExtension, util.DEFAULT_DOCUMENTDB_IMAGE)
	}
	if intent.Images.Gateway != util.DEFAULT_GATEWAY_IMAGE {
		t.Errorf("Images.Gateway = %q, want %q", intent.Images.Gateway, util.DEFAULT_GATEWAY_IMAGE)
	}
	if intent.Images.Postgres != "" {
		t.Errorf("Images.Postgres = %q, want empty", intent.Images.Postgres)
	}
	if intent.CredentialSecret != util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET {
		t.Errorf("CredentialSecret = %q, want %q", intent.CredentialSecret, util.DEFAULT_DOCUMENTDB_CREDENTIALS_SECRET)
	}
	if intent.Plugins.SidecarInjectorName != util.DEFAULT_SIDECAR_INJECTOR_PLUGIN {
		t.Errorf("Plugins.SidecarInjectorName = %q, want %q", intent.Plugins.SidecarInjectorName, util.DEFAULT_SIDECAR_INJECTOR_PLUGIN)
	}
	if intent.Plugins.WalReplicaName != util.DEFAULT_WAL_REPLICA_PLUGIN {
		t.Errorf("Plugins.WalReplicaName = %q, want %q", intent.Plugins.WalReplicaName, util.DEFAULT_WAL_REPLICA_PLUGIN)
	}
	if intent.Product.Name != "DocumentDB" {
		t.Errorf("Product.Name = %q, want DocumentDB", intent.Product.Name)
	}
}

func TestToClusterIntentOverrides(t *testing.T) {
	a := DocumentDBAdapter{}
	db := &dbpreview.DocumentDB{Spec: dbpreview.DocumentDBSpec{
		Image: &dbpreview.ImageSpec{
			DocumentDB: "reg/ext:v2",
			Gateway:    "reg/gw:v2",
			Postgres:   "reg/pg:16",
		},
		DocumentDbCredentialSecret: "my-secret",
		Plugins:                    &dbpreview.PluginsSpec{SidecarInjectorName: "custom-injector.example.io"},
	}}

	intent := a.ToClusterIntent(db)

	if intent.Images.PostgresExtension != "reg/ext:v2" {
		t.Errorf("Images.PostgresExtension = %q, want reg/ext:v2", intent.Images.PostgresExtension)
	}
	if intent.Images.Gateway != "reg/gw:v2" {
		t.Errorf("Images.Gateway = %q, want reg/gw:v2", intent.Images.Gateway)
	}
	if intent.Images.Postgres != "reg/pg:16" {
		t.Errorf("Images.Postgres = %q, want reg/pg:16", intent.Images.Postgres)
	}
	if intent.CredentialSecret != "my-secret" {
		t.Errorf("CredentialSecret = %q, want my-secret", intent.CredentialSecret)
	}
	if intent.Plugins.SidecarInjectorName != "custom-injector.example.io" {
		t.Errorf("Plugins.SidecarInjectorName = %q, want custom-injector.example.io", intent.Plugins.SidecarInjectorName)
	}
}

func TestToClusterIntentTopologyStorageIdentity(t *testing.T) {
	a := DocumentDBAdapter{}
	db := &dbpreview.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "my-db", UID: "uid-123"},
		TypeMeta:   metav1.TypeMeta{APIVersion: "documentdb.io/preview", Kind: "DocumentDB"},
		Spec: dbpreview.DocumentDBSpec{
			InstancesPerNode: 3,
			Resource: dbpreview.Resource{
				Storage: dbpreview.StorageConfiguration{PvcSize: "20Gi"},
			},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "regcred"}},
		},
	}

	intent := a.ToClusterIntent(db)

	if intent.Topology.InstancesPerNode != 3 {
		t.Errorf("Topology.InstancesPerNode = %d, want 3", intent.Topology.InstancesPerNode)
	}
	if intent.Storage.PvcSize != "20Gi" {
		t.Errorf("Storage.PvcSize = %q, want 20Gi", intent.Storage.PvcSize)
	}
	if len(intent.Images.PullSecrets) != 1 || intent.Images.PullSecrets[0].Name != "regcred" {
		t.Errorf("Images.PullSecrets = %+v, want [regcred]", intent.Images.PullSecrets)
	}
	if intent.Identity.Name != "my-db" || intent.Identity.UID != "uid-123" {
		t.Errorf("Identity = %+v, want name=my-db uid=uid-123", intent.Identity)
	}
	if intent.Identity.APIVersion != "documentdb.io/preview" || intent.Identity.Kind != "DocumentDB" {
		t.Errorf("Identity type = %s/%s, want documentdb.io/preview/DocumentDB", intent.Identity.APIVersion, intent.Identity.Kind)
	}
}

func TestToClusterIntentPostgresAndFeatureGates(t *testing.T) {
	a := DocumentDBAdapter{}
	uid, gid := int64(26), int64(999)
	db := &dbpreview.DocumentDB{
		Spec: dbpreview.DocumentDBSpec{
			Postgres: &dbpreview.PostgresSpec{
				UID:         &uid,
				GID:         &gid,
				PostInitSQL: []string{"SELECT 1"},
			},
			FeatureGates: map[string]bool{dbpreview.FeatureGateIOUring: true},
		},
	}

	intent := a.ToClusterIntent(db)

	if intent.Postgres.UID == nil || *intent.Postgres.UID != 26 {
		t.Errorf("Postgres.UID = %v, want 26", intent.Postgres.UID)
	}
	if intent.Postgres.GID == nil || *intent.Postgres.GID != 999 {
		t.Errorf("Postgres.GID = %v, want 999", intent.Postgres.GID)
	}
	if len(intent.Postgres.PostInitSQL) != 1 || intent.Postgres.PostInitSQL[0] != "SELECT 1" {
		t.Errorf("Postgres.PostInitSQL = %v, want [SELECT 1]", intent.Postgres.PostInitSQL)
	}
	if !intent.FeatureGates.IOUring {
		t.Error("FeatureGates.IOUring = false, want true")
	}
}

func TestToClusterIntentBootstrapRecovery(t *testing.T) {
	a := DocumentDBAdapter{}

	backup := &dbpreview.DocumentDB{Spec: dbpreview.DocumentDBSpec{
		Bootstrap: &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: "my-backup"},
			},
		},
	}}
	if r := a.ToClusterIntent(backup).Bootstrap.Recovery; r == nil || r.BackupName != "my-backup" {
		t.Errorf("Bootstrap.Recovery = %+v, want BackupName=my-backup", r)
	}

	pv := &dbpreview.DocumentDB{Spec: dbpreview.DocumentDBSpec{
		Bootstrap: &dbpreview.BootstrapConfiguration{
			Recovery: &dbpreview.RecoveryConfiguration{
				PersistentVolume: &dbpreview.PVRecoveryConfiguration{Name: "my-pv"},
			},
		},
	}}
	if r := a.ToClusterIntent(pv).Bootstrap.Recovery; r == nil || r.PersistentVolumeName != "my-pv" {
		t.Errorf("Bootstrap.Recovery = %+v, want PersistentVolumeName=my-pv", r)
	}

	none := &dbpreview.DocumentDB{Spec: dbpreview.DocumentDBSpec{}}
	if r := a.ToClusterIntent(none).Bootstrap.Recovery; r != nil {
		t.Errorf("Bootstrap.Recovery = %+v, want nil", r)
	}
}
