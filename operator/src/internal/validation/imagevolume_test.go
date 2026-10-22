// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package validation

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestImageVolumeProbeProductDisplayNames(t *testing.T) {
	for _, product := range []string{"Cosmos DB", "Cosmos/DB (Preview)", strings.Repeat("Long Product ", 30)} {
		for _, behavior := range []string{"enabled", "rejected", "pruned"} {
			t.Run(product+"/"+behavior, func(t *testing.T) {
				scheme := runtime.NewScheme()
				if err := corev1.AddToScheme(scheme); err != nil {
					t.Fatal(err)
				}
				var creates int
				c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
					Create: func(_ context.Context, _ client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						creates++
						pod := obj.(*corev1.Pod)
						if errs := apivalidation.NameIsDNSSubdomain(pod.GenerateName, true); len(errs) != 0 {
							return fmt.Errorf("invalid metadata.generateName: %v", errs)
						}
						options := (&client.CreateOptions{}).ApplyOptions(opts)
						if len(options.DryRun) != 1 || options.DryRun[0] != "All" {
							t.Fatal("capability probe must be a server-side dry run")
						}
						for i := range pod.Spec.Volumes {
							if pod.Spec.Volumes[i].Image == nil {
								continue
							}
							switch behavior {
							case "rejected":
								return fmt.Errorf("ImageVolume feature disabled")
							case "pruned":
								pod.Spec.Volumes[i].Image = nil
							}
						}
						return nil
					},
				}).Build()
				var confirmed atomic.Bool
				probe := &ImageVolumeProbe{Client: c, Confirmed: &confirmed, Product: product, Log: logr.Discard()}
				err := probe.Ensure(context.Background(), "default")
				if behavior == "enabled" {
					if err != nil || !confirmed.Load() {
						t.Fatalf("expected supported feature to be cached, got error %v, cached %v", err, confirmed.Load())
					}
					if err := probe.Ensure(context.Background(), "default"); err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), "ImageVolume feature is not enabled") ||
					!strings.Contains(err.Error(), product) || confirmed.Load() {
					t.Fatalf("expected uncached capability rejection naming %q, got error %v, cached %v", product, err, confirmed.Load())
				}
				if creates != 2 {
					t.Fatalf("expected a valid baseline and image probe, got %d creates", creates)
				}
			})
		}
	}
}
