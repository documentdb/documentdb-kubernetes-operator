// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package backup provides CRUD and waiting helpers for the DocumentDB
// Backup / ScheduledBackup preview CRs and for the underlying
// VolumeSnapshot resources that CNPG creates beneath them.
//
// The package is deliberately framework-agnostic: it returns plain
// errors rather than calling into Ginkgo/Gomega, so unit tests can
// exercise it with a controller-runtime fake client. Spec code wraps
// these in gomega.Eventually where appropriate.
package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/tests/utils/envsubst"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

// DefaultPollInterval is the polling cadence used by the Wait* helpers.
const DefaultPollInterval = 5 * time.Second

// manifestsDir resolves the absolute path of test/e2e/manifests/backup
// regardless of the caller's working directory. Mirrors the pattern
// used by pkg/e2eutils/fixtures.
func manifestsDir() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed while locating backup manifests")
	}
	// this file lives at test/e2e/pkg/e2eutils/backup/<file>.go —
	// walk up four dirs to reach test/e2e, then descend into manifests/backup.
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "manifests", "backup"), nil
}

// renderTemplate envsubst-renders the file at path relative to
// manifestsDir() and returns the rendered bytes.
func renderTemplate(relPath string, vars map[string]string) ([]byte, error) {
	root, err := manifestsDir()
	if err != nil {
		return nil, err
	}
	return RenderTemplateFrom(filepath.Join(root, relPath), vars)
}

// RenderTemplateFrom envsubst-renders an absolute-path template with
// the supplied variables. Exposed for unit tests that write fixtures
// to a t.TempDir().
func RenderTemplateFrom(absPath string, vars map[string]string) ([]byte, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("reading template %s: %w", absPath, err)
	}
	rendered, err := envsubst.Envsubst(vars, data)
	if err != nil {
		return nil, fmt.Errorf("envsubst on %s: %w", absPath, err)
	}
	return rendered, nil
}

// BackupVars names the variables substituted into backup.yaml.template.
type BackupVars struct {
	Name          string
	Namespace     string
	ClusterName   string
	RetentionDays int
}

// toMap produces the envsubst map. RetentionDays defaults to 7 when
// unset so the rendered YAML parses as valid int.
func (v BackupVars) toMap() map[string]string {
	rd := v.RetentionDays
	if rd == 0 {
		rd = 7
	}
	return map[string]string{
		"NAME":           v.Name,
		"NAMESPACE":      v.Namespace,
		"CLUSTER_NAME":   v.ClusterName,
		"RETENTION_DAYS": fmt.Sprintf("%d", rd),
	}
}

// Render loads manifests/backup/backup.yaml.template, substitutes vars,
// and unmarshals into a preview Backup object. Name/Namespace are
// copied from vars when the rendered manifest omits them.
func Render(vars BackupVars) (*previewv1.Backup, error) {
	if vars.Name == "" || vars.Namespace == "" || vars.ClusterName == "" {
		return nil, errors.New("backup.Render: Name, Namespace and ClusterName are required")
	}
	raw, err := renderTemplate("backup.yaml.template", vars.toMap())
	if err != nil {
		return nil, err
	}
	return decodeBackup(raw, vars.Name, vars.Namespace)
}

// decodeBackup is exported indirectly via Render and the unit tests.
func decodeBackup(raw []byte, name, ns string) (*previewv1.Backup, error) {
	obj := &previewv1.Backup{}
	if err := yaml.Unmarshal(raw, obj); err != nil {
		return nil, fmt.Errorf("unmarshal Backup YAML: %w", err)
	}
	if obj.Name == "" {
		obj.Name = name
	}
	if obj.Namespace == "" {
		obj.Namespace = ns
	}
	return obj, nil
}

// Create renders the Backup CR and applies it via c.Create. The
// returned object reflects the in-cluster state after Create.
func Create(ctx context.Context, c client.Client, vars BackupVars) (*previewv1.Backup, error) {
	if c == nil {
		return nil, errors.New("backup.Create: client must not be nil")
	}
	obj, err := Render(vars)
	if err != nil {
		return nil, err
	}
	if err := c.Create(ctx, obj); err != nil {
		return nil, fmt.Errorf("creating Backup %s/%s: %w", obj.Namespace, obj.Name, err)
	}
	return obj, nil
}

// Get fetches a Backup by (namespace, name).
func Get(ctx context.Context, c client.Client, ns, name string) (*previewv1.Backup, error) {
	var b previewv1.Backup
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &b); err != nil {
		return nil, fmt.Errorf("get Backup %s/%s: %w", ns, name, err)
	}
	return &b, nil
}

// IsCompleted is the predicate used by WaitForCompleted. The backup
// controller writes the phase string verbatim from CNPG — the literal
// constant is "completed" (lowercase).
func IsCompleted(b *previewv1.Backup) bool {
	return b != nil && b.Status.Phase == cnpgv1.BackupPhaseCompleted
}

// IsTerminalFailure returns true when the backup has reached a phase
// from which no further reconcile loop will recover the object.
func IsTerminalFailure(b *previewv1.Backup) bool {
	if b == nil {
		return false
	}
	switch b.Status.Phase {
	case cnpgv1.BackupPhaseFailed:
		return true
	case previewv1.BackupPhaseSkipped:
		return true
	}
	return false
}

// WaitForCompleted polls the Backup CR until its phase is "completed"
// or timeout elapses. A terminal failure phase short-circuits with a
// descriptive error instead of consuming the full timeout.
func WaitForCompleted(ctx context.Context, c client.Client, ns, name string, timeout time.Duration) (*previewv1.Backup, error) {
	if c == nil {
		return nil, errors.New("backup.WaitForCompleted: client must not be nil")
	}
	deadline := time.Now().Add(timeout)
	var last previewv1.Backup
	for {
		err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &last)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("polling Backup %s/%s: %w", ns, name, err)
		}
		if err == nil {
			if IsCompleted(&last) {
				out := last
				return &out, nil
			}
			if IsTerminalFailure(&last) {
				return &last, fmt.Errorf("Backup %s/%s reached terminal phase %q: %s",
					ns, name, last.Status.Phase, last.Status.Message)
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for Backup %s/%s to complete (last phase=%q)",
				timeout, ns, name, last.Status.Phase)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}

// Delete issues a foreground delete on the Backup CR and returns when
// the object is gone or timeout elapses. Safe to call on a missing
// object (IsNotFound is treated as success).
func Delete(ctx context.Context, c client.Client, ns, name string, timeout time.Duration) error {
	obj := &previewv1.Backup{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get Backup %s/%s: %w", ns, name, err)
	}
	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete Backup %s/%s: %w", ns, name, err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, obj)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for Backup %s/%s to delete", timeout, ns, name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(DefaultPollInterval):
		}
	}
}
