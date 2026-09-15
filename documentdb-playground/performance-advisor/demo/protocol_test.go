// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package demo

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/capture"
	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/internal/server"
)

func preparedRun(t *testing.T) (string, Run, *capture.Store) {
	t.Helper()
	store, err := capture.New(capture.DefaultConfig(Namespace, Cluster))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.NewHTTPHandler(store))
	t.Cleanup(upstream.Close)
	dir := filepath.Join(t.TempDir(), "run-"+strings.Repeat("a", 32))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(t.Context(), dir, upstream.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
	run, err := LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, run, store
}

func coordinator(t *testing.T, dir, captureID string) *Coordinator {
	t.Helper()
	gate, err := OpenCoordinator(t.Context(), dir, captureID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := gate.Close(); err != nil {
			t.Error(err)
		}
	})
	return gate
}

func saveProof(t *testing.T, dir string, proof Proof) {
	t.Helper()
	if err := writeJSON(filepath.Join(dir, "observer.json"), proof); err != nil {
		t.Fatal(err)
	}
}

func TestPreparationDoesNotImpersonateObserver(t *testing.T) {
	dir, run, _ := preparedRun(t)
	if _, err := os.Stat(filepath.Join(dir, "observer.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation must not pass readiness: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	gate, err := OpenCoordinator(ctx, dir, run.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if err := gate.WaitReady(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing observer should fail with a bounded deadline: %v", err)
	}
}

func TestReadinessRejectsStaleForeignAndDisconnectedProof(t *testing.T) {
	for _, name := range []string{"stale", "foreign-run", "foreign-capture", "disconnected", "future"} {
		t.Run(name, func(t *testing.T) {
			dir, run, _ := preparedRun(t)
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			gate, err := OpenCoordinator(ctx, dir, run.CaptureID)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			now := time.Now()
			proof := Proof{RunID: run.ID, CaptureID: run.CaptureID, Ready: now, LastRead: now, Calls: 1}
			switch name {
			case "stale":
				proof.LastRead = now.Add(-time.Minute)
			case "foreign-run":
				proof.RunID = strings.Repeat("b", 32)
			case "foreign-capture":
				proof.CaptureID = "another-capture"
			case "disconnected":
				proof.Disconnected = true
			case "future":
				proof.Ready = now.Add(time.Minute)
			}
			saveProof(t, dir, proof)
			if err := gate.WaitReady(); err == nil {
				t.Fatal("invalid readiness proof passed")
			}
		})
	}
}

func TestTrialRequiresBothDetailedReadsInsideWindow(t *testing.T) {
	for _, name := range []string{"both", "missing-metric", "missing-trace", "retrospective", "other-trial", "disconnected"} {
		t.Run(name, func(t *testing.T) {
			dir, run, _ := preparedRun(t)
			gate := coordinator(t, dir, run.CaptureID)
			now := time.Now()
			proof := Proof{RunID: run.ID, CaptureID: run.CaptureID, Ready: now, LastRead: now, Calls: 4}
			saveProof(t, dir, proof)
			if err := gate.WaitReady(); err != nil {
				t.Fatal(err)
			}
			start := now.Add(-TrialDuration - time.Second)
			if err := gate.BeginTrial("trial-1", start); err != nil {
				t.Fatal(err)
			}
			if gate.run.Trial.Deadline.Sub(start) != TrialDuration {
				t.Fatal("demo duration changed")
			}
			observation := Observation{
				TrialID: "trial-1", MetricRead: start.Add(30 * time.Second), TraceRead: start.Add(40 * time.Second),
			}
			switch name {
			case "missing-metric":
				observation.MetricRead = time.Time{}
			case "missing-trace":
				observation.TraceRead = time.Time{}
			case "retrospective":
				observation.MetricRead = now
			case "other-trial":
				observation.TrialID = "trial-2"
			case "disconnected":
				proof.Disconnected = true
			}
			proof.Observations = []Observation{observation}
			saveProof(t, dir, proof)
			err := gate.EndTrial(now, 900)
			if (err == nil) != (name == "both") {
				t.Fatalf("trial gate result: %v", err)
			}
		})
	}
}

func TestCancelledAndMalformedRunsFailClosed(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		dir, run, _ := preparedRun(t)
		gate := coordinator(t, dir, run.CaptureID)
		if err := os.WriteFile(filepath.Join(dir, "cancel"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		select {
		case <-gate.Context().Done():
			if !strings.Contains(context.Cause(gate.Context()).Error(), "cancelled") {
				t.Fatal(context.Cause(gate.Context()))
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not stop the workload context")
		}
	})
	for _, name := range []string{"expired", "remote-endpoint", "foreign-id", "oversized", "symlink"} {
		t.Run(name, func(t *testing.T) {
			dir, run, _ := preparedRun(t)
			switch name {
			case "expired":
				run.Created = time.Now().Add(-2 * RunTimeout)
				run.Expires = run.Created.Add(RunTimeout)
			case "remote-endpoint":
				run.Endpoint = "http://example.invalid:8080/mcp"
			case "foreign-id":
				run.ID = strings.Repeat("b", 32)
			}
			if err := writeJSON(filepath.Join(dir, "run.json"), run); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "oversized":
				if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(strings.Repeat(" ", maxStateBytes+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(filepath.Join(dir, "run.json"), filepath.Join(dir, "saved.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("saved.json", filepath.Join(dir, "run.json")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := LoadRun(dir); err == nil {
				t.Fatal("unsafe run metadata was accepted")
			}
		})
	}
}
