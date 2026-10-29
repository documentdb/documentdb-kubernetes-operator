// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package preview

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestPreviewSchemaMatchesV1 guards the "strategy: None" CRD conversion: the
// API server only rewrites apiVersion between preview and v1, so any schema
// drift between the two packages would silently drop or reject fields.
func TestPreviewSchemaMatchesV1(t *testing.T) {
	crds, err := filepath.Glob(filepath.Join("..", "..", "config", "crd", "bases", "documentdb.io_*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(crds) == 0 {
		t.Fatal("no generated CRDs found; run `make manifests`")
	}

	for _, path := range crds {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var crd struct {
				Spec struct {
					Conversion *struct {
						Strategy string `json:"strategy"`
					} `json:"conversion"`
					Versions []map[string]any `json:"versions"`
				} `json:"spec"`
			}
			if err := yaml.Unmarshal(raw, &crd); err != nil {
				t.Fatal(err)
			}
			if c := crd.Spec.Conversion; c != nil && c.Strategy != "" && c.Strategy != "None" {
				t.Fatalf("conversion strategy = %q, want None", c.Strategy)
			}

			versions := map[string]map[string]any{}
			for _, v := range crd.Spec.Versions {
				versions[v["name"].(string)] = v
			}
			pv, v1 := versions["preview"], versions["v1"]
			if pv == nil || v1 == nil {
				t.Fatalf("want versions preview and v1, got %d versions", len(crd.Spec.Versions))
			}
			if pv["storage"] != false || v1["storage"] != true {
				t.Errorf("storage version must be v1 (preview=%v, v1=%v)", pv["storage"], v1["storage"])
			}
			if pv["deprecated"] != true {
				t.Error("preview must be marked deprecated")
			}
			for _, field := range []string{"schema", "subresources", "additionalPrinterColumns"} {
				if !reflect.DeepEqual(pv[field], v1[field]) {
					t.Errorf("%s differs between preview and v1; edit api/v1 only and run `make manifests`", field)
				}
			}
		})
	}
}
