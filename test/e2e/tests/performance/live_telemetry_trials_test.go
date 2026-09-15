// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package performance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/documentdb/documentdb-kubernetes-operator/documentdb-playground/performance-advisor/demo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"
	driver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	wire "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	sharedwire "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

var _ = Describe("Live telemetry observation trials",
	Label(e2e.PerformanceLabel, e2e.SlowLabel, "live-telemetry-trials"), Serial, func() {
		It("records three neutral operation-level observation windows", func(ctx SpecContext) {
			requireTelemetryEnvironment(ctx)
			session, err := openTelemetryClient(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(session.Close()).To(Succeed()) })
			initial, err := telemetryTool(ctx, session, "get_capture_status", nil)
			Expect(err).NotTo(HaveOccurred())
			var gate *demo.Coordinator
			var workCtx context.Context = ctx
			duration := 45 * time.Second
			if dir := os.Getenv("E2E_LIVE_TELEMETRY_DEMO_DIR"); dir != "" {
				gate, err = demo.OpenCoordinator(ctx, dir, initial.CaptureID)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { Expect(gate.Close()).To(Succeed()) })
				Expect(gate.WaitReady()).To(Succeed(), "a real observer must read status before workload setup")
				workCtx, duration = gate.Context(), demo.TrialDuration
			}
			handle, err := wire.NewFromDocumentDB(workCtx, e2e.SuiteEnv(), telemetryNamespace, telemetryCluster)
			Expect(err).NotTo(HaveOccurred())
			database := fixtures.DBNameFor("live-telemetry-observation-trials")
			DeferCleanup(func(cleanupCtx SpecContext) {
				Expect(sharedwire.DropDatabase(cleanupCtx, handle.Client(), database)).To(Succeed())
				Expect(handle.Close(cleanupCtx)).To(Succeed())
			})
			const count = 30000
			docs := make([]bson.M, count)
			for i := range docs {
				docs[i] = bson.M{"_id": i, "value": i, "padding": strings.Repeat("x", 128)}
			}
			inserted, err := sharedwire.Seed(workCtx, handle.Client(), database, "observations", docs)
			Expect(err).NotTo(HaveOccurred())
			Expect(inserted).To(Equal(count))
			collection := handle.Database(database).Collection("observations")
			index := driver.IndexModel{
				Keys: bson.D{{Key: "value", Value: 1}}, Options: options.Index().SetName("idx_live_value"),
			}
			_, err = collection.Indexes().CreateOne(workCtx, index)
			Expect(err).NotTo(HaveOccurred())
			for trial, indexed := range []bool{true, false, true} {
				if trial == 1 {
					Expect(collection.Indexes().DropOne(workCtx, "idx_live_value")).To(Succeed())
				}
				if trial == 2 {
					_, err = collection.Indexes().CreateOne(workCtx, index)
					Expect(err).NotTo(HaveOccurred())
				}
				cursor, err := collection.Indexes().List(workCtx)
				Expect(err).NotTo(HaveOccurred())
				var indexes []bson.M
				Expect(cursor.All(workCtx, &indexes)).To(Succeed())
				hasIndex := false
				for _, item := range indexes {
					if item["name"] == "idx_live_value" {
						hasIndex = true
					}
				}
				Expect(hasIndex).To(Equal(indexed))
				id := fmt.Sprintf("trial-%d", trial+1)
				start := time.Now()
				if gate != nil {
					Expect(gate.BeginTrial(id, start)).To(Succeed())
				}
				fmt.Fprintf(GinkgoWriter, "live-telemetry-trial id=%s start=%s nominal_duration=%s capture=%s\n",
					id, start.UTC().Format(time.RFC3339Nano), duration, initial.CaptureID)
				ticker := time.NewTicker(100 * time.Millisecond)
				operations := 0
				for time.Since(start) < duration {
					var result bson.M
					Expect(collection.FindOne(workCtx, bson.M{"value": count - 1}).Decode(&result)).To(Succeed())
					operations++
					select {
					case <-workCtx.Done():
						ticker.Stop()
						Fail("trial exceeded its context deadline")
					case <-ticker.C:
					}
				}
				ticker.Stop()
				ended := time.Now()
				if gate != nil {
					Expect(gate.EndTrial(ended, operations)).To(Succeed())
				}
				view, err := telemetryTool(ctx, session, "get_recent_traces", map[string]any{
					"lookback_seconds": 30, "limit": 20,
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(view.CaptureID).To(Equal(initial.CaptureID))
				type evidence struct {
					Trial       string             `json:"trial"`
					CaptureID   string             `json:"capture_session_id"`
					Start       time.Time          `json:"start"`
					End         time.Time          `json:"end"`
					Operations  int                `json:"harness_operations"`
					TraceID     string             `json:"example_trace_id"`
					PhaseMillis map[string]float64 `json:"example_span_elapsed_ms"`
					Warnings    []string           `json:"coverage_warnings"`
				}
				summary := evidence{
					Trial: id, CaptureID: initial.CaptureID, Start: start.UTC(), End: ended.UTC(),
					Operations: operations, Warnings: view.Warnings,
				}
				for _, trace := range view.Traces {
					for _, span := range trace.Spans {
						operation, _ := span.Attributes["db.operation.name"].(string)
						if span.Name == "gateway.request" && strings.EqualFold(operation, "find") &&
							!span.Start.Before(start) && !span.End.After(ended) {
							detail, err := telemetryTool(ctx, session, "get_trace", map[string]any{"trace_id": trace.ID, "limit": 100})
							Expect(err).NotTo(HaveOccurred())
							Expect(detail.Traces).To(HaveLen(1))
							summary.TraceID = trace.ID
							summary.PhaseMillis = make(map[string]float64)
							for _, observed := range detail.Traces[0].Spans {
								summary.PhaseMillis[observed.Name] = float64(observed.End.Sub(observed.Start)) / float64(time.Millisecond)
							}
							break
						}
					}
					if summary.TraceID != "" {
						break
					}
				}
				Expect(summary.TraceID).NotTo(BeEmpty(), "each trial needs a received request trace")
				data, err := json.Marshal(summary)
				Expect(err).NotTo(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "live-telemetry-evidence %s\n", data)
			}
		}, SpecTimeout(12*time.Minute))
	})
