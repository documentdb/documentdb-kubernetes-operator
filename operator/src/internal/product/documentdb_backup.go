// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package product

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
)

// DocumentDBBackupAdapter maps the documentdb.io Backup custom resource onto
// the shared backup spec and status (engine.BackupAdapter).
type DocumentDBBackupAdapter struct{}

func (DocumentDBBackupAdapter) New() *dbpreview.Backup     { return &dbpreview.Backup{} }
func (DocumentDBBackupAdapter) NewList() client.ObjectList { return &dbpreview.BackupList{} }
func (DocumentDBBackupAdapter) Spec(b *dbpreview.Backup) *dbpreview.BackupSpec {
	return &b.Spec
}
func (DocumentDBBackupAdapter) Status(b *dbpreview.Backup) *dbpreview.BackupStatus {
	return &b.Status
}

// Items returns pointers to the Backups of a BackupList.
func (DocumentDBBackupAdapter) Items(list client.ObjectList) []*dbpreview.Backup {
	backupList, ok := list.(*dbpreview.BackupList)
	if !ok {
		return nil
	}
	items := make([]*dbpreview.Backup, 0, len(backupList.Items))
	for i := range backupList.Items {
		items = append(items, &backupList.Items[i])
	}
	return items
}

// DocumentDBScheduledBackupAdapter maps the documentdb.io ScheduledBackup
// custom resource onto the shared scheduled-backup spec and status
// (engine.ScheduledBackupAdapter).
type DocumentDBScheduledBackupAdapter struct{}

func (DocumentDBScheduledBackupAdapter) New() *dbpreview.ScheduledBackup {
	return &dbpreview.ScheduledBackup{}
}
func (DocumentDBScheduledBackupAdapter) Spec(s *dbpreview.ScheduledBackup) *dbpreview.ScheduledBackupSpec {
	return &s.Spec
}
func (DocumentDBScheduledBackupAdapter) Status(s *dbpreview.ScheduledBackup) *dbpreview.ScheduledBackupStatus {
	return &s.Status
}
