// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package longhaul

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/documentdb/documentdb-operator/test/longhaul/config"
	"github.com/documentdb/documentdb-operator/test/longhaul/journal"
	"github.com/documentdb/documentdb-operator/test/longhaul/monitor"
	"github.com/documentdb/documentdb-operator/test/longhaul/operations"
	"github.com/documentdb/documentdb-operator/test/longhaul/report"
	"github.com/documentdb/documentdb-operator/test/longhaul/workload"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var testConfig config.Config

var _ = BeforeSuite(func() {
	if !config.IsEnabled() {
		Skip("Long haul tests are disabled. Set LONGHAUL_ENABLED=true to run.")
	}

	var err error
	testConfig, err = config.LoadFromEnv()
	Expect(err).NotTo(HaveOccurred(), "Failed to load long haul config from environment")

	err = testConfig.Validate()
	Expect(err).NotTo(HaveOccurred(), "Invalid long haul config")

	GinkgoWriter.Printf("Long haul test config:\n")
	GinkgoWriter.Printf("  MaxDuration:     %s\n", testConfig.MaxDuration)
	GinkgoWriter.Printf("  Namespace:       %s\n", testConfig.Namespace)
	GinkgoWriter.Printf("  ClusterName:     %s\n", testConfig.ClusterName)
	GinkgoWriter.Printf("  MongoURI:        %s\n", maskURI(testConfig.MongoURI))
	GinkgoWriter.Printf("  NumWriters:      %d\n", testConfig.NumWriters)
	GinkgoWriter.Printf("  NumVerifiers:    %d\n", testConfig.NumVerifiers)
	GinkgoWriter.Printf("  OpCooldown:      %s\n", testConfig.OpCooldown)
	GinkgoWriter.Printf("  RecoveryTimeout: %s\n", testConfig.RecoveryTimeout)
})

var _ = Describe("Long Haul Test", func() {
	It("should maintain data integrity through continuous operations", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Apply max duration timeout if configured.
		if testConfig.MaxDuration > 0 {
			var timeoutCancel context.CancelFunc
			ctx, timeoutCancel = context.WithTimeout(ctx, testConfig.MaxDuration)
			defer timeoutCancel()
		}

		// Initialize components.
		j := journal.New()
		metrics := workload.NewMetrics()

		// Connect to MongoDB.
		Expect(testConfig.MongoURI).NotTo(BeEmpty(), "LONGHAUL_MONGO_URI must be set")
		mongoClient, err := mongo.Connect(options.Client().ApplyURI(testConfig.MongoURI))
		Expect(err).NotTo(HaveOccurred(), "Failed to connect to MongoDB")
		defer func() {
			disconnectCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = mongoClient.Disconnect(disconnectCtx)
		}()

		db := mongoClient.Database("longhaul")

		// Create indexes.
		err = workload.EnsureIndexes(ctx, db)
		Expect(err).NotTo(HaveOccurred(), "Failed to create indexes")

		j.Info("main", "long haul test starting")

		// Initialize cluster client (placeholder — real implementation connects to k8s).
		clusterClient := newPlaceholderClusterClient(testConfig)

		// Start health monitor.
		healthMon := monitor.NewHealthMonitor(clusterClient, j, testConfig.SteadyStateWait)
		go healthMon.Run(ctx)

		// Start leak detector.
		leakDetector := monitor.NewLeakDetector(j, 10.0, 10) // 10 MB/hour threshold

		// Start writers.
		workload.StartWriters(ctx, testConfig.NumWriters, db, metrics, j)
		j.Info("main", fmt.Sprintf("started %d writers", testConfig.NumWriters))

		// Start verifiers.
		workload.StartVerifiers(ctx, testConfig.NumVerifiers, db, metrics, j)
		j.Info("main", fmt.Sprintf("started %d verifiers", testConfig.NumVerifiers))

		// Configure operations.
		ops := []operations.Operation{
			operations.NewScaleUp(clusterClient, healthMon, testConfig.MaxReplicas, testConfig.RecoveryTimeout),
			operations.NewScaleDown(clusterClient, healthMon, testConfig.MinReplicas, testConfig.RecoveryTimeout),
		}

		// Start operation scheduler.
		scheduler := operations.NewScheduler(ops, healthMon, j, testConfig.OpCooldown)
		go scheduler.Run(ctx)

		j.Info("main", "all components started, entering main loop")

		// Main loop: wait for context expiry (timeout or cancellation).
		<-ctx.Done()

		j.Info("main", fmt.Sprintf("test ending: %v", ctx.Err()))

		// Generate report.
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

		summary := report.Summary{
			Result:       result,
			Duration:     snap.Elapsed,
			Metrics:      snap,
			LeakAnalysis: leakAnalysis,
			OpsExecuted:  scheduler.OpsExecuted(),
			Windows:      j.DisruptionWindows(),
			Events:       j.Events(),
			FailReason:   failReason,
		}

		markdown := report.GenerateMarkdown(summary)
		GinkgoWriter.Println("\n" + markdown)

		// Assert pass.
		Expect(result).To(Equal(report.ResultPass), "Long haul test failed: %s", failReason)
	})
})

// maskURI hides credentials in a MongoDB URI for logging.
func maskURI(uri string) string {
	if uri == "" {
		return "<not set>"
	}
	if len(uri) > 20 {
		return uri[:20] + "..."
	}
	return uri
}

// placeholderClusterClient is a minimal implementation of monitor.ClusterClient
// that returns safe defaults. It will be replaced with a real k8s client in Phase 2.
type placeholderClusterClient struct {
	replicas int
}

func newPlaceholderClusterClient(cfg config.Config) *placeholderClusterClient {
	return &placeholderClusterClient{replicas: cfg.MinReplicas}
}

func (p *placeholderClusterClient) GetClusterHealth(_ context.Context) (monitor.ClusterHealth, error) {
	return monitor.ClusterHealth{
		Timestamp:     time.Now(),
		AllPodsReady:  true,
		ReadyPods:     p.replicas,
		TotalPods:     p.replicas,
		CRReady:       true,
		RestartCount:  0,
		WritesHealthy: true,
	}, nil
}

func (p *placeholderClusterClient) GetCurrentReplicas(_ context.Context) (int, error) {
	return p.replicas, nil
}

func (p *placeholderClusterClient) ScaleCluster(_ context.Context, replicas int) error {
	p.replicas = replicas
	return nil
}
