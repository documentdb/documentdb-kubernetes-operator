// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package testenv

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	previewv1 "github.com/documentdb/documentdb-operator/api/preview"
	corev1 "k8s.io/api/core/v1"
)

func TestDefaultDocumentDBSchemeRegistersExpectedGroups(t *testing.T) {
	s, err := DefaultDocumentDBScheme()
	if err != nil {
		t.Fatalf("DefaultDocumentDBScheme: %v", err)
	}

	if !s.Recognizes(cnpgv1.SchemeGroupVersion.WithKind("Cluster")) {
		t.Errorf("expected scheme to recognize cnpg apiv1 Cluster")
	}
	if !s.Recognizes(previewv1.GroupVersion.WithKind("DocumentDB")) {
		t.Errorf("expected scheme to recognize DocumentDB preview group")
	}
	if !s.Recognizes(corev1.SchemeGroupVersion.WithKind("Pod")) {
		t.Errorf("expected scheme to recognize core/v1 Pod")
	}
}

func TestDefaultConstants(t *testing.T) {
	if DefaultOperatorNamespace == "" {
		t.Fatal("DefaultOperatorNamespace must not be empty")
	}
	if DefaultPostgresImage == "" {
		t.Fatal("DefaultPostgresImage must not be empty")
	}
}
