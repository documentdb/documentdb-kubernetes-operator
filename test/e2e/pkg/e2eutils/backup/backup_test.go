// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := previewv1.AddToScheme(s); err != nil {
		t.Fatalf("previewv1.AddToScheme: %v", err)
	}
	if err := cnpgv1.AddToScheme(s); err != nil {
		t.Fatalf("cnpgv1.AddToScheme: %v", err)
	}
	if err := snapshotv1.AddToScheme(s); err != nil {
		t.Fatalf("snapshotv1.AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("corev1.AddToScheme: %v", err)
	}
	return s
}

func TestBackupVarsDefaultsRetention(t *testing.T) {
	t.Parallel()
	v := BackupVars{Name: "b", Namespace: "ns", ClusterName: "c"}
	m := v.toMap()
	if m["RETENTION_DAYS"] != "7" {
		t.Fatalf("expected default retention=7, got %s", m["RETENTION_DAYS"])
	}
	v.RetentionDays = 42
	if v.toMap()["RETENTION_DAYS"] != "42" {
		t.Fatalf("explicit retention not propagated: %q", v.toMap()["RETENTION_DAYS"])
	}
}

func TestRenderTemplateFromSubstitutesVars(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "b.tpl",
		"apiVersion: documentdb.io/preview\nkind: Backup\nmetadata:\n  name: ${NAME}\n  namespace: ${NAMESPACE}\nspec:\n  cluster:\n    name: ${CLUSTER_NAME}\n  retentionDays: ${RETENTION_DAYS}\n")
	raw, err := RenderTemplateFrom(path, map[string]string{
		"NAME": "on-demand", "NAMESPACE": "e2e",
		"CLUSTER_NAME": "c", "RETENTION_DAYS": "3",
	})
	if err != nil {
		t.Fatalf("RenderTemplateFrom: %v", err)
	}
	s := string(raw)
	for _, want := range []string{"name: on-demand", "namespace: e2e", "name: c", "retentionDays: 3"} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected %q in rendered output; got:\n%s", want, s)
		}
	}
}

func TestDecodeBackupFillsNameAndNamespace(t *testing.T) {
	t.Parallel()
	// Manifest omits metadata.name/namespace; decoder should inject them.
	raw := []byte("apiVersion: documentdb.io/preview\nkind: Backup\nspec:\n  cluster:\n    name: c\n")
	obj, err := decodeBackup(raw, "fallback-name", "fallback-ns")
	if err != nil {
		t.Fatalf("decodeBackup: %v", err)
	}
	if obj.Name != "fallback-name" || obj.Namespace != "fallback-ns" {
		t.Fatalf("fallback name/namespace not applied: got %s/%s", obj.Namespace, obj.Name)
	}
}

func TestIsCompletedAndTerminalFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		phase                  cnpgv1.BackupPhase
		wantDone, wantTerminal bool
	}{
		{"", false, false},
		{cnpgv1.BackupPhaseCompleted, true, false},
		{cnpgv1.BackupPhaseFailed, false, true},
		{previewv1.BackupPhaseSkipped, false, true},
		{cnpgv1.BackupPhaseRunning, false, false},
	}
	for _, tc := range cases {
		b := &previewv1.Backup{Status: previewv1.BackupStatus{Phase: tc.phase}}
		if got := IsCompleted(b); got != tc.wantDone {
			t.Errorf("IsCompleted(%q)=%v want %v", tc.phase, got, tc.wantDone)
		}
		if got := IsTerminalFailure(b); got != tc.wantTerminal {
			t.Errorf("IsTerminalFailure(%q)=%v want %v", tc.phase, got, tc.wantTerminal)
		}
	}
}

func TestWaitForCompletedReturnsWhenCompleted(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	ready := &previewv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		Spec:       previewv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "c"}},
		Status:     previewv1.BackupStatus{Phase: cnpgv1.BackupPhaseCompleted},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(ready).Build()
	got, err := WaitForCompleted(context.Background(), c, "ns", "b", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !IsCompleted(got) {
		t.Fatalf("expected completed, got %q", got.Status.Phase)
	}
}

func TestWaitForCompletedShortCircuitsOnFailure(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	failed := &previewv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		Status:     previewv1.BackupStatus{Phase: cnpgv1.BackupPhaseFailed, Message: "oh no"},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(failed).Build()
	_, err := WaitForCompleted(context.Background(), c, "ns", "b", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "terminal phase") {
		t.Fatalf("expected terminal-phase error, got %v", err)
	}
}

func TestListChildBackupsFiltersByClusterName(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	objs := []client.Object{
		&previewv1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"}, Spec: previewv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "c1"}}},
		&previewv1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "b2", Namespace: "ns"}, Spec: previewv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "c2"}}},
		&previewv1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "b3", Namespace: "ns"}, Spec: previewv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "c1"}}},
	}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	children, err := ListChildBackups(context.Background(), c, "ns", "c1")
	if err != nil {
		t.Fatalf("ListChildBackups: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("want 2 children, got %d", len(children))
	}
}

func TestIsSnapshotReady(t *testing.T) {
	t.Parallel()
	tru, fls := true, false
	if IsSnapshotReady(nil) {
		t.Error("nil snapshot must not be ready")
	}
	if IsSnapshotReady(&snapshotv1.VolumeSnapshot{}) {
		t.Error("snapshot with no status must not be ready")
	}
	if IsSnapshotReady(&snapshotv1.VolumeSnapshot{Status: &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &fls}}) {
		t.Error("ReadyToUse=false must not be ready")
	}
	if !IsSnapshotReady(&snapshotv1.VolumeSnapshot{Status: &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &tru}}) {
		t.Error("ReadyToUse=true must be ready")
	}
}

func TestPVBelongsToClusterMatchesLabelsOrClaimPrefix(t *testing.T) {
	t.Parallel()
	claimRefFor := func(ns, name string) *corev1.ObjectReference {
		return &corev1.ObjectReference{Namespace: ns, Name: name}
	}
	cases := []struct {
		name    string
		pv      *corev1.PersistentVolume
		cluster string
		want    bool
	}{
		{"nil", nil, "c", false},
		{
			name:    "cnpg label match",
			pv:      &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"cnpg.io/cluster": "c"}}},
			cluster: "c", want: true,
		},
		{
			name:    "documentdb label match",
			pv:      &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"documentdb.io/cluster": "c"}}},
			cluster: "c", want: true,
		},
		{
			name:    "claimRef exact prefix",
			pv:      &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{ClaimRef: claimRefFor("ns", "c-1")}},
			cluster: "c", want: true,
		},
		{
			name:    "unrelated",
			pv:      &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{ClaimRef: claimRefFor("ns", "other-1")}},
			cluster: "c", want: false,
		},
	}
	for _, tc := range cases {
		if got := pvBelongsToCluster(tc.pv, tc.cluster); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestWaitForPVCDeletedReturnsWhenMissing(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(s).Build()
	if err := WaitForPVCDeleted(context.Background(), c, "ns", "nope", 500*time.Millisecond); err != nil {
		t.Fatalf("expected nil for missing PVC, got %v", err)
	}
}

func TestWaitForPVCDeletedTimesOutWhenPresent(t *testing.T) {
	t.Parallel()
	s := newScheme(t)
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	c := fakeclient.NewClientBuilder().WithScheme(s).WithObjects(pvc).Build()
	err := WaitForPVCDeleted(context.Background(), c, "ns", "p", 500*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

// writeTempFile writes content to a file in dir and returns the absolute path.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}
