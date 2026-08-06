// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package backup

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBackup(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Long Haul Backup Suite")
}
