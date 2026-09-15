// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package capture

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/testutil"
)

var testNow = time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)

const cumulative = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
const delta = metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA

func newStore(t *testing.T, configure func(*Config)) *Store {
	t.Helper()
	cfg := DefaultConfig(testutil.Namespace, testutil.Cluster)
	if configure != nil {
		configure(&cfg)
	}
	store, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return testNow }
	store.started = testNow.Add(-time.Minute)
	return store
}

func admitMetrics(t *testing.T, store *Store, request *collectormetrics.ExportMetricsServiceRequest) {
	t.Helper()
	out, err := store.AddMetrics(request)
	if err != nil || out.Rejected != 0 {
		t.Fatalf("admit metrics: outcome=%+v err=%v", out, err)
	}
}

func metricReply(t *testing.T, store *Store) Reply {
	t.Helper()
	reply, err := store.MetricWindow("test.requests", time.Minute, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

func integer(t *testing.T, value *Number) int64 {
	t.Helper()
	if value == nil || value.Int == nil {
		t.Fatalf("expected exact integer, got %+v", value)
	}
	return *value.Int
}

func warningContains(warnings []string, text string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, text) {
			return true
		}
	}
	return false
}

func TestConfigAndCaptureIdentity(t *testing.T) {
	a, b := newStore(t, nil), newStore(t, nil)
	if a.id == b.id {
		t.Fatal("a restart must produce a different capture identity")
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Namespace = "" },
		func(c *Config) { c.Cluster = "../other" },
		func(c *Config) { c.MaxSeries = 0 },
		func(c *Config) { c.PayloadBytes = 65 << 20 },
		func(c *Config) { c.StaleAfter = c.Retention + time.Second },
	} {
		cfg := DefaultConfig(testutil.Namespace, testutil.Cluster)
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	status := a.CaptureStatus()
	if !warningContains(status.Warnings, "metrics_not_observed") || !warningContains(status.Warnings, "traces_not_observed") {
		t.Fatalf("empty capture did not report missing signals: %+v", status)
	}
}

func TestCumulativeSumsAndResetEpochs(t *testing.T) {
	store := newStore(t, nil)
	epoch := testNow.Add(-10 * time.Minute)
	points := []*metricspb.NumberDataPoint{
		testutil.Number(epoch, testNow.Add(-50*time.Second), 9007199254740993),
		testutil.Number(epoch, testNow.Add(-40*time.Second), 9007199254740994),
		testutil.Number(testNow.Add(-30*time.Second), testNow.Add(-20*time.Second), 3),
		testutil.Number(testNow.Add(-30*time.Second), testNow.Add(-10*time.Second), 7),
	}
	// Arrival order must not affect event-time calculations.
	slices.Reverse(points)
	admitMetrics(t, store, testutil.Metrics("test.requests", cumulative, points...))
	reply := metricReply(t, store)
	if len(reply.Aggregates) != 2 {
		t.Fatalf("reset epochs were merged: %+v", reply.Aggregates)
	}
	values := []int64{integer(t, reply.Aggregates[0].Value), integer(t, reply.Aggregates[1].Value)}
	slices.Sort(values)
	if !slices.Equal(values, []int64{1, 4}) {
		t.Fatalf("wrong exact deltas: %v", values)
	}
	for _, aggregate := range reply.Aggregates {
		if !warningContains(aggregate.Warnings, "multiple_reset_epochs") {
			t.Fatal("reset boundary was not reported")
		}
	}
}

func TestDeltaIntervalsAreNotProrated(t *testing.T) {
	store := newStore(t, nil)
	admitMetrics(t, store, testutil.Metrics("test.requests", delta,
		testutil.Number(testNow.Add(-80*time.Second), testNow.Add(-50*time.Second), 100),
		testutil.Number(testNow.Add(-50*time.Second), testNow.Add(-20*time.Second), 7),
		testutil.Number(testNow.Add(-10*time.Second), testNow, 4),
	))
	a := metricReply(t, store).Aggregates[0]
	if integer(t, a.Value) != 11 || a.Intervals != 2 ||
		!warningContains(a.Warnings, "not_prorated") || !warningContains(a.Warnings, "gaps_between") {
		t.Fatalf("incorrect delta coverage: %+v", a)
	}
}

func TestAmbiguousMetricIntervalsAreNotAggregated(t *testing.T) {
	for _, tc := range []struct {
		name   string
		temp   metricspb.AggregationTemporality
		points []*metricspb.NumberDataPoint
	}{
		{"overlap", delta, []*metricspb.NumberDataPoint{
			testutil.Number(testNow.Add(-30*time.Second), testNow.Add(-10*time.Second), 5),
			testutil.Number(testNow.Add(-20*time.Second), testNow, 6),
		}},
		{"unknown_epoch", cumulative, []*metricspb.NumberDataPoint{
			testutil.Number(time.Time{}, testNow.Add(-20*time.Second), 5),
			testutil.Number(time.Time{}, testNow, 6),
		}},
		{"unmarked_reset", cumulative, []*metricspb.NumberDataPoint{
			testutil.Number(testNow.Add(-40*time.Second), testNow.Add(-20*time.Second), 5),
			testutil.Number(testNow.Add(-40*time.Second), testNow, 1),
		}},
		{"duplicate_timestamp", cumulative, []*metricspb.NumberDataPoint{
			testutil.Number(testNow.Add(-40*time.Second), testNow, 5),
			testutil.Number(testNow.Add(-40*time.Second), testNow, 5),
		}},
		{"integer_overflow", delta, []*metricspb.NumberDataPoint{
			testutil.Number(testNow.Add(-40*time.Second), testNow.Add(-20*time.Second), math.MaxInt64),
			testutil.Number(testNow.Add(-20*time.Second), testNow, 1),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t, nil)
			admitMetrics(t, store, testutil.Metrics("test.requests", tc.temp, tc.points...))
			a := metricReply(t, store).Aggregates[0]
			if a.Value != nil || a.Intervals != 0 || len(a.Warnings) == 0 {
				t.Fatalf("ambiguous aggregate returned a measurement: %+v", a)
			}
		})
	}
}

func TestGaugeIsNotDifferentiated(t *testing.T) {
	store := newStore(t, nil)
	request := testutil.Metrics("test.requests", delta)
	request.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
		DataPoints: []*metricspb.NumberDataPoint{
			testutil.Number(time.Time{}, testNow.Add(-20*time.Second), 10),
			testutil.Number(time.Time{}, testNow, 3),
		},
	}}
	admitMetrics(t, store, request)
	a := metricReply(t, store).Aggregates[0]
	if integer(t, a.Value) != 3 || a.Intervals != 0 || !warningContains(a.Warnings, "not_a_counter") {
		t.Fatalf("gauge interpreted as a counter: %+v", a)
	}
}

func TestHiddenDimensionsAndUnknownInstancesDoNotMerge(t *testing.T) {
	store := newStore(t, nil)
	a := testutil.Number(testNow.Add(-20*time.Second), testNow, 1)
	b := testutil.Number(testNow.Add(-20*time.Second), testNow, 2)
	a.Attributes = append(a.Attributes, testutil.Attribute("db.namespace", "private-a"))
	b.Attributes = append(b.Attributes, testutil.Attribute("db.namespace", "private-b"))
	admitMetrics(t, store, testutil.Metrics("test.requests", delta, a, b))
	reply := metricReply(t, store)
	if len(reply.Aggregates) != 2 || reply.Points[0].SeriesID == reply.Points[1].SeriesID {
		t.Fatal("redaction merged distinct metric streams")
	}
	data, err := json.Marshal(reply)
	if err != nil || strings.Contains(string(data), "private-") {
		t.Fatalf("hidden dimensions leaked: err=%v", err)
	}
	unknown := newStore(t, nil)
	for i := range 2 {
		request := testutil.Metrics("test.requests", cumulative,
			testutil.Number(testNow.Add(-40*time.Second), testNow.Add(time.Duration(i-1)*time.Second), int64(i+1)))
		request.ResourceMetrics[0].Resource.Attributes = slices.DeleteFunc(request.ResourceMetrics[0].Resource.Attributes,
			func(kv *commonpb.KeyValue) bool { return kv.Key == "service.instance.id" })
		admitMetrics(t, unknown, request)
	}
	reply = metricReply(t, unknown)
	if len(reply.Aggregates) != 2 || reply.Aggregates[0].Value != nil || reply.Aggregates[1].Value != nil {
		t.Fatalf("unidentified exports were joined: %+v", reply.Aggregates)
	}
}

func TestLateDataFreshnessAndRetention(t *testing.T) {
	store := newStore(t, nil)
	now := testNow
	store.now = func() time.Time { return now }
	admitMetrics(t, store, testutil.Metrics("test.requests", delta,
		testutil.Number(now.Add(-120*time.Second), now.Add(-100*time.Second), 4)))
	reply := store.CaptureStatus()
	if !warningContains(reply.Warnings, "metrics_event_time_stale") ||
		!reply.Status.Counters.Metrics.LastAcceptedArrival.Equal(now) {
		t.Fatal("arrival freshness concealed old events")
	}
	out, err := store.AddMetrics(testutil.Metrics("test.requests", delta,
		testutil.Number(now.Add(-400*time.Second), now.Add(-301*time.Second), 4)))
	if err != nil || out.Rejected != 1 || out.Reasons["stale_event"] != 1 {
		t.Fatalf("stale event was not explicitly rejected: %+v %v", out, err)
	}
	now = now.Add(6 * time.Minute)
	store.Prune()
	reply = store.CaptureStatus()
	if reply.Status.Points != 0 || reply.Status.PayloadBytes != 0 || reply.Status.Counters.ExpiredPoints != 1 {
		t.Fatalf("retention did not reclaim the payload: %+v", reply.Status)
	}
}

func TestCapacityAndSeriesLimits(t *testing.T) {
	store := newStore(t, func(c *Config) { c.MaxPoints = 2 })
	admitMetrics(t, store, testutil.Metrics("test.requests", delta,
		testutil.Number(testNow.Add(-40*time.Second), testNow.Add(-30*time.Second), 1),
		testutil.Number(testNow.Add(-30*time.Second), testNow.Add(-20*time.Second), 2),
		testutil.Number(testNow.Add(-20*time.Second), testNow.Add(-10*time.Second), 3)))
	status := store.CaptureStatus().Status
	if status.Points != 2 || status.Counters.CapacityEvictions != 1 {
		t.Fatalf("point limit not enforced: %+v", status)
	}
	store = newStore(t, func(c *Config) { c.MaxSeries = 1 })
	admitMetrics(t, store, testutil.Metrics("test.requests", delta,
		testutil.Number(testNow.Add(-20*time.Second), testNow, 1)))
	out, err := store.AddMetrics(testutil.Metrics("another.instrument", delta,
		testutil.Number(testNow.Add(-20*time.Second), testNow, 2)))
	if err != nil || out.Rejected != 1 || out.Reasons["series_limit"] != 1 {
		t.Fatalf("series limit not reported: %+v %v", out, err)
	}
	store = newStore(t, func(c *Config) { c.PayloadBytes = 4096 })
	for i := range 20 {
		admitMetrics(t, store, testutil.Metrics("test.requests", delta,
			testutil.Number(testNow.Add(-30*time.Second), testNow.Add(time.Duration(i-20)*time.Second), 1)))
	}
	status = store.CaptureStatus().Status
	if status.PayloadBytes > 4096 || status.Counters.CapacityEvictions == 0 {
		t.Fatalf("payload budget not enforced: %+v", status)
	}
}

func TestMalformedAndUnsupportedMetrics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*collectormetrics.ExportMetricsServiceRequest)
		reason string
	}{
		{"scope", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].Resource.Attributes[0] = testutil.Attribute("k8s.namespace.name", "other")
		}, "deployment_scope_mismatch"},
		{"duplicate_attribute", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].Resource.Attributes = append(r.ResourceMetrics[0].Resource.Attributes,
				testutil.Attribute("k8s.namespace.name", testutil.Namespace))
		}, "invalid_attributes"},
		{"timestamp", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetSum().DataPoints[0].TimeUnixNano = math.MaxUint64
		}, "invalid_timestamp"},
		{"nonfinite", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetSum().DataPoints[0].Value =
				&metricspb.NumberDataPoint_AsDouble{AsDouble: math.NaN()}
		}, "nonfinite_value"},
		{"temporality", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetSum().AggregationTemporality = 0
		}, "unsupported_temporality"},
		{"summary", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Data = &metricspb.Metric_Summary{Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{{TimeUnixNano: uint64(testNow.UnixNano())}},
			}}
		}, "unsupported_metric_type"},
		{"exponential_histogram", func(r *collectormetrics.ExportMetricsServiceRequest) {
			r.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Data =
				&metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
					DataPoints: []*metricspb.ExponentialHistogramDataPoint{{TimeUnixNano: uint64(testNow.UnixNano())}},
				}}
		}, "unsupported_metric_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t, nil)
			request := testutil.Metrics("test.requests", delta, testutil.Number(testNow.Add(-time.Second), testNow, 1))
			tc.mutate(request)
			out, err := store.AddMetrics(request)
			if err != nil || out.Accepted != 0 || out.Rejected != 1 || out.Reasons[tc.reason] != 1 {
				t.Fatalf("bad batch result: %+v, %v", out, err)
			}
			if store.CaptureStatus().Status.Points != 0 {
				t.Fatal("rejected data retained")
			}
		})
	}
}

func TestRequestLimitsAreAtomic(t *testing.T) {
	store := newStore(t, nil)
	points := make([]*metricspb.NumberDataPoint, MaxBatchItems)
	for i := range points {
		points[i] = testutil.Number(testNow.Add(-time.Second), testNow, 1)
	}
	_, err := store.AddMetrics(testutil.Metrics("test.requests", delta, points...))
	if !errors.Is(err, ErrBatchLimit) || store.CaptureStatus().Status.Points != 0 {
		t.Fatal("oversized item batch was partially admitted")
	}
	span := testutil.Span(testNow, 1, 1, 0)
	span.Name = strings.Repeat("x", MaxOTLPBytes)
	_, err = store.AddTraces(testutil.Traces(span))
	if !errors.Is(err, ErrBatchLimit) || store.CaptureStatus().Status.Spans != 0 {
		t.Fatal("oversized byte batch was partially admitted")
	}
}

func TestTraceRedactionRelationshipsAndDuplicates(t *testing.T) {
	store := newStore(t, nil)
	root, child := testutil.Span(testNow, 1, 1, 0), testutil.Span(testNow, 1, 2, 1)
	child.Attributes = append(child.Attributes, testutil.Attribute("db.query.text", "private-query-content"))
	child.Events = []*tracepb.Span_Event{{Name: "private-document-content"}}
	child.Status = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "private-status-content"}
	root.Name = "unsafe span name with private literals"
	out, err := store.AddTraces(testutil.Traces(child))
	if err != nil || out.Accepted != 1 {
		t.Fatalf("admit trace: %+v %v", out, err)
	}
	id := hex.EncodeToString(child.TraceId)
	reply, err := store.TraceByID(id, 100)
	if err != nil || reply.Traces[0].RootObserved || len(reply.Traces[0].MissingParents) != 1 {
		t.Fatalf("missing parent not reported: %+v %v", reply, err)
	}
	out, err = store.AddTraces(testutil.Traces(root, child))
	if err != nil || out.Accepted != 2 || out.Duplicates != 1 {
		t.Fatalf("duplicate not acknowledged: %+v %v", out, err)
	}
	child.Status.Message = "different-private-status-content"
	out, err = store.AddTraces(testutil.Traces(child))
	if err != nil || out.Rejected != 1 || out.Reasons["conflicting_duplicate_span"] != 1 {
		t.Fatalf("conflicting hidden payload not detected: %+v %v", out, err)
	}
	reply, err = store.TraceByID(id, 100)
	if err != nil || !reply.Traces[0].RootObserved || reply.Traces[0].Complete ||
		len(reply.Traces[0].Spans) != 2 || reply.Traces[0].ConflictingSpans != 1 {
		t.Fatalf("incorrect trace view: %+v %v", reply, err)
	}
	data, err := json.Marshal(reply)
	if err != nil || strings.Contains(string(data), "private") || strings.Contains(string(data), "unsafe span name") {
		t.Fatalf("redacted trace data leaked: err=%v", err)
	}
	status := store.CaptureStatus().Status
	if status.Counters.RemovedEvents != 1 || status.Counters.DuplicateSpans != 1 || status.Spans != 2 {
		t.Fatalf("incorrect trace counters: %+v", status)
	}
}

func TestSpanLimitAndTraceQueryValidation(t *testing.T) {
	store := newStore(t, func(c *Config) { c.MaxSpans = 1 })
	root := testutil.Span(testNow.Add(-time.Second), 1, 1, 0)
	child := testutil.Span(testNow, 1, 2, 1)
	if _, err := store.AddTraces(testutil.Traces(root, child)); err != nil {
		t.Fatal(err)
	}
	reply, err := store.TraceByID(hex.EncodeToString(child.TraceId), 100)
	if err != nil || reply.Traces[0].RootObserved || len(reply.Traces[0].MissingParents) != 1 {
		t.Fatalf("evicted parent not reported: %+v %v", reply, err)
	}
	if _, err := store.RecentTraces(time.Minute, 21); err == nil {
		t.Fatal("unbounded trace list accepted")
	}
	if _, err := store.TraceByID(strings.Repeat("0", 32), 100); err == nil {
		t.Fatal("zero trace ID accepted")
	}
	if _, err := store.MetricWindow("test.requests", time.Hour, 10); err == nil {
		t.Fatal("out-of-retention window accepted")
	}
}

func TestDuplicateSpanAttributeOrderIsNotAConflict(t *testing.T) {
	store := newStore(t, nil)
	span := testutil.Span(testNow, 1, 1, 0)
	span.Attributes = append(span.Attributes, testutil.Attribute("pool", "primary"))
	if _, err := store.AddTraces(testutil.Traces(span)); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(span.Attributes)
	out, err := store.AddTraces(testutil.Traces(span))
	if err != nil || out.Duplicates != 1 || out.Rejected != 0 {
		t.Fatalf("attribute ordering produced a conflicting duplicate: %+v %v", out, err)
	}
}

func TestNoRecordedValueDoesNotBecomeZero(t *testing.T) {
	store := newStore(t, nil)
	point := testutil.Number(testNow.Add(-time.Second), testNow, 0)
	point.Flags, point.Value = 1, nil
	admitMetrics(t, store, testutil.Metrics("test.requests", delta, point))
	reply := metricReply(t, store)
	if len(reply.Points) != 1 || !reply.Points[0].NoRecorded || reply.Aggregates[0].Value != nil {
		t.Fatalf("no-recorded-value marker became a measurement: %+v", reply)
	}
}

func TestExplicitHistograms(t *testing.T) {
	for _, temp := range []metricspb.AggregationTemporality{delta, cumulative} {
		t.Run(temp.String(), func(t *testing.T) {
			store := newStore(t, nil)
			request := testutil.Metrics("test.requests", temp)
			start := testNow.Add(-40 * time.Second)
			sumA, sumB := 4.0, 10.0
			a := &metricspb.HistogramDataPoint{
				StartTimeUnixNano: uint64(start.UnixNano()), TimeUnixNano: uint64(testNow.Add(-20 * time.Second).UnixNano()),
				Count: 2, Sum: &sumA, ExplicitBounds: []float64{5}, BucketCounts: []uint64{2, 0},
			}
			b := &metricspb.HistogramDataPoint{
				StartTimeUnixNano: uint64(start.UnixNano()), TimeUnixNano: uint64(testNow.UnixNano()),
				Count: 4, Sum: &sumB, ExplicitBounds: []float64{5}, BucketCounts: []uint64{3, 1},
			}
			if temp == delta {
				b.StartTimeUnixNano = a.TimeUnixNano
			}
			request.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Data = &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
				AggregationTemporality: temp, DataPoints: []*metricspb.HistogramDataPoint{a, b},
			}}
			admitMetrics(t, store, request)
			result := metricReply(t, store).Aggregates[0]
			want := uint64(6)
			if temp == cumulative {
				want = 2
			}
			if result.Histogram == nil || result.Histogram.Count != want ||
				!warningContains(result.Warnings, "no_percentiles") {
				t.Fatalf("incorrect histogram calculation: %+v", result)
			}
			b.BucketCounts[0] = 100
			out, err := store.AddMetrics(request)
			if err != nil || out.Rejected != 1 || out.Reasons["histogram_count_mismatch"] != 1 {
				t.Fatalf("invalid histogram accepted: %+v %v", out, err)
			}
		})
	}
}

func TestConcurrentIngestionAndReads(t *testing.T) {
	store := newStore(t, func(c *Config) { c.MaxPoints, c.MaxSpans = 300, 100 })
	var workers sync.WaitGroup
	for worker := range 6 {
		workers.Go(func() {
			for i := range 40 {
				switch worker % 3 {
				case 0:
					_, err := store.AddMetrics(testutil.Metrics("test.requests", delta,
						testutil.Number(testNow.Add(-time.Second), testNow, 1)))
					if err != nil {
						t.Error(err)
					}
				case 1:
					_, err := store.AddTraces(testutil.Traces(testutil.Span(testNow, uint64(worker+1), uint64(i+1), 0)))
					if err != nil {
						t.Error(err)
					}
				case 2:
					store.CaptureStatus()
					if _, err := store.MetricWindow("test.requests", time.Minute, 100); err != nil {
						t.Error(err)
					}
					if _, err := store.RecentTraces(time.Minute, 20); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	workers.Wait()
	status := store.CaptureStatus().Status
	if status.PayloadBytes > status.PayloadLimit || status.Points > status.PointLimit || status.Spans > status.SpanLimit {
		t.Fatalf("concurrent ingestion exceeded limits: %+v", status)
	}
}
