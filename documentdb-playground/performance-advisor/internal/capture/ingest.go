// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package capture

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

var (
	identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/+-]{0,127}$`)
	metricName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_./-]{0,127}$`)
	metricUnit = regexp.MustCompile(`^[a-zA-Z0-9_./{}%*()-]{0,32}$`)
)

var resourceKeys = map[string]bool{
	"documentdb.cluster": true, "k8s.namespace.name": true,
	"k8s.pod.name": true, "k8s.pod.uid": true, "k8s.node.name": true,
	"k8s.container.name": true, "container.name": true, "container.id": true,
	"service.name": true, "service.version": true, "service.instance.id": true,
	"process.pid": true, "process.start_time": true,
	"telemetry.sdk.name": true, "telemetry.sdk.language": true, "telemetry.sdk.version": true,
}

var observationKeys = map[string]bool{
	"db.operation.name": true, "db.system.name": true, "db.response.status_code": true,
	"db.operation.phase": true, "db.client.operation.phase": true, "operation": true, "phase": true,
	"pool":         true,
	"request.type": true, "request_type": true, "error.type": true,
	"error_code": true, "status": true, "status_code": true,
	"network.protocol.name": true, "server.port": true,
}

var spanNames = map[string]bool{
	"gateway.request": true, "gateway.process_request": true, "gateway.write_response": true,
	"postgres.transaction": true, "postgres.execute": true, "postgres.acquire_connection": true,
	"synthetic.request": true,
}

var scopeNames = map[string]bool{
	"": true, "documentdb_gateway": true, "synthetic-workload": true,
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlqueryreceiver": true,
}

var scopeVersions = map[string]bool{
	"": true, "1.0.0": true, "0.116.0": true, "0.149.0": true,
}

func cleanAttributes(attrs []*commonpb.KeyValue, allowed map[string]bool) (Attributes, int, string) {
	if len(attrs) > 64 {
		return nil, 0, "attribute_limit"
	}
	clean := make(Attributes)
	seen := make(map[string]bool, len(attrs))
	redacted := 0
	for _, kv := range attrs {
		if kv == nil || kv.Key == "" || len(kv.Key) > 256 || seen[kv.Key] || kv.Value == nil {
			return nil, 0, "invalid_attributes"
		}
		seen[kv.Key] = true
		if !allowed[kv.Key] {
			redacted++
			continue
		}
		switch value := kv.Value.Value.(type) {
		case *commonpb.AnyValue_StringValue:
			if identifier.MatchString(value.StringValue) {
				clean[strings.Clone(kv.Key)] = strings.Clone(value.StringValue)
			} else {
				redacted++
			}
		case *commonpb.AnyValue_IntValue:
			clean[strings.Clone(kv.Key)] = value.IntValue
		case *commonpb.AnyValue_BoolValue:
			clean[strings.Clone(kv.Key)] = value.BoolValue
		case *commonpb.AnyValue_DoubleValue:
			if finite(value.DoubleValue) {
				clean[strings.Clone(kv.Key)] = value.DoubleValue
			} else {
				return nil, 0, "nonfinite_attribute"
			}
		default:
			redacted++
		}
	}
	return clean, redacted, ""
}

func (s *Store) diagnosticName(value, field string, allowed map[string]bool) (string, int) {
	if allowed[value] {
		return strings.Clone(value), 0
	}
	hash := s.digest([]byte(field), []byte(value))
	return "redacted-" + hex.EncodeToString(hash[:8]), 1
}

func (s *Store) digest(parts ...[]byte) [32]byte {
	h := hmac.New(sha256.New, s.secret[:])
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write(part)
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func (s *Store) attributeDigest(attrs []*commonpb.KeyValue) ([32]byte, error) {
	sorted := slices.Clone(attrs)
	slices.SortFunc(sorted, func(a, b *commonpb.KeyValue) int {
		return strings.Compare(a.GetKey(), b.GetKey())
	})
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(&commonpb.KeyValueList{Values: sorted})
	if err != nil {
		return [32]byte{}, err
	}
	return s.digest(data), nil
}

func (s *Store) source(resource *resourcepb.Resource, scope *commonpb.InstrumentationScope) (Source, [32]byte, string) {
	var source Source
	attrs, removed, reason := cleanAttributes(resource.GetAttributes(), resourceKeys)
	if reason != "" {
		return source, [32]byte{}, reason
	}
	if attrs["documentdb.cluster"] != s.cfg.Cluster || attrs["k8s.namespace.name"] != s.cfg.Namespace {
		return source, [32]byte{}, "deployment_scope_mismatch"
	}
	source.Resource = attrs
	source.RedactedFields = removed
	source.ScopeAttrs, removed, reason = cleanAttributes(scope.GetAttributes(), observationKeys)
	if reason != "" {
		return source, [32]byte{}, reason
	}
	source.RedactedFields += removed
	source.ScopeName, removed = s.diagnosticName(scope.GetName(), "scope_name", scopeNames)
	source.RedactedFields += removed
	source.ScopeVersion, removed = s.diagnosticName(scope.GetVersion(), "scope_version", scopeVersions)
	source.RedactedFields += removed
	instance, _ := attrs["service.instance.id"].(string)
	container, _ := attrs["container.id"].(string)
	pod, _ := attrs["k8s.pod.uid"].(string)
	_, hasStart := attrs["process.start_time"]
	source.InstanceKnown = instance != "" || container != "" || (pod != "" && hasStart)
	resourceHash, err := s.attributeDigest(resource.GetAttributes())
	if err != nil {
		return source, [32]byte{}, "invalid_attributes"
	}
	scopeHash, err := s.attributeDigest(scope.GetAttributes())
	if err != nil {
		return source, [32]byte{}, "invalid_attributes"
	}
	hash := s.digest(resourceHash[:], scopeHash[:], []byte(scope.GetName()), []byte(scope.GetVersion()))
	return source, hash, ""
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func timestamp(n uint64) (time.Time, bool) {
	if n == 0 || n > math.MaxInt64 {
		return time.Time{}, false
	}
	return time.Unix(0, int64(n)).UTC(), true
}

func (s *Store) eventTime(n uint64, now time.Time) (time.Time, string) {
	event, ok := timestamp(n)
	if !ok || event.After(now.Add(30*time.Second)) {
		return event, "invalid_timestamp"
	}
	if event.Before(now.Add(-s.cfg.Retention)) {
		return event, "stale_event"
	}
	return event, ""
}

func metricCount(m *metricspb.Metric) int64 {
	return int64(len(m.GetGauge().GetDataPoints()) + len(m.GetSum().GetDataPoints()) +
		len(m.GetHistogram().GetDataPoints()) + len(m.GetExponentialHistogram().GetDataPoints()) +
		len(m.GetSummary().GetDataPoints()))
}

func metricBatchItems(req *collectormetrics.ExportMetricsServiceRequest) int {
	count := len(req.GetResourceMetrics())
	for _, resource := range req.GetResourceMetrics() {
		count += len(resource.GetScopeMetrics())
		for _, scope := range resource.GetScopeMetrics() {
			count += len(scope.GetMetrics())
			for _, metric := range scope.GetMetrics() {
				count += int(metricCount(metric))
			}
		}
	}
	return count
}

// AddMetrics admits supported observations and returns OTLP partial-rejection counts.
func (s *Store) AddMetrics(req *collectormetrics.ExportMetricsServiceRequest) (Outcome, error) {
	if proto.Size(req) > MaxOTLPBytes || metricBatchItems(req) > MaxBatchItems {
		return Outcome{}, ErrBatchLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	out := s.begin(&s.counters.Metrics, now)
	for _, resource := range req.GetResourceMetrics() {
		s.counters.UpstreamDropsStated += uint64(resource.GetResource().GetDroppedAttributesCount())
		for _, scope := range resource.GetScopeMetrics() {
			source, sourceHash, sourceReason := s.source(resource.GetResource(), scope.GetScope())
			s.counters.UpstreamDropsStated += uint64(scope.GetScope().GetDroppedAttributesCount())
			for _, metric := range scope.GetMetrics() {
				reason := sourceReason
				if reason == "" && (!metricName.MatchString(metric.GetName()) || !metricUnit.MatchString(metric.GetUnit())) {
					reason = "invalid_metric_metadata"
				}
				if reason != "" {
					s.reject(&out, &s.counters.Metrics, reason, metricCount(metric))
					continue
				}
				base := Point{
					Source: source, Name: strings.Clone(metric.GetName()),
					Unit: strings.Clone(metric.GetUnit()), Arrival: now,
				}
				switch metric.GetData().(type) {
				case *metricspb.Metric_Gauge:
					base.Kind = "gauge"
					for _, point := range metric.GetGauge().GetDataPoints() {
						s.addNumber(&out, base, sourceHash, point, now)
					}
				case *metricspb.Metric_Sum:
					base.Kind, base.Monotonic = "sum", metric.GetSum().GetIsMonotonic()
					base.Temporality = temporality(metric.GetSum().GetAggregationTemporality())
					for _, point := range metric.GetSum().GetDataPoints() {
						s.addNumber(&out, base, sourceHash, point, now)
					}
				case *metricspb.Metric_Histogram:
					base.Kind = "histogram"
					base.Temporality = temporality(metric.GetHistogram().GetAggregationTemporality())
					for _, point := range metric.GetHistogram().GetDataPoints() {
						p := base
						reason := s.pointMetadata(&p, sourceHash, point.GetAttributes(),
							point.GetStartTimeUnixNano(), point.GetTimeUnixNano(), point.GetFlags(), now)
						if reason == "" && !p.NoRecorded {
							p.Histogram, reason = histogram(point)
						}
						if reason == "" {
							s.addExemplars(&p, point.GetExemplars(), now)
						}
						s.finishPoint(&out, &p, reason, now)
					}
				default:
					s.reject(&out, &s.counters.Metrics, "unsupported_metric_type", metricCount(metric))
				}
			}
		}
	}
	return out, nil
}

func temporality(value metricspb.AggregationTemporality) string {
	switch value {
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:
		return "delta"
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:
		return "cumulative"
	default:
		return "unsupported"
	}
}

func (s *Store) pointMetadata(p *Point, sourceHash [32]byte, attrs []*commonpb.KeyValue,
	start, end uint64, flags uint32, now time.Time) string {
	if p.Temporality == "unsupported" {
		return "unsupported_temporality"
	}
	if flags & ^uint32(1) != 0 {
		return "unsupported_flags"
	}
	p.NoRecorded = flags&1 != 0
	var reason string
	p.Time, reason = s.eventTime(end, now)
	if reason != "" {
		return reason
	}
	if start != 0 {
		t, ok := timestamp(start)
		if !ok || t.After(p.Time) {
			return "invalid_start_timestamp"
		}
		p.Start = &t
	}
	p.Attributes, p.Redacted, reason = cleanAttributes(attrs, observationKeys)
	if reason != "" {
		return reason
	}
	attrHash, err := s.attributeDigest(attrs)
	if err != nil {
		return "invalid_attributes"
	}
	// Hidden dimensions participate in identity without retaining their contents.
	parts := [][]byte{sourceHash[:], attrHash[:], []byte(p.Name), []byte(p.Unit),
		[]byte(p.Kind), []byte(p.Temporality), []byte(strconv.FormatBool(p.Monotonic))}
	if !p.Source.InstanceKnown {
		// Without a process identity, exports must not be joined across lifetimes.
		parts = append(parts, []byte(strconv.FormatUint(s.batch, 10)))
	}
	hash := s.digest(parts...)
	p.SeriesID = hex.EncodeToString(hash[:16])
	p.Late = now.Sub(p.Time) > 30*time.Second
	return ""
}

func (s *Store) addNumber(out *Outcome, base Point, sourceHash [32]byte, raw *metricspb.NumberDataPoint, now time.Time) {
	p := base
	reason := s.pointMetadata(&p, sourceHash, raw.GetAttributes(),
		raw.GetStartTimeUnixNano(), raw.GetTimeUnixNano(), raw.GetFlags(), now)
	if reason == "" && !p.NoRecorded {
		switch value := raw.GetValue().(type) {
		case *metricspb.NumberDataPoint_AsInt:
			v := value.AsInt
			p.Number = &Number{Int: &v}
		case *metricspb.NumberDataPoint_AsDouble:
			v := value.AsDouble
			if !finite(v) {
				reason = "nonfinite_value"
			} else {
				p.Number = &Number{Double: &v}
			}
		default:
			reason = "missing_value"
		}
	}
	if p.Monotonic && p.Number != nil && numberNegative(p.Number) {
		reason = "negative_monotonic_sum"
	}
	if reason == "" {
		s.addExemplars(&p, raw.GetExemplars(), now)
	}
	s.finishPoint(out, &p, reason, now)
}

func (s *Store) finishPoint(out *Outcome, p *Point, reason string, now time.Time) {
	if reason == "" {
		reason = s.admit(&entry{point: p, event: p.Time})
	}
	if reason != "" {
		s.reject(out, &s.counters.Metrics, reason, 1)
		return
	}
	s.counters.RedactedFields += uint64(p.Redacted + p.Source.RedactedFields)
	if p.OmittedExemplars > 0 {
		s.counters.OmittedExemplars += uint64(p.OmittedExemplars)
		out.Reasons["metric_exemplars_omitted"] += int64(p.OmittedExemplars)
	}
	s.accepted(out, &s.counters.Metrics, now, p.Time)
}

func (s *Store) addExemplars(p *Point, raw []*metricspb.Exemplar, now time.Time) {
	p.OmittedExemplars = len(raw)
	if p.NoRecorded {
		return
	}
	for _, sample := range raw[:min(len(raw), MaxExemplars)] {
		event, reason := s.eventTime(sample.GetTimeUnixNano(), now)
		if reason != "" || event.After(p.Time) || (p.Start != nil && event.Before(*p.Start)) {
			continue
		}
		if (len(sample.GetTraceId()) != 0 && !validID(sample.GetTraceId(), 16)) ||
			(len(sample.GetSpanId()) != 0 && !validID(sample.GetSpanId(), 8)) {
			continue
		}
		attrs, removed, reason := cleanAttributes(sample.GetFilteredAttributes(), observationKeys)
		if reason != "" {
			continue
		}
		exemplar := Exemplar{
			Time: event, TraceID: hex.EncodeToString(sample.GetTraceId()),
			SpanID: hex.EncodeToString(sample.GetSpanId()), Attributes: attrs, Redacted: removed,
		}
		switch value := sample.GetValue().(type) {
		case *metricspb.Exemplar_AsInt:
			v := value.AsInt
			exemplar.Number.Int = &v
		case *metricspb.Exemplar_AsDouble:
			v := value.AsDouble
			if !finite(v) {
				continue
			}
			exemplar.Number.Double = &v
		default:
			continue
		}
		p.Exemplars = append(p.Exemplars, exemplar)
		p.OmittedExemplars--
		p.Redacted += removed
	}
}

func numberNegative(n *Number) bool {
	return (n.Int != nil && *n.Int < 0) || (n.Double != nil && *n.Double < 0)
}

func histogram(raw *metricspb.HistogramDataPoint) (*Histogram, string) {
	bounds, counts := raw.GetExplicitBounds(), raw.GetBucketCounts()
	if len(bounds) > 128 || (len(counts) != len(bounds)+1 && (len(bounds) != 0 || len(counts) != 0)) {
		return nil, "invalid_histogram_buckets"
	}
	for i, bound := range bounds {
		if !finite(bound) || (i > 0 && bound <= bounds[i-1]) {
			return nil, "invalid_histogram_bounds"
		}
	}
	var count uint64
	for _, bucket := range counts {
		if math.MaxUint64-count < bucket {
			return nil, "histogram_count_overflow"
		}
		count += bucket
	}
	if len(counts) > 0 && count != raw.Count {
		return nil, "histogram_count_mismatch"
	}
	for _, value := range []*float64{raw.Sum, raw.Min, raw.Max} {
		if value != nil && !finite(*value) {
			return nil, "nonfinite_value"
		}
	}
	if raw.Min != nil && raw.Max != nil && *raw.Min > *raw.Max {
		return nil, "invalid_histogram_range"
	}
	if !validHistogramSum(raw.Count, raw.Sum) {
		return nil, "invalid_histogram_sum"
	}
	if raw.Sum != nil && raw.Min != nil && *raw.Min < 0 {
		return nil, "histogram_sum_for_negative_population"
	}
	h := &Histogram{Count: raw.Count, Bounds: slices.Clone(bounds), BucketCounts: slices.Clone(counts)}
	if raw.Sum != nil {
		v := *raw.Sum
		h.Sum = &v
	}
	if raw.Min != nil {
		v := *raw.Min
		h.Min = &v
	}
	if raw.Max != nil {
		v := *raw.Max
		h.Max = &v
	}
	return h, ""
}

func validHistogramSum(count uint64, sum *float64) bool {
	return sum == nil || (finite(*sum) && *sum >= 0 && (count > 0 || *sum == 0))
}

func traceBatchItems(req *collectortrace.ExportTraceServiceRequest) int {
	count := len(req.GetResourceSpans())
	for _, resource := range req.GetResourceSpans() {
		count += len(resource.GetScopeSpans())
		for _, scope := range resource.GetScopeSpans() {
			count += len(scope.GetSpans())
		}
	}
	return count
}

func validID(id []byte, size int) bool {
	if len(id) != size {
		return false
	}
	for _, b := range id {
		if b != 0 {
			return true
		}
	}
	return false
}

// AddTraces deduplicates span identities and preserves conflicting-duplicate evidence.
func (s *Store) AddTraces(req *collectortrace.ExportTraceServiceRequest) (Outcome, error) {
	if proto.Size(req) > MaxOTLPBytes || traceBatchItems(req) > MaxBatchItems {
		return Outcome{}, ErrBatchLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	out := s.begin(&s.counters.Traces, now)
	for _, resource := range req.GetResourceSpans() {
		s.counters.UpstreamDropsStated += uint64(resource.GetResource().GetDroppedAttributesCount())
		for _, scope := range resource.GetScopeSpans() {
			source, sourceHash, sourceReason := s.source(resource.GetResource(), scope.GetScope())
			s.counters.UpstreamDropsStated += uint64(scope.GetScope().GetDroppedAttributesCount())
			for _, raw := range scope.GetSpans() {
				span, fingerprint, reason := s.cleanSpan(raw, source, sourceHash, now)
				if sourceReason != "" {
					reason = sourceReason
				}
				if reason != "" {
					s.reject(&out, &s.counters.Traces, reason, 1)
					continue
				}
				key := span.TraceID + "/" + span.SpanID
				if old := s.spans[key]; old != nil {
					if old.fingerprint != fingerprint {
						old.conflicts++
						s.counters.ConflictingSpans++
						s.reject(&out, &s.counters.Traces, "conflicting_duplicate_span", 1)
					} else {
						out.Duplicates++
						s.counters.DuplicateSpans++
						s.accepted(&out, &s.counters.Traces, now, span.End)
					}
					continue
				}
				reason = s.admit(&entry{span: span, event: span.End, fingerprint: fingerprint})
				if reason != "" {
					s.reject(&out, &s.counters.Traces, reason, 1)
					continue
				}
				s.counters.RedactedFields += uint64(span.Redacted + source.RedactedFields)
				s.counters.RemovedEvents += uint64(span.RemovedEvents)
				s.counters.RemovedLinks += uint64(span.RemovedLinks)
				s.counters.UpstreamDropsStated += uint64(raw.GetDroppedAttributesCount()) +
					uint64(raw.GetDroppedEventsCount()) + uint64(raw.GetDroppedLinksCount())
				s.accepted(&out, &s.counters.Traces, now, span.End)
			}
		}
	}
	return out, nil
}

func (s *Store) cleanSpan(raw *tracepb.Span, source Source, sourceHash [32]byte, now time.Time) (*Span, [32]byte, string) {
	span := &Span{Source: source, Arrival: now}
	var zero [32]byte
	if !validID(raw.GetTraceId(), 16) || !validID(raw.GetSpanId(), 8) ||
		(len(raw.GetParentSpanId()) != 0 && !validID(raw.GetParentSpanId(), 8)) {
		return span, zero, "invalid_span_identity"
	}
	span.TraceID, span.SpanID = hex.EncodeToString(raw.TraceId), hex.EncodeToString(raw.SpanId)
	if len(raw.ParentSpanId) != 0 {
		span.ParentID = hex.EncodeToString(raw.ParentSpanId)
		if span.ParentID == span.SpanID {
			return span, zero, "self_parent_span"
		}
	}
	var reason string
	span.End, reason = s.eventTime(raw.GetEndTimeUnixNano(), now)
	if reason != "" {
		return span, zero, reason
	}
	var ok bool
	span.Start, ok = timestamp(raw.GetStartTimeUnixNano())
	if !ok || span.Start.After(span.End) {
		return span, zero, "invalid_start_timestamp"
	}
	if raw.Kind < 0 || raw.Kind > tracepb.Span_SPAN_KIND_CONSUMER ||
		raw.GetStatus().GetCode() < 0 || raw.GetStatus().GetCode() > tracepb.Status_STATUS_CODE_ERROR {
		return span, zero, "unsupported_span_metadata"
	}
	span.Name, span.Redacted = s.diagnosticName(raw.Name, "span_name", spanNames)
	span.Kind, span.Status = raw.Kind.String(), raw.GetStatus().GetCode().String()
	attrs, removed, reason := cleanAttributes(raw.Attributes, observationKeys)
	if reason != "" {
		return span, zero, reason
	}
	span.Attributes, span.Redacted = attrs, span.Redacted+removed
	span.RemovedEvents, span.RemovedLinks = len(raw.Events), len(raw.Links)
	span.Sampled, span.Late = raw.Flags&1 != 0, now.Sub(span.End) > 30*time.Second
	if raw.GetStatus().GetMessage() != "" {
		span.Redacted++
	}
	if raw.TraceState != "" {
		span.Redacted++
	}
	canonical := proto.Clone(raw).(*tracepb.Span)
	slices.SortFunc(canonical.Attributes, func(a, b *commonpb.KeyValue) int {
		return strings.Compare(a.GetKey(), b.GetKey())
	})
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(canonical)
	if err != nil {
		return span, zero, "invalid_span_payload"
	}
	return span, s.digest(sourceHash[:], data), ""
}
