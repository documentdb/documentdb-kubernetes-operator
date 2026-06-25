// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package journal

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJournal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Journal Suite")
}
