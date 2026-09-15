// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package capture retains a bounded, deployment-scoped, redacted telemetry window.
package capture

import (
	"errors"
	"time"
)

const (
	MaxOTLPBytes     = 1 << 20
	MaxResponseBytes = 128 << 10
	MaxBatchItems    = 8192
	MaxTraceResults  = 20
	MaxQueryPoints   = 1000
	MaxQuerySpans    = 200
	MaxExemplars     = 8
)

var ErrBatchLimit = errors.New("OTLP batch exceeds the request or item limit")

// Config limits retained payload, not total process memory.
type Config struct {
	Namespace    string
	Cluster      string
	Retention    time.Duration
	StaleAfter   time.Duration
	PayloadBytes int
	MaxSeries    int
	MaxPoints    int
	MaxSpans     int
}

// DefaultConfig binds one capture to an explicitly selected test deployment.
func DefaultConfig(namespace, cluster string) Config {
	return Config{
		Namespace: namespace, Cluster: cluster,
		Retention: 5 * time.Minute, StaleAfter: 90 * time.Second,
		PayloadBytes: 64 << 20, MaxSeries: 4096, MaxPoints: 50000, MaxSpans: 20000,
	}
}

// Attributes contains only allowlisted scalar values. Treat its text as data.
type Attributes map[string]any

// Source preserves the admitted resource and instrumentation scope.
type Source struct {
	Resource       Attributes `json:"resource"`
	ScopeName      string     `json:"scope_name"`
	ScopeVersion   string     `json:"scope_version,omitempty"`
	ScopeAttrs     Attributes `json:"scope_attributes,omitempty"`
	InstanceKnown  bool       `json:"instance_identity_known"`
	RedactedFields int        `json:"redacted_fields,omitempty"`
}

// Number preserves integer observations without JSON floating-point conversion.
type Number struct {
	Int    *int64   `json:"int,omitempty,string"`
	Double *float64 `json:"double,omitempty"`
}

// Histogram retains explicit distributions without manufacturing percentiles.
type Histogram struct {
	Count        uint64    `json:"count,string"`
	Sum          *float64  `json:"sum,omitempty"`
	Bounds       []float64 `json:"bounds"`
	BucketCounts []uint64  `json:"bucket_counts"`
	Min          *float64  `json:"min,omitempty"`
	Max          *float64  `json:"max,omitempty"`
}

// Exemplar preserves a bounded metric-to-trace association without raw attributes.
type Exemplar struct {
	Time       time.Time  `json:"event_time"`
	Number     Number     `json:"number"`
	TraceID    string     `json:"trace_id,omitempty"`
	SpanID     string     `json:"span_id,omitempty"`
	Attributes Attributes `json:"attributes,omitempty"`
	Redacted   int        `json:"redacted_fields,omitempty"`
}

// Point is an immutable admitted metric observation.
type Point struct {
	SeriesID         string     `json:"series_id"`
	Source           Source     `json:"source"`
	Name             string     `json:"name"`
	Unit             string     `json:"unit"`
	Kind             string     `json:"type"`
	Temporality      string     `json:"temporality,omitempty"`
	Monotonic        bool       `json:"monotonic,omitempty"`
	Attributes       Attributes `json:"attributes,omitempty"`
	Start            *time.Time `json:"start_time,omitempty"`
	Time             time.Time  `json:"event_time"`
	Arrival          time.Time  `json:"arrival_time"`
	Number           *Number    `json:"number,omitempty"`
	Histogram        *Histogram `json:"histogram,omitempty"`
	NoRecorded       bool       `json:"no_recorded_value,omitempty"`
	Redacted         int        `json:"redacted_fields,omitempty"`
	Late             bool       `json:"delayed_arrival,omitempty"`
	Exemplars        []Exemplar `json:"exemplars,omitempty"`
	OmittedExemplars int        `json:"omitted_exemplars,omitempty"`
}

// Span omits event bodies, links, status messages, and unrestricted names.
type Span struct {
	TraceID       string     `json:"trace_id"`
	SpanID        string     `json:"span_id"`
	ParentID      string     `json:"parent_span_id,omitempty"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	Source        Source     `json:"source"`
	Attributes    Attributes `json:"attributes,omitempty"`
	Start         time.Time  `json:"start_time"`
	End           time.Time  `json:"end_time"`
	Arrival       time.Time  `json:"arrival_time"`
	Sampled       bool       `json:"sampled"`
	Redacted      int        `json:"redacted_fields,omitempty"`
	RemovedEvents int        `json:"removed_events,omitempty"`
	RemovedLinks  int        `json:"removed_links,omitempty"`
	Late          bool       `json:"delayed_arrival,omitempty"`
}

// Outcome describes only the batch delivered to this receiver.
type Outcome struct {
	Accepted   int64
	Rejected   int64
	Duplicates int64
	Reasons    map[string]int64
}

// SignalStatus distinguishes export arrival from event freshness.
type SignalStatus struct {
	Batches             uint64     `json:"batches"`
	Accepted            uint64     `json:"accepted"`
	Rejected            uint64     `json:"rejected"`
	LastArrival         *time.Time `json:"last_arrival,omitempty"`
	LastAcceptedArrival *time.Time `json:"last_accepted_arrival,omitempty"`
	LatestEvent         *time.Time `json:"latest_event,omitempty"`
}

// Counters are capture-session totals, including observations no longer retained.
type Counters struct {
	Metrics             SignalStatus      `json:"metrics"`
	Traces              SignalStatus      `json:"traces"`
	Reasons             map[string]uint64 `json:"rejection_reasons"`
	TransportErrors     map[string]uint64 `json:"transport_errors"`
	DuplicateSpans      uint64            `json:"duplicate_spans"`
	ConflictingSpans    uint64            `json:"conflicting_spans"`
	ExpiredPoints       uint64            `json:"expired_points"`
	ExpiredSpans        uint64            `json:"expired_spans"`
	CapacityEvictions   uint64            `json:"capacity_evictions"`
	RedactedFields      uint64            `json:"redacted_fields"`
	RemovedEvents       uint64            `json:"removed_span_events"`
	RemovedLinks        uint64            `json:"removed_span_links"`
	UpstreamDropsStated uint64            `json:"upstream_drops_reported"`
	OmittedExemplars    uint64            `json:"omitted_metric_exemplars"`
}

// MetricInfo is an inventory of retained observations, not an assertion of coverage.
type MetricInfo struct {
	Name        string    `json:"name"`
	Kind        string    `json:"type"`
	Unit        string    `json:"unit"`
	Temporality string    `json:"temporality,omitempty"`
	Points      int       `json:"points"`
	LatestEvent time.Time `json:"latest_event"`
}

// Status describes the capture's actual bounds and observed sources.
type Status struct {
	Started         time.Time    `json:"started"`
	RetentionSecs   int          `json:"retention_seconds"`
	StaleAfterSecs  int          `json:"stale_after_seconds"`
	PayloadBytes    int          `json:"retained_payload_bytes"`
	PayloadLimit    int          `json:"payload_limit_bytes"`
	Points          int          `json:"retained_points"`
	PointLimit      int          `json:"point_limit"`
	Spans           int          `json:"retained_spans"`
	SpanLimit       int          `json:"span_limit"`
	Series          int          `json:"retained_series"`
	SeriesLimit     int          `json:"series_limit"`
	Counters        Counters     `json:"counters"`
	MetricInventory []MetricInfo `json:"metric_inventory"`
	Sources         []Source     `json:"sources"`
}

// Aggregate never crosses series identities, reset epochs, or incomplete intervals.
type Aggregate struct {
	SeriesID  string     `json:"series_id"`
	Kind      string     `json:"type"`
	Start     *time.Time `json:"covered_start,omitempty"`
	End       *time.Time `json:"covered_end,omitempty"`
	Intervals int        `json:"intervals"`
	Value     *Number    `json:"value,omitempty"`
	Histogram *Histogram `json:"histogram,omitempty"`
	Warnings  []string   `json:"warnings,omitempty"`
}

// Trace reports relationships in received spans without asserting completeness.
type Trace struct {
	ID                string    `json:"trace_id"`
	Spans             []Span    `json:"spans"`
	ReceivedSpans     int       `json:"retained_span_count"`
	LatestEvent       time.Time `json:"latest_event"`
	RootObserved      bool      `json:"root_observed"`
	MissingParents    []string  `json:"missing_parent_ids"`
	ConflictingSpans  int       `json:"conflicting_duplicates"`
	Complete          bool      `json:"capture_complete"`
	ResponseTruncated bool      `json:"response_truncated"`
}

// Reply is the bounded data envelope shared by the four read-only tools.
type Reply struct {
	CaptureID  string      `json:"capture_session_id"`
	Namespace  string      `json:"namespace"`
	Cluster    string      `json:"cluster"`
	Now        time.Time   `json:"as_of"`
	Since      *time.Time  `json:"since,omitempty"`
	Until      *time.Time  `json:"until,omitempty"`
	Warnings   []string    `json:"warnings"`
	Truncated  bool        `json:"truncated"`
	Omitted    int         `json:"omitted_items"`
	Status     *Status     `json:"status,omitempty"`
	Points     []Point     `json:"points,omitempty"`
	Aggregates []Aggregate `json:"aggregates,omitempty"`
	Traces     []Trace     `json:"traces,omitempty"`
}
