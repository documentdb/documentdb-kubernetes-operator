// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package mongo

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewFromURI", func() {
	It("rejects an empty URI", func() {
		c, err := NewFromURI(context.Background(), "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("uri is required"))
		Expect(c).To(BeNil())
	})

	It("returns a connected client for a syntactically valid URI without dialing", func() {
		// mongo-driver v2 Connect is lazy (no network until Ping/op),
		// so a well-formed URI to an unreachable host should still
		// succeed at this stage.
		c, err := NewFromURI(context.Background(), "mongodb://127.0.0.1:1/?directConnection=true")
		Expect(err).NotTo(HaveOccurred())
		Expect(c).NotTo(BeNil())
		Expect(c.Disconnect(context.Background())).To(Succeed())
	})

	It("rejects a malformed URI", func() {
		c, err := NewFromURI(context.Background(), "not-a-mongo-uri://oops")
		Expect(err).To(HaveOccurred())
		Expect(c).To(BeNil())
	})
})
