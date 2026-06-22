// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

// Package main provides a standalone binary entry point for running
// long haul tests as a Kubernetes Deployment (without Ginkgo test framework).
// A Deployment is used (not a Job) so the kubelet auto-restarts the driver
// pod on crash; the canonical "did the test pass?" signal is the
// longhaul-report ConfigMap and the GitHub Actions annotations, not the pod
// exit status.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/documentdb/documentdb-operator/test/longhaul/config"
	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
	"github.com/documentdb/documentdb-operator/test/longhaul/operations"
	"github.com/documentdb/documentdb-operator/test/longhaul/report"
	"github.com/documentdb/documentdb-operator/test/longhaul/workload"

	shareddocdb "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[longhaul] ")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	log.Printf("config loaded: duration=%s namespace=%s cluster=%s writers=%d",
		cfg.MaxDuration, cfg.Namespace, cfg.ClusterName, cfg.NumWriters)

	exitCode := run(cfg)
	os.Exit(exitCode)
}

func run(cfg config.Config) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.MaxDuration > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, cfg.MaxDuration)
		defer timeoutCancel()
	}

	// Initialize components.
	j := journal.New()
	metrics := workload.NewMetrics()

	// Connect to DocumentDB.
	if cfg.DocumentDBURI == "" {
		log.Fatal("LONGHAUL_DOCUMENTDB_URI must be set")
	}
	docdbClient, err := shareddocdb.NewFromURI(ctx, cfg.DocumentDBURI)
	if err != nil {
		log.Fatalf("failed to connect to DocumentDB: %v", err)
	}
	defer func() {
		disconnectCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = docdbClient.Disconnect(disconnectCtx)
	}()

	// Verify connectivity.
	pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
	defer pingCancel()
	if err := docdbClient.Ping(pingCtx, nil); err != nil {
		log.Fatalf("DocumentDB ping failed: %v", err)
	}
	log.Println("DocumentDB connection established")

	db := docdbClient.Database("longhaul")

	// Optionally drop previous test data. Disabled by default so that pod
	// restarts (Deployment auto-restart on crash) preserve durability
	// history for post-mortem; opt in with LONGHAUL_RESET_DATA=true for
	// local/dev iterations or fresh CI runs.
	if cfg.ResetData {
		if err := db.Collection(workload.CollectionName).Drop(ctx); err != nil {
			log.Fatalf("failed to drop collection: %v", err)
		}
		log.Println("workload collection dropped (LONGHAUL_RESET_DATA=true)")
	}

	// Create indexes.
	if err := workload.EnsureIndexes(ctx, db); err != nil {
		log.Fatalf("failed to create indexes: %v", err)
	}

	j.Info("main", "long haul test starting")

	// Initialize real k8s cluster client. The clientset built inside
	// K8sClusterClient is reused for ConfigMap operations (reporter) below
	// instead of building a second one against the same REST config.
	clusterClient, err := monitor.NewK8sClusterClient(monitor.K8sClientConfig{
		Namespace:   cfg.Namespace,
		ClusterName: cfg.ClusterName,
		Kubeconfig:  os.Getenv("KUBECONFIG"),
	})
	if err != nil {
		log.Fatalf("failed to initialize k8s client: %v", err)
	}
	k8sClientset := clusterClient.Clientset()
	j.Info("main", "k8s client initialized")

	// Start health monitor.
	healthMon := monitor.NewHealthMonitor(clusterClient, j, cfg.SteadyStateWait)
	go healthMon.Run(ctx)

	// Start leak detector.
	leakDetector := monitor.NewLeakDetector(j, 10.0, 10)

	// Start writers.
	writers := workload.StartWriters(ctx, cfg.NumWriters, db, metrics, j)
	j.Info("main", fmt.Sprintf("started %d writers", cfg.NumWriters))

	// Start verifier. A single verifier is sufficient — see StartVerifier
	// godoc. Writers are passed so the verifier can detect tail loss by
	// comparing each writer's acked tip against what's in the DB.
	workload.StartVerifier(ctx, db, writers, metrics, j)
	j.Info("main", "verifier started")

	// Configure operations.
	ops := []operations.Operation{
		operations.NewScaleUp(clusterClient, healthMon, cfg.MaxInstances, cfg.RecoveryTimeout),
		operations.NewScaleDown(clusterClient, healthMon, cfg.MinInstances, cfg.RecoveryTimeout),
		operations.NewUpgradeDocumentDB(clusterClient, k8sClientset, healthMon, j, cfg.Namespace, cfg.RecoveryTimeout),
	}

	// Start operation scheduler.
	scheduler := operations.NewScheduler(ops, healthMon, j, cfg.OpCooldown)
	go scheduler.Run(ctx)

	// Start metrics sampling goroutine (feeds leak detector).
	go runMetricsSampling(ctx, clusterClient, leakDetector, j)

	// Start periodic checkpoint reporter.
	summaryFunc := func() report.Summary {
		return buildSummary(metrics, leakDetector, scheduler, j)
	}
	reporter := report.NewCheckpointReporter(k8sClientset, cfg.Namespace, cfg.ReportInterval, summaryFunc)
	go reporter.Run(ctx)

	j.Info("main", "all components started, entering main loop")

	// Main loop: wait for context expiry.
	<-ctx.Done()
	j.Info("main", fmt.Sprintf("test ending: %v", ctx.Err()))

	// Allow goroutines to flush.
	time.Sleep(500 * time.Millisecond)

	// Generate final report. Persist to the report ConfigMap synchronously
	// here (before os.Exit) so the authoritative verdict reaches the source
	// of truth that operators consult — the Run() goroutine cannot do this
	// reliably because os.Exit can kill it mid-Update.
	summary := buildSummary(metrics, leakDetector, scheduler, j)
	markdown := report.GenerateMarkdown(summary)
	fmt.Println("\n" + markdown)
	reporter.EmitFinal()

	// Emit final GitHub Actions annotation.
	report.EmitAnnotation(summary)

	if summary.Result == report.ResultFail {
		log.Printf("TEST FAILED: %s", summary.FailReason)
		return 1
	}

	log.Println("TEST PASSED")
	return 0
}

// buildSummary constructs a report.Summary from current state.
func buildSummary(metrics *workload.Metrics, leakDetector *monitor.LeakDetector, scheduler *operations.Scheduler, j *journal.Journal) report.Summary {
	snap := metrics.Snapshot()
	leakAnalysis := leakDetector.Analyze()

	result := report.ResultPass
	failReason := ""

	if snap.HasDataLoss() {
		result = report.ResultFail
		failReason = fmt.Sprintf("data loss: %d gaps, %d checksum errors",
			snap.GapsDetected, snap.ChecksumErrors)
	}
	if j.HasPolicyViolation() {
		result = report.ResultFail
		if failReason != "" {
			failReason += "; "
		}
		failReason += "outage policy violated"
	}

	return report.Summary{
		Result:       result,
		Duration:     snap.Elapsed,
		Metrics:      snap,
		LeakAnalysis: leakAnalysis,
		OpsExecuted:  scheduler.OpsExecuted(),
		Windows:      j.DisruptionWindows(),
		Events:       j.Events(),
		FailReason:   failReason,
	}
}

// runMetricsSampling periodically collects pod resource metrics and feeds the leak detector.
func runMetricsSampling(ctx context.Context, client *monitor.K8sClusterClient, ld *monitor.LeakDetector, j *journal.Journal) {
	if !client.MetricsAvailable() {
		j.Info("metrics", "metrics-server not available, leak detection sampling disabled")
		return
	}
	j.Info("metrics", "metrics sampling started (60s interval)")

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			podMetrics, err := client.GetPodMetrics(ctx)
			if err != nil {
				j.Warn("metrics", fmt.Sprintf("metrics query error: %v", err))
				continue
			}
			if podMetrics == nil {
				// Metrics became unavailable.
				j.Warn("metrics", "metrics-server became unavailable, stopping sampling")
				return
			}

			// Sum memory and CPU across all DocumentDB pods.
			var totalMem, totalCPU float64
			for _, pm := range podMetrics {
				totalMem += pm.MemoryMB
				totalCPU += pm.CPUCores
			}

			ld.AddSample(monitor.ResourceSample{
				Timestamp: time.Now(),
				MemoryMB:  totalMem,
				CPUCores:  totalCPU,
			})
		}
	}
}
