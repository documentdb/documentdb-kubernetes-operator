// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package k8s

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestK8sShared(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Shared Kubernetes Pod Helpers Suite")
}
