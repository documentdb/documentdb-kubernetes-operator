// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package report

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// isGitHubActions returns true when running inside GitHub Actions.
func isGitHubActions() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true"
}

// escapeAnnotation escapes characters that are special in GitHub Actions
// workflow commands. Per the runner spec, %, CR and LF must be percent-escaped
// in the message body or they corrupt the workflow command stream.
// See: https://docs.github.com/actions/using-workflows/workflow-commands-for-github-actions
func escapeAnnotation(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
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
		fmt.Printf("::error title=Long Haul Test Failure::%s\n", escapeAnnotation(msg))

	case ResultPass:
		// For intermediate checkpoints, emit a notice.
		fmt.Printf("::notice title=Long Haul Checkpoint::%s\n",
			escapeAnnotation(fmt.Sprintf("PASS after %s — %d writes, %d ops, %d gaps",
				s.Duration.Round(time.Second), s.Metrics.WriteAttempted, s.OpsExecuted, s.Metrics.GapsDetected)))
	}

	// Emit warning for memory leak regardless of result.
	if s.LeakAnalysis.HasLeak {
		fmt.Printf("::warning title=Memory Leak Suspected::%s\n",
			escapeAnnotation(fmt.Sprintf("%.2f MB/hour over %s (%d samples)",
				s.LeakAnalysis.MemorySlopeMB, s.LeakAnalysis.Duration.Round(time.Second), s.LeakAnalysis.SampleCount)))
	}
}
