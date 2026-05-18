package e2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	cnpgtests "github.com/cloudnative-pg/cloudnative-pg/tests"
	"github.com/onsi/ginkgo/v2"
)

// Level represents a depth/intensity tier for a test. Specs can gate
// themselves on the currently configured level so that short CI runs
// execute only the most important specs while nightly/manual runs
// expand coverage.
//
// Level is a type alias for CNPG's tests.Level so the constants match
// upstream's iota ordering byte-for-byte. We keep our own SkipUnlessLevel
// helper because CNPG does not export an equivalent.
type Level = cnpgtests.Level

// Depth tier constants re-exported from CNPG so callers can keep using
// e2e.Highest…e2e.Lowest without importing the upstream package.
const (
	Highest = cnpgtests.Highest
	High    = cnpgtests.High
	Medium  = cnpgtests.Medium
	Low     = cnpgtests.Low
	Lowest  = cnpgtests.Lowest
)

// testDepthEnv is the environment variable consulted by CurrentLevel.
// Accepted values are integers 0–4 mapping to Highest…Lowest, or the
// case-insensitive names "highest", "high", "medium", "low", "lowest".
// Invalid or unset values fall back to defaultLevel (Medium) and emit a
// one-shot warning to GinkgoWriter so misconfiguration is visible.
const testDepthEnv = "TEST_DEPTH"

// defaultLevel is the depth applied when TEST_DEPTH is unset or
// invalid. Chosen to match the design document.
const defaultLevel = Medium

// invalidDepthWarn ensures we log the "invalid TEST_DEPTH" warning at
// most once per process so a tight Eventually loop doesn't spam the
// output.
var invalidDepthWarn sync.Once

// CurrentLevel reads TEST_DEPTH from the environment and returns the
// corresponding Level. Accepts both numeric strings (0..4) and
// case-insensitive names (Highest..Lowest). Defaults to Medium when
// unset; logs a one-time warning and falls back to Medium when set to
// anything else.
func CurrentLevel() Level {
	raw, ok := os.LookupEnv(testDepthEnv)
	if !ok {
		return defaultLevel
	}
	if l, ok := parseLevel(raw); ok {
		return l
	}
	invalidDepthWarn.Do(func() {
		fmt.Fprintf(ginkgo.GinkgoWriter,
			"e2e: WARNING — TEST_DEPTH=%q is not a recognized depth (expected 0..4 "+
				"or one of Highest|High|Medium|Low|Lowest); falling back to Medium.\n",
			raw)
	})
	return defaultLevel
}

// parseLevel parses a single TEST_DEPTH value. Returns (level, true) on
// success and (defaultLevel, false) on any unrecognized input.
func parseLevel(raw string) (Level, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultLevel, false
	}
	if v, err := strconv.Atoi(trimmed); err == nil {
		switch Level(v) {
		case Highest, High, Medium, Low, Lowest:
			return Level(v), true
		default:
			return defaultLevel, false
		}
	}
	switch strings.ToLower(trimmed) {
	case "highest":
		return Highest, true
	case "high":
		return High, true
	case "medium":
		return Medium, true
	case "low":
		return Low, true
	case "lowest":
		return Lowest, true
	}
	return defaultLevel, false
}

// ShouldRun reports whether a spec declared at `required` should run
// given the currently configured level. A spec runs when the configured
// level is at least as deep as the spec's required level.
//
// Deprecated: Phase 2 specs should use [SkipUnlessLevel] instead —
// it is the single, uniform gate documented for area authors and it
// integrates with Ginkgo's reporting by invoking Skip rather than
// silently returning a bool.
func ShouldRun(required Level) bool {
	return CurrentLevel() >= required
}

// SkipUnlessLevel calls Ginkgo's Skip when the current depth level is
// shallower than min. Typical use from an `It`/`DescribeTable`:
//
//	It("exercises the pool under sustained load", Label(e2e.SlowLabel), func() {
//	    e2e.SkipUnlessLevel(e2e.Low)
//	    ...
//	})
//
// SkipUnlessLevel is the only level-gating pattern Phase 2 test writers
// should use; prefer it over raw calls to [ShouldRun].
func SkipUnlessLevel(min Level) {
	if CurrentLevel() < min {
		ginkgo.Skip(fmt.Sprintf("TEST_DEPTH=%d (%s) is shallower than required %s",
			CurrentLevel(), levelName(CurrentLevel()), levelName(min)))
	}
}

// levelName returns a human-readable name for a Level for use in skip
// messages.
func levelName(l Level) string {
	switch l {
	case Highest:
		return "Highest"
	case High:
		return "High"
	case Medium:
		return "Medium"
	case Low:
		return "Low"
	case Lowest:
		return "Lowest"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}
