package e2e

import (
	"fmt"
	"os"
	"strconv"

	"github.com/onsi/ginkgo/v2"
)

// Level represents a depth/intensity tier for a test. Specs can gate
// themselves on the currently configured level so that short CI runs
// execute only the most important specs while nightly/manual runs
// expand coverage.
//
// NOTE: CNPG does not currently expose a `tests/utils/levels` package
// in v1.28.1 (verified with `go doc`). If upstream adds one later,
// replace this file with a thin re-export.
type Level int

const (
	// Highest runs only the most critical specs (fast smoke).
	Highest Level = iota
	// High adds the core area-suite coverage.
	High
	// Medium adds broader coverage for the area. This is the default
	// per docs/designs/e2e-test-suite.md.
	Medium
	// Low adds long-running or edge-case scenarios.
	Low
	// Lowest runs everything, including slow/destructive corners.
	Lowest
)

// testDepthEnv is the environment variable consulted by CurrentLevel.
// Values are integers 0–4 mapping to Highest…Lowest. Invalid or unset
// values fall back to defaultLevel (Medium).
const testDepthEnv = "TEST_DEPTH"

// defaultLevel is the depth applied when TEST_DEPTH is unset or
// invalid. Chosen to match the design document.
const defaultLevel = Medium

// CurrentLevel reads TEST_DEPTH from the environment and returns the
// corresponding Level. Defaults to Medium when unset or invalid.
func CurrentLevel() Level {
	raw, ok := os.LookupEnv(testDepthEnv)
	if !ok {
		return defaultLevel
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defaultLevel
	}
	switch Level(v) {
	case Highest, High, Medium, Low, Lowest:
		return Level(v)
	default:
		return defaultLevel
	}
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
