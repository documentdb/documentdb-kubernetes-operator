// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package monitor

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMonitor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Monitor Suite")
}
