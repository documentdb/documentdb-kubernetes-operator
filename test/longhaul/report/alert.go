// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"fmt"
	"os"
	"time"
)

// isGitHubActions returns true when running inside GitHub Actions.
func isGitHubActions() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true"
}

// EmitAnnotation emits GitHub Actions workflow annotations based on test status.
// These annotations appear in the Actions UI on the workflow run summary.
func EmitAnnotation(s Summary) {
	if !isGitHubActions() {
		return
	}

	switch s.Result {
	case ResultFail:
		msg := "Long haul test FAILED"
		if s.FailReason != "" {
			msg = fmt.Sprintf("Long haul test FAILED: %s", s.FailReason)
		}
		// ::error:: annotations show as red in the Actions UI.
		fmt.Printf("::error title=Long Haul Test Failure::%s\n", msg)

	case ResultPass:
		// For intermediate checkpoints, emit a notice.
		fmt.Printf("::notice title=Long Haul Checkpoint::PASS after %s — %d writes, %d ops, %d gaps\n",
			s.Duration.Round(time.Second), s.Metrics.WriteAttempted, s.OpsExecuted, s.Metrics.GapsDetected)
	}

	// Emit warning for memory leak regardless of result.
	if s.LeakAnalysis.HasLeak {
		fmt.Printf("::warning title=Memory Leak Suspected::%.2f MB/hour over %s (%d samples)\n",
			s.LeakAnalysis.MemorySlopeMB, s.LeakAnalysis.Duration.Round(time.Second), s.LeakAnalysis.SampleCount)
	}
}
