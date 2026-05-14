// Package mongo extras: NewFromURI is a convenience constructor for
// callers (e.g. the long-haul driver) that already hold a fully-formed
// mongodb:// URI from configuration and just need a connected client
// with the same default timeout NewClient applies.
package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewFromURI builds a connected *mongo.Client against an externally
// supplied mongodb:// URI. Connect time is bounded by
// DefaultConnectTimeout. The driver's Connect is lazy, so callers who
// need a post-connect round-trip should call Ping themselves.
//
// This helper exists so the long-haul driver can stop calling
// mongo.Connect(options.Client().ApplyURI(...)) directly, which would
// silently bypass the connect timeout that NewClient applies for
// every other caller.
func NewFromURI(_ context.Context, uri string) (*mongo.Client, error) {
	if uri == "" {
		return nil, errors.New("mongo: uri is required")
	}
	co := options.Client().ApplyURI(uri).SetConnectTimeout(DefaultConnectTimeout)
	c, err := mongo.Connect(co)
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}
	return c, nil
}
