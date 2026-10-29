// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package portforward

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
)

func TestGatewayServiceName(t *testing.T) {
	cases := []struct {
		name string
		dd   *previewv1.DocumentDB
		want string
	}{
		{"nil", nil, ""},
		{
			"short",
			&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "my-dd"}},
			"documentdb-service-my-dd",
		},
		{
			"exactly 63",
			&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("x", 44)}},
			// 19 (prefix) + 44 xs = 63: unchanged
			"documentdb-service-" + strings.Repeat("x", 44),
		},
		{
			// Same vector as the operator's TestGatewayServiceNameHashesOnlyLongNames.
			"hashed",
			&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("a", 44) + "-east"}},
			"documentdb-service-" + strings.Repeat("a", 35) + "-2f5a7991",
		},
		{
			"recorded legacy name",
			&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{
				Name:        strings.Repeat("a", 44) + "x-east",
				Annotations: map[string]string{ServiceNameAnnotation: "documentdb-service-" + strings.Repeat("a", 44)},
			}},
			"documentdb-service-" + strings.Repeat("a", 44),
		},
		{
			"unrecognized recorded name is ignored",
			&previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{
				Name:        strings.Repeat("a", 44) + "-east",
				Annotations: map[string]string{ServiceNameAnnotation: "documentdb-service-other"},
			}},
			"documentdb-service-" + strings.Repeat("a", 35) + "-2f5a7991",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GatewayServiceName(tc.dd)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
			if len(got) > 63 {
				t.Errorf("name exceeds 63 chars: %d", len(got))
			}
		})
	}
}

func TestGatewayPort(t *testing.T) {
	if GatewayPort != 10260 {
		t.Errorf("GatewayPort drift: got %d want 10260 (see operator/src/internal/utils/constants.go)", GatewayPort)
	}
}

func TestOpenWithErr_NilEnv(t *testing.T) {
	t.Parallel()
	stop, err := OpenWithErr(nil, nil, &previewv1.DocumentDB{ObjectMeta: metav1.ObjectMeta{Name: "x"}}, 0) //nolint:staticcheck // testing nil-ctx/env guard
	if err == nil {
		t.Fatal("expected error for nil env")
	}
	if stop != nil {
		t.Fatal("expected nil stop when error is returned")
	}
	if !strings.Contains(err.Error(), "env") {
		t.Fatalf("error should mention env: %v", err)
	}
}

func TestOpenWithErr_NilDD(t *testing.T) {
	t.Parallel()
	// Open is a wrapper around OpenWithErr; exercise the backward-
	// compat shim's validation path in the same package without
	// needing a real *environment.TestingEnvironment.
	stop, err := Open(nil, nil, nil, 0) //nolint:staticcheck // testing nil-guard
	if err == nil {
		t.Fatal("expected error for nil env/dd")
	}
	if stop != nil {
		t.Fatal("expected nil stop when error is returned")
	}
}
