// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mongo

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMongoShared(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Shared Mongo Client Suite")
}
