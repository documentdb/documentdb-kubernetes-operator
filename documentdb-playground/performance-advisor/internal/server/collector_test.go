// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package server

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/testutil"
)

const collectorImage = "otel/opentelemetry-collector-contrib@sha256:0fba96233274f6d665ac8831ad99dfe6479a9a20459f6e2719c0d20945773b46"

func TestRealCollector(t *testing.T) {
	if os.Getenv("RUN_COLLECTOR_TEST") != "1" {
		t.Skip("set RUN_COLLECTOR_TEST=1 inside the devcontainer to run the pinned real Collector")
	}
	captureServer := startCapture(t)
	temporary := t.TempDir()
	portReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	collectorAddress := portReservation.Addr().String()
	if err := portReservation.Close(); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`receivers:
  otlp:
    protocols:
      grpc:
        endpoint: %s
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 64
    spike_limit_mib: 16
  resource:
    attributes:
      - {key: documentdb.cluster, value: %s, action: insert}
      - {key: k8s.namespace.name, value: %s, action: insert}
  batch:
    timeout: 100ms
    send_batch_size: 32
    send_batch_max_size: 32
exporters:
  otlp:
    endpoint: %s
    tls:
      insecure: true
    sending_queue:
      queue_size: 4
      num_consumers: 1
    retry_on_failure:
      initial_interval: 200ms
      max_interval: 1s
      max_elapsed_time: 5s
service:
  telemetry:
    metrics:
      level: none
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, resource, batch]
      exporters: [otlp]
    traces:
      receivers: [otlp]
      processors: [memory_limiter, resource, batch]
      exporters: [otlp]
`, collectorAddress, testutil.Cluster, testutil.Namespace, captureServer.otlp)
	configPath := filepath.Join(temporary, "collector.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(temporary, "collector.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	name := fmt.Sprintf("live-telemetry-collector-%d", time.Now().UnixNano())
	command := exec.CommandContext(ctx, "docker", "run", "--name", name,
		"--network", "host", "--memory", "128m", "--cpus", "1", "--read-only",
		"--mount", "type=bind,src="+configPath+",dst=/etc/otelcol-contrib/config.yaml,readonly",
		collectorImage, "--config=/etc/otelcol-contrib/config.yaml")
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if output, err := exec.CommandContext(cleanupCtx, "docker", "rm", "--force", name).CombinedOutput(); err != nil {
			t.Errorf("remove test Collector %s: %v: %s", name, err, output)
		}
		cancel()
	})
	conn, err := grpc.NewClient(collectorAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	metricsClient, tracesClient := collectormetrics.NewMetricsServiceClient(conn), collectortrace.NewTraceServiceClient(conn)
	readyBy := time.Now().Add(20 * time.Second)
	for {
		probeCtx, probeCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		_, probeErr := metricsClient.Export(probeCtx, &collectormetrics.ExportMetricsServiceRequest{})
		probeCancel()
		if probeErr == nil {
			break
		}
		select {
		case exitErr := <-exited:
			data, readErr := os.ReadFile(logPath)
			t.Fatalf("Collector exited before readiness: %v; log read=%v\n%s", exitErr, readErr, data)
		default:
		}
		if time.Now().After(readyBy) {
			data, readErr := os.ReadFile(logPath)
			t.Fatalf("Collector did not become responsive: %v; log read=%v\n%s", probeErr, readErr, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	now := time.Now().Add(-time.Millisecond)
	metrics := testutil.Metrics("test.requests", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		testutil.Number(now.Add(-time.Second), now, 7))
	root, child := testutil.Span(now, 51, 1, 0), testutil.Span(now, 51, 2, 1)
	traces := testutil.Traces(root, child)
	isDeploymentAttribute := func(kv *commonpb.KeyValue) bool {
		return kv.Key == "documentdb.cluster" || kv.Key == "k8s.namespace.name"
	}
	metrics.ResourceMetrics[0].Resource.Attributes = slices.DeleteFunc(metrics.ResourceMetrics[0].Resource.Attributes, isDeploymentAttribute)
	traces.ResourceSpans[0].Resource.Attributes = slices.DeleteFunc(traces.ResourceSpans[0].Resource.Attributes, isDeploymentAttribute)
	if _, err := metricsClient.Export(ctx, metrics); err != nil {
		t.Fatal(err)
	}
	if _, err := tracesClient.Export(ctx, traces); err != nil {
		t.Fatal(err)
	}
	deliveredBy := time.Now().Add(10 * time.Second)
	for {
		reply := readTool(t, captureServer.session, "get_capture_status", nil)
		if reply.Status.Points == 1 && reply.Status.Spans == 2 {
			break
		}
		if time.Now().After(deliveredBy) {
			data, readErr := os.ReadFile(logPath)
			t.Fatalf("Collector failed to deliver both signals: %+v; log read=%v\n%s", reply.Status, readErr, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	reply := readTool(t, captureServer.session, "get_metric_window", map[string]any{"metric": "test.requests"})
	if len(reply.Points) != 1 || reply.Points[0].Source.Resource["documentdb.cluster"] != testutil.Cluster ||
		reply.Points[0].Source.Resource["k8s.namespace.name"] != testutil.Namespace {
		t.Fatalf("Collector enrichment not preserved: %+v", reply)
	}
	reply = readTool(t, captureServer.session, "get_trace", map[string]any{"trace_id": hex.EncodeToString(root.TraceId)})
	if len(reply.Traces) != 1 || len(reply.Traces[0].Spans) != 2 || !reply.Traces[0].RootObserved {
		t.Fatalf("Collector trace relationships not preserved: %+v", reply)
	}
	t.Logf("Collector %s delivered a delta sum and two related spans through a real MCP client", collectorImage)
}
