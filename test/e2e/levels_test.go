package e2e

import (
	"os"
	"testing"
)

func TestCurrentLevelDefault(t *testing.T) {
	// t.Setenv with empty value still sets the variable; explicitly
	// unset to exercise the "unset" branch.
	orig, had := os.LookupEnv(testDepthEnv)
	_ = os.Unsetenv(testDepthEnv)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(testDepthEnv, orig)
		}
	})
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("default CurrentLevel = %v, want Medium", got)
	}
}

func TestCurrentLevelInvalidFallsBack(t *testing.T) {
	t.Setenv(testDepthEnv, "not-an-int")
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("invalid TEST_DEPTH CurrentLevel = %v, want Medium", got)
	}
	t.Setenv(testDepthEnv, "99")
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("out-of-range TEST_DEPTH CurrentLevel = %v, want Medium", got)
	}
}

func TestCurrentLevelParses(t *testing.T) {
	cases := []struct {
		raw  string
		want Level
	}{
		{"0", Highest},
		{"1", High},
		{"2", Medium},
		{"3", Low},
		{"4", Lowest},
	}
	for _, c := range cases {
		t.Setenv(testDepthEnv, c.raw)
		if got := CurrentLevel(); got != c.want {
			t.Errorf("CurrentLevel(%s) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestShouldRunRespectsOrdering(t *testing.T) {
	t.Setenv(testDepthEnv, "2") // Medium
	// Specs at Highest/High/Medium must run; Low/Lowest must not.
	for _, required := range []Level{Highest, High, Medium} {
		if !ShouldRun(required) {
			t.Errorf("at Medium, ShouldRun(%v) = false; want true", required)
		}
	}
	for _, required := range []Level{Low, Lowest} {
		if ShouldRun(required) {
			t.Errorf("at Medium, ShouldRun(%v) = true; want false", required)
		}
	}
}

func TestLevelName(t *testing.T) {
	for _, c := range []struct {
		l    Level
		want string
	}{
		{Highest, "Highest"},
		{High, "High"},
		{Medium, "Medium"},
		{Low, "Low"},
		{Lowest, "Lowest"},
	} {
		if got := levelName(c.l); got != c.want {
			t.Errorf("levelName(%v) = %q, want %q", c.l, got, c.want)
		}
	}
	if got := levelName(Level(42)); got == "" {
		t.Error("levelName for unknown should not be empty")
	}
}

// (helpers removed — tests use os.Setenv/Unsetenv directly.)
