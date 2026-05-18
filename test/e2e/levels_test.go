package e2e

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	cnpgtests "github.com/cloudnative-pg/cloudnative-pg/tests"
	"github.com/onsi/ginkgo/v2"
)

// resetInvalidDepthWarn re-arms the package-level sync.Once so tests
// that assert on warning emission can run independently. Same-package
// access is fine because tests live in the e2e package.
func resetInvalidDepthWarn() {
	invalidDepthWarn = sync.Once{}
}

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
	resetInvalidDepthWarn()
	t.Setenv(testDepthEnv, "not-an-int")
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("invalid TEST_DEPTH CurrentLevel = %v, want Medium", got)
	}
	t.Setenv(testDepthEnv, "99")
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("out-of-range TEST_DEPTH CurrentLevel = %v, want Medium", got)
	}
	t.Setenv(testDepthEnv, "")
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("empty TEST_DEPTH CurrentLevel = %v, want Medium", got)
	}
}

func TestCurrentLevelParses(t *testing.T) {
	cases := []struct {
		raw  string
		want Level
	}{
		// Numeric form.
		{"0", Highest},
		{"1", High},
		{"2", Medium},
		{"3", Low},
		{"4", Lowest},
		// Named form (canonical capitalization, as exported by the
		// test-e2e.yml workflow_dispatch input).
		{"Highest", Highest},
		{"High", High},
		{"Medium", Medium},
		{"Low", Low},
		{"Lowest", Lowest},
		// Named form is case-insensitive — workflow_dispatch surfaces
		// the choice exactly, but operators occasionally lowercase env
		// values when invoking ginkgo locally.
		{"high", High},
		{"MEDIUM", Medium},
		{"  Low  ", Low},
	}
	for _, c := range cases {
		t.Setenv(testDepthEnv, c.raw)
		if got := CurrentLevel(); got != c.want {
			t.Errorf("CurrentLevel(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// TestParseLevel exercises the parser directly so we can assert on the
// (Level, ok) pair returned for each variant. CurrentLevel collapses ok=false
// into "fall back to Medium", which would mask whether the parser actually
// recognized the input or silently defaulted — exactly the regression mode
// that motivated this refactor in the first place.
func TestParseLevel(t *testing.T) {
	t.Run("numeric/recognized", func(t *testing.T) {
		for _, c := range []struct {
			raw  string
			want Level
		}{
			{"0", Highest},
			{"1", High},
			{"2", Medium},
			{"3", Low},
			{"4", Lowest},
			{"  3  ", Low}, // whitespace tolerated for numerics too
		} {
			got, ok := parseLevel(c.raw)
			if !ok {
				t.Errorf("parseLevel(%q) ok = false; want true", c.raw)
				continue
			}
			if got != c.want {
				t.Errorf("parseLevel(%q) = %v, want %v", c.raw, got, c.want)
			}
		}
	})

	t.Run("numeric/out-of-range", func(t *testing.T) {
		// Anything outside 0..4 must report ok=false. This is the silent-
		// drift case: pre-fix, Atoi("Low") returned 0/error, fell back, and
		// CurrentLevel was indistinguishable from a deliberate Medium.
		for _, raw := range []string{"-1", "5", "99", "9999"} {
			got, ok := parseLevel(raw)
			if ok {
				t.Errorf("parseLevel(%q) ok = true; want false", raw)
			}
			if got != defaultLevel {
				t.Errorf("parseLevel(%q) fallback = %v, want %v", raw, got, defaultLevel)
			}
		}
	})

	t.Run("named/recognized", func(t *testing.T) {
		for _, c := range []struct {
			raw  string
			want Level
		}{
			{"Highest", Highest},
			{"HIGHEST", Highest},
			{"high", High},
			{"  Medium  ", Medium},
			{"low", Low},
			{"Lowest", Lowest},
		} {
			got, ok := parseLevel(c.raw)
			if !ok {
				t.Errorf("parseLevel(%q) ok = false; want true", c.raw)
				continue
			}
			if got != c.want {
				t.Errorf("parseLevel(%q) = %v, want %v", c.raw, got, c.want)
			}
		}
	})

	t.Run("rejects-unrecognized", func(t *testing.T) {
		// Bug-fence cases — every entry below would have silently fallen
		// back to Medium under the original Atoi-only parser without the
		// caller ever knowing the env var was wrong.
		for _, raw := range []string{
			"",       // unset-equivalent
			"   ",    // whitespace-only
			"Foo",    // bogus name
			"1.5",    // float
			"0x2",    // hex literal — strconv.Atoi does NOT accept these
			"two",    // English number-word
			"medium ", // OK actually trimmed → "medium" — exclude this case
		} {
			if raw == "medium " {
				continue // sanity: this one DOES parse after trim
			}
			got, ok := parseLevel(raw)
			if ok {
				t.Errorf("parseLevel(%q) ok = true; want false (would mask misconfiguration)", raw)
			}
			if got != defaultLevel {
				t.Errorf("parseLevel(%q) fallback = %v, want %v", raw, got, defaultLevel)
			}
		}
	})
}

// TestCurrentLevelWarnsOnceOnInvalid verifies the regression-fence we
// added with sync.Once: invalid TEST_DEPTH values must log a warning so
// misconfiguration is visible, but CurrentLevel is hot-pathed by
// Eventually() polls and we cannot afford to spam GinkgoWriter on every
// invocation.
func TestCurrentLevelWarnsOnceOnInvalid(t *testing.T) {
	resetInvalidDepthWarn()

	var buf bytes.Buffer
	// Swap GinkgoWriter for a capture buffer for the duration of the test.
	origWriter := ginkgo.GinkgoWriter
	ginkgo.GinkgoWriter = &captureWriter{buf: &buf}
	t.Cleanup(func() { ginkgo.GinkgoWriter = origWriter })

	t.Setenv(testDepthEnv, "not-a-level")
	for i := 0; i < 5; i++ {
		if got := CurrentLevel(); got != Medium {
			t.Fatalf("call %d: CurrentLevel = %v, want Medium", i, got)
		}
	}
	// Even when the invalid value changes, the warning must still emit
	// only once per process — sync.Once is cheaper than per-call dedup
	// and good enough for misconfiguration noise.
	t.Setenv(testDepthEnv, "still-not-a-level")
	if got := CurrentLevel(); got != Medium {
		t.Fatalf("CurrentLevel after env change = %v, want Medium", got)
	}

	out := buf.String()
	count := strings.Count(out, "WARNING — TEST_DEPTH=")
	if count != 1 {
		t.Errorf("warning emitted %d times; want exactly 1.\nCaptured: %q", count, out)
	}
	if !strings.Contains(out, `TEST_DEPTH="not-a-level"`) {
		t.Errorf("warning should quote the offending raw value; got: %q", out)
	}
}

// TestCurrentLevelDoesNotWarnOnValid is the symmetric guard: a valid
// value (numeric or named) must never emit the misconfiguration warning,
// regardless of how often CurrentLevel is called.
func TestCurrentLevelDoesNotWarnOnValid(t *testing.T) {
	resetInvalidDepthWarn()

	var buf bytes.Buffer
	origWriter := ginkgo.GinkgoWriter
	ginkgo.GinkgoWriter = &captureWriter{buf: &buf}
	t.Cleanup(func() { ginkgo.GinkgoWriter = origWriter })

	for _, v := range []string{"0", "Medium", "  high  "} {
		t.Setenv(testDepthEnv, v)
		_ = CurrentLevel()
	}
	if got := buf.String(); strings.Contains(got, "WARNING — TEST_DEPTH=") {
		t.Errorf("valid TEST_DEPTH should not warn; captured: %q", got)
	}
}

// TestLevelConstantsMatchCNPG locks down our type-alias re-export
// contract. If CNPG ever renumbers their tier constants this test fails
// loudly rather than silently miscategorising every spec in the suite.
func TestLevelConstantsMatchCNPG(t *testing.T) {
	cases := []struct {
		ours Level
		cnpg cnpgtests.Level
		name string
	}{
		{Highest, cnpgtests.Highest, "Highest"},
		{High, cnpgtests.High, "High"},
		{Medium, cnpgtests.Medium, "Medium"},
		{Low, cnpgtests.Low, "Low"},
		{Lowest, cnpgtests.Lowest, "Lowest"},
	}
	for _, c := range cases {
		if int(c.ours) != int(c.cnpg) {
			t.Errorf("%s: e2e=%d, cnpg=%d (constants drifted — re-export broken)",
				c.name, int(c.ours), int(c.cnpg))
		}
	}
	// Ordering invariant — Highest must be numerically smallest so that
	// CurrentLevel() >= required works as a depth comparator.
	if !(Highest < High && High < Medium && Medium < Low && Low < Lowest) {
		t.Fatalf("level ordering broken: Highest=%d High=%d Medium=%d Low=%d Lowest=%d",
			int(Highest), int(High), int(Medium), int(Low), int(Lowest))
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

// captureWriter is a minimal io.Writer that mirrors writes into an
// internal buffer. We cannot just assign a *bytes.Buffer to
// ginkgo.GinkgoWriter because GinkgoWriter is the GinkgoWriterInterface
// type, not io.Writer.
type captureWriter struct {
	buf *bytes.Buffer
}

func (c *captureWriter) Write(p []byte) (int, error)               { return c.buf.Write(p) }
func (c *captureWriter) Print(args ...interface{})                 {}
func (c *captureWriter) Printf(format string, args ...interface{}) {}
func (c *captureWriter) Println(args ...interface{})               {}
func (c *captureWriter) TeeTo(writer io.Writer)                    {}
func (c *captureWriter) ClearTeeWriters()                          {}

