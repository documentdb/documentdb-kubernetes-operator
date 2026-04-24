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
	a := buildName("lifecycle", "lifecycle creates a cluster", "1")
	b := buildName("lifecycle", "lifecycle creates a cluster", "1")
	if a != b {
		t.Fatalf("non-deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "e2e-lifecycle-run1-p1-") {
		t.Fatalf("unexpected prefix: %q", a)
	}
}

func TestBuildNameUniquePerSpec(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	a := buildName("scale", "scale up to 3", "1")
	b := buildName("scale", "scale up to 4", "1")
	if a == b {
		t.Fatalf("distinct specs produced same name: %q", a)
	}
}

func TestBuildNameUniquePerProc(t *testing.T) {
	SetRunIDFunc(func() string { return "run1" })
	a := buildName("data", "spec x", "1")
	b := buildName("data", "spec x", "2")
	if a == b {
		t.Fatalf("distinct procs produced same name: %q", a)
	}
}

func TestBuildNameLengthAndDNS(t *testing.T) {
	SetRunIDFunc(func() string { return strings.Repeat("x", 80) })
	longArea := strings.Repeat("area", 20)
	name := buildName(longArea, "some-spec-text", "1")
	if len(name) > maxNameLen {
		t.Fatalf("name too long (%d): %q", len(name), name)
	}
	if !dns1123Label.MatchString(name) {
		t.Fatalf("name not DNS-1123: %q", name)
	}
}

func TestBuildNameEmptyArea(t *testing.T) {
	SetRunIDFunc(func() string { return "r" })
	name := buildName("", "spec", "1")
	if !strings.HasPrefix(name, "e2e-spec-") {
		t.Fatalf("empty area did not default to 'spec': %q", name)
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
