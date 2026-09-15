// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/capture"
	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/testutil"
)

type testCapture struct {
	store   *capture.Store
	otlp    string
	http    string
	metrics collectormetrics.MetricsServiceClient
	traces  collectortrace.TraceServiceClient
	session *mcp.ClientSession
}

func startCapture(t *testing.T) *testCapture {
	t.Helper()
	store, err := capture.New(capture.DefaultConfig(testutil.Namespace, testutil.Cluster))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewGRPC(store)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-served; err != nil {
			t.Errorf("OTLP server stopped: %v", err)
		}
	})
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	httpServer := httptest.NewServer(NewHTTPHandler(store))
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "telemetry-protocol-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp", HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return &testCapture{
		store: store, otlp: listener.Addr().String(), http: httpServer.URL,
		metrics: collectormetrics.NewMetricsServiceClient(conn),
		traces:  collectortrace.NewTraceServiceClient(conn), session: session,
	}
}

func readTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) capture.Reply {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("tool %s failed: %+v", name, result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected tool content type: %T", result.Content[0])
	}
	var reply capture.Reply
	if err := json.Unmarshal([]byte(text.Text), &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestGeneratedOTLPThroughRealMCPClient(t *testing.T) {
	captureServer := startCapture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	tools, err := captureServer.session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 4 {
		t.Fatalf("expected exactly four tools: %+v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool is not declared closed and read-only: %+v", tool)
		}
	}
	now := time.Now().Add(-time.Millisecond)
	metrics := testutil.Metrics("test.requests", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		testutil.Number(now.Add(-time.Second), now, 7))
	response, err := captureServer.metrics.Export(ctx, metrics)
	if err != nil || response.PartialSuccess != nil {
		t.Fatalf("OTLP metrics export failed: %+v %v", response, err)
	}
	root, child := testutil.Span(now, 1, 1, 0), testutil.Span(now, 1, 2, 1)
	traceResponse, err := captureServer.traces.Export(ctx, testutil.Traces(root, child))
	if err != nil || traceResponse.PartialSuccess != nil {
		t.Fatalf("OTLP trace export failed: %+v %v", traceResponse, err)
	}
	reply := readTool(t, captureServer.session, "get_capture_status", nil)
	if reply.Status.Points != 1 || reply.Status.Spans != 2 ||
		reply.Namespace != testutil.Namespace || reply.Cluster != testutil.Cluster {
		t.Fatalf("unexpected capture status: %+v", reply)
	}
	reply = readTool(t, captureServer.session, "get_metric_window", map[string]any{"metric": "test.requests"})
	if len(reply.Points) != 1 || len(reply.Aggregates) != 1 ||
		reply.Aggregates[0].Value == nil || *reply.Aggregates[0].Value.Int != 7 {
		t.Fatalf("metric tool did not expose received value: %+v", reply)
	}
	reply = readTool(t, captureServer.session, "get_recent_traces", nil)
	if len(reply.Traces) != 1 || len(reply.Traces[0].Spans) != 2 || reply.Traces[0].Complete {
		t.Fatalf("trace tool did not preserve partial-capture semantics: %+v", reply)
	}
	reply = readTool(t, captureServer.session, "get_trace", map[string]any{"trace_id": hex.EncodeToString(root.TraceId)})
	if !reply.Traces[0].RootObserved || len(reply.Traces[0].MissingParents) != 0 {
		t.Fatalf("span relationships not preserved: %+v", reply)
	}
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 30 {
			if _, err := captureServer.metrics.Export(ctx, metrics); err != nil {
				t.Error(err)
				return
			}
		}
	})
	workers.Go(func() {
		for range 15 {
			readTool(t, captureServer.session, "get_capture_status", nil)
			readTool(t, captureServer.session, "get_recent_traces", nil)
		}
	})
	workers.Wait()
	if captureServer.store.CaptureStatus().Status.Counters.Metrics.Accepted != 31 {
		t.Fatal("concurrent tool calls interrupted ingestion")
	}
}

func TestPartialSuccessAndOversizedExports(t *testing.T) {
	captureServer := startCapture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	now := time.Now()
	request := testutil.Metrics("test.requests", 0, testutil.Number(now.Add(-time.Second), now, 1))
	response, err := captureServer.metrics.Export(ctx, request)
	if err != nil || response.PartialSuccess == nil || response.PartialSuccess.RejectedDataPoints != 1 ||
		!strings.Contains(response.PartialSuccess.ErrorMessage, "unsupported_temporality") {
		t.Fatalf("invalid points were not acknowledged with partial failure: %+v %v", response, err)
	}
	span := testutil.Span(now, 1, 1, 0)
	span.Events = []*tracepb.Span_Event{{Name: strings.Repeat("x", capture.MaxOTLPBytes)}}
	for _, options := range [][]grpc.CallOption{nil, {grpc.UseCompressor("gzip")}} {
		_, err = captureServer.traces.Export(ctx, testutil.Traces(span), options...)
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("oversized OTLP request was not rejected before admission: %v", err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for captureServer.store.CaptureStatus().Status.Counters.TransportErrors["ResourceExhausted"] != 2 {
		if time.Now().After(deadline) {
			t.Fatal("oversized transport rejection was not counted")
		}
		time.Sleep(time.Millisecond)
	}
	if captureServer.store.CaptureStatus().Status.Spans != 0 {
		t.Fatal("oversized span was retained")
	}
}

func TestLegacyBatchesCannotBypassReaderOrResponseLimits(t *testing.T) {
	captureServer := startCapture(t)
	body := `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capture_status","arguments":{}}},` +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_capture_status","arguments":{}}}]`
	for _, version := range []string{"", "2025-03-26", "2025-11-25"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, captureServer.http+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if version != "" {
			request.Header.Set("MCP-Protocol-Version", version)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusBadRequest ||
			!strings.Contains(string(data), "batches are not supported") {
			t.Fatalf("legacy batch reached SDK dispatch: version=%q status=%d err=%v", version, response.StatusCode, err)
		}
	}
}

func TestToolArgumentValidation(t *testing.T) {
	captureServer := startCapture(t)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_recent_traces", map[string]any{"limit": 21}},
		{"get_recent_traces", map[string]any{"lookback_seconds": 0}},
		{"get_recent_traces", map[string]any{"lookback_seconds": 301}},
		{"get_trace", map[string]any{"trace_id": "not-an-id"}},
		{"get_metric_window", map[string]any{"metric": "test.requests", "limit": -1}},
		{"get_metric_window", map[string]any{"metric": "arbitrary query expression"}},
		{"get_capture_status", map[string]any{"namespace": "other"}},
	} {
		result, err := captureServer.session.CallTool(t.Context(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid arguments accepted for %s: %+v", tc.name, tc.args)
		}
	}
}

func postMCP(t *testing.T, endpoint, host, origin, body string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if host != "" {
		request.Host = host
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, capture.MaxResponseBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestHTTPOriginHostAndBodyLimits(t *testing.T) {
	captureServer := startCapture(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	for _, tc := range []struct{ host, origin string }{
		{"untrusted.example", ""},
		{"", "https://untrusted.example"},
		{"", "null"},
	} {
		response, _ := postMCP(t, captureServer.http, tc.host, tc.origin, body)
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("unsafe HTTP origin/host accepted: %+v status=%d", tc, response.StatusCode)
		}
	}
	response, _ := postMCP(t, captureServer.http, "", "", body+strings.Repeat(" ", maxMCPRequestBytes))
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized MCP request not rejected: %d", response.StatusCode)
	}
	if err := Run(t.Context(), captureServer.store, "127.0.0.1:0", "0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback MCP listener accepted")
	}
}

func TestFullWireResponseBudgetAndTruncation(t *testing.T) {
	captureServer := startCapture(t)
	now := time.Now().Add(-time.Millisecond)
	for i := range 600 {
		point := testutil.Number(now.Add(-time.Second), now, int64(i))
		point.Attributes = append(point.Attributes, testutil.Attribute("error.type", strings.Repeat("a", 128)))
		request := testutil.Metrics("test.requests", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, point)
		request.ResourceMetrics[0].Resource.Attributes = append(request.ResourceMetrics[0].Resource.Attributes,
			testutil.Attribute("container.id", fmt.Sprintf("container-%d", i)))
		if _, err := captureServer.store.AddMetrics(request); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"jsonrpc":"2.0","id":"` + strings.Repeat("<", 7000) +
		`","method":"tools/call","params":{"name":"get_metric_window","arguments":{"metric":"test.requests","limit":1000}}}`
	response, data := postMCP(t, captureServer.http, "", "", body)
	if response.StatusCode != http.StatusOK || len(data) > capture.MaxResponseBytes {
		t.Fatalf("full JSON-RPC response exceeded budget: status=%d bytes=%d", response.StatusCode, len(data))
	}
	var envelope struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Result.IsError || len(envelope.Result.Content) != 1 {
		t.Fatalf("bounded result was not successful: %s", data[:min(len(data), 500)])
	}
	var reply capture.Reply
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &reply); err != nil {
		t.Fatal(err)
	}
	if !reply.Truncated || reply.Omitted == 0 || len(reply.Points) == 0 {
		t.Fatalf("response truncation was not explicit or useful: %+v", reply)
	}
}

func TestSlowReadersHaveNoUnboundedQueue(t *testing.T) {
	captureServer := startCapture(t)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	slow := httptest.NewServer(protectHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	})))
	defer slow.Close()
	var requests sync.WaitGroup
	for range 2 {
		requests.Go(func() {
			response, err := http.Post(slow.URL+"/mcp", "application/json", bytes.NewBufferString("{}"))
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
		})
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("slow requests did not enter")
		}
	}
	response, _ := postMCP(t, slow.URL, "", "", "{}")
	if response.StatusCode != http.StatusTooManyRequests {
		t.Errorf("excess reader was queued: %d", response.StatusCode)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	now := time.Now()
	_, err := captureServer.metrics.Export(ctx, testutil.Metrics("test.requests",
		metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		testutil.Number(now.Add(-time.Second), now, 1)))
	if err != nil {
		t.Errorf("slow readers blocked OTLP ingestion: %v", err)
	}
	close(release)
	requests.Wait()
}
