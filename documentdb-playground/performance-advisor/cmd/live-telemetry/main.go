// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/capture"
	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/server"
)

func main() {
	namespace := flag.String("namespace", "", "Required synthetic test namespace")
	cluster := flag.String("cluster", "", "Required test deployment name")
	otlpAddress := flag.String("otlp-address", "127.0.0.1:4317", "OTLP/gRPC listener; use 0.0.0.0:4317 only in the test pod")
	mcpAddress := flag.String("mcp-address", "127.0.0.1:8080", "Loopback-only MCP Streamable HTTP listener")
	flag.Parse()
	if flag.NArg() != 0 {
		slog.Error("unexpected positional arguments")
		os.Exit(2)
	}
	store, err := capture.New(capture.DefaultConfig(*namespace, *cluster))
	if err != nil {
		slog.Error("invalid capture configuration", "error", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := server.Run(ctx, store, *otlpAddress, *mcpAddress); err != nil {
		slog.Error("capture stopped with an error", "error", err)
		os.Exit(1)
	}
}
