// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package capture

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

func addWarning(reply *Reply, warning string) {
	if !slices.Contains(reply.Warnings, warning) {
		reply.Warnings = append(reply.Warnings, warning)
	}
}

// CaptureStatus inventories only signals actually retained in this capture.
func (s *Store) CaptureStatus() Reply {
	snap := s.snapshot()
	metrics := make(map[string]*MetricInfo)
	sources := make(map[string]Source)
	for _, e := range snap.entries {
		var source Source
		if p := e.point; p != nil {
			source = p.Source
			key := p.Name + "\x00" + p.Kind + "\x00" + p.Unit + "\x00" + p.Temporality
			info := metrics[key]
			if info == nil {
				info = &MetricInfo{Name: p.Name, Kind: p.Kind, Unit: p.Unit, Temporality: p.Temporality}
				metrics[key] = info
			}
			info.Points++
			if p.Time.After(info.LatestEvent) {
				info.LatestEvent = p.Time
			}
		} else {
			source = e.span.Source
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			addWarning(&snap.reply, "source_inventory_encoding_failed")
			continue
		}
		sources[string(encoded)] = source
		if !source.InstanceKnown {
			addWarning(&snap.reply, "source_instance_unknown; exports_not_joined")
		}
		if e.event.After(snap.reply.Now) {
			addWarning(&snap.reply, "future_event_time_within_clock_skew_allowance")
		}
	}
	for _, info := range metrics {
		snap.status.MetricInventory = append(snap.status.MetricInventory, *info)
	}
	slices.SortFunc(snap.status.MetricInventory, func(a, b MetricInfo) int {
		return strings.Compare(a.Name+"\x00"+a.Kind+"\x00"+a.Unit+"\x00"+a.Temporality,
			b.Name+"\x00"+b.Kind+"\x00"+b.Unit+"\x00"+b.Temporality)
	})
	keys := make([]string, 0, len(sources))
	for key := range sources {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		snap.status.Sources = append(snap.status.Sources, sources[key])
	}
	snap.reply.Status = &snap.status
	addWarning(&snap.reply, "payload_budget_excludes_heap_indexes_and_inflight_protocol_buffers")
	return snap.reply
}

func (s *Store) validateWindow(lookback time.Duration) error {
	if lookback <= 0 || lookback > s.cfg.Retention {
		return fmt.Errorf("lookback must be positive and no longer than capture retention")
	}
	return nil
}

// MetricWindow returns raw observations and per-stream, per-epoch calculations.
func (s *Store) MetricWindow(name string, lookback time.Duration, limit int) (Reply, error) {
	if !metricName.MatchString(name) {
		return Reply{}, fmt.Errorf("metric must be a valid, bounded instrument name")
	}
	if err := s.validateWindow(lookback); err != nil {
		return Reply{}, err
	}
	if limit < 1 || limit > MaxQueryPoints {
		return Reply{}, fmt.Errorf("point limit must be between 1 and %d", MaxQueryPoints)
	}
	snap := s.snapshot()
	since, until := snap.reply.Now.Add(-lookback), snap.reply.Now
	snap.reply.Since, snap.reply.Until = &since, &until
	groups := make(map[string][]Point)
	for _, e := range snap.entries {
		p := e.point
		if p == nil || p.Name != name || p.Time.Before(since) || p.Time.After(until) {
			continue
		}
		snap.reply.Points = append(snap.reply.Points, *p)
		key := p.SeriesID
		if p.Temporality == "cumulative" && p.Start != nil {
			key += "/" + p.Start.Format(time.RFC3339Nano)
		}
		groups[key] = append(groups[key], *p)
	}
	if len(snap.reply.Points) == 0 {
		addWarning(&snap.reply, "metric_not_observed_in_requested_window")
	}
	keys := make([]string, 0, len(groups))
	epochs := make(map[string]int)
	for key, points := range groups {
		keys = append(keys, key)
		if points[0].Temporality == "cumulative" {
			epochs[points[0].SeriesID]++
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		result := aggregate(groups[key], since, until)
		var latestEvent, latestArrival time.Time
		for _, point := range groups[key] {
			if point.Time.After(latestEvent) {
				latestEvent = point.Time
			}
			if point.Arrival.After(latestArrival) {
				latestArrival = point.Arrival
			}
			if point.OmittedExemplars > 0 {
				addWarning(&snap.reply, "metric_exemplars_omitted_by_validation_or_limit")
			}
		}
		if until.Sub(latestEvent) > s.cfg.StaleAfter {
			result.Warnings = append(result.Warnings, "selected_series_event_time_stale")
			addWarning(&snap.reply, "selected_metric_has_stale_event_times")
		}
		if until.Sub(latestArrival) > s.cfg.StaleAfter {
			result.Warnings = append(result.Warnings, "selected_series_arrival_stale")
			addWarning(&snap.reply, "selected_metric_has_stale_arrivals")
		}
		if epochs[result.SeriesID] > 1 {
			result.Warnings = append(result.Warnings, "multiple_reset_epochs; calculated_separately")
		}
		snap.reply.Aggregates = append(snap.reply.Aggregates, result)
	}
	slices.SortFunc(snap.reply.Points, func(a, b Point) int {
		if cmp := b.Time.Compare(a.Time); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.SeriesID, b.SeriesID)
	})
	if len(snap.reply.Points) > limit {
		snap.reply.Truncated = true
		snap.reply.Omitted = len(snap.reply.Points) - limit
		snap.reply.Points = snap.reply.Points[:limit]
		addWarning(&snap.reply, "observations_truncated; aggregates_use_full_selected_window")
	}
	addWarning(&snap.reply, "metric_delivery_is_not_guaranteed_exactly_once")
	return snap.reply, nil
}

func traceView(id string, entries []entry, limit int) Trace {
	trace := Trace{ID: id, ReceivedSpans: len(entries), MissingParents: []string{}}
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.span.SpanID] = true
	}
	missing := make(map[string]bool)
	for _, e := range entries {
		span := *e.span
		trace.Spans = append(trace.Spans, span)
		trace.ConflictingSpans += e.conflicts
		if span.End.After(trace.LatestEvent) {
			trace.LatestEvent = span.End
		}
		if span.ParentID == "" {
			trace.RootObserved = true
		} else if !ids[span.ParentID] {
			missing[span.ParentID] = true
		}
	}
	for id := range missing {
		trace.MissingParents = append(trace.MissingParents, id)
	}
	slices.Sort(trace.MissingParents)
	slices.SortFunc(trace.Spans, func(a, b Span) int {
		if a.ParentID == "" && b.ParentID != "" {
			return -1
		}
		if a.ParentID != "" && b.ParentID == "" {
			return 1
		}
		if cmp := a.Start.Compare(b.Start); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.SpanID, b.SpanID)
	})
	if len(trace.Spans) > limit {
		trace.Spans = trace.Spans[:limit]
		trace.ResponseTruncated = true
	}
	return trace
}

func addTraceWarnings(reply *Reply) {
	addWarning(reply, "trace_completeness_unknown; no_end_of_trace_marker")
	for _, trace := range reply.Traces {
		if !trace.RootObserved {
			addWarning(reply, "trace_root_not_received")
		}
		if len(trace.MissingParents) > 0 {
			addWarning(reply, "parent_spans_missing_from_capture")
		}
		if trace.ConflictingSpans > 0 {
			addWarning(reply, "conflicting_duplicate_spans; first_observation_retained")
		}
		if trace.ResponseTruncated {
			reply.Truncated = true
			reply.Omitted += trace.ReceivedSpans - len(trace.Spans)
		}
	}
}

// RecentTraces includes at most ten spans per trace; TraceByID provides detail.
func (s *Store) RecentTraces(lookback time.Duration, limit int) (Reply, error) {
	if err := s.validateWindow(lookback); err != nil {
		return Reply{}, err
	}
	if limit < 1 || limit > MaxTraceResults {
		return Reply{}, fmt.Errorf("trace limit must be between 1 and %d", MaxTraceResults)
	}
	snap := s.snapshot()
	since, until := snap.reply.Now.Add(-lookback), snap.reply.Now
	snap.reply.Since, snap.reply.Until = &since, &until
	groups := make(map[string][]entry)
	selected := make(map[string]bool)
	for _, e := range snap.entries {
		if e.span != nil {
			groups[e.span.TraceID] = append(groups[e.span.TraceID], e)
			if !e.event.Before(since) && !e.event.After(until) {
				selected[e.span.TraceID] = true
			}
		}
	}
	for id := range selected {
		snap.reply.Traces = append(snap.reply.Traces, traceView(id, groups[id], 10))
	}
	slices.SortFunc(snap.reply.Traces, func(a, b Trace) int {
		if cmp := b.LatestEvent.Compare(a.LatestEvent); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(snap.reply.Traces) > limit {
		snap.reply.Truncated = true
		snap.reply.Omitted = len(snap.reply.Traces) - limit
		snap.reply.Traces = snap.reply.Traces[:limit]
	}
	if len(snap.reply.Traces) == 0 {
		addWarning(&snap.reply, "traces_not_observed_in_requested_window")
	}
	addWarning(&snap.reply, "related_spans_may_precede_requested_window")
	addTraceWarnings(&snap.reply)
	return snap.reply, nil
}

// TraceByID cannot retrieve traces outside this process's active capture.
func (s *Store) TraceByID(id string, limit int) (Reply, error) {
	raw, err := hex.DecodeString(id)
	if err != nil || !validID(raw, 16) {
		return Reply{}, fmt.Errorf("trace_id must be a nonzero 32-character hexadecimal ID")
	}
	if limit < 1 || limit > MaxQuerySpans {
		return Reply{}, fmt.Errorf("span limit must be between 1 and %d", MaxQuerySpans)
	}
	id = strings.ToLower(id)
	snap := s.snapshot()
	var entries []entry
	for _, e := range snap.entries {
		if e.span != nil && e.span.TraceID == id {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		addWarning(&snap.reply, "trace_not_in_capture")
	} else {
		snap.reply.Traces = []Trace{traceView(id, entries, limit)}
	}
	addTraceWarnings(&snap.reply)
	return snap.reply, nil
}

// Trim reduces the largest result collection without hiding truncation.
func (r *Reply) Trim() bool {
	largest := 0
	var trim func() int
	choose := func(n int, f func() int) {
		if n > largest {
			largest, trim = n, f
		}
	}
	choose(len(r.Points), func() int {
		n := len(r.Points)
		r.Points = r.Points[:n/2]
		addWarning(r, "observations_truncated; aggregates_use_full_selected_window")
		return n - n/2
	})
	choose(len(r.Aggregates), func() int {
		n := len(r.Aggregates)
		r.Aggregates = r.Aggregates[:n/2]
		return n - n/2
	})
	if r.Status != nil {
		choose(len(r.Status.MetricInventory), func() int {
			n := len(r.Status.MetricInventory)
			r.Status.MetricInventory = r.Status.MetricInventory[:n/2]
			return n - n/2
		})
		choose(len(r.Status.Sources), func() int {
			n := len(r.Status.Sources)
			r.Status.Sources = r.Status.Sources[:n/2]
			return n - n/2
		})
	}
	for i := range r.Traces {
		trace := &r.Traces[i]
		choose(len(trace.Spans), func() int {
			n := len(trace.Spans)
			trace.Spans = trace.Spans[:n/2]
			trace.ResponseTruncated = true
			return n - n/2
		})
		choose(len(trace.MissingParents), func() int {
			n := len(trace.MissingParents)
			trace.MissingParents = trace.MissingParents[:n/2]
			trace.ResponseTruncated = true
			return n - n/2
		})
	}
	if trim == nil {
		choose(len(r.Traces), func() int {
			n := len(r.Traces)
			r.Traces = r.Traces[:n/2]
			return n - n/2
		})
	}
	if trim == nil {
		return false
	}
	r.Omitted += trim()
	r.Truncated = true
	addWarning(r, "response_size_limit; request_a_smaller_window_or_limit")
	return true
}
