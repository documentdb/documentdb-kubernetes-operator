// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package performance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.mongodb.org/mongo-driver/v2/bson"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/documentdb/documentdb-operator/test/e2e"
	"github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/fixtures"
	wire "github.com/documentdb/documentdb-operator/test/e2e/pkg/e2eutils/mongo"
	sharedwire "github.com/documentdb/documentdb-operator/test/shared/mongo"
)

const telemetryNamespace = "live-telemetry"
const telemetryCluster = "telemetry-db"

type telemetryNumber struct {
	Int    *int64   `json:"int,string"`
	Double *float64 `json:"double"`
}

type telemetrySource struct {
	Resource      map[string]any `json:"resource"`
	InstanceKnown bool           `json:"instance_identity_known"`
}

type telemetryPoint struct {
	Name        string          `json:"name"`
	Kind        string          `json:"type"`
	Temporality string          `json:"temporality"`
	Unit        string          `json:"unit"`
	SeriesID    string          `json:"series_id"`
	Source      telemetrySource `json:"source"`
	Attributes  map[string]any  `json:"attributes"`
	Number      telemetryNumber `json:"number"`
	Start       time.Time       `json:"start_time"`
	Event       time.Time       `json:"event_time"`
	Arrival     time.Time       `json:"arrival_time"`
}

type telemetrySpan struct {
	TraceID    string          `json:"trace_id"`
	SpanID     string          `json:"span_id"`
	ParentID   string          `json:"parent_span_id"`
	Name       string          `json:"name"`
	Source     telemetrySource `json:"source"`
	Attributes map[string]any  `json:"attributes"`
	Start      time.Time       `json:"start_time"`
	End        time.Time       `json:"end_time"`
}

type telemetryTrace struct {
	ID           string          `json:"trace_id"`
	Spans        []telemetrySpan `json:"spans"`
	RootObserved bool            `json:"root_observed"`
	Complete     bool            `json:"capture_complete"`
}

type telemetryView struct {
	CaptureID string           `json:"capture_session_id"`
	Namespace string           `json:"namespace"`
	Cluster   string           `json:"cluster"`
	Warnings  []string         `json:"warnings"`
	Truncated bool             `json:"truncated"`
	Points    []telemetryPoint `json:"points"`
	Traces    []telemetryTrace `json:"traces"`
}

func telemetryTool(ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any) (telemetryView, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return telemetryView{}, fmt.Errorf("call %s: %w", name, err)
	}
	if result.IsError || len(result.Content) != 1 {
		return telemetryView{}, fmt.Errorf("tool %s did not return one successful data result", name)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return telemetryView{}, fmt.Errorf("tool %s returned unexpected content", name)
	}
	var view telemetryView
	if err := json.Unmarshal([]byte(content.Text), &view); err != nil {
		return view, fmt.Errorf("decode %s: %w", name, err)
	}
	if view.Namespace != telemetryNamespace || view.Cluster != telemetryCluster || view.CaptureID == "" {
		return view, fmt.Errorf("tool capture is not bound to the selected test deployment")
	}
	return view, nil
}

func openTelemetryClient(ctx context.Context) (*mcp.ClientSession, error) {
	endpoint := os.Getenv("E2E_LIVE_TELEMETRY_MCP_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080/mcp"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Path != "/mcp" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("E2E_LIVE_TELEMETRY_MCP_URL must be a loopback HTTP /mcp endpoint")
	}
	ip := net.ParseIP(parsed.Hostname())
	if parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("telemetry test client requires a localhost-only port-forward")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "live-telemetry-e2e", Version: "0.1.0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}, nil)
}

func requireTelemetryEnvironment(ctx context.Context) {
	GinkgoHelper()
	if os.Getenv("E2E_LIVE_TELEMETRY") != "1" {
		Skip("live telemetry requires E2E_LIVE_TELEMETRY=1 and the disposable playground deployment")
	}
	env := e2e.SuiteEnv()
	Expect(env).NotTo(BeNil())
	namespace := &corev1.Namespace{}
	Expect(env.Client.Get(ctx, types.NamespacedName{Name: telemetryNamespace}, namespace)).To(Succeed())
	Expect(namespace.Labels).To(HaveKeyWithValue("app.kubernetes.io/part-of", "live-telemetry-prototype"),
		"refusing to use an unmarked namespace")
}

func positiveTelemetryNumber(number telemetryNumber) bool {
	return (number.Int != nil && *number.Int > 0) || (number.Double != nil && *number.Double > 0)
}

var _ = Describe("Live telemetry prototype",
	Label(e2e.PerformanceLabel, "live-telemetry"), Serial, func() {
		It("delivers real request metrics and related spans through the sidecar to MCP", func(ctx SpecContext) {
			requireTelemetryEnvironment(ctx)
			session, err := openTelemetryClient(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(session.Close()).To(Succeed()) })
			initial, err := telemetryTool(ctx, session, "get_capture_status", nil)
			Expect(err).NotTo(HaveOccurred())
			handle, err := wire.NewFromDocumentDB(ctx, e2e.SuiteEnv(), telemetryNamespace, telemetryCluster)
			Expect(err).NotTo(HaveOccurred())
			database := fixtures.DBNameFor("live-telemetry-protocol")
			DeferCleanup(func(cleanupCtx SpecContext) {
				Expect(sharedwire.DropDatabase(cleanupCtx, handle.Client(), database)).To(Succeed())
				Expect(handle.Close(cleanupCtx)).To(Succeed())
			})
			started := time.Now()
			docs := make([]bson.M, 64)
			for i := range docs {
				docs[i] = bson.M{"_id": i, "value": i % 8}
			}
			inserted, err := sharedwire.Seed(ctx, handle.Client(), database, "observations", docs)
			Expect(err).NotTo(HaveOccurred())
			Expect(inserted).To(Equal(len(docs)))
			for i := range 8 {
				var result bson.M
				Expect(handle.Database(database).Collection("observations").FindOne(ctx, bson.M{"_id": i}).Decode(&result)).To(Succeed())
			}
			var observedPoint telemetryPoint
			Eventually(func(g Gomega) {
				view, err := telemetryTool(ctx, session, "get_metric_window", map[string]any{
					"metric": "db.client.operations", "lookback_seconds": 60, "limit": 100,
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(view.CaptureID).To(Equal(initial.CaptureID))
				found := false
				for _, point := range view.Points {
					operation, _ := point.Attributes["db.operation.name"].(string)
					if strings.EqualFold(operation, "find") && point.Event.After(started) &&
						point.Source.Resource["service.name"] == "documentdb_gateway" && positiveTelemetryNumber(point.Number) {
						observedPoint, found = point, true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "gateway request counters are required; a database health gauge is insufficient")
			}, 60*time.Second, time.Second).Should(Succeed())
			var observedTrace telemetryTrace
			Eventually(func(g Gomega) {
				view, err := telemetryTool(ctx, session, "get_recent_traces", map[string]any{"lookback_seconds": 60, "limit": 20})
				g.Expect(err).NotTo(HaveOccurred())
				found := false
				for _, trace := range view.Traces {
					for _, span := range trace.Spans {
						operation, _ := span.Attributes["db.operation.name"].(string)
						if strings.EqualFold(operation, "find") && !span.Start.Before(started) &&
							span.Source.Resource["service.name"] == "documentdb_gateway" {
							observedTrace, found = trace, true
							break
						}
					}
				}
				g.Expect(found).To(BeTrue(), "a request span for the controlled operation is required")
			}, 30*time.Second, time.Second).Should(Succeed())
			detail, err := telemetryTool(ctx, session, "get_trace", map[string]any{"trace_id": observedTrace.ID, "limit": 100})
			Expect(err).NotTo(HaveOccurred())
			Expect(detail.Traces).To(HaveLen(1))
			Expect(detail.Traces[0].Complete).To(BeFalse())
			Expect(observedPoint.Kind).To(Equal("sum"))
			Expect(observedPoint.Temporality).To(Equal("delta"))
			fmt.Fprintf(GinkgoWriter, "live-telemetry capture=%s metric=%s type=%s temporality=%s event=%s arrival=%s instance_known=%t trace=%s spans=%d\n",
				initial.CaptureID, observedPoint.Name, observedPoint.Kind, observedPoint.Temporality,
				observedPoint.Event.Format(time.RFC3339Nano), observedPoint.Arrival.Format(time.RFC3339Nano),
				observedPoint.Source.InstanceKnown, observedTrace.ID, len(detail.Traces[0].Spans))
			for _, span := range detail.Traces[0].Spans {
				fmt.Fprintf(GinkgoWriter, "live-telemetry span=%s name=%s parent=%s duration=%s\n",
					span.SpanID, span.Name, span.ParentID, span.End.Sub(span.Start))
			}
		}, SpecTimeout(2*time.Minute))
	})
