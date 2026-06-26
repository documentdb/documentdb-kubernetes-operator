// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package operations

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOperations(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Operations Suite")
}
