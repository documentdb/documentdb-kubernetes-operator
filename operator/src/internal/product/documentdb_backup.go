// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
)

// DocumentDBBackupAdapter maps the documentdb.io Backup custom resource onto
// the shared backup spec and status (engine.BackupAdapter).
type DocumentDBBackupAdapter struct{}

func (DocumentDBBackupAdapter) New() *dbv1.Backup          { return &dbv1.Backup{} }
func (DocumentDBBackupAdapter) NewList() client.ObjectList { return &dbv1.BackupList{} }
func (DocumentDBBackupAdapter) Spec(b *dbv1.Backup) *dbv1.BackupSpec {
	return &b.Spec
}
func (DocumentDBBackupAdapter) Status(b *dbv1.Backup) *dbv1.BackupStatus {
	return &b.Status
}

// Items returns pointers to the Backups of a BackupList.
func (DocumentDBBackupAdapter) Items(list client.ObjectList) []*dbv1.Backup {
	backupList, ok := list.(*dbv1.BackupList)
	if !ok {
		return nil
	}
	items := make([]*dbv1.Backup, 0, len(backupList.Items))
	for i := range backupList.Items {
		items = append(items, &backupList.Items[i])
	}
	return items
}

// DocumentDBScheduledBackupAdapter maps the documentdb.io ScheduledBackup
// custom resource onto the shared scheduled-backup spec and status
// (engine.ScheduledBackupAdapter).
type DocumentDBScheduledBackupAdapter struct{}

func (DocumentDBScheduledBackupAdapter) New() *dbv1.ScheduledBackup {
	return &dbv1.ScheduledBackup{}
}
func (DocumentDBScheduledBackupAdapter) Spec(s *dbv1.ScheduledBackup) *dbv1.ScheduledBackupSpec {
	return &s.Spec
}
func (DocumentDBScheduledBackupAdapter) Status(s *dbv1.ScheduledBackup) *dbv1.ScheduledBackupStatus {
	return &s.Status
}
