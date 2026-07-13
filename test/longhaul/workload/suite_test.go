// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package workload

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWorkload(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Workload Suite")
}
