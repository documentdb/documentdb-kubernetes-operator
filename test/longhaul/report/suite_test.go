// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestReport(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Report Suite")
}
