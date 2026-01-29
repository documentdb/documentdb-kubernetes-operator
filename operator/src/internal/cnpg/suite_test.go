// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package cnpg

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCNPG(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CNPG Suite")
}
