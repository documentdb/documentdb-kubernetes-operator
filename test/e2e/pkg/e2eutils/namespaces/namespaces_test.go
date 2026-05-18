package namespaces

import (
	"regexp"
	"strings"
	"testing"
)

// dns1123Label matches the Kubernetes DNS-1123 label regex.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestBuildNameDeterministic(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	a := buildName("lifecycle", "lifecycle creates a cluster", "1", 1)
	b := buildName("lifecycle", "lifecycle creates a cluster", "1", 1)
	if a != b {
		t.Fatalf("non-deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "e2e-lifecycle-run1-p1-") {
		t.Fatalf("unexpected prefix: %q", a)
	}
}

func TestBuildNameUniquePerSpec(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	a := buildName("scale", "scale up to 3", "1", 1)
	b := buildName("scale", "scale up to 4", "1", 1)
	if a == b {
		t.Fatalf("distinct specs produced same name: %q", a)
	}
}

func TestBuildNameUniquePerProc(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	a := buildName("data", "spec x", "1", 1)
	b := buildName("data", "spec x", "2", 1)
	if a == b {
		t.Fatalf("distinct procs produced same name: %q", a)
	}
}

func TestBuildNameLengthAndDNS(t *testing.T) {
	SetRunIDFunc(func() string { return strings.Repeat("x", 80) })
	longArea := strings.Repeat("area", 20)
	name := buildName(longArea, "some-spec-text", "1", 1)
	if len(name) > maxNameLen {
		t.Fatalf("name too long (%d): %q", len(name), name)
	}
	if !dns1123Label.MatchString(name) {
		t.Fatalf("name not DNS-1123: %q", name)
	}
}

func TestBuildNameEmptyArea(t *testing.T) {
	SetRunIDFunc(func() string { return "r" })
	name := buildName("", "spec", "1", 1)
	if !strings.HasPrefix(name, "e2e-spec-") {
		t.Fatalf("empty area did not default to 'spec': %q", name)
	}
}

// TestBuildNameFirstAttemptIsHistorical pins that NumAttempts <= 1
// reproduces the layout that pre-flake-attempts namespaces used. Triage
// tooling, junit-name to-namespace mappings, and operator logs all
// assume the historical shape for the common (no-retry) path.
func TestBuildNameFirstAttemptIsHistorical(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	want := "e2e-lifecycle-run1-p1-"
	cases := []int{0, 1}
	for _, attempt := range cases {
		got := buildName("lifecycle", "spec text", "1", attempt)
		if !strings.HasPrefix(got, want) {
			t.Fatalf("attempt=%d: got %q, want prefix %q", attempt, got, want)
		}
		if strings.Contains(got, "-a") {
			t.Fatalf("attempt=%d: name unexpectedly carries retry segment: %q", attempt, got)
		}
	}
}

// TestBuildNameRetryDistinctFromFirstAttempt is the regression guard
// for the flake-attempts namespace-collision bug: retries must produce
// a name distinct from the first attempt so the retry's BeforeEach
// can create a fresh namespace while the first attempt's namespace
// is still Terminating.
func TestBuildNameRetryDistinctFromFirstAttempt(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	first := buildName("backup", "PV recovery spec", "1", 1)
	retry := buildName("backup", "PV recovery spec", "1", 2)
	if first == retry {
		t.Fatalf("retry must produce distinct namespace, both got %q", first)
	}
	if !strings.Contains(retry, "-a2-") {
		t.Fatalf("retry name missing -a2 segment: %q", retry)
	}
	if !dns1123Label.MatchString(retry) {
		t.Fatalf("retry name not DNS-1123: %q", retry)
	}
	// Successive retries must keep producing fresh names.
	retry3 := buildName("backup", "PV recovery spec", "1", 3)
	if retry3 == retry {
		t.Fatalf("attempt 3 collided with attempt 2: %q", retry3)
	}
	if !strings.Contains(retry3, "-a3-") {
		t.Fatalf("attempt-3 name missing -a3 segment: %q", retry3)
	}
}

// TestBuildNameRetryHonoursLengthBudget pins that the retry suffix
// survives truncation: even with a maxed-out runID + area, the retry
// segment must remain in the final name (otherwise retries collide
// with first attempts at long-input boundaries).
func TestBuildNameRetryHonoursLengthBudget(t *testing.T) {
	SetRunIDFunc(func() string { return strings.Repeat("x", 80) })
	longArea := strings.Repeat("area", 20)
	name := buildName(longArea, "spec", "1", 2)
	if len(name) > maxNameLen {
		t.Fatalf("retry name too long (%d): %q", len(name), name)
	}
	if !dns1123Label.MatchString(name) {
		t.Fatalf("retry name not DNS-1123: %q", name)
	}
	if !strings.Contains(name, "-a2-") {
		t.Fatalf("truncation dropped retry segment: %q", name)
	}
}

func TestSanitizeSegment(t *testing.T) {
	cases := map[string]string{
		"Hello World": "hello-world",
		"lifecycle":   "lifecycle",
		"a/b c":       "a-b-c",
		"---leading":  "leading",
		"":            "",
		"UPPER-123":   "upper-123",
	}
	for in, want := range cases {
		if got := sanitizeSegment(in); got != want {
			t.Errorf("sanitizeSegment(%q) = %q, want %q", in, got, want)
		}
	}
}
