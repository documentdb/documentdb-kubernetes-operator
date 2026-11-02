// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dbpreview "github.com/documentdb/documentdb-operator/api/preview"
	"github.com/documentdb/documentdb-operator/internal/imagemode"
	"github.com/documentdb/documentdb-operator/internal/product"
)

func imageModeTestCluster() *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "ns"},
		Spec: cnpgv1.ClusterSpec{
			ImageName: "ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie",
			PostgresConfiguration: cnpgv1.PostgresConfiguration{
				Extensions: []cnpgv1.ExtensionConfiguration{{
					Name:              "documentdb",
					ImageVolumeSource: corev1.ImageVolumeSource{Reference: "documentdb:1.0.0"},
				}},
			},
		},
	}
}

func imageModeReconciler(detect func(context.Context, string) (imagemode.Mode, string, error)) *ClusterReconciler[*dbpreview.DocumentDB] {
	return &ClusterReconciler[*dbpreview.DocumentDB]{Adapter: product.DocumentDBAdapter{}, ImageModeDetector: detect}
}

var imageModeTestIntent = product.ClusterIntent{Images: product.Images{Combined: "documentdb-combined:18-1.0.0"}}

func TestSelectImageModeCombined(t *testing.T) {
	var gotNamespace string
	r := imageModeReconciler(func(_ context.Context, ns string) (imagemode.Mode, string, error) {
		gotNamespace = ns
		return imagemode.Combined, "containerd 1.7", nil
	})
	cluster := imageModeTestCluster()
	mode, reason, err := r.selectImageMode(context.Background(), cluster, imageModeTestIntent)
	if err != nil {
		t.Fatal(err)
	}
	if gotNamespace != "ns" || mode != imagemode.Combined || reason != "containerd 1.7" {
		t.Errorf("got namespace=%q mode=%q reason=%q", gotNamespace, mode, reason)
	}
	if cluster.Spec.ImageName != "documentdb-combined:18-1.0.0" || len(cluster.Spec.PostgresConfiguration.Extensions) != 0 {
		t.Errorf("cluster not rendered in combined mode: image=%q extensions=%d", cluster.Spec.ImageName, len(cluster.Spec.PostgresConfiguration.Extensions))
	}
	if cluster.Annotations[imagemode.AnnotationKey] != "combined" {
		t.Errorf("annotation = %q, want combined", cluster.Annotations[imagemode.AnnotationKey])
	}
}

func TestSelectImageModeImageVolume(t *testing.T) {
	r := imageModeReconciler(func(context.Context, string) (imagemode.Mode, string, error) {
		return imagemode.ImageVolume, "supported", nil
	})
	cluster := imageModeTestCluster()
	if _, _, err := r.selectImageMode(context.Background(), cluster, imageModeTestIntent); err != nil {
		t.Fatal(err)
	}
	if cluster.Spec.ImageName != "ghcr.io/cloudnative-pg/postgresql:18-minimal-trixie" || len(cluster.Spec.PostgresConfiguration.Extensions) != 1 {
		t.Errorf("ImageVolume rendering changed: image=%q extensions=%d", cluster.Spec.ImageName, len(cluster.Spec.PostgresConfiguration.Extensions))
	}
	if cluster.Annotations[imagemode.AnnotationKey] != "imageVolume" {
		t.Errorf("annotation = %q, want imageVolume", cluster.Annotations[imagemode.AnnotationKey])
	}
}

func TestSelectImageModeDefaultsToImageVolume(t *testing.T) {
	cluster := imageModeTestCluster()
	mode, _, err := imageModeReconciler(nil).selectImageMode(context.Background(), cluster, imageModeTestIntent)
	if err != nil {
		t.Fatal(err)
	}
	if mode != imagemode.ImageVolume || len(cluster.Spec.PostgresConfiguration.Extensions) != 1 {
		t.Errorf("mode=%q extensions=%d, want ImageVolume with 1 extension", mode, len(cluster.Spec.PostgresConfiguration.Extensions))
	}
}

func TestSelectImageModeLeavesClusterUntouchedOnError(t *testing.T) {
	for name, tc := range map[string]struct {
		detect  func(context.Context, string) (imagemode.Mode, string, error)
		intent  product.ClusterIntent
		wantErr string
	}{
		"detection fails": {
			detect: func(context.Context, string) (imagemode.Mode, string, error) {
				return "", "", fmt.Errorf("apiserver timeout")
			},
			intent:  imageModeTestIntent,
			wantErr: "apiserver timeout",
		},
		"product has no combined image": {
			detect: func(context.Context, string) (imagemode.Mode, string, error) {
				return imagemode.Combined, "containerd 1.7", nil
			},
			intent:  product.ClusterIntent{},
			wantErr: "defines no combined image",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cluster := imageModeTestCluster()
			_, _, err := imageModeReconciler(tc.detect).selectImageMode(context.Background(), cluster, tc.intent)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if _, ok := cluster.Annotations[imagemode.AnnotationKey]; ok || len(cluster.Spec.PostgresConfiguration.Extensions) != 1 {
				t.Errorf("cluster modified on error: annotations=%v extensions=%d", cluster.Annotations, len(cluster.Spec.PostgresConfiguration.Extensions))
			}
		})
	}
}
