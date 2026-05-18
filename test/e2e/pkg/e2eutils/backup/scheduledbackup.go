// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	utilslabels "github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

// ScheduledBackupVars names the variables substituted into
// scheduled_backup.yaml.template.
type ScheduledBackupVars struct {
	Name          string
	Namespace     string
	ClusterName   string
	Schedule      string
	RetentionDays int
}

func (v ScheduledBackupVars) toMap() map[string]string {
	rd := v.RetentionDays
	if rd == 0 {
		rd = 7
	}
	schedule := v.Schedule
	if schedule == "" {
		schedule = "*/1 * * * *"
	}
	return map[string]string{
		"NAME":           v.Name,
		"NAMESPACE":      v.Namespace,
		"CLUSTER_NAME":   v.ClusterName,
		"SCHEDULE":       schedule,
		"RETENTION_DAYS": fmt.Sprintf("%d", rd),
	}
}

// RenderScheduled loads manifests/backup/scheduled_backup.yaml.template,
// substitutes vars, and unmarshals into a preview ScheduledBackup.
func RenderScheduled(vars ScheduledBackupVars) (*previewv1.ScheduledBackup, error) {
	if vars.Name == "" || vars.Namespace == "" || vars.ClusterName == "" {
		return nil, errors.New("backup.RenderScheduled: Name, Namespace and ClusterName are required")
	}
	raw, err := renderTemplate("scheduled_backup.yaml.template", vars.toMap())
	if err != nil {
		return nil, err
	}
	obj := &previewv1.ScheduledBackup{}
	if err := yaml.Unmarshal(raw, obj); err != nil {
		return nil, fmt.Errorf("unmarshal ScheduledBackup YAML: %w", err)
	}
	if obj.Name == "" {
		obj.Name = vars.Name
	}
	if obj.Namespace == "" {
		obj.Namespace = vars.Namespace
	}
	return obj, nil
}

// CreateScheduled renders and applies a ScheduledBackup CR.
func CreateScheduled(ctx context.Context, c client.Client, vars ScheduledBackupVars) (*previewv1.ScheduledBackup, error) {
	if c == nil {
		return nil, errors.New("backup.CreateScheduled: client must not be nil")
	}
	obj, err := RenderScheduled(vars)
	if err != nil {
		return nil, err
	}
	if err := c.Create(ctx, obj); err != nil {
		return nil, fmt.Errorf("creating ScheduledBackup %s/%s: %w", obj.Namespace, obj.Name, err)
	}
	return obj, nil
}

// GetScheduled fetches a ScheduledBackup by key.
func GetScheduled(ctx context.Context, c client.Client, ns, name string) (*previewv1.ScheduledBackup, error) {
	var s previewv1.ScheduledBackup
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &s); err != nil {
		return nil, fmt.Errorf("get ScheduledBackup %s/%s: %w", ns, name, err)
	}
	return &s, nil
}

// DeleteScheduled issues a foreground delete on the ScheduledBackup CR
// and returns when the object is gone or timeout elapses.
func DeleteScheduled(ctx context.Context, c client.Client, ns, name string, timeout time.Duration) error {
	obj := &previewv1.ScheduledBackup{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get ScheduledBackup %s/%s: %w", ns, name, err)
	}
	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete ScheduledBackup %s/%s: %w", ns, name, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for ScheduledBackup %s/%s to delete", timeout, ns, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}

// ListChildBackups returns every Backup CR in ns whose spec points at
// the supplied cluster name. This covers both the CRs created directly
// by the ScheduledBackup reconciler and any ad-hoc Backup objects the
// test itself wrote against the same cluster.
func ListChildBackups(ctx context.Context, c client.Client, ns, clusterName string) ([]previewv1.Backup, error) {
	var list previewv1.BackupList
	if err := c.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list Backups in %s: %w", ns, err)
	}
	out := make([]previewv1.Backup, 0, len(list.Items))
	for _, b := range list.Items {
		if b.Spec.Cluster.Name == clusterName {
			out = append(out, b)
		}
	}
	return out, nil
}

// WaitForFirstChildCompleted polls until at least one child Backup CR
// for clusterName has phase "completed". Returns the first such
// completed Backup (as observed).
func WaitForFirstChildCompleted(ctx context.Context, c client.Client, ns, clusterName string, timeout time.Duration) (*previewv1.Backup, error) {
	if c == nil {
		return nil, errors.New("backup.WaitForFirstChildCompleted: client must not be nil")
	}
	deadline := time.Now().Add(timeout)
	var observed int
	for {
		children, err := ListChildBackups(ctx, c, ns, clusterName)
		if err != nil {
			return nil, err
		}
		observed = len(children)
		for i := range children {
			if IsCompleted(&children[i]) {
				return &children[i], nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for a completed child Backup of cluster %s/%s (observed %d child backups, none completed)",
				timeout, ns, clusterName, observed)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}

// ClusterLabelSelector returns a controller-runtime ListOption that
// selects child objects (Backup, VolumeSnapshot, …) CNPG tags with the
// cluster name. Exposed for callers that want to list raw cnpgv1.Backup
// or snapshot resources by cluster.
func ClusterLabelSelector(clusterName string) client.MatchingLabels {
	return client.MatchingLabels{utilslabels.ClusterLabelName: clusterName}
}

// BackupNameLabelSelector returns the label selector CNPG stamps on the
// VolumeSnapshot(s) produced for a given cnpg.Backup name.
func BackupNameLabelSelector(backupName string) client.MatchingLabels {
	return client.MatchingLabels{utilslabels.BackupNameLabelName: backupName}
}

// assertBackupPhase is a shared guard used in tests; the cnpgv1 import
// is only needed for its BackupPhase string constants.
var _ cnpgv1.BackupPhase = cnpgv1.BackupPhaseCompleted
