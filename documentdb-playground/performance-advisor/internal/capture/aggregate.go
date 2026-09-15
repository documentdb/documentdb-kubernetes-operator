// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package capture

import (
	"math"
	"slices"
	"time"
)

func numberArithmetic(a, b *Number, subtract bool) (*Number, string) {
	if a == nil || b == nil {
		return nil, "missing_numeric_value"
	}
	if a.Int != nil && b.Int != nil {
		x, y := *a.Int, *b.Int
		var value int64
		if subtract {
			if (y > 0 && x < math.MinInt64+y) || (y < 0 && x > math.MaxInt64+y) {
				return nil, "integer_overflow"
			}
			value = x - y
		} else {
			if (y > 0 && x > math.MaxInt64-y) || (y < 0 && x < math.MinInt64-y) {
				return nil, "integer_overflow"
			}
			value = x + y
		}
		return &Number{Int: &value}, ""
	}
	if a.Double != nil && b.Double != nil {
		value := *a.Double + *b.Double
		if subtract {
			value = *a.Double - *b.Double
		}
		if !finite(value) {
			return nil, "floating_point_overflow"
		}
		return &Number{Double: &value}, ""
	}
	return nil, "numeric_representation_changed"
}

func histogramArithmetic(a, b *Histogram, subtract bool) (*Histogram, string) {
	if a == nil || b == nil || !slices.Equal(a.Bounds, b.Bounds) || len(a.BucketCounts) != len(b.BucketCounts) {
		return nil, "incompatible_histogram_distributions"
	}
	op := func(x, y uint64) (uint64, bool) {
		if subtract {
			return x - y, x >= y
		}
		return x + y, math.MaxUint64-x >= y
	}
	h := &Histogram{Bounds: slices.Clone(a.Bounds), BucketCounts: make([]uint64, len(a.BucketCounts))}
	var ok bool
	h.Count, ok = op(a.Count, b.Count)
	if !ok {
		return nil, "histogram_count_reset_or_overflow"
	}
	for i, value := range a.BucketCounts {
		h.BucketCounts[i], ok = op(value, b.BucketCounts[i])
		if !ok {
			return nil, "histogram_bucket_reset_or_overflow"
		}
	}
	if a.Sum != nil && b.Sum != nil {
		value := *a.Sum + *b.Sum
		if subtract {
			value = *a.Sum - *b.Sum
		}
		if !finite(value) {
			return nil, "floating_point_overflow"
		}
		h.Sum = &value
	}
	if !validHistogramSum(h.Count, h.Sum) {
		return nil, "inconsistent_histogram_sum_or_reset"
	}
	return h, ""
}

func aggregate(points []Point, since, until time.Time) Aggregate {
	slices.SortFunc(points, func(a, b Point) int { return a.Time.Compare(b.Time) })
	first := points[0]
	result := Aggregate{SeriesID: first.SeriesID, Kind: first.Kind}
	warn := func(reason string) {
		if !slices.Contains(result.Warnings, reason) {
			result.Warnings = append(result.Warnings, reason)
		}
	}
	fail := func(reason string) Aggregate {
		warn(reason)
		result.Value, result.Histogram = nil, nil
		result.Start, result.End, result.Intervals = nil, nil, 0
		return result
	}
	if !first.Source.InstanceKnown {
		warn("source_instance_unknown; exports_not_joined")
		if len(points) > 1 {
			return fail("ambiguous_source_identity")
		}
	}
	for _, p := range points {
		if p.NoRecorded {
			return fail("no_recorded_value_prevents_aggregation")
		}
	}
	if first.Kind == "gauge" {
		last := points[len(points)-1]
		result.Value, result.End = last.Number, &last.Time
		warn("latest_gauge_observation; not_a_counter_delta")
		return result
	}
	cumulative := first.Temporality == "cumulative"
	if cumulative && first.Start == nil {
		return fail("cumulative_reset_epoch_unknown")
	}
	if cumulative && len(points) < 2 {
		return fail("cumulative_baseline_not_received_in_window")
	}
	for i, p := range points {
		var start time.Time
		value, distribution := p.Number, p.Histogram
		if cumulative {
			if i == 0 {
				continue
			}
			prev := points[i-1]
			if p.Start == nil || !p.Start.Equal(*first.Start) {
				return fail("reset_epoch_changed")
			}
			if !p.Time.After(prev.Time) {
				return fail("duplicate_or_conflicting_metric_timestamp")
			}
			start = prev.Time
			var reason string
			if first.Kind == "histogram" {
				distribution, reason = histogramArithmetic(p.Histogram, prev.Histogram, true)
			} else {
				value, reason = numberArithmetic(p.Number, prev.Number, true)
				if reason == "" && p.Monotonic && numberNegative(value) {
					reason = "counter_decreased_within_reset_epoch"
				}
			}
			if reason != "" {
				return fail(reason)
			}
		} else {
			if p.Start == nil || !p.Start.Before(p.Time) {
				return fail("delta_interval_unknown_or_empty")
			}
			start = *p.Start
		}
		if start.Before(since) || p.Time.After(until) {
			warn("interval_crosses_window_boundary; not_prorated")
			continue
		}
		if result.End != nil {
			if start.Before(*result.End) {
				return fail("overlapping_or_duplicate_metric_intervals")
			}
			if start.After(*result.End) {
				warn("gaps_between_covered_intervals")
			}
		}
		if result.Intervals == 0 {
			result.Value, result.Histogram, result.Start = value, distribution, &start
		} else {
			var reason string
			if first.Kind == "histogram" {
				result.Histogram, reason = histogramArithmetic(result.Histogram, distribution, false)
			} else {
				result.Value, reason = numberArithmetic(result.Value, value, false)
			}
			if reason != "" {
				return fail(reason)
			}
		}
		result.End = &p.Time
		result.Intervals++
	}
	if result.Start == nil || result.Start.After(since) || result.End.Before(until) {
		warn("requested_window_not_fully_covered")
	}
	if result.Histogram != nil {
		if result.Histogram.Sum == nil {
			warn("histogram_sum_unavailable")
		}
		warn("explicit_distribution_only; no_percentiles_estimated")
	}
	return result
}
