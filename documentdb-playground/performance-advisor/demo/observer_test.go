// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package demo

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/capture"
	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/testutil"
)

func startObserver(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, dir, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "demo-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	return session
}

func callObserver(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || result.IsError || len(result.Content) != 2 {
		t.Fatalf("observer tool %s: %v %+v", name, err, result)
	}
	return result
}

func gatewayResource() []*commonpb.KeyValue {
	return []*commonpb.KeyValue{
		testutil.Attribute("k8s.namespace.name", Namespace),
		testutil.Attribute("documentdb.cluster", Cluster),
		testutil.Attribute("service.name", "documentdb_gateway"),
		testutil.Attribute("service.instance.id", "demo-test-instance"),
	}
}

func TestAdapterUsesRealMCPReadsAndNeutralMetadata(t *testing.T) {
	dir, run, store := preparedRun(t)
	gate := coordinator(t, dir, run.CaptureID)
	session := startObserver(t, dir)
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 4 {
		t.Fatalf("expected exactly four tools: %v %+v", err, tools)
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("adapter lost read-only annotation: %+v", tool)
		}
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_recent_traces", Arguments: map[string]any{"limit": 3},
	})
	if err == nil && !result.IsError {
		t.Fatal("observation before initial status was permitted")
	}
	callObserver(t, session, "get_capture_status", nil)
	if err := gate.WaitReady(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := gate.BeginTrial("trial-1", now.Add(-50*time.Second)); err != nil {
		t.Fatal(err)
	}
	metrics := testutil.Metrics("db.client.operations", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		testutil.Number(now.Add(-30*time.Second), now.Add(-10*time.Second), 7))
	metrics.ResourceMetrics[0].Resource.Attributes = gatewayResource()
	if outcome, err := store.AddMetrics(metrics); err != nil || outcome.Accepted != 1 {
		t.Fatalf("seed metrics: %v %+v", err, outcome)
	}
	span := testutil.Span(now.Add(-5*time.Second), 1, 1, 0)
	span.Name = "gateway.request"
	traces := testutil.Traces(span)
	traces.ResourceSpans[0].Resource.Attributes = gatewayResource()
	if outcome, err := store.AddTraces(traces); err != nil || outcome.Accepted != 1 {
		t.Fatalf("seed trace: %v %+v", err, outcome)
	}
	callObserver(t, session, "get_metric_window", map[string]any{"metric": "db.client.operations", "limit": 10})
	callObserver(t, session, "get_recent_traces", map[string]any{"limit": 3})
	proof, err := readProof(dir, run)
	if err != nil || len(proof.Observations) != 1 {
		t.Fatalf("missing proof: %v %+v", err, proof)
	}
	if proof.Observations[0].MetricRead.IsZero() || !proof.Observations[0].TraceRead.IsZero() {
		t.Fatalf("trace listing must not substitute for single-trace detail: %+v", proof)
	}
	detail := callObserver(t, session, "get_trace", map[string]any{"trace_id": hex.EncodeToString(span.TraceId)})
	proof, err = readProof(dir, run)
	if err != nil || proof.Observations[0].TraceRead.IsZero() || proof.Observations[0].MetricValue != "7" {
		t.Fatalf("detailed read was not recorded: %v %+v", err, proof)
	}
	var captureReply capture.Reply
	if err := json.Unmarshal([]byte(detail.Content[0].(*mcp.TextContent).Text), &captureReply); err != nil {
		t.Fatal(err)
	}
	if captureReply.Traces[0].Complete {
		t.Fatal("adapter asserted trace completeness")
	}
	if err := SaveReport(dir, filepath.Join(dir, "report.json")); err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := readJSON(filepath.Join(dir, "report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if report.Completed {
		t.Fatal("an unfinished run was presented as a completed demo")
	}
}

func TestInvalidObservationsDoNotSatisfyLiveReadGate(t *testing.T) {
	now := time.Now()
	trial := &Trial{ID: "trial-1", Start: now.Add(-30 * time.Second), Deadline: now.Add(30 * time.Second)}
	start := now.Add(-10 * time.Second)
	value := int64(7)
	for _, name := range []string{"crossing-interval", "health-gauge", "zero", "wrong-source", "future", "old-trace", "list-only"} {
		t.Run(name, func(t *testing.T) {
			o := &observer{}
			point := capture.Point{
				Name: "db.client.operations", Kind: "sum", Temporality: "delta",
				Source:     capture.Source{Resource: capture.Attributes{"service.name": "documentdb_gateway"}},
				Attributes: capture.Attributes{"db.operation.name": "Find"},
				Start:      &start, Time: now.Add(-time.Second), Arrival: now, Number: &capture.Number{Int: &value},
			}
			span := capture.Span{
				Name: "gateway.request", TraceID: "trace", SpanID: "span",
				Source: point.Source, Attributes: point.Attributes, Start: start, End: now.Add(-time.Second),
			}
			tool := "get_metric_window"
			switch name {
			case "crossing-interval":
				crossing := trial.Start.Add(-time.Second)
				point.Start = &crossing
			case "health-gauge":
				point.Name, point.Kind = "documentdb.postgres.up", "gauge"
			case "zero":
				zero := int64(0)
				point.Number = &capture.Number{Int: &zero}
			case "wrong-source":
				point.Source.Resource["service.name"] = "another-service"
			case "future":
				point.Time = now.Add(time.Second)
			case "old-trace":
				tool = "get_trace"
				span.Start = trial.Start.Add(-time.Second)
			case "list-only":
				tool = "get_recent_traces"
			}
			o.record(tool, trial, capture.Reply{
				Points: []capture.Point{point}, Traces: []capture.Trace{{ID: "trace", Spans: []capture.Span{span}}},
			}, now)
			observation := o.proof.Observations[0]
			if !observation.MetricRead.IsZero() || !observation.TraceRead.IsZero() {
				t.Fatalf("invalid observation passed: %+v", observation)
			}
		})
	}
}
