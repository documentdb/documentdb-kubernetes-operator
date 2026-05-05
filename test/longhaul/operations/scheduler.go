// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package operations implements the operation scheduler and individual
// disruptive operations for long haul tests.
package operations

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
)

// Operation defines the interface for a disruptive operation.
type Operation interface {
	// Name returns a human-readable identifier for this operation.
	Name() string

	// Weight returns the relative probability of selection (higher = more likely).
	Weight() int

	// Precondition checks if the operation can be executed in the current state.
	Precondition(ctx context.Context) (bool, string)

	// Execute performs the operation and returns when complete.
	Execute(ctx context.Context) error

	// OutagePolicy returns the disruption budget for this operation.
	OutagePolicy() journal.OutagePolicy
}

// Scheduler selects and executes operations based on weighted random selection,
// preconditions, cooldowns, and steady-state gates.
type Scheduler struct {
	operations    []Operation
	healthMonitor *monitor.HealthMonitor
	journal       *journal.Journal
	cooldown      time.Duration

	mu           sync.Mutex
	lastOpTime   time.Time
	opsExecuted  int
	inProgress   bool
}

// NewScheduler creates an operation scheduler.
func NewScheduler(
	ops []Operation,
	health *monitor.HealthMonitor,
	j *journal.Journal,
	cooldown time.Duration,
) *Scheduler {
	return &Scheduler{
		operations:    ops,
		healthMonitor: health,
		journal:       j,
		cooldown:      cooldown,
	}
}

// Run starts the scheduler loop. It blocks until context is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.journal.Info("scheduler", "operation scheduler started")
	defer s.journal.Info("scheduler", "operation scheduler stopped")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tryExecute(ctx)
		}
	}
}

func (s *Scheduler) tryExecute(ctx context.Context) {
	s.mu.Lock()
	if s.inProgress {
		s.mu.Unlock()
		return
	}

	// Check cooldown.
	if !s.lastOpTime.IsZero() && time.Since(s.lastOpTime) < s.cooldown {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// Check steady-state gate.
	if !s.healthMonitor.IsSteadyState() {
		return
	}

	// Select an operation.
	op := s.selectOperation(ctx)
	if op == nil {
		return
	}

	// Execute.
	s.mu.Lock()
	s.inProgress = true
	s.mu.Unlock()

	s.executeOp(ctx, op)

	s.mu.Lock()
	s.inProgress = false
	s.lastOpTime = time.Now()
	s.opsExecuted++
	s.mu.Unlock()
}

func (s *Scheduler) selectOperation(ctx context.Context) Operation {
	// Filter by preconditions and build weighted list.
	type candidate struct {
		op     Operation
		weight int
	}
	var candidates []candidate
	totalWeight := 0

	for _, op := range s.operations {
		ok, _ := op.Precondition(ctx)
		if ok {
			w := op.Weight()
			candidates = append(candidates, candidate{op: op, weight: w})
			totalWeight += w
		}
	}

	if len(candidates) == 0 || totalWeight == 0 {
		return nil
	}

	// Weighted random selection.
	r := rand.Intn(totalWeight)
	for _, c := range candidates {
		r -= c.weight
		if r < 0 {
			return c.op
		}
	}
	return candidates[len(candidates)-1].op
}

func (s *Scheduler) executeOp(ctx context.Context, op Operation) {
	s.journal.Info("scheduler", fmt.Sprintf("executing operation: %s", op.Name()))
	s.journal.OpenDisruptionWindow(op.Name(), op.OutagePolicy())

	err := op.Execute(ctx)

	s.journal.CloseDisruptionWindow()

	if err != nil {
		s.journal.Error("scheduler", fmt.Sprintf("operation %s failed: %v", op.Name(), err))
	} else {
		s.journal.Info("scheduler", fmt.Sprintf("operation %s completed successfully", op.Name()))
	}
}

// OpsExecuted returns the number of operations completed.
func (s *Scheduler) OpsExecuted() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opsExecuted
}
