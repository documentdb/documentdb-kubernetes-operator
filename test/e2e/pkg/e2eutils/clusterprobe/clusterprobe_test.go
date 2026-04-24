package clusterprobe

import (
	"context"
	"errors"
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func schemeWithSnapshots(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	if err := snapshotv1.AddToScheme(s); err != nil {
		t.Fatalf("add snapshotv1 scheme: %v", err)
	}
	return s
}

func schemeWithoutSnapshots(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	return s
}

func TestHasVolumeSnapshotCRD(t *testing.T) {
	t.Run("scheme lacks VolumeSnapshot returns false", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(schemeWithoutSnapshots(t)).Build()
		ok, err := HasVolumeSnapshotCRD(context.Background(), c)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatalf("want false, got true")
		}
	})
	t.Run("scheme has VolumeSnapshot returns true", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(schemeWithSnapshots(t)).Build()
		ok, err := HasVolumeSnapshotCRD(context.Background(), c)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok {
			t.Fatalf("want true, got false")
		}
	})
	t.Run("nil client is an error", func(t *testing.T) {
		_, err := HasVolumeSnapshotCRD(context.Background(), nil)
		if err == nil {
			t.Fatalf("want error, got nil")
		}
	})
}

func TestHasUsableSnapshotClass(t *testing.T) {
	t.Run("CRD missing returns false", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(schemeWithoutSnapshots(t)).Build()
		ok, err := HasUsableSnapshotClass(context.Background(), c)
		if err != nil || ok {
			t.Fatalf("want (false, nil), got (%v, %v)", ok, err)
		}
	})
	t.Run("no classes returns false", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(schemeWithSnapshots(t)).Build()
		ok, err := HasUsableSnapshotClass(context.Background(), c)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatalf("want false, got true")
		}
	})
	t.Run("at least one class returns true", func(t *testing.T) {
		vsc := &snapshotv1.VolumeSnapshotClass{
			ObjectMeta: metav1.ObjectMeta{Name: "csi-hostpath-snapclass"},
			Driver:     "hostpath.csi.k8s.io",
		}
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(vsc).
			Build()
		ok, err := HasUsableSnapshotClass(context.Background(), c)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok {
			t.Fatalf("want true, got false")
		}
	})
}

func boolPtr(b bool) *bool { return &b }

func TestStorageClassAllowsExpansion(t *testing.T) {
	mk := func(name string, allow *bool, annotations map[string]string) *storagev1.StorageClass {
		return &storagev1.StorageClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Annotations: annotations,
			},
			Provisioner:          "kubernetes.io/host-path",
			AllowVolumeExpansion: allow,
		}
	}

	t.Run("named class with expansion true", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(mk("csi-hostpath-sc", boolPtr(true), nil)).
			Build()
		ok, err := StorageClassAllowsExpansion(context.Background(), c, "csi-hostpath-sc")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok {
			t.Fatalf("want true, got false")
		}
	})
	t.Run("named class with expansion nil (default false)", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(mk("standard", nil, nil)).
			Build()
		ok, err := StorageClassAllowsExpansion(context.Background(), c, "standard")
		if err != nil || ok {
			t.Fatalf("want (false, nil), got (%v, %v)", ok, err)
		}
	})
	t.Run("named class with expansion false", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(mk("standard", boolPtr(false), nil)).
			Build()
		ok, _ := StorageClassAllowsExpansion(context.Background(), c, "standard")
		if ok {
			t.Fatalf("want false, got true")
		}
	})
	t.Run("missing named class returns false nil", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(schemeWithSnapshots(t)).Build()
		ok, err := StorageClassAllowsExpansion(context.Background(), c, "does-not-exist")
		if err != nil || ok {
			t.Fatalf("want (false, nil), got (%v, %v)", ok, err)
		}
	})
	t.Run("empty name resolves default via GA annotation", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(
				mk("other", boolPtr(false), nil),
				mk("standard", boolPtr(true), map[string]string{
					DefaultStorageClassAnnotation: "true",
				}),
			).
			Build()
		ok, err := StorageClassAllowsExpansion(context.Background(), c, "")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok {
			t.Fatalf("want true for default class, got false")
		}
	})
	t.Run("empty name honours legacy beta default annotation", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(
				mk("legacy", boolPtr(true), map[string]string{
					"storageclass.beta.kubernetes.io/is-default-class": "true",
				}),
			).
			Build()
		ok, err := StorageClassAllowsExpansion(context.Background(), c, "")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok {
			t.Fatalf("want true for legacy default, got false")
		}
	})
	t.Run("empty name with no default class returns false", func(t *testing.T) {
		c := fake.NewClientBuilder().
			WithScheme(schemeWithSnapshots(t)).
			WithObjects(mk("other", boolPtr(true), nil)).
			Build()
		ok, err := StorageClassAllowsExpansion(context.Background(), c, "")
		if err != nil || ok {
			t.Fatalf("want (false, nil), got (%v, %v)", ok, err)
		}
	})
	t.Run("nil client is an error", func(t *testing.T) {
		_, err := StorageClassAllowsExpansion(context.Background(), nil, "anything")
		if err == nil {
			t.Fatalf("want error, got nil")
		}
	})
}

func TestIsMissingKindErrSmoke(t *testing.T) {
	if isMissingKindErr(nil) {
		t.Fatalf("nil err should not be missing")
	}
	if isMissingKindErr(errors.New("boom")) {
		t.Fatalf("arbitrary error should not be missing")
	}
}
