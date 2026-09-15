// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package testutil builds synthetic generated OTLP messages for protocol tests.
package testutil

import (
	"encoding/binary"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const Namespace = "telemetry-test"
const Cluster = "telemetry-db"

// Attribute creates a string attribute, including redaction-test inputs.
func Attribute(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// Resource identifies a synthetic source with a stable instance lifetime.
func Resource() *resourcepb.Resource {
	return &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
		Attribute("k8s.namespace.name", Namespace), Attribute("documentdb.cluster", Cluster),
		Attribute("service.name", "synthetic-gateway"), Attribute("service.instance.id", "instance-1"),
		Attribute("k8s.pod.name", "telemetry-db-1"),
	}}
}

// Number creates an integer observation covering the supplied interval.
func Number(start, end time.Time, value int64) *metricspb.NumberDataPoint {
	point := &metricspb.NumberDataPoint{
		TimeUnixNano: uint64(end.UnixNano()), Value: &metricspb.NumberDataPoint_AsInt{AsInt: value},
		Attributes: []*commonpb.KeyValue{Attribute("db.operation.name", "find")},
	}
	if !start.IsZero() {
		point.StartTimeUnixNano = uint64(start.UnixNano())
	}
	return point
}

// Metrics creates a monotonic sum batch with the requested temporality.
func Metrics(name string, temporality metricspb.AggregationTemporality, points ...*metricspb.NumberDataPoint) *collectormetrics.ExportMetricsServiceRequest {
	return &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: Resource(), ScopeMetrics: []*metricspb.ScopeMetrics{{
				Scope: &commonpb.InstrumentationScope{Name: "synthetic-workload", Version: "1.0.0"},
				Metrics: []*metricspb.Metric{{
					Name: name, Unit: "{operation}",
					Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
						AggregationTemporality: temporality, IsMonotonic: true, DataPoints: points,
					}},
				}},
			}},
		}},
	}
}

// Span creates a request or child span with reproducible nonzero IDs.
func Span(now time.Time, trace, id, parent uint64) *tracepb.Span {
	span := &tracepb.Span{
		TraceId: make([]byte, 16), SpanId: make([]byte, 8),
		Name: "synthetic.request", Kind: tracepb.Span_SPAN_KIND_SERVER,
		StartTimeUnixNano: uint64(now.Add(-time.Second).UnixNano()), EndTimeUnixNano: uint64(now.UnixNano()),
		Attributes: []*commonpb.KeyValue{Attribute("db.operation.name", "find")}, Flags: 1,
	}
	binary.BigEndian.PutUint64(span.TraceId[8:], trace)
	binary.BigEndian.PutUint64(span.SpanId, id)
	if parent != 0 {
		span.ParentSpanId = make([]byte, 8)
		binary.BigEndian.PutUint64(span.ParentSpanId, parent)
	}
	return span
}

// Traces wraps synthetic spans in the same resource used by Metrics.
func Traces(spans ...*tracepb.Span) *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: Resource(), ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &commonpb.InstrumentationScope{Name: "synthetic-workload", Version: "1.0.0"}, Spans: spans,
		}},
	}}}
}
