// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/demo"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "live telemetry demo:", err)
		os.Exit(1)
	}
}

func run() error {
	action := flag.String("action", "observe", "prepare, observe, or report")
	dir := flag.String("dir", "", "Private directory for one demo run")
	endpoint := flag.String("endpoint", "", "Loopback MCP upstream, used only by prepare")
	output := flag.String("output", "", "Compact report path, used only by report")
	flag.Parse()
	if *dir == "" || flag.NArg() != 0 {
		return fmt.Errorf("a run directory is required; positional arguments are not supported")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch *action {
	case "prepare":
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return demo.Prepare(ctx, *dir, *endpoint)
	case "observe":
		return demo.Serve(ctx, *dir, &mcp.StdioTransport{MaxLineLength: 8 << 10})
	case "report":
		if *output == "" {
			return fmt.Errorf("report output is required")
		}
		return demo.SaveReport(*dir, *output)
	default:
		return fmt.Errorf("unknown action %q", *action)
	}
}
