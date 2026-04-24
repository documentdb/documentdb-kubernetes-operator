// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package longhaul

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLongHaul(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Suite")
}
