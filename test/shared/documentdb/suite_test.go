// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package documentdb

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDocumentDBShared(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Shared DocumentDB CR Helpers Suite")
}
