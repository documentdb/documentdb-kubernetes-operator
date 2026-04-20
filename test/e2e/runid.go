package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"
)

// runIDEnv is the environment variable consulted to pin the run
// identifier. When set and non-empty, its value is used verbatim; when
// unset, a per-process id is generated from the current time and a
// small random suffix on first access.
const runIDEnv = "E2E_RUN_ID"

var (
	runIDOnce sync.Once
	runIDVal  string
)

// RunID returns the process-scoped run identifier used to namespace
// shared fixtures and to label every cluster-scoped object the e2e
// suite creates. Stable for the life of the process.
//
// The identifier is resolved in this order:
//
//  1. $E2E_RUN_ID when set and non-empty (useful for reusing / cleaning
//     up fixtures across invocations);
//  2. otherwise a short, low-collision id derived from the current
//     Unix nanosecond timestamp plus four random bytes.
//
// Multiple test binaries that run independently each get their own
// id, which is what the fixture teardown logic relies on to avoid
// deleting another binary's still-live resources.
func RunID() string {
	runIDOnce.Do(func() {
		if v := os.Getenv(runIDEnv); v != "" {
			runIDVal = v
			return
		}
		runIDVal = generateRunID()
	})
	return runIDVal
}

// generateRunID produces a short, lowercase alphanumeric identifier.
// Exposed for tests via the resetRunIDForTest helper below.
func generateRunID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail in practice; fall back to a
		// time-derived suffix so the suite keeps running.
		return fmt.Sprintf("t%x", time.Now().UnixNano())[:10]
	}
	// UnixNano in base-36 keeps the prefix short while remaining
	// monotonic; 8 hex chars of randomness reduce cross-process
	// collision risk when two binaries start in the same nanosecond.
	ts := time.Now().UnixNano()
	return fmt.Sprintf("%x%s", ts&0xFFFFFFFF, hex.EncodeToString(b[:]))
}

// resetRunIDForTest re-initialises the once-guard so tests can exercise
// the generation path deterministically. Not part of the public API.
func resetRunIDForTest() {
	runIDOnce = sync.Once{}
	runIDVal = ""
}
