package e2e

import (
	"os"
	"regexp"
	"testing"
)

func TestRunIDFromEnv(t *testing.T) {
	t.Setenv(runIDEnv, "pinned-run-42")
	resetRunIDForTest()
	t.Cleanup(resetRunIDForTest)

	if got := RunID(); got != "pinned-run-42" {
		t.Fatalf("RunID with env override = %q, want %q", got, "pinned-run-42")
	}
	// Second call must return the same value (cached).
	if got := RunID(); got != "pinned-run-42" {
		t.Fatalf("second RunID = %q, want %q", got, "pinned-run-42")
	}
}

func TestRunIDGeneratedWhenEnvMissing(t *testing.T) {
	_ = os.Unsetenv(runIDEnv)
	resetRunIDForTest()
	t.Cleanup(resetRunIDForTest)

	a := RunID()
	if a == "" {
		t.Fatal("generated RunID must not be empty")
	}
	// Stable across calls.
	if b := RunID(); a != b {
		t.Fatalf("RunID not stable: %q != %q", a, b)
	}
	// Short and lowercase hex/alnum.
	if len(a) > 24 {
		t.Fatalf("RunID unexpectedly long: %q", a)
	}
	if !regexp.MustCompile(`^[a-z0-9]+$`).MatchString(a) {
		t.Fatalf("RunID not lowercase alnum: %q", a)
	}
}

func TestGenerateRunIDUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for range 16 {
		v := generateRunID()
		if _, dup := seen[v]; dup {
			t.Fatalf("generateRunID produced duplicate %q", v)
		}
		seen[v] = struct{}{}
	}
}
