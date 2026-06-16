// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// ConfigMapName is the name of the ConfigMap used to persist reports.
	ConfigMapName = "longhaul-report"
)

// SummaryFunc is called to generate the current test summary.
type SummaryFunc func() Summary

// CheckpointReporter periodically generates and persists reports.
type CheckpointReporter struct {
	clientset   kubernetes.Interface
	namespace   string
	interval    time.Duration
	summaryFunc SummaryFunc
}

// NewCheckpointReporter creates a periodic reporter that writes to stdout and ConfigMap.
func NewCheckpointReporter(clientset kubernetes.Interface, namespace string, interval time.Duration, fn SummaryFunc) *CheckpointReporter {
	return &CheckpointReporter{
		clientset:   clientset,
		namespace:   namespace,
		interval:    interval,
		summaryFunc: fn,
	}
}

// Run starts the periodic reporting loop. Blocks until context is cancelled.
func (r *CheckpointReporter) Run(ctx context.Context) {
	log.Printf("[checkpoint] periodic reporter started (interval=%s)", r.interval)
	defer log.Println("[checkpoint] periodic reporter stopped")

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final report on exit.
			r.emit(context.Background())
			return
		case <-ticker.C:
			r.emit(ctx)
		}
	}
}

func (r *CheckpointReporter) emit(ctx context.Context) {
	summary := r.summaryFunc()

	// Mark as RUNNING for intermediate checkpoints (unless already FAIL).
	resultStr := string(summary.Result)
	if summary.Result == ResultPass {
		resultStr = "RUNNING"
	}

	markdown := GenerateMarkdown(summary)

	// Print to stdout with clear delimiter.
	fmt.Printf("\n%s\n", "=== CHECKPOINT REPORT ===")
	fmt.Println(markdown)
	fmt.Printf("%s\n\n", "=== END CHECKPOINT ===")

	// Emit GitHub Actions annotations.
	EmitAnnotation(summary)

	// Persist to ConfigMap.
	if r.clientset == nil {
		return
	}

	data := map[string]string{
		"latest-report": markdown,
		"last-updated":  time.Now().UTC().Format(time.RFC3339),
		"result":        resultStr,
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: r.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":    "longhaul-test",
				"app.kubernetes.io/part-of": "documentdb-operator",
			},
		},
		Data: data,
	}

	existing, err := r.clientset.CoreV1().ConfigMaps(r.namespace).Get(ctx, ConfigMapName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = r.clientset.CoreV1().ConfigMaps(r.namespace).Create(ctx, cm, metav1.CreateOptions{})
		if err != nil {
			log.Printf("[checkpoint] failed to create ConfigMap: %v", err)
		} else {
			log.Println("[checkpoint] ConfigMap created")
		}
	} else if err == nil {
		existing.Data = data
		_, err = r.clientset.CoreV1().ConfigMaps(r.namespace).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			log.Printf("[checkpoint] failed to update ConfigMap: %v", err)
		} else {
			log.Println("[checkpoint] ConfigMap updated")
		}
	} else {
		log.Printf("[checkpoint] failed to get ConfigMap: %v", err)
	}

	// Also log the summary as JSON for structured log consumers.
	summaryJSON, _ := json.Marshal(map[string]interface{}{
		"result":         resultStr,
		"elapsed":        summary.Duration.String(),
		"writes":         summary.Metrics.WriteAttempted,
		"gaps":           summary.Metrics.GapsDetected,
		"ops":            summary.OpsExecuted,
		"memory_leak":    summary.LeakAnalysis.HasLeak,
		"memory_slope":   fmt.Sprintf("%.2f MB/h", summary.LeakAnalysis.MemorySlopeMB),
		"checkpoint_time": time.Now().UTC().Format(time.RFC3339),
	})
	log.Printf("[checkpoint] %s", string(summaryJSON))
}
