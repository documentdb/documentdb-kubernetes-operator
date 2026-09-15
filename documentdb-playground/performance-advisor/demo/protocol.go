// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package demo coordinates a bounded synthetic workload and a read-only observer.
package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The demo has fixed scope and finite readiness, trial, and session budgets.
const (
	Namespace     = "live-telemetry"
	Cluster       = "telemetry-db"
	TrialDuration = 90 * time.Second
	ReadyTimeout  = 3 * time.Minute
	RunTimeout    = 12 * time.Minute
	maxStateBytes = 64 << 10
)

var runIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Trial contains neutral harness boundaries, never a fault label.
type Trial struct {
	ID         string    `json:"id"`
	Start      time.Time `json:"start"`
	Deadline   time.Time `json:"deadline"`
	End        time.Time `json:"end,omitempty"`
	Operations int       `json:"operations,omitempty"`
}

// Run is written only by the workload coordinator.
type Run struct {
	Version   int       `json:"version"`
	ID        string    `json:"run_id"`
	CaptureID string    `json:"capture_session_id"`
	Endpoint  string    `json:"endpoint"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	Phase     string    `json:"phase"`
	Trial     *Trial    `json:"trial,omitempty"`
	Trials    []Trial   `json:"completed_trials"`
}

// Observation is compact proof of successful in-window MCP reads, not a raw archive.
type Observation struct {
	TrialID     string    `json:"trial_id"`
	MetricRead  time.Time `json:"metric_read_at,omitempty"`
	MetricStart time.Time `json:"metric_start,omitempty"`
	MetricEvent time.Time `json:"metric_event,omitempty"`
	MetricName  string    `json:"metric_name,omitempty"`
	MetricValue string    `json:"metric_value,omitempty"`
	SeriesID    string    `json:"series_id,omitempty"`
	TraceRead   time.Time `json:"trace_read_at,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	SpanID      string    `json:"request_span_id,omitempty"`
	SpanStart   time.Time `json:"request_start,omitempty"`
	SpanEnd     time.Time `json:"request_end,omitempty"`
}

// Proof is written only by the observer adapter after successful upstream reads.
type Proof struct {
	RunID        string        `json:"run_id"`
	CaptureID    string        `json:"capture_session_id"`
	Ready        time.Time     `json:"ready_at"`
	LastRead     time.Time     `json:"last_read_at"`
	Disconnected bool          `json:"disconnected"`
	Calls        int           `json:"calls"`
	Observations []Observation `json:"observations"`
}

// Report persists only schedule metadata and selected read evidence.
type Report struct {
	Run       Run    `json:"run"`
	Observer  Proof  `json:"observer"`
	Completed bool   `json:"completed"`
	Scope     string `json:"scope"`
}

func readJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return fmt.Errorf("demo state must be a bounded regular file: %s", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode demo state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("demo state contains trailing data")
	}
	return nil
}

func writeJSON(path string, value any) (err error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxStateBytes {
		return fmt.Errorf("demo state exceeds its byte limit")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func validateDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	base := filepath.Base(dir)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		!strings.HasPrefix(base, "run-") || !runIDPattern.MatchString(strings.TrimPrefix(base, "run-")) {
		return fmt.Errorf("expected a private run-<32 hex characters> directory")
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("demo run directory must not be accessible to other users")
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Path != "/mcp" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("demo upstream must be a numeric loopback HTTP /mcp endpoint")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("demo upstream must be a numeric loopback HTTP /mcp endpoint")
	}
	return nil
}

// LoadRun rejects expired, foreign, or malformed run metadata.
func LoadRun(dir string) (Run, error) {
	var run Run
	if err := validateDirectory(dir); err != nil {
		return run, err
	}
	if err := readJSON(filepath.Join(dir, "run.json"), &run); err != nil {
		return run, err
	}
	if run.Version != 1 || !runIDPattern.MatchString(run.ID) ||
		filepath.Base(dir) != "run-"+run.ID || run.CaptureID == "" ||
		run.Created.IsZero() || !run.Expires.After(run.Created) ||
		run.Expires.Sub(run.Created) > RunTimeout || run.Created.After(time.Now().Add(time.Second)) {
		return run, fmt.Errorf("invalid demo run identity or lifetime")
	}
	if !time.Now().Before(run.Expires) {
		return run, fmt.Errorf("demo run expired")
	}
	if err := validateEndpoint(run.Endpoint); err != nil {
		return run, err
	}
	switch run.Phase {
	case "waiting_for_observer", "active", "between_trials", "completed", "failed":
	default:
		return run, fmt.Errorf("unknown demo phase")
	}
	return run, nil
}

func readProof(dir string, run Run) (Proof, error) {
	var proof Proof
	if err := readJSON(filepath.Join(dir, "observer.json"), &proof); err != nil {
		return proof, err
	}
	if proof.RunID != run.ID || proof.CaptureID != run.CaptureID ||
		proof.Calls < 1 || proof.Calls > 128 || len(proof.Observations) > 3 {
		return proof, fmt.Errorf("observer proof belongs to another run or exceeds its bounds")
	}
	return proof, nil
}

// Coordinator owns schedule changes and cancellation for one E2E invocation.
type Coordinator struct {
	dir    string
	run    Run
	ctx    context.Context
	cancel context.CancelCauseFunc
}

// OpenCoordinator binds the workload to an already prepared capture.
func OpenCoordinator(ctx context.Context, dir, captureID string) (*Coordinator, error) {
	run, err := LoadRun(dir)
	if err != nil {
		return nil, err
	}
	if run.CaptureID != captureID || run.Phase != "waiting_for_observer" {
		return nil, fmt.Errorf("demo capture changed or this run was already used")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	coordinator := &Coordinator{dir: dir, run: run, ctx: ctx, cancel: cancel}
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !time.Now().Before(run.Expires) {
					cancel(fmt.Errorf("demo lifetime exceeded"))
					return
				}
				if _, err := os.Lstat(filepath.Join(dir, "cancel")); err == nil {
					cancel(fmt.Errorf("demo cancelled"))
					return
				} else if !errors.Is(err, os.ErrNotExist) {
					cancel(fmt.Errorf("check demo cancellation: %w", err))
					return
				}
				proof, err := readProof(dir, run)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					cancel(err)
					return
				}
				if proof.Disconnected {
					cancel(fmt.Errorf("observer disconnected"))
					return
				}
			}
		}
	}()
	return coordinator, nil
}

// Context is cancelled if the observer disconnects or the run is stopped.
func (c *Coordinator) Context() context.Context { return c.ctx }

// Close stops the monitor and marks incomplete runs explicitly.
func (c *Coordinator) Close() error {
	c.cancel(context.Canceled)
	if c.run.Phase != "completed" {
		c.run.Phase = "failed"
		return writeJSON(filepath.Join(c.dir, "run.json"), c.run)
	}
	return nil
}

// WaitReady requires a fresh successful status read before any workload setup.
func (c *Coordinator) WaitReady() error {
	ctx, cancel := context.WithTimeout(c.ctx, ReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		proof, err := readProof(c.dir, c.run)
		if err == nil && !proof.Disconnected && !proof.Ready.Before(c.run.Created) &&
			!proof.Ready.After(time.Now()) && !proof.LastRead.Before(proof.Ready) &&
			!proof.LastRead.After(time.Now()) && time.Since(proof.LastRead) <= 30*time.Second {
			return nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("observer readiness gate: %w", context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// BeginTrial publishes neutral boundaries before the first measured lookup.
func (c *Coordinator) BeginTrial(id string, start time.Time) error {
	if err := c.ctx.Err(); err != nil {
		return context.Cause(c.ctx)
	}
	if len(c.run.Trials) >= 3 || id != fmt.Sprintf("trial-%d", len(c.run.Trials)+1) ||
		(c.run.Phase != "waiting_for_observer" && c.run.Phase != "between_trials") {
		return fmt.Errorf("invalid demo trial sequence")
	}
	c.run.Phase = "active"
	c.run.Trial = &Trial{ID: id, Start: start.UTC(), Deadline: start.Add(TrialDuration).UTC()}
	return writeJSON(filepath.Join(c.dir, "run.json"), c.run)
}

// EndTrial requires detailed metric and trace reads completed inside the trial.
func (c *Coordinator) EndTrial(end time.Time, operations int) error {
	if err := c.ctx.Err(); err != nil {
		return context.Cause(c.ctx)
	}
	trial := c.run.Trial
	if trial == nil || c.run.Phase != "active" || end.Before(trial.Deadline) || operations < 1 {
		return fmt.Errorf("demo trial did not run for its full bounded window")
	}
	proof, err := readProof(c.dir, c.run)
	if err != nil {
		return err
	}
	if proof.Disconnected {
		return fmt.Errorf("observer disconnected during the trial")
	}
	found := false
	for _, observation := range proof.Observations {
		if observation.TrialID == trial.ID &&
			inWindow(observation.MetricRead, trial) && inWindow(observation.TraceRead, trial) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%s lacks successful in-window metric and single-trace reads; retrospective reads do not pass", trial.ID)
	}
	trial.End, trial.Operations = end.UTC(), operations
	c.run.Trials = append(c.run.Trials, *trial)
	c.run.Trial = nil
	c.run.Phase = "between_trials"
	if len(c.run.Trials) == 3 {
		c.run.Phase = "completed"
	}
	return writeJSON(filepath.Join(c.dir, "run.json"), c.run)
}

func inWindow(at time.Time, trial *Trial) bool {
	return !at.Before(trial.Start) && at.Before(trial.Deadline)
}

// SaveReport preserves selected evidence without retaining all tool responses.
func SaveReport(dir, path string) error {
	run, err := LoadRun(dir)
	if err != nil {
		return err
	}
	proof, err := readProof(dir, run)
	if err != nil {
		return err
	}
	return writeJSON(path, Report{
		Run: run, Observer: proof, Completed: run.Phase == "completed" && len(run.Trials) == 3,
		Scope: "Client readiness and successful live metric/trace reads, not proof of diagnosis correctness or complete telemetry.",
	})
}
